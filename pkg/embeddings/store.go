package embeddings

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

// RecordEmbedding stores an embedding vector for a provenance record
type RecordEmbedding struct {
	RecordID string
	Vector   []float32
}

// Store manages the local embedding storage at .linespec/embeddings.bin
type Store struct {
	filePath string
	dim      int // embedding dimension
}

// NewStore creates a new embedding store
func NewStore(repoRoot string) *Store {
	return &Store{
		filePath: filepath.Join(repoRoot, ".linespec", "embeddings.bin"),
		dim:      1536, // Default OpenAI text-embedding-3-small dimension
	}
}

// SetDimension allows overriding the default dimension (for testing)
func (s *Store) SetDimension(dim int) {
	s.dim = dim
}

// ensureDir creates the .linespec directory if it doesn't exist
func (s *Store) ensureDir() error {
	dir := filepath.Dir(s.filePath)
	return os.MkdirAll(dir, 0755)
}

// Write stores an embedding vector for a record
func (s *Store) Write(embedding RecordEmbedding) error {
	if len(embedding.Vector) != s.dim {
		return fmt.Errorf("vector dimension mismatch: got %d, expected %d", len(embedding.Vector), s.dim)
	}

	if err := s.ensureDir(); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Read existing embeddings
	embeddings, err := s.ReadAll()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing embeddings: %w", err)
	}

	// Update or append
	found := false
	for i, e := range embeddings {
		if e.RecordID == embedding.RecordID {
			embeddings[i] = embedding
			found = true
			break
		}
	}
	if !found {
		embeddings = append(embeddings, embedding)
	}

	// Write all embeddings back
	return s.writeAll(embeddings)
}

// ReadAll reads all embeddings from the store
func (s *Store) ReadAll() ([]RecordEmbedding, error) {
	file, err := os.Open(s.filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var embeddings []RecordEmbedding
	reader := bufio.NewReader(file)

	for {
		// Read record ID length (4 bytes)
		var idLen uint32
		if err := binary.Read(reader, binary.LittleEndian, &idLen); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return nil, fmt.Errorf("failed to read ID length: %w", err)
		}

		// Read record ID
		idBytes := make([]byte, idLen)
		if _, err := reader.Read(idBytes); err != nil {
			return nil, fmt.Errorf("failed to read ID: %w", err)
		}
		recordID := string(idBytes)

		// Read vector
		vector := make([]float32, s.dim)
		for i := 0; i < s.dim; i++ {
			if err := binary.Read(reader, binary.LittleEndian, &vector[i]); err != nil {
				return nil, fmt.Errorf("failed to read vector: %w", err)
			}
		}

		embeddings = append(embeddings, RecordEmbedding{
			RecordID: recordID,
			Vector:   vector,
		})
	}

	return embeddings, nil
}

// writeAll writes all embeddings to the store
func (s *Store) writeAll(embeddings []RecordEmbedding) error {
	file, err := os.Create(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)

	for _, e := range embeddings {
		// Write record ID length
		idLen := uint32(len(e.RecordID))
		if err := binary.Write(writer, binary.LittleEndian, idLen); err != nil {
			return fmt.Errorf("failed to write ID length: %w", err)
		}

		// Write record ID
		if _, err := writer.WriteString(e.RecordID); err != nil {
			return fmt.Errorf("failed to write ID: %w", err)
		}

		// Write vector
		for _, v := range e.Vector {
			if err := binary.Write(writer, binary.LittleEndian, v); err != nil {
				return fmt.Errorf("failed to write vector: %w", err)
			}
		}
	}

	return writer.Flush()
}

// Delete removes an embedding for a record
func (s *Store) Delete(recordID string) error {
	embeddings, err := s.ReadAll()
	if err != nil {
		return err
	}

	var filtered []RecordEmbedding
	for _, e := range embeddings {
		if e.RecordID != recordID {
			filtered = append(filtered, e)
		}
	}

	return s.writeAll(filtered)
}

// Find searches for similar embeddings using brute-force cosine similarity
// Returns results sorted by similarity (highest first), limited to topN results
func (s *Store) Find(query []float32, topN int) ([]SearchResult, error) {
	if len(query) != s.dim {
		return nil, fmt.Errorf("query dimension mismatch: got %d, expected %d", len(query), s.dim)
	}

	embeddings, err := s.ReadAll()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No embeddings yet
		}
		return nil, err
	}

	var results []SearchResult
	for _, e := range embeddings {
		similarity := cosineSimilarity(query, e.Vector)
		results = append(results, SearchResult{
			RecordID:   e.RecordID,
			Similarity: similarity,
		})
	}

	// Sort by similarity (descending)
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Similarity > results[i].Similarity {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit results
	if topN > 0 && topN < len(results) {
		results = results[:topN]
	}

	return results, nil
}

// SearchResult represents a similarity search result
type SearchResult struct {
	RecordID   string
	Similarity float64
}

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// Exists returns true if an embedding exists for the record
func (s *Store) Exists(recordID string) (bool, error) {
	embeddings, err := s.ReadAll()
	if err != nil {
		return false, err
	}

	for _, e := range embeddings {
		if e.RecordID == recordID {
			return true, nil
		}
	}

	return false, nil
}
