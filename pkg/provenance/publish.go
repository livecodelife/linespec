package provenance

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// PublishOptions controls the linespec provenance publish command.
type PublishOptions struct {
	ManifestPath string // path to linespec.manifest.json; defaults to ./linespec.manifest.json
	Version      string // explicit version label; empty means auto-increment
	SpecsPath    string // optional path to specs artifact (file or directory)
	CodePath     string // optional path to code artifact (file or directory)
	PromptPath   string // optional path to prompt artifact file
}

// Publish packages the loaded provenance records into a versioned, content-addressed
// linespec.manifest.json. It applies the transformation pipeline, serializes the
// provenance layer, hashes all present layers, and appends an immutable version entry.
// Optional layers (specs, code, prompt) are read from the paths in opts when provided.
// URL fields are left empty for the author to fill in after uploading.
func (c *Commands) Publish(opts PublishOptions) error {
	manifestPath := opts.ManifestPath
	if manifestPath == "" {
		manifestPath = "linespec.manifest.json"
	}

	m, err := loadOrCreateManifest(manifestPath)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to load manifest: %v", err))
		return err
	}

	version := opts.Version
	if version == "" {
		version = nextVersion(m.Versions)
	}
	if _, exists := m.Versions[version]; exists {
		c.Formatter.FormatError(fmt.Sprintf("Version %q already exists in manifest; published versions are immutable", version))
		return fmt.Errorf("version %q already exists", version)
	}

	records := make([]Record, len(c.Loader.Records))
	for i, r := range c.Loader.Records {
		records[i] = *r
	}
	transformed := applyTransformPipeline(records)
	provBytes, err := packProvenanceLayer(transformed)
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to package provenance layer: %v", err))
		return err
	}

	type artifact struct {
		data []byte
		ext  string
	}
	artifacts := map[string]artifact{"provenance": {data: provBytes, ext: ".tar"}}

	for layerName, layerPath := range map[string]string{
		"specs":  opts.SpecsPath,
		"code":   opts.CodePath,
		"prompt": opts.PromptPath,
	} {
		if layerPath == "" {
			continue
		}
		data, ext, err := packLayer(layerPath)
		if err != nil {
			c.Formatter.FormatError(fmt.Sprintf("Failed to package %s layer from %q: %v", layerName, layerPath, err))
			return err
		}
		artifacts[layerName] = artifact{data: data, ext: ext}
	}

	layerBytes := make(map[string][]byte, len(artifacts))
	for name, a := range artifacts {
		layerBytes[name] = a.data
	}
	mv := newManifestVersion(layerBytes)

	manifestDir := filepath.Dir(manifestPath)
	for layerName, a := range artifacts {
		artifactPath := filepath.Join(manifestDir, fmt.Sprintf("linespec-%s-%s%s", layerName, version, a.ext))
		if err := atomicWrite(artifactPath, a.data); err != nil {
			c.Formatter.FormatError(fmt.Sprintf("Failed to write %s artifact: %v", layerName, err))
			return err
		}
		fmt.Fprintf(os.Stdout, "  wrote %s\n", artifactPath)
	}

	if m.Versions == nil {
		m.Versions = make(map[string]ManifestVersion)
	}
	m.Versions[version] = mv
	m.Latest = version

	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to serialize manifest: %v", err))
		return err
	}
	if err := atomicWrite(manifestPath, manifestBytes); err != nil {
		c.Formatter.FormatError(fmt.Sprintf("Failed to write manifest: %v", err))
		return err
	}

	fmt.Fprintf(os.Stdout, "\n✓ Published %s (root_hash: %s)\n", version, mv.RootHash)
	fmt.Fprintf(os.Stdout, "  manifest: %s\n\n", manifestPath)
	fmt.Fprintf(os.Stdout, "  Next steps:\n")
	fmt.Fprintf(os.Stdout, "  1. Upload each layer artifact to your hosting location\n")
	fmt.Fprintf(os.Stdout, "  2. Fill in the 'url' fields in %s\n\n", manifestPath)
	return nil
}

// loadOrCreateManifest reads an existing linespec.manifest.json or returns an empty one.
func loadOrCreateManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Manifest{Versions: map[string]ManifestVersion{}}, nil
		}
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Versions == nil {
		m.Versions = map[string]ManifestVersion{}
	}
	return &m, nil
}

