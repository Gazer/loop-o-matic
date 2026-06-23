package clients

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"testing"
)

func TestFailingChecks(t *testing.T) {
	checks := []json.RawMessage{
		json.RawMessage(`{"name":"Unit Tests","conclusion":"FAILURE","detailsUrl":"https://example.test/run/1"}`),
		json.RawMessage(`{"name":"Lint","conclusion":"SUCCESS","detailsUrl":"https://example.test/run/2"}`),
	}
	failing := failingChecks(checks)
	if len(failing) != 1 {
		t.Fatalf("expected 1 failing check, got %d", len(failing))
	}
	if failing[0].Name != "Unit Tests" || failing[0].Link != "https://example.test/run/1" {
		t.Fatalf("unexpected failing check: %+v", failing[0])
	}
}

func TestPRFeedback(t *testing.T) {
	comments := []json.RawMessage{
		json.RawMessage(`{"author":{"login":"reviewer"},"body":"Please rename this callback.","url":"https://example.test/comment"}`),
	}
	reviews := []json.RawMessage{
		json.RawMessage(`{"author":{"login":"lead"},"state":"CHANGES_REQUESTED","body":"Add tests for multiple listeners.","url":"https://example.test/review"}`),
	}
	feedback := prFeedback(comments, reviews, nil)
	if len(feedback) != 2 {
		t.Fatalf("expected 2 feedback items, got %d", len(feedback))
	}
	if feedback[0].Author != "reviewer" || feedback[0].Body != "Please rename this callback." {
		t.Fatalf("unexpected comment feedback: %+v", feedback[0])
	}
	if feedback[1].State != "CHANGES_REQUESTED" {
		t.Fatalf("unexpected review feedback: %+v", feedback[1])
	}
}

func TestHumanFeedbackFiltersBots(t *testing.T) {
	feedback := []PRFeedback{
		{Author: "contentsquare-ci", Body: "bot summary"},
		{Author: "renovate[bot]", Body: "bot"},
		{Author: "copilot-swe-agent", Body: "copilot resolved"},
		{Author: "github-copilot", Body: "copilot summary"},
		{Author: "ricardo-markiewicz-cs", Body: "please change this"},
		{Author: "lucia", Body: "lucia is human"},
		{Author: "patricia", Body: "patricia is human"},
		{Author: "francisco", Body: "francisco is human"},
	}
	human := HumanFeedback(feedback)
	if len(human) != 4 {
		t.Fatalf("expected 4 human feedback items, got %d", len(human))
	}
	if human[0].Author != "ricardo-markiewicz-cs" || human[1].Author != "lucia" || human[2].Author != "patricia" || human[3].Author != "francisco" {
		t.Fatalf("unexpected human feedback list: %+v", human)
	}
}

func TestPRStatusIsOutOfDate(t *testing.T) {
	if !((PRStatus{MergeState: "BEHIND"}).IsOutOfDate()) {
		t.Fatal("expected BEHIND PR to be out-of-date")
	}
	if (PRStatus{MergeState: "CLEAN"}).IsOutOfDate() {
		t.Fatal("expected CLEAN PR not to be out-of-date")
	}
	if !((PRStatus{MergeState: "BLOCKED", Mergeable: "conflict-BEHIND"}).IsOutOfDate()) {
		t.Fatal("expected BLOCKED + BEHIND mergeable PR to be out-of-date")
	}
	if (PRStatus{MergeState: "BLOCKED", Mergeable: "conflict-DIRTY"}).IsOutOfDate() {
		t.Fatal("expected BLOCKED + DIRTY mergeable PR not to be out-of-date")
	}
}

func TestExtractRunID(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"https://github.com/Gazer/caminante/actions/runs/28050091519/job/83038149112", "28050091519"},
		{"https://github.com/Gazer/loop-o-matic/actions/runs/12345678901/job/98765432109", "12345678901"},
		{"https://github.com/Gazer/caminante/actions/runs/111/job/222", "111"},
		{"https://example.com/no-run-id", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := ExtractRunID(tt.url)
		if result != tt.expected {
			t.Errorf("ExtractRunID(%q) = %q, want %q", tt.url, result, tt.expected)
		}
	}
}

func TestExtractLastNLines(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10"
	zipData := createTestZip(t, content)

	result := ExtractLastNLines(zipData, 3)
	lines := splitLines(result)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %q", len(lines), result)
	}
	if lines[0] != "line8" || lines[1] != "line9" || lines[2] != "line10" {
		t.Errorf("unexpected lines: %v", lines)
	}
}

func TestExtractLastNLinesMoreThanAvailable(t *testing.T) {
	content := "line1\nline2\nline3\n"
	zipData := createTestZip(t, content)

	result := ExtractLastNLines(zipData, 100)
	lines := splitLines(result)
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %q", len(lines), result)
	}
}

func TestExtractLastNLinesEmpty(t *testing.T) {
	zipData := createTestZip(t, "")
	result := ExtractLastNLines(zipData, 10)
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}

func TestExtractLastNLinesInvalidZip(t *testing.T) {
	result := ExtractLastNLines([]byte("not a zip"), 10)
	if result != "" {
		t.Errorf("expected empty result for invalid zip, got %q", result)
	}
}

func createTestZip(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("test.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	var result []string
	for _, line := range splitString(s, "\n") {
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func splitString(s, sep string) []string {
	var result []string
	for {
		idx := indexOf(s, sep)
		if idx == -1 {
			if s != "" {
				result = append(result, s)
			}
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	return result
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
