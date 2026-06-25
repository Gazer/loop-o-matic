package clients

import (
	"errors"
	"testing"
)

func TestIsPushRetryable(t *testing.T) {
	err := errors.New("failed: non-fast-forward tip of your current branch is behind")
	if !isPushRetryable(err) {
		t.Fatal("expected non-fast-forward error to be detected")
	}

	err = errors.New("rejected (stale info)")
	if !isPushRetryable(err) {
		t.Fatal("expected stale info error to be detected")
	}
}

func TestMergeConflictError(t *testing.T) {
	err := &MergeConflictError{
		Conflicts: []string{"main.go", "utils.go"},
		Output:    "Conflict in main.go",
	}
	expected := "git merge failed with conflicts on files: main.go, utils.go"
	if err.Error() != expected {
		t.Fatalf("expected: %q, got: %q", expected, err.Error())
	}
}

func TestBareBranchRef(t *testing.T) {
	if got := bareBranchRef("main"); got != "refs/heads/main" {
		t.Fatalf("expected refs/heads/main, got %q", got)
	}
}
