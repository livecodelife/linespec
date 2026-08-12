package provenance

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// hashManifest is the in-memory view of .linespec/hash_manifest.json: a map of
// record ID to its sealed content hash.
//
// On disk the manifest is stored as one JSON object per line (sorted by ID),
// not as a single JSON document — see writeManifest for why. There is no
// aggregate/summary hash: earlier revisions stored a full-graph hash and an
// active-subset hash alongside the per-record map, but both were recomputed
// and rewritten on every single seal, so two branches that sealed different
// records always touched the same two lines and text-conflicted on every
// rebase (issue #163). Nothing in this codebase verifies against an aggregate
// digest, so it is simplest and safest to just not store one; a full digest
// of the graph is cheap to recompute from the per-record hashes whenever
// something actually needs it.
type hashManifest struct {
	Records map[string]string // record ID -> sealed content hash
}

// manifestLine is the on-disk shape of a single line in the manifest file.
type manifestLine struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

// Hasher manages content hashing for provenance records and maintains the hash manifest.
type Hasher struct {
	manifestPath string
}

// NewHasher creates a Hasher whose manifest lives at <root>/.linespec/hash_manifest.json.
// root is the directory the manifest is scoped to: the repo root for a
// root-level provenance config, or a package directory for a config resolved
// via -c <path>/.linespec.yml, so each package config gets its own manifest
// instead of clobbering a shared one.
func NewHasher(root string) *Hasher {
	return &Hasher{
		manifestPath: filepath.Join(root, ".linespec", "hash_manifest.json"),
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
//
// The file is one JSON object per line rather than a single JSON document —
// see writeManifest — so it is parsed line by line. A legacy manifest written
// by an older version of LineSpec (a single JSON document with a top-level
// "records" object plus aggregate hash fields) is also accepted, so existing
// repos are not forced through a manual migration: the next write converts it
// to the new line-based form.
func (h *Hasher) LoadManifest() (*hashManifest, error) {
	data, err := os.ReadFile(h.manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &hashManifest{Records: map[string]string{}}, nil
		}
		return nil, fmt.Errorf("read hash manifest: %w", err)
	}

	if legacy, ok := parseLegacyManifest(data); ok {
		return legacy, nil
	}

	records := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Individual record entries are small, but a very large manifest can still
	// exceed bufio's default 64KiB line limit as it grows; give it more room.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry manifestLine
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, fmt.Errorf("parse hash manifest: %w", err)
		}
		records[entry.ID] = entry.Hash
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse hash manifest: %w", err)
	}

	return &hashManifest{Records: records}, nil
}

// parseLegacyManifest recognizes the pre-#163 single-JSON-document manifest
// format ({"records": {...}, "full_graph_hash": "...", "active_subset_hash":
// "..."}) and extracts just the per-record hashes. ok is false for any input
// that is not that legacy shape, including the current line-based format —
// in particular, a single-record manifest (one line) is also valid JSON on
// its own, so this specifically requires a top-level "records" key rather
// than just checking that the whole file parses as one JSON value.
func parseLegacyManifest(data []byte) (*hashManifest, bool) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, false
	}
	recordsRaw, ok := probe["records"]
	if !ok {
		return nil, false
	}
	var records map[string]string
	if err := json.Unmarshal(recordsRaw, &records); err != nil {
		return nil, false
	}
	if records == nil {
		records = map[string]string{}
	}
	return &hashManifest{Records: records}, true
}

// SealRecord computes the content hash for r and stores it in the manifest.
// Only r's own entry is written — sealing one record never rewrites another
// record's line — so two branches that each seal a different record produce
// non-overlapping single-line insertions that git merges without conflict.
// The updated manifest is written atomically.
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

// ManifestNonEmpty reports whether the manifest exists and has at least one
// sealed record entry. It distinguishes a manifest that legitimately predates
// the hash-sealing feature (no manifest, or an empty one) from a manifest
// that a bad rebase/merge silently dropped an entry from: once any record has
// been sealed, every other implemented record is expected to have an entry
// too, so a missing one is significant.
func (h *Hasher) ManifestNonEmpty() (bool, error) {
	if !h.ManifestExists() {
		return false, nil
	}
	m, err := h.LoadManifest()
	if err != nil {
		return false, err
	}
	return len(m.Records) > 0, nil
}

func (h *Hasher) ManifestPath() string {
	return h.manifestPath
}

// CompileManifest recomputes hashes for every record and writes the manifest only
// when the result differs from what is already on disk. Returns true if the manifest
// was written, false if it was already up to date.
func (h *Hasher) CompileManifest(records []*Record) (bool, error) {
	freshHashes := make(map[string]string, len(records))
	for _, r := range records {
		hash, err := HashRecord(r)
		if err != nil {
			return false, err
		}
		freshHashes[r.ID] = hash
	}

	m, err := h.LoadManifest()
	if err != nil {
		return false, err
	}

	if len(m.Records) == len(freshHashes) {
		allMatch := true
		for id, hash := range freshHashes {
			if stored, ok := m.Records[id]; !ok || stored != hash {
				allMatch = false
				break
			}
		}
		if allMatch {
			return false, nil
		}
	}

	m.Records = freshHashes
	return true, h.writeManifest(m)
}

// writeManifest atomically writes the manifest to disk as one compact JSON
// object per line, sorted by record ID.
//
// This line-based shape (rather than a single JSON document with a nested
// "records" object) is what makes concurrent seals merge cleanly: each record
// occupies exactly one self-contained line terminated by "\n", with no
// separators shared with neighboring lines and no aggregate field that every
// seal has to touch. Inserting a new record only ever adds one line; it never
// requires editing an existing line to add or remove a trailing comma the way
// a JSON array or object would. That also makes a plain `merge=union` git
// merge driver (see .gitattributes) safe as a fallback for the rare case
// where two branches insert at the same sorted position: union-merging two
// independently valid JSON lines can never produce invalid JSON, unlike the
// old format where it produced duplicate keys and a broken aggregate hash.
func (h *Hasher) writeManifest(m *hashManifest) error {
	ids := make([]string, 0, len(m.Records))
	for id := range m.Records {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var buf bytes.Buffer
	for _, id := range ids {
		line, err := json.Marshal(manifestLine{ID: id, Hash: m.Records[id]})
		if err != nil {
			return fmt.Errorf("marshal hash manifest entry %s: %w", id, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
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

	if _, err := tmp.Write(buf.Bytes()); err != nil {
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
