package provenance

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

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
	URL      string            `json:"url,omitempty"`
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
