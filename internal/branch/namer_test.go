package branch

import "testing"

func TestFirstLineIgnoresAgentMetadata(t *testing.T) {
	input := "> build · mimo-v2.5\n\nfeat/task-20260624-085024-expandable-menu\n"
	got := firstLine(input)
	if got != "feat/task-20260624-085024-expandable-menu" {
		t.Fatalf("expected branch name, got %q", got)
	}
}

func TestFirstLineIgnoresLeadingEmptyLines(t *testing.T) {
	input := "\n\n\nfeat/task-1-add-feature\n"
	got := firstLine(input)
	if got != "feat/task-1-add-feature" {
		t.Fatalf("expected branch name, got %q", got)
	}
}

func TestFirstLineIgnoresMarkdownFences(t *testing.T) {
	input := "```\nfeat/task-1-add-feature\n```"
	got := firstLine(input)
	if got != "feat/task-1-add-feature" {
		t.Fatalf("expected branch name, got %q", got)
	}
}

func TestFirstLineReturnsEmptyOnEmptyInput(t *testing.T) {
	got := firstLine("")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestFirstLineReturnsEmptyOnOnlyMetadata(t *testing.T) {
	input := "> build · mimo-v2.5\n> some other line\n"
	got := firstLine(input)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestFirstLineWithRealAgentOutput(t *testing.T) {
	input := `> build · mimo-v2.5

feat/task-20260624-085024-expandable-menu`
	got := firstLine(input)
	if got != "feat/task-20260624-085024-expandable-menu" {
		t.Fatalf("expected branch name, got %q", got)
	}
}

func TestSanitizeStripsAgentMetadata(t *testing.T) {
	input := "> build · mimo-v2.5\n\nfeat/task-20260624-085024-expandable-menu\n"
	got := Sanitize(input)
	if got != "feat/task-20260624-085024-expandable-menu" {
		t.Fatalf("expected clean branch name, got %q", got)
	}
}

func TestSanitizeStripsMarkdownFences(t *testing.T) {
	input := "```\nfeat/task-1-add-feature\n```"
	got := Sanitize(input)
	if got != "feat/task-1-add-feature" {
		t.Fatalf("expected clean branch name, got %q", got)
	}
}

func TestFirstLineIgnoresPluginInitialized(t *testing.T) {
	input := "Plugin initialized!\nfeat/task-20260624-085024-consolidate-actions-into-expandable-menu\n"
	got := firstLine(input)
	if got != "feat/task-20260624-085024-consolidate-actions-into-expandable-menu" {
		t.Fatalf("expected branch name, got %q", got)
	}
}

func TestFirstLineIgnoresLinesWithoutSlash(t *testing.T) {
	input := "some random text\nanother line without slash\nfeat/task-1-add-feature\n"
	got := firstLine(input)
	if got != "feat/task-1-add-feature" {
		t.Fatalf("expected branch name, got %q", got)
	}
}

func TestSanitizeWithPluginInitialized(t *testing.T) {
	input := "Plugin initialized!\nfeat/task-20260624-085024-consolidate-actions-into-expandable-menu\n"
	got := Sanitize(input)
	if got != "feat/task-20260624-085024-consolidate-actions-into-expandable-menu" {
		t.Fatalf("expected clean branch name, got %q", got)
	}
}
