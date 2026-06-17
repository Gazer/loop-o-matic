package clients

import "testing"

func TestLooksLikeCLIHelp(t *testing.T) {
	raw := "Jira Cloud commands.\n\nUsage:\n  acli jira [command]\n\nAvailable Commands:\n  workitem  Jira work item commands."
	if !looksLikeCLIHelp(raw) {
		t.Fatal("expected help output to be detected")
	}
}

func TestLooksLikeCLIHelpAllowsJSON(t *testing.T) {
	raw := `{"key":"SDK-123","fields":{"summary":"Usage tracking"}}`
	if looksLikeCLIHelp(raw) {
		t.Fatal("expected JSON issue output not to be detected as help")
	}
}

func TestIssueSummary(t *testing.T) {
	raw := `{"key":"SDK-123","fields":{"summary":"Usage tracking"}}`
	if got := SummaryFromRaw(raw); got != "Usage tracking" {
		t.Fatalf("unexpected summary: %q", got)
	}
}