// packProvenanceLayer creates a tar archive containing one YAML file per record,
// named <record.ID>.yml. This preserves individual record identity so linespec clone
// can extract records directly into a provenance/ directory.
func packProvenanceLayer(records []Record) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for i := range records {
		data, err := yaml.Marshal(&records[i])
		if err != nil {
			return nil, fmt.Errorf("marshal record %s: %w", records[i].ID, err)
		}
		hdr := &tar.Header{
			Name: records[i].ID + ".yml",
			Mode: 0644,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// packLayer packages a file or directory as a layer artifact.
// Returns (bytes, ".tar", error). Both files and directories become tar archives
// so all layer artifacts have a consistent format for clone/import extraction.
func packLayer(path string) ([]byte, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		hdr := &tar.Header{
			Name: filepath.Base(path),
			Mode: int64(info.Mode()),
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, "", err
		}
		if _, err := tw.Write(data); err != nil {
			return nil, "", err
		}
		if err := tw.Close(); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), ".tar", nil
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err = filepath.Walk(path, func(p string, fi os.FileInfo, werr error) error {
		if werr != nil || fi.IsDir() {
			return werr
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		hdr := &tar.Header{
			Name: rel,
			Mode: int64(fi.Mode()),
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), ".tar", nil
}

// atomicWrite writes data to path via a temp file and rename.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".linespec-publish-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}


// Manifest is the top-level structure for linespec.manifest.json.
type Manifest struct {
	Latest   string                     `json:"latest"`
	Versions map[string]ManifestVersion `json:"versions"`
}

// ManifestVersion represents one immutable published version.
type ManifestVersion struct {
	CreatedAt string                    `json:"created_at"`
	RootHash  string                    `json:"root_hash"`
	Layers    map[string]ManifestLayer  `json:"layers"`
}

// ManifestLayer holds the hash and optional URL for a single layer artifact.
type ManifestLayer struct {
	SHA256   string            `json:"sha256"`
	URL      string            `json:"url"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// layerOrder defines the canonical declaration order for root_hash computation.
var layerOrder = []string{"provenance", "specs", "code", "prompt"}

// applyTransformPipeline runs the deterministic transformation pipeline on records,
// returning a new slice that is safe to package for consumers.
func applyTransformPipeline(records []Record) []Record {
	out := stripImprints(records)
	out = filterSuperseded(out)
	out = promoteBugs(out)
	out = cleanDanglingRefs(out)
	out = resetStatus(out)
	return out
}

func stripImprints(records []Record) []Record {
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if r.Type != RecordTypeImprint {
			out = append(out, r)
		}
	}
	return out
}

func filterSuperseded(records []Record) []Record {
	supersededIDs := map[string]bool{}
	for _, r := range records {
		if r.Supersedes != "" {
			supersededIDs[r.Supersedes] = true
		}
	}
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if !supersededIDs[r.ID] {
			out = append(out, r)
		}
	}
	return out
}

func promoteBugs(records []Record) []Record {
	out := make([]Record, len(records))
	copy(out, records)
	for i := range out {
		if out[i].Type == RecordTypeBug {
			out[i].Type = RecordTypeBlueprint
		}
	}
	return out
}

func cleanDanglingRefs(records []Record) []Record {
	present := map[string]bool{}
	for _, r := range records {
		present[r.ID] = true
	}
	out := make([]Record, len(records))
	copy(out, records)
	for i := range out {
		if !present[out[i].Supersedes] {
			out[i].Supersedes = ""
		}
		if !present[out[i].Implements] {
			out[i].Implements = ""
		}
		filtered := out[i].Related[:0:0]
		for _, ref := range out[i].Related {
			if present[ref] {
				filtered = append(filtered, ref)
			}
		}
		out[i].Related = filtered
	}
	return out
}

func resetStatus(records []Record) []Record {
	out := make([]Record, len(records))
	copy(out, records)
	for i := range out {
		out[i].Status = StatusOpen
		out[i].SealedAtSHA = ""
	}
	return out
}

// hashBytes returns the lowercase hex SHA-256 of data.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

// rootHash computes SHA-256 of the concatenation of the provided layer hashes
// in the order given.
func rootHash(layerHashes []string) string {
	return hashBytes([]byte(strings.Join(layerHashes, "")))
}

// nextVersion returns the next auto-incremented version label given the existing
// version keys in a manifest. Returns "v1" when the map is empty.
func nextVersion(existing map[string]ManifestVersion) string {
	max := 0
	for k := range existing {
		n := 0
		fmt.Sscanf(k, "v%d", &n)
		if n > max {
			max = n
		}
	}
	return fmt.Sprintf("v%d", max+1)
}

// newManifestVersion builds a ManifestVersion from a map of layer name → artifact bytes.
// Only layers whose names appear in layerOrder are included in root_hash computation.
func newManifestVersion(layers map[string][]byte) ManifestVersion {
	ml := make(map[string]ManifestLayer, len(layers))
	var hashes []string
	for _, name := range layerOrder {
		data, ok := layers[name]
		if !ok {
			continue
		}
		h := hashBytes(data)
		ml[name] = ManifestLayer{SHA256: h}
		hashes = append(hashes, h)
	}
	return ManifestVersion{
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		RootHash:  rootHash(hashes),
		Layers:    ml,
	}
}
