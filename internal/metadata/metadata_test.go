package metadata

import (
	"strings"
	"testing"
)

func TestEnsurePRFooterAddsCoauthorAndModel(t *testing.T) {
	body := EnsurePRFooter("## Summary\n- Test", "github-copilot/gemini-3.5-flash")
	if !strings.Contains(body, "Co-authored-by: loop-o-matic") {
		t.Fatalf("missing coauthor footer: %s", body)
	}
	if !strings.Contains(body, "Generated-with: github-copilot/gemini-3.5-flash") {
		t.Fatalf("missing model footer: %s", body)
	}
}

func TestEnsureCommitFooter(t *testing.T) {
	// Case 1: Empty body should be replaced by footer only
	body1 := EnsureCommitFooter("", "github-copilot/gemini-3.5-flash", "TASK-20260617-103512")
	expected1 := "Co-authored-by: loop-o-matic\nGenerated-with: github-copilot/gemini-3.5-flash"
	if body1 != expected1 {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected1, body1)
	}

	// Case 2: Useful descriptive body should keep the description and append the footer
	body2 := EnsureCommitFooter("Configure shortcut keybindings for each action.", "github-copilot/gemini-3.5-flash", "TASK-20260617-103512")
	expected2 := "Configure shortcut keybindings for each action.\n\nCo-authored-by: loop-o-matic\nGenerated-with: github-copilot/gemini-3.5-flash"
	if body2 != expected2 {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected2, body2)
	}
}

func TestParseFencedJSON(t *testing.T) {
	meta, err := parse("```json\n{\"title\":\"feat: configure action keybindings\",\"commit_body\":\"Body\",\"pr_body\":\"PR\"}\n```")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "feat: configure action keybindings" {
		t.Fatalf("unexpected title: %s", meta.Title)
	}
}
