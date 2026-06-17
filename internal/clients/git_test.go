package clients

import (
	"errors"
	"testing"
)

func TestIsNonFastForward(t *testing.T) {
	err := errors.New("failed: non-fast-forward tip of your current branch is behind")
	if !isNonFastForward(err) {
		t.Fatal("expected non-fast-forward error to be detected")
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
