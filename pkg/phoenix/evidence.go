package phoenix

import (
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // SHA-1 used for content fingerprinting, not security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/livecodelife/linespec/pkg/config"
)

const evidenceKind = "linespec_behavioral_test/v1"

// SpecResult holds the outcome of a single .linespec test file run.
type SpecResult struct {
	Path   string
	Passed bool
}

// evidenceRecord mirrors Phoenix's EvidenceRecord JSON schema exactly.
type evidenceRecord struct {
	EvidenceID   string   `json:"evidence_id"`
	Kind         string   `json:"kind"`
	Status       string   `json:"status"`
	IUID         string   `json:"iu_id"`
	CanonIDs     []string `json:"canon_ids"`
	ArtifactHash string   `json:"artifact_hash,omitempty"`
	Message      string   `json:"message,omitempty"`
	Timestamp    string   `json:"timestamp"`
}

type evidenceIndex struct {
	Records []evidenceRecord `json:"records"`
}

// EmitEvidence writes EvidenceRecords into the Phoenix evidence store for every
// IU mapping whose spec path appears in results. Errors are non-fatal.
func EmitEvidence(cfg *config.PhoenixConfig, results []SpecResult, baseDir string) error {
	if cfg == nil || len(cfg.IUMappings) == 0 {
		return nil
	}

	resultsByPath := make(map[string]bool, len(results))
	for _, r := range results {
		abs, err := filepath.Abs(r.Path)
		if err != nil {
			abs = r.Path
		}
		resultsByPath[abs] = r.Passed
	}

	root := cfg.Root
	if !filepath.IsAbs(root) {
		root = filepath.Join(baseDir, root)
	}
	evidencePath := filepath.Join(root, ".phoenix", "graphs", "evidence.json")

	var records []evidenceRecord
	now := time.Now().UTC().Format(time.RFC3339)

	for _, mapping := range cfg.IUMappings {
		specPath := mapping.Spec
		if !filepath.IsAbs(specPath) {
			specPath = filepath.Join(baseDir, specPath)
		}
		abs, err := filepath.Abs(specPath)
		if err != nil {
			abs = specPath
		}

		passed, ok := resultsByPath[abs]
		if !ok {
			continue
		}

		hash, err := sha1File(abs)
		if err != nil {
			hash = ""
		}

		status := "PASS"
		if !passed {
			status = "FAIL"
		}

		id, err := newUUID()
		if err != nil {
			return fmt.Errorf("phoenix: failed to generate evidence ID: %w", err)
		}

		records = append(records, evidenceRecord{
			EvidenceID:   id,
			Kind:         evidenceKind,
			Status:       status,
			IUID:         mapping.IUID,
			CanonIDs:     []string{},
			ArtifactHash: hash,
			Timestamp:    now,
		})
	}

	if len(records) == 0 {
		return nil
	}

	return appendRecords(evidencePath, records)
}

func appendRecords(path string, newRecords []evidenceRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("phoenix: failed to create evidence directory: %w", err)
	}

	var idx evidenceIndex
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &idx); err != nil {
			return fmt.Errorf("phoenix: failed to parse existing evidence.json: %w", err)
		}
	}

	idx.Records = append(idx.Records, newRecords...)

	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("phoenix: failed to marshal evidence: %w", err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("phoenix: failed to write evidence.json: %w", err)
	}
	return nil
}

func sha1File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha1.New() //nolint:gosec
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
