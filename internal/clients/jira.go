package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/run"
)

type JiraIssue struct {
	Key     string
	Raw     string
	Summary string
}

type Jira struct {
	cfg config.JiraConfig
}

func NewJira(cfg config.JiraConfig) Jira {
	return Jira{cfg: cfg}
}

func (j Jira) ViewIssue(ctx context.Context, issueKey string) (JiraIssue, error) {
	cmd := []string{j.cli(), "jira", "workitem", "view", issueKey, "--json", "--fields", "key,issuetype,summary,status,assignee,description,comment"}
	res, err := run.Command(ctx, "", nil, cmd[0], cmd[1:]...)
	if err != nil {
		return JiraIssue{}, err
	}
	raw := strings.TrimSpace(res.Stdout)
	if raw == "" {
		raw = strings.TrimSpace(res.Stderr)
	}
	if looksLikeCLIHelp(raw) {
		return JiraIssue{}, fmt.Errorf("jira command returned help/usage instead of issue content; check jira.commands.view_issue config")
	}
	if !strings.Contains(strings.ToLower(raw), strings.ToLower(issueKey)) {
		return JiraIssue{}, fmt.Errorf("jira output does not appear to contain issue %s", issueKey)
	}
	return JiraIssue{Key: issueKey, Raw: raw, Summary: SummaryFromRaw(raw)}, nil
}

func (j Jira) AddComment(ctx context.Context, issueKey, body string) error {
	cmd := []string{j.cli(), "jira", "workitem", "comment", "create", "--key", issueKey, "--body", body}
	_, err := run.Command(ctx, "", nil, cmd[0], cmd[1:]...)
	return err
}

func (j Jira) cli() string {
	if j.cfg.CLI == "" {
		return "acli"
	}
	return j.cfg.CLI
}

func looksLikeCLIHelp(raw string) bool {
	lower := strings.ToLower(raw)
	return strings.Contains(lower, "usage:") && strings.Contains(lower, "available commands")
}

func SummaryFromRaw(raw string) string {
	var parsed struct {
		Fields struct {
			Summary string `json:"summary"`
		} `json:"fields"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.Fields.Summary)
}
