package embeddings

import (
	"math"
	"os"
	"path/filepath"
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
