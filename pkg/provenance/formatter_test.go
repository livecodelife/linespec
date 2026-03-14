package provenance

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatCheckResult_EmptyFileViolation(t *testing.T) {
	// Test that violations with empty File fields display the Message
	var buf bytes.Buffer
	formatter := NewFormatter(&buf, false)

	violations := []Violation{
		{
			RecordID: "prov-2026-028",
			File:     "",
			Commit:   "HEAD",
			Message:  "prov-2026-028 is already implemented - cannot commit with this ID. Create a new record or supersede this one.",
		},
	}

	formatter.FormatCheckResult(violations, nil, "HEAD")

	output := buf.String()

	// Should contain the record ID
	if !strings.Contains(output, "prov-2026-028") {
		t.Errorf("Expected output to contain record ID 'prov-2026-028', got: %s", output)
	}

	// Should display the message, not an empty bullet
	if !strings.Contains(output, "already implemented") {
		t.Errorf("Expected output to contain 'already implemented' message, got: %s", output)
	}

	// Should NOT show an empty bullet ("·" followed by nothing or whitespace)
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "·") && strings.TrimSpace(strings.TrimPrefix(line, "·")) == "" {
			t.Errorf("Found empty bullet point in output: %s", output)
		}
	}
}

func TestFormatCheckResult_FileViolation(t *testing.T) {
	// Test that violations with non-empty File fields display with bullet points
	var buf bytes.Buffer
	formatter := NewFormatter(&buf, false)

	violations := []Violation{
		{
			RecordID: "prov-2026-001",
			File:     "src/auth.go",
			Commit:   "HEAD",
			Message:  "src/auth.go is not in scope",
		},
	}

	formatter.FormatCheckResult(violations, nil, "HEAD")

	output := buf.String()

	// Should contain the record ID and "forbids changes to"
	if !strings.Contains(output, "prov-2026-001 forbids changes to") {
		t.Errorf("Expected output to contain 'prov-2026-001 forbids changes to', got: %s", output)
	}

	// Should show the file with a bullet point
	if !strings.Contains(output, "· src/auth.go") {
		t.Errorf("Expected output to contain bullet point with file '· src/auth.go', got: %s", output)
	}
}

func TestFormatCheckResult_MixedViolations(t *testing.T) {
	// Test both empty and non-empty File violations together
	var buf bytes.Buffer
	formatter := NewFormatter(&buf, false)

	violations := []Violation{
		{
			RecordID: "prov-2026-001",
			File:     "src/auth.go",
			Commit:   "HEAD",
			Message:  "src/auth.go is not in scope",
		},
		{
			RecordID: "prov-2026-028",
			File:     "",
			Commit:   "HEAD",
			Message:  "prov-2026-028 is already implemented - cannot commit with this ID. Create a new record or supersede this one.",
		},
	}

	formatter.FormatCheckResult(violations, nil, "HEAD")

	output := buf.String()

	// Both record IDs should be present
	if !strings.Contains(output, "prov-2026-001") {
		t.Errorf("Expected output to contain 'prov-2026-001', got: %s", output)
	}
	if !strings.Contains(output, "prov-2026-028") {
		t.Errorf("Expected output to contain 'prov-2026-028', got: %s", output)
	}

	// Should contain the message for the implemented record
	if !strings.Contains(output, "already implemented") {
		t.Errorf("Expected output to contain 'already implemented' message, got: %s", output)
	}

	// Should contain the file with bullet for the scope violation
	if !strings.Contains(output, "· src/auth.go") {
		t.Errorf("Expected output to contain bullet point '· src/auth.go', got: %s", output)
	}
}

func TestFormatCheckResult_NoViolations(t *testing.T) {
	var buf bytes.Buffer
	formatter := NewFormatter(&buf, false)

	formatter.FormatCheckResult(nil, nil, "HEAD")

	output := buf.String()

	// Should show success message
	if !strings.Contains(output, "No forbidden scope violations") {
		t.Errorf("Expected output to contain 'No forbidden scope violations', got: %s", output)
	}
}

func TestFormatCheckResult_NoRecordIDViolation(t *testing.T) {
	// Test violations with empty RecordID (e.g., missing commit tag)
	var buf bytes.Buffer
	formatter := NewFormatter(&buf, false)

	violations := []Violation{
		{
			RecordID: "",
			File:     "",
			Commit:   "staged",
			Message:  "Commit tag required but no provenance ID found in message",
		},
	}

	formatter.FormatCheckResult(violations, nil, "staged")

	output := buf.String()

	// Should display the message directly
	if !strings.Contains(output, "Commit tag required") {
		t.Errorf("Expected output to contain 'Commit tag required' message, got: %s", output)
	}
}
