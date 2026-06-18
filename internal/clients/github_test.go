package clients

import (
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
