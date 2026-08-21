package embeddings

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

// RecordEmbedding stores an embedding vector for a provenance record
type RecordEmbedding struct {
	RecordID string
	Vector   []float32
}

// storeMagic marks an embeddings.bin file as self-describing: an 8-byte
// header (storeMagic + a little-endian uint32 vector width) precedes the
// records. Files written before this format existed have no header at all.
var storeMagic = [4]byte{'L', 'S', 'E', '1'}

// legacyDimension is the vector width implied by a headerless (pre-header)
// embeddings.bin. Every such file was written while Client.Dimension()
// unconditionally returned 2048 (voyage-4-large/voyage-4-lite), so it's the
// only width a headerless file can legitimately contain.
const legacyDimension = 2048

// Store manages the local embedding storage at .linespec/embeddings.bin
type Store struct {
	filePath string
	dim      int // established vector width; 0 means not yet established
}

// NewStore creates a new embedding store. The vector width is not assumed
// up front — it is derived from the store itself: from an existing file's
// header, inferred as legacyDimension for a headerless pre-existing file, or
// established by whatever vector the first Write receives.
func NewStore(repoRoot string) *Store {
	return &Store{
		filePath: filepath.Join(repoRoot, ".linespec", "embeddings.bin"),
	}
}

// SetDimension explicitly establishes the vector width, bypassing
// auto-detection. Intended for tests that want a small, fast vector width;
// production callers should let Write/Find derive the width from the store
// itself rather than supplying one.
func (s *Store) SetDimension(dim int) {
	s.dim = dim
}

// ensureDir creates the .linespec directory if it doesn't exist
func (s *Store) ensureDir() error {
	dir := filepath.Dir(s.filePath)
	return os.MkdirAll(dir, 0755)
}

// establishedDimension returns the store's vector width without reading
// full records, deriving it from the on-disk file if this Store value
// hasn't already fixed one. Returns 0 if the store has no established width
// yet: no file, an empty file, or a too-short/malformed file.
func (s *Store) establishedDimension() (int, error) {
	if s.dim != 0 {
		return s.dim, nil
	}

	file, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	header := make([]byte, 8)
	if _, err := io.ReadFull(file, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return 0, nil
		}
		return 0, err
	}

	if bytes.Equal(header[:4], storeMagic[:]) {
		return int(binary.LittleEndian.Uint32(header[4:8])), nil
	}

	// Headerless file with content: predates this format, so it can only
	// be a legacy voyage-provider index.
	return legacyDimension, nil
}

// Write stores an embedding vector for a record
func (s *Store) Write(embedding RecordEmbedding) error {
	dim, err := s.establishedDimension()
	if err != nil {
		return fmt.Errorf("failed to determine store dimension: %w", err)
	}
	if dim == 0 {
		// No width established yet — this vector establishes it.
		dim = len(embedding.Vector)
	} else if len(embedding.Vector) != dim {
		return fmt.Errorf("vector dimension mismatch: got %d, expected %d", len(embedding.Vector), dim)
	}
	s.dim = dim

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

	reader := bufio.NewReader(file)

	dim, err := s.readDim(reader)
	if err != nil {
		return nil, err
	}
	if dim == 0 {
		// No header and no content: an empty store has no records.
		return nil, nil
	}

	var embeddings []RecordEmbedding

	for {
		// Read record ID length (4 bytes)
		var idLen uint32
		if err := binary.Read(reader, binary.LittleEndian, &idLen); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read ID length: %w", err)
		}

		// Read record ID
		idBytes := make([]byte, idLen)
		if _, err := io.ReadFull(reader, idBytes); err != nil {
			return nil, fmt.Errorf("failed to read ID: %w", err)
		}
		recordID := string(idBytes)

		// Read vector
		vector := make([]float32, dim)
		for i := 0; i < dim; i++ {
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

// readDim consumes the store's header from reader, if one is present, and
// returns the vector width to use for decoding the records that follow.
// Returns 0 if the file has no records (nothing to decode).
func (s *Store) readDim(reader *bufio.Reader) (int, error) {
	peek, err := reader.Peek(4)
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return 0, nil
		}
		return 0, err
	}

	if bytes.Equal(peek, storeMagic[:]) {
		header := make([]byte, 8)
		if _, err := io.ReadFull(reader, header); err != nil {
			return 0, err
		}
		dim := int(binary.LittleEndian.Uint32(header[4:8]))
		s.dim = dim
		return dim, nil
	}

	// Headerless file with content: legacy format, always legacyDimension.
	if s.dim == 0 {
		s.dim = legacyDimension
	}
	return s.dim, nil
}

// writeAll writes all embeddings to the store, prefixed by a header
// recording the vector width so a later process can read it back without
// being told. A store left with zero records has no width worth
// preserving, so it is written as an empty file.
func (s *Store) writeAll(embeddings []RecordEmbedding) error {
	file, err := os.Create(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if len(embeddings) == 0 {
		s.dim = 0
		return nil
	}

	writer := bufio.NewWriter(file)

	header := make([]byte, 8)
	copy(header[:4], storeMagic[:])
	binary.LittleEndian.PutUint32(header[4:8], uint32(s.dim))
	if _, err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}

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
	dim, err := s.establishedDimension()
	if err != nil {
		return nil, err
	}
	if dim == 0 {
		// Nothing has ever been written, so there's nothing to compare
		// query against and nothing to find.
		return nil, nil
	}
	if len(query) != dim {
		return nil, fmt.Errorf("query dimension mismatch: got %d, expected %d", len(query), dim)
	}
	s.dim = dim

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
