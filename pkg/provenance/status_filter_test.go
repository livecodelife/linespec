package provenance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func makeStatusTestCommands(records []*Record) *Commands {
	loader := &Loader{
		Records:     records,
		RecordsByID: make(map[string]*Record),
	}
	for _, r := range records {
		loader.RecordsByID[r.ID] = r
	}
	var buf bytes.Buffer
	return &Commands{
		Loader:    loader,
		Formatter: NewFormatter(&buf, false),
		Config:    &ProvenanceConfig{Enforcement: "strict"},
	}
}

func statusFilterTestRecords() []*Record {
	return []*Record{
		{ID: "prov-2026-aaaa0001", Title: "an open record", Status: StatusOpen},
		{ID: "prov-2026-aaaa0002", Title: "a draft record", Status: StatusDraft},
		{ID: "prov-2026-aaaa0003", Title: "an implemented record", Status: StatusImplemented},
	}
}

// TestStatusJSONHonorsFilter guards against the JSON output path silently
// dumping every record regardless of --filter (the "all records" branch of
// Commands.Status used to build its result from c.Loader.Records directly,
// never touching opts.Filter).
func TestStatusJSONHonorsFilter(t *testing.T) {
	cmds := makeStatusTestCommands(statusFilterTestRecords())
	var buf bytes.Buffer
	cmds.Formatter.Output = &buf

	if err := cmds.Status(StatusOptions{Filter: "open", Format: "json"}); err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	var out struct {
		Records []*Record `json:"records"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("failed to parse JSON output: %v\noutput: %s", err, buf.String())
	}

	if len(out.Records) != 1 || out.Records[0].ID != "prov-2026-aaaa0001" {
		t.Fatalf("expected --filter open --format json to return only the open record, got %d records: %+v", len(out.Records), out.Records)
	}
}

// TestStatusHumanHonorsDraftFilter guards the human-output filter switch,
// which previously recognized only open/implemented/superseded/deprecated
// and "tag:" prefixes, silently falling back to the full unfiltered list
// for any other value -- including the real "draft" status.
func TestStatusHumanHonorsDraftFilter(t *testing.T) {
	cmds := makeStatusTestCommands(statusFilterTestRecords())
	var buf bytes.Buffer
	cmds.Formatter.Output = &buf

	if err := cmds.Status(StatusOptions{Filter: "draft"}); err != nil {
		t.Fatalf("Status returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "prov-2026-aaaa0002") {
		t.Errorf("expected --filter draft output to contain the draft record, got: %s", output)
	}
	if strings.Contains(output, "prov-2026-aaaa0001") || strings.Contains(output, "prov-2026-aaaa0003") {
		t.Errorf("expected --filter draft output to exclude non-draft records, got: %s", output)
	}
}

// TestStatusUnknownFilterErrors guards against an unrecognized filter value
// silently falling back to the unfiltered list -- it must be reported as an
// error instead, for both human and JSON output.
func TestStatusUnknownFilterErrors(t *testing.T) {
	for _, format := range []string{"", "json"} {
		cmds := makeStatusTestCommands(statusFilterTestRecords())
		var buf bytes.Buffer
		cmds.Formatter.Output = &buf

		err := cmds.Status(StatusOptions{Filter: "not-a-real-status", Format: format})
		if err == nil {
			t.Errorf("format=%q: expected an error for an unrecognized --filter value, got nil", format)
		}
	}
}
