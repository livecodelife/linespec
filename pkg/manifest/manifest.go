package manifest

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// layerOrder is the canonical order for root_hash computation, matching publish.
var layerOrder = []string{"provenance", "specs", "code", "prompt"}

// Manifest is the top-level structure of linespec.manifest.json.
type Manifest struct {
	Latest   string                     `json:"latest"`
	Versions map[string]ManifestVersion `json:"versions"`
}

// ManifestVersion represents one immutable published version.
type ManifestVersion struct {
	CreatedAt string                   `json:"created_at"`
	RootHash  string                   `json:"root_hash"`
	Layers    map[string]ManifestLayer `json:"layers"`
}

// ManifestLayer holds the declared hash and download URL for a single layer artifact.
type ManifestLayer struct {
	SHA256   string            `json:"sha256"`
	URL      string            `json:"url"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// FetchedManifest is the result of a successful Fetch: all layers downloaded
// and hash-verified, ready to be extracted to disk.
type FetchedManifest struct {
	Version string
	Layers  map[string][]byte // layer name → raw artifact bytes
}

// Fetch downloads the manifest from manifestURL, resolves the target version,
// verifies the root_hash against the layer hashes (aborting before any layer
// download on mismatch), then downloads and SHA256-verifies each layer.
// Nothing is written to disk on any verification failure.
//
// Version resolution order: @version URL suffix → version argument → manifest.latest.
func Fetch(manifestURL, version string) (*FetchedManifest, error) {
	baseURL, urlVersion := parseVersionSuffix(manifestURL)
	if urlVersion != "" && version == "" {
		version = urlVersion
	}

	manifestData, err := httpGet(baseURL)
	if err != nil {
		return nil, fmt.Errorf("fetch manifest: %w", err)
	}

	var m Manifest
	if err := json.Unmarshal(manifestData, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}

	if version == "" {
		version = m.Latest
	}
	if version == "" {
		return nil, fmt.Errorf("manifest has no versions")
	}
	mv, ok := m.Versions[version]
	if !ok {
		return nil, fmt.Errorf("version %q not found in manifest", version)
	}

	// Verify root_hash before fetching any layer artifact.
	if err := verifyRootHash(mv); err != nil {
		return nil, err
	}

	layers := make(map[string][]byte, len(mv.Layers))
	for name, layer := range mv.Layers {
		if layer.URL == "" {
			return nil, fmt.Errorf("layer %q has no URL", name)
		}
		data, err := httpGet(layer.URL)
		if err != nil {
			return nil, fmt.Errorf("download layer %q: %w", name, err)
		}
		if err := verifyLayerHash(name, data, layer.SHA256); err != nil {
			return nil, err
		}
		layers[name] = data
	}

	return &FetchedManifest{Version: version, Layers: layers}, nil
}

// ExtractProvenance unpacks the provenance layer tar into destDir.
// Each tar entry (named <recordID>.yml) is written directly under destDir.
func ExtractProvenance(data []byte, destDir string) error {
	return extractTar(data, destDir)
}

// ExtractSpecs unpacks the specs layer tar into destDir, preserving the
// repo-relative paths recorded at publish time.
func ExtractSpecs(data []byte, destDir string) error {
	return extractTar(data, destDir)
}

// ExtractRaw writes a single-file layer directly to destPath.
func ExtractRaw(data []byte, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(destPath, data, 0644)
}

// ProvenanceRecordIDs returns the list of record IDs (filenames without .yml)
// present in a provenance layer tar, without extracting anything to disk.
func ProvenanceRecordIDs(data []byte) ([]string, error) {
	var ids []string
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		name := filepath.Base(hdr.Name)
		ids = append(ids, strings.TrimSuffix(name, ".yml"))
	}
	return ids, nil
}

// verifyRootHash checks that mv.RootHash equals SHA256 of the concatenation
// of layer SHA256 hex strings in canonical layerOrder.
func verifyRootHash(mv ManifestVersion) error {
	var parts []string
	for _, name := range layerOrder {
		layer, ok := mv.Layers[name]
		if !ok {
			continue
		}
		parts = append(parts, layer.SHA256)
	}
	computed := hashString(strings.Join(parts, ""))
	if computed != mv.RootHash {
		return fmt.Errorf("root_hash mismatch: manifest declares %s, computed %s — aborting", mv.RootHash, computed)
	}
	return nil
}

// verifyLayerHash checks that data's SHA256 matches the declared value.
func verifyLayerHash(name string, data []byte, declared string) error {
	actual := hashBytes(data)
	if actual != declared {
		return fmt.Errorf("layer %q hash mismatch: declared %s, got %s", name, declared, actual)
	}
	return nil
}

// extractTar unpacks a tar archive into destDir, preserving relative paths.
// Directory traversal via .. components is rejected.
func extractTar(data []byte, destDir string) error {
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		dest := filepath.Join(destDir, filepath.Clean(hdr.Name))
		if !strings.HasPrefix(dest, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry %q escapes destination directory", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0777)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("write %s: %w", dest, err)
		}
		f.Close()
	}
	return nil
}

// parseVersionSuffix splits "https://host/manifest.json@v3" into the base URL
// and version string. Returns the original URL and "" if no valid suffix is found.
func parseVersionSuffix(rawURL string) (base, version string) {
	idx := strings.LastIndex(rawURL, "@")
	if idx < 0 {
		return rawURL, ""
	}
	// Only treat as version suffix if no slash follows the @
	// (avoids matching userinfo in mailto: or user@host URLs).
	if strings.Contains(rawURL[idx:], "/") {
		return rawURL, ""
	}
	return rawURL[:idx], rawURL[idx+1:]
}

// httpGet fetches the given URL and returns the body bytes.
func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// hashBytes returns the lowercase hex SHA-256 of data.
func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// hashString returns the lowercase hex SHA-256 of s.
func hashString(s string) string {
	return hashBytes([]byte(s))
}
