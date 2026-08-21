package embeddings

import "testing"

// TestReadAllSurvivesNonUniformIDLengths reproduces issue #206: a record ID
// whose length differs from its neighbors (e.g. carries a suffix) desyncs
// ReadAll for records written after it, and Store.Write/Exists then treat a
// present-but-unreadable embedding as if it were never written or already
// indexed. Filled in during implementation of prov-2026-772101a3.
func TestReadAllSurvivesNonUniformIDLengths(t *testing.T) {
	t.Skip("pending implementation of prov-2026-772101a3")
}
