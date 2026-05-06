package provenance

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// hashManifest is the on-disk structure stored in .linespec/hash_manifest.json.
type hashManifest struct {
	Records         map[string]string `json:"records"`
	FullGraphHash   string            `json:"full_graph_hash"`
	ActiveSubsetHash string           `json:"active_subset_hash"`
}

// Hasher manages content hashing for provenance records and maintains the hash manifest.
type Hasher struct {
	manifestPath string
}

// NewHasher creates a Hasher whose manifest lives at <repoRoot>/.linespec/hash_manifest.json.
func NewHasher(repoRoot string) *Hasher {
	return &Hasher{
		manifestPath: filepath.Join(repoRoot, ".linespec", "hash_manifest.json"),
	}
}

// HashRecord computes a content hash for a single record.
// The hash covers all exported fields; FilePath is excluded because it is a
// runtime path, not part of the record's content.
func HashRecord(r *Record) (string, error) {
	// Shallow copy so we can clear the runtime field without mutating the caller's record.
	copy := *r
	copy.FilePath = ""

	data, err := yaml.Marshal(&copy)
	if err != nil {
		return "", fmt.Errorf("marshal record %s: %w", r.ID, err)
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

// LoadManifest reads the hash manifest from disk. Returns an empty manifest if the file
// does not exist yet.
func (h *Hasher) LoadManifest() (*hashManifest, error) {
	data, err := os.ReadFile(h.manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &hashManifest{Records: map[string]string{}}, nil
		}
		return nil, fmt.Errorf("read hash manifest: %w", err)
	}

	var m hashManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse hash manifest: %w", err)
	}
	if m.Records == nil {
		m.Records = map[string]string{}
	}
	return &m, nil
}

// SealRecord computes the content hash for r, stores it in the manifest, and
// recomputes both graph hashes using all records provided in allRecords. The
// updated manifest is written atomically.
func (h *Hasher) SealRecord(r *Record, allRecords []*Record) error {
	hash, err := HashRecord(r)
	if err != nil {
		return err
	}

	m, err := h.LoadManifest()
	if err != nil {
		return err
	}

	m.Records[r.ID] = hash

	// Merge live hashes for records already in manifest with new hash.
	// Build per-record hashes: prefer the just-computed hash; for others use
	// whatever is already stored in the manifest.
	allHashes := make(map[string]string, len(allRecords))
	for _, rec := range allRecords {
		if rec.ID == r.ID {
			allHashes[rec.ID] = hash
			continue
		}
		if stored, ok := m.Records[rec.ID]; ok {
			allHashes[rec.ID] = stored
		} else {
			// Compute hash for records not yet in manifest.
			h2, err2 := HashRecord(rec)
			if err2 != nil {
				return err2
			}
			allHashes[rec.ID] = h2
		}
	}

	m.FullGraphHash = computeGraphHash(allRecords, allHashes, nil)
	m.ActiveSubsetHash = computeGraphHash(allRecords, allHashes, isActiveRecord)

	return h.writeManifest(m)
}

// VerifyRecord returns the stored hash for recordID and the current content hash.
// ok is false when the manifest has no entry for the record.
func (h *Hasher) VerifyRecord(r *Record) (stored, current string, ok bool, err error) {
	m, err := h.LoadManifest()
	if err != nil {
		return "", "", false, err
	}

	stored, ok = m.Records[r.ID]
	if !ok {
		return "", "", false, nil
	}

	current, err = HashRecord(r)
	if err != nil {
		return "", "", false, err
	}
	return stored, current, true, nil
}

// ManifestExists returns true if the hash manifest file exists on disk.
func (h *Hasher) ManifestExists() bool {
	_, err := os.Stat(h.manifestPath)
	return err == nil
}

// isActiveRecord returns true for records that are not superseded or deprecated.
func isActiveRecord(r *Record) bool {
	return r.Status != StatusSuperseded && r.Status != StatusDeprecated
}

// computeGraphHash produces a SHA-256 hash over the sorted concatenation of per-record
// hashes for all records accepted by filter. filter may be nil to include all records.
func computeGraphHash(records []*Record, hashes map[string]string, filter func(*Record) bool) string {
	// Collect IDs in sorted order for determinism.
	ids := make([]string, 0, len(records))
	for _, r := range records {
		if filter == nil || filter(r) {
			ids = append(ids, r.ID)
		}
	}
	sort.Strings(ids)

	h := sha256.New()
	for _, id := range ids {
		if hash, ok := hashes[id]; ok {
			h.Write([]byte(id))
			h.Write([]byte(hash))
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// writeManifest atomically writes the manifest to disk.
func (h *Hasher) writeManifest(m *hashManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal hash manifest: %w", err)
	}

	dir := filepath.Dir(h.manifestPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create .linespec dir: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".hash_manifest_*.tmp")
	if err != nil {
		return fmt.Errorf("create temp manifest: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write temp manifest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp manifest: %w", err)
	}
	if err := os.Rename(tmpPath, h.manifestPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename manifest: %w", err)
	}
	return nil
}
