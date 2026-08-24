package embeddings

import (
	"fmt"
	"testing"
)

// TestReadAllSurvivesNonUniformIDLengths reproduces issue #206: a record ID
// whose length differs from its neighbors (e.g. carries a suffix) desyncs
// ReadAll for records written after it, and Store.Write/Exists then treat a
// present-but-unreadable embedding as if it were never written or already
// indexed. Filled in during implementation of prov-2026-772101a3.
func TestReadAllSurvivesNonUniformIDLengths(t *testing.T) {
	tmpDir := t.TempDir()
	store := NewStore(tmpDir)
	store.SetDimension(4) // small dimension keeps the test fast

	const total = 200

	// Matches the issue's exact repro: 200 synthetic records, one ID carrying
	// a disambiguating suffix (so it's a different length than its uniform
	// neighbors) positioned first.
	var ids []string
	ids = append(ids, "prov-2026-40000000-aaaaaaaaaaaaa")
	for i := 1; i < total; i++ {
		ids = append(ids, fmt.Sprintf("prov-2026-%08d", i))
	}

	var embeddings []RecordEmbedding
	for i, id := range ids {
		embeddings = append(embeddings, RecordEmbedding{
			RecordID: id,
			Vector:   []float32{float32(i), float32(i) + 0.5, float32(i) + 1, float32(i) + 1.5},
		})
	}

	if err := store.ensureDir(); err != nil {
		t.Fatalf("ensureDir failed: %v", err)
	}
	if err := store.writeAll(embeddings); err != nil {
		t.Fatalf("writeAll failed: %v", err)
	}

	got, err := store.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll failed to round-trip a non-uniform-length-ID corpus: %v", err)
	}

	if len(got) != total {
		t.Fatalf("expected %d records, got %d", total, len(got))
	}

	for i, id := range ids {
		if got[i].RecordID != id {
			t.Fatalf("record %d: expected ID %q, got %q (store desynced)", i, id, got[i].RecordID)
		}
		exists, err := store.Exists(id)
		if err != nil {
			t.Fatalf("Exists(%q) errored: %v", id, err)
		}
		if !exists {
			t.Fatalf("Exists(%q) = false, want true", id)
		}
	}
}

// TestReadAllSurvivesSuffixedIDAtEveryPosition confirms the fix isn't
// order-dependent: the suffixed (longer) ID desyncs ReadAll whenever it lands
// at a byte offset that produces a short bufio.Reader.Read, not just when
// it's first. Sweep a handful of positions across a 200-record corpus.
func TestReadAllSurvivesSuffixedIDAtEveryPosition(t *testing.T) {
	tmpDir := t.TempDir()
	const total = 200

	positions := []int{0, 1, 50, 100, 150, 199}
	for _, pos := range positions {
		t.Run(fmt.Sprintf("pos-%d", pos), func(t *testing.T) {
			store := NewStore(tmpDir)
			store.SetDimension(4)
			store.filePath += fmt.Sprintf(".pos%d", pos)

			var ids []string
			for i := 0; i < total; i++ {
				if i == pos {
					ids = append(ids, "prov-2026-40000000-aaaaaaaaaaaaa")
				} else {
					ids = append(ids, fmt.Sprintf("prov-2026-%08d", i))
				}
			}

			var embeddings []RecordEmbedding
			for i, id := range ids {
				embeddings = append(embeddings, RecordEmbedding{
					RecordID: id,
					Vector:   []float32{float32(i), float32(i) + 0.5, float32(i) + 1, float32(i) + 1.5},
				})
			}

			if err := store.ensureDir(); err != nil {
				t.Fatalf("ensureDir failed: %v", err)
			}
			if err := store.writeAll(embeddings); err != nil {
				t.Fatalf("writeAll failed: %v", err)
			}

			got, err := store.ReadAll()
			if err != nil {
				t.Fatalf("ReadAll failed with suffixed ID at position %d: %v", pos, err)
			}
			if len(got) != total {
				t.Fatalf("position %d: expected %d records, got %d", pos, total, len(got))
			}
			for i, id := range ids {
				if got[i].RecordID != id {
					t.Fatalf("position %d: record %d: expected ID %q, got %q", pos, i, id, got[i].RecordID)
				}
			}
		})
	}
}
