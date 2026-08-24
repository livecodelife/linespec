package embeddings

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	store.SetDimension(3) // Use small dimension for testing

	// Test Write
	embedding := RecordEmbedding{
		RecordID: "prov-2026-001",
		Vector:   []float32{1.0, 2.0, 3.0},
	}

	if err := store.Write(embedding); err != nil {
		t.Fatalf("Failed to write embedding: %v", err)
	}

	// Test ReadAll
	embeddings, err := store.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read embeddings: %v", err)
	}

	if len(embeddings) != 1 {
		t.Fatalf("Expected 1 embedding, got %d", len(embeddings))
	}

	if embeddings[0].RecordID != "prov-2026-001" {
		t.Errorf("Expected record ID 'prov-2026-001', got '%s'", embeddings[0].RecordID)
	}

	// Test Exists
	exists, err := store.Exists("prov-2026-001")
	if err != nil {
		t.Fatalf("Failed to check existence: %v", err)
	}
	if !exists {
		t.Error("Expected embedding to exist")
	}

	// Test Find (similarity search)
	results, err := store.Find([]float32{1.0, 2.0, 3.0}, 5)
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	// Similarity should be 1.0 for identical vectors
	if math.Abs(results[0].Similarity-1.0) > 0.0001 {
		t.Errorf("Expected similarity ~1.0, got %f", results[0].Similarity)
	}

	// Test Delete
	if err := store.Delete("prov-2026-001"); err != nil {
		t.Fatalf("Failed to delete embedding: %v", err)
	}

	embeddings, err = store.ReadAll()
	if err != nil {
		t.Fatalf("Failed to read embeddings after delete: %v", err)
	}

	if len(embeddings) != 0 {
		t.Errorf("Expected 0 embeddings after delete, got %d", len(embeddings))
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name      string
		a         []float32
		b         []float32
		expected  float64
		tolerance float64
	}{
		{
			name:      "identical vectors",
			a:         []float32{1.0, 0.0, 0.0},
			b:         []float32{1.0, 0.0, 0.0},
			expected:  1.0,
			tolerance: 0.0001,
		},
		{
			name:      "orthogonal vectors",
			a:         []float32{1.0, 0.0},
			b:         []float32{0.0, 1.0},
			expected:  0.0,
			tolerance: 0.0001,
		},
		{
			name:      "opposite vectors",
			a:         []float32{1.0, 0.0},
			b:         []float32{-1.0, 0.0},
			expected:  -1.0,
			tolerance: 0.0001,
		},
		{
			name:      "45 degree angle",
			a:         []float32{1.0, 0.0},
			b:         []float32{1.0, 1.0},
			expected:  0.7071, // cos(45°) = 1/√2 ≈ 0.7071
			tolerance: 0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			if math.Abs(result-tt.expected) > tt.tolerance {
				t.Errorf("cosineSimilarity() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestExtractTextFromRecord(t *testing.T) {
	title := "Test Record"
	intent := "This is the intent of the record."
	constraints := []string{"Constraint 1", "Constraint 2"}

	text := ExtractTextFromRecord(title, intent, constraints)

	// Should contain title, intent, and constraints
	if text == "" {
		t.Error("Expected non-empty text")
	}

	// Simple sanity checks
	if len(text) < len(title) {
		t.Error("Text should at least contain the title")
	}
}

func TestStoreFilePath(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	expectedPath := filepath.Join(tmpDir, ".linespec", "embeddings.bin")
	if store.filePath != expectedPath {
		t.Errorf("Expected file path %s, got %s", expectedPath, store.filePath)
	}
}

func TestStoreReadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	// Reading from non-existent file should return error
	_, err := store.ReadAll()
	if err == nil {
		t.Error("Expected error when reading non-existent file")
	}

	if !os.IsNotExist(err) {
		t.Errorf("Expected IsNotExist error, got: %v", err)
	}
}

// vectorOf returns a vector of the given width, distinct per seed so
// records with different IDs don't collide in cosine-similarity tests.
func vectorOf(width int, seed float32) []float32 {
	v := make([]float32, width)
	for i := range v {
		v[i] = seed + float32(i)*0.001
	}
	return v
}

// TestStoreEstablishesWidthFromFirstWrite verifies the core fix: with no
// SetDimension override, a fresh store derives its width from whatever
// vector the first Write receives (e.g. a 768-dim nomic-embed-text-v1.5
// vector), not from any hardcoded constant.
func TestStoreEstablishesWidthFromFirstWrite(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	if err := store.Write(RecordEmbedding{RecordID: "prov-2026-aaa", Vector: vectorOf(768, 0.1)}); err != nil {
		t.Fatalf("Write() with no established width failed: %v", err)
	}

	// A mismatched width against the now-established store must still fail.
	err := store.Write(RecordEmbedding{RecordID: "prov-2026-bbb", Vector: vectorOf(384, 0.2)})
	if err == nil {
		t.Fatal("expected dimension mismatch error for a differently-sized vector, got nil")
	}
	if !strings.Contains(err.Error(), "vector dimension mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestStorePersistsWidthAcrossProcesses simulates the real usage pattern
// (index, search, audit, complete all open a fresh Store per command): the
// width established by one Store value must be recoverable by a brand new
// Store value pointed at the same repo root, entirely from the on-disk file.
func TestStorePersistsWidthAcrossProcesses(t *testing.T) {
	tmpDir := t.TempDir()

	writer := NewStore(tmpDir)
	if err := writer.Write(RecordEmbedding{RecordID: "prov-2026-aaa", Vector: vectorOf(1024, 0.1)}); err != nil {
		t.Fatalf("Write() failed: %v", err)
	}

	// Fresh Store value, no SetDimension — must read the width back from disk.
	reader := NewStore(tmpDir)
	results, err := reader.Find(vectorOf(1024, 0.1), 5)
	if err != nil {
		t.Fatalf("Find() with matching width on a fresh Store failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// And a mismatched query against that persisted width must still fail.
	fresh := NewStore(tmpDir)
	if _, err := fresh.Find(vectorOf(384, 0.1), 5); err == nil {
		t.Fatal("expected dimension mismatch error, got nil")
	} else if !strings.Contains(err.Error(), "query dimension mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestStoreLegacyHeaderlessFileStillWorks writes an embeddings.bin the way
// the pre-fix implementation always did (no header, since Client.Dimension()
// unconditionally returned 2048), and verifies a fresh Store still reads and
// searches it correctly without being told the width.
func TestStoreLegacyHeaderlessFileStillWorks(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, ".linespec")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	const legacyWidth = 2048
	vector := vectorOf(legacyWidth, 0.5)

	var buf bytes.Buffer
	recordID := "prov-2026-legacy"
	if err := binary.Write(&buf, binary.LittleEndian, uint32(len(recordID))); err != nil {
		t.Fatalf("binary.Write id len: %v", err)
	}
	buf.WriteString(recordID)
	for _, v := range vector {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			t.Fatalf("binary.Write vector: %v", err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "embeddings.bin"), buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := NewStore(tmpDir)
	results, err := store.Find(vector, 5)
	if err != nil {
		t.Fatalf("Find() against legacy headerless file failed: %v", err)
	}
	if len(results) != 1 || results[0].RecordID != recordID {
		t.Fatalf("unexpected results: %+v", results)
	}

	// A new write against the legacy store must still be validated at 2048.
	fresh := NewStore(tmpDir)
	err = fresh.Write(RecordEmbedding{RecordID: "prov-2026-new", Vector: vectorOf(768, 0.1)})
	if err == nil {
		t.Fatal("expected dimension mismatch error writing a 768-wide vector into a legacy 2048 store")
	}
	if !strings.Contains(err.Error(), "vector dimension mismatch: got 768, expected 2048") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestStoreEmptyAfterDeleteAcceptsNewWidth verifies the constraint that a
// store containing zero records has no established width to preserve: once
// every record is deleted, the next Write is free to establish a new width.
func TestStoreEmptyAfterDeleteAcceptsNewWidth(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir)

	if err := store.Write(RecordEmbedding{RecordID: "prov-2026-only", Vector: vectorOf(1024, 0.1)}); err != nil {
		t.Fatalf("Write() failed: %v", err)
	}
	if err := store.Delete("prov-2026-only"); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}

	// Fresh Store value re-opening the now-empty file: no width to inherit.
	fresh := NewStore(tmpDir)
	if err := fresh.Write(RecordEmbedding{RecordID: "prov-2026-new", Vector: vectorOf(384, 0.2)}); err != nil {
		t.Fatalf("Write() of a differently-sized vector after emptying the store failed: %v", err)
	}
}
