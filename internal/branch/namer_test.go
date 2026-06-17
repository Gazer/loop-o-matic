package branch

import "testing"

func TestFallbackDoesNotCopySpanishRequest(t *testing.T) {
	branch := Fallback("TASK-20260616-013722", "quiero poder configurar los keybindings de cada accion y que no sean estaticos")
	if branch != "feat/task-20260616-013722-configure-action-keybindings" {
		t.Fatalf("unexpected branch fallback: %s", branch)
	}
}

func TestFallbackAvoidsGenericRequestedChange(t *testing.T) {
	branch := Fallback("TASK-1", "mejora como se generan los nombres de branch")
	if branch == "feat/task-1-implement-requested-change" {
		t.Fatal("generic fallback should not be used")
	}
	if branch != "feat/task-1-improve-branch-naming" {
		t.Fatalf("unexpected branch fallback: %s", branch)
	}
}
