package metadata

import (
	"strings"
	"testing"

	"loop-o-matic/internal/core"
)

func TestFallbackTitleDoesNotCopySpanishRequest(t *testing.T) {
	meta := Fallback(&core.Loop{IssueKey: "TASK-20260616-013722", Summary: "quiero poder configurar los keybindings de cada accion y que no sean estaticos"}, core.RepoRun{}, "github-copilot/gemini-3.5-flash", Evidence{})
	if meta.Title != "feat: configure action keybindings" {
		t.Fatalf("unexpected title fallback: %s", meta.Title)
	}
	if !ValidTitle(meta.Title) {
		t.Fatalf("fallback title should be valid: %s", meta.Title)
	}
}

func TestConventionalTypePrefersFeatForCallbacks(t *testing.T) {
	typeName := conventionalType("Create SessionReplayUrlCallback", Evidence{ImplementationSummary: "Added listener support for session replay URLs."})
	if typeName != "feat" {
		t.Fatalf("expected feat, got %s", typeName)
	}
}

func TestFallbackPRBodyDoesNotExposeLocalPaths(t *testing.T) {
	evidence := Evidence{
		ImplementationSummary: "Moved BaseProfiler to core and added SessionReplayUrlCallback listener support.",
		VerificationSummary:   "Ran ./gradlew test and ./gradlew lint successfully.",
		DiffStat:              "3 files changed, 120 insertions(+), 20 deletions(-)",
	}
	meta := Fallback(&core.Loop{IssueKey: "MOBILE-17686", Summary: "Create SessionReplayUrlCallback", RunDir: "/Users/me/.loop-o-matic/runs/MOBILE-17686", PlanPath: "/Users/me/.loop-o-matic/runs/MOBILE-17686/plan.md"}, core.RepoRun{RepoName: "android-sdk"}, "github-copilot/gemini-3.5-flash", evidence)
	if strings.Contains(meta.PRBody, "/Users/me") || strings.Contains(meta.PRBody, "implementation-summary.md") || strings.Contains(meta.PRBody, "verification-summary.md") {
		t.Fatalf("fallback PR body exposed local paths: %s", meta.PRBody)
	}
	if !strings.Contains(meta.PRBody, "Moved BaseProfiler to core") {
		t.Fatalf("fallback PR body should include human-readable implementation detail: %s", meta.PRBody)
	}
}

func TestEnsurePRFooterAddsCoauthorAndModel(t *testing.T) {
	body := EnsurePRFooter("## Summary\n- Test", "github-copilot/gemini-3.5-flash")
	if !strings.Contains(body, "Co-authored-by: loop-o-matic") {
		t.Fatalf("missing coauthor footer: %s", body)
	}
	if !strings.Contains(body, "Generated-with: github-copilot/gemini-3.5-flash") {
		t.Fatalf("missing model footer: %s", body)
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
