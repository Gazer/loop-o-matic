package clients

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/run"
)

type GitHub struct {
	cli string
}

func NewGitHub(cfg config.GitHubConfig) GitHub {
	return GitHub{cli: cfg.CLI}
}

func (g GitHub) CreatePR(ctx context.Context, repoPath, base, head, title, body string) (string, int, error) {
	args := []string{"pr", "create", "--base", base, "--title", title, "--body", body, "--assignee", "@me"}
	if head != "" {
		args = append(args, "--head", head)
	}
	res, err := run.Command(ctx, repoPath, nil, g.cli, args...)
	if err != nil {
		return "", 0, err
	}
	url := strings.TrimSpace(res.Stdout)
	if url == "" {
		url = strings.TrimSpace(res.Stderr)
	}
	number := parsePRNumber(url)
	return url, number, nil
}

type PRStatus struct {
	ReviewDecision string
	ChecksState    string
	MergeState     string
	Mergeable      string
	FailingChecks  []FailingCheck
	Feedback       []PRFeedback
}

type FailingCheck struct {
	Name       string
	State      string
	Conclusion string
	Link       string
}

type PRFeedback struct {
	Kind      string
	Author    string
	State     string
	Body      string
	URL       string
	Path      string
	Line      string
	CreatedAt string
}

func (g GitHub) PRStatus(ctx context.Context, repoPath string, number int) (PRStatus, error) {
	if number <= 0 {
		return PRStatus{}, fmt.Errorf("pr number is required")
	}
	res, err := run.Command(ctx, repoPath, nil, g.cli, "pr", "view", strconv.Itoa(number), "--json", "reviewDecision,statusCheckRollup,comments,reviews,latestReviews,mergeStateStatus,mergeable")
	if err != nil {
		return PRStatus{}, err
	}
	var raw struct {
		ReviewDecision    string            `json:"reviewDecision"`
		StatusCheckRollup []json.RawMessage `json:"statusCheckRollup"`
		Comments          []json.RawMessage `json:"comments"`
		Reviews           []json.RawMessage `json:"reviews"`
		LatestReviews     []json.RawMessage `json:"latestReviews"`
		MergeStateStatus  string            `json:"mergeStateStatus"`
		Mergeable         string            `json:"mergeable"`
	}
	if err := json.Unmarshal([]byte(res.Stdout), &raw); err != nil {
		return PRStatus{}, err
	}

	feedback := prFeedback(raw.Comments, raw.Reviews, raw.LatestReviews)
	seen := map[string]bool{}
	for _, fb := range feedback {
		key := fb.Kind + "|" + fb.Author + "|" + fb.State + "|" + fb.Body + "|" + fb.URL
		seen[key] = true
	}

	// Fetch inline review/code comments using the GitHub API
	owner, repo := gitRemoteOwnerRepo(ctx, repoPath)
	if owner != "" && repo != "" {
		apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments", owner, repo, number)
		apiRes, err := run.Command(ctx, repoPath, nil, g.cli, "api", apiPath)
		if err == nil {
			var apiComments []map[string]any
			if err := json.Unmarshal([]byte(apiRes.Stdout), &apiComments); err == nil {
				for _, item := range apiComments {
					author := ""
					if userMap, ok := item["user"].(map[string]any); ok {
						author = fmt.Sprint(firstNonEmpty(userMap["login"], userMap["name"]))
					}
					if author == "" {
						author = fmt.Sprint(item["user"])
					}
					fb := PRFeedback{
						Kind:      "code-comment",
						Author:    author,
						State:     fmt.Sprint(item["author_association"]),
						Body:      strings.TrimSpace(fmt.Sprint(item["body"])),
						URL:       fmt.Sprint(item["html_url"]),
						Path:      fmt.Sprint(item["path"]),
						Line:      fmt.Sprint(firstNonEmpty(item["line"], item["original_line"])),
						CreatedAt: fmt.Sprint(item["created_at"]),
					}
					appendFeedback(&feedback, seen, fb)
				}
			}
		}
	}

	return PRStatus{ReviewDecision: raw.ReviewDecision, ChecksState: checksState(raw.StatusCheckRollup), MergeState: raw.MergeStateStatus, Mergeable: raw.Mergeable, FailingChecks: failingChecks(raw.StatusCheckRollup), Feedback: feedback}, nil
}

func (s PRStatus) IsOutOfDate() bool {
	state := strings.ToUpper(strings.TrimSpace(s.MergeState))
	mergeable := strings.ToUpper(strings.TrimSpace(s.Mergeable))
	return state == "BEHIND" || state == "DIRTY" || (state == "BLOCKED" && strings.Contains(mergeable, "BEHIND"))
}

func checksState(checks []json.RawMessage) string {
	if len(checks) == 0 {
		return "unknown"
	}
	for _, raw := range checks {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			return "unknown"
		}
		conclusion := strings.ToUpper(fmt.Sprint(item["conclusion"]))
		if conclusion == "FAILURE" || conclusion == "CANCELLED" || conclusion == "TIMED_OUT" || conclusion == "ACTION_REQUIRED" {
			return strings.ToLower(conclusion)
		}
		state := strings.ToUpper(fmt.Sprint(firstNonEmpty(item["state"], item["status"])))
		if state == "PENDING" || state == "QUEUED" || state == "IN_PROGRESS" || state == "EXPECTED" || state == "REQUESTED" {
			return "pending"
		}
		if conclusion == "SUCCESS" || conclusion == "NEUTRAL" || conclusion == "SKIPPED" || state == "SUCCESS" || state == "COMPLETED" || state == "PASSING" {
			continue
		}
		if state == "" && conclusion == "" {
			return "unknown"
		}
		return strings.ToLower(state)
	}
	return "success"
}

func failingChecks(checks []json.RawMessage) []FailingCheck {
	var failing []FailingCheck
	for _, raw := range checks {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		conclusion := strings.ToUpper(fmt.Sprint(item["conclusion"]))
		state := strings.ToUpper(fmt.Sprint(firstNonEmpty(item["state"], item["status"])))
		if !isFailingCheckState(state, conclusion) {
			continue
		}
		failing = append(failing, FailingCheck{
			Name:       fmt.Sprint(firstNonEmpty(item["name"], item["workflowName"], item["context"])),
			State:      state,
			Conclusion: conclusion,
			Link:       fmt.Sprint(firstNonEmpty(item["detailsUrl"], item["targetUrl"], item["link"], item["url"])),
		})
	}
	return failing
}

func isFailingCheckState(state, conclusion string) bool {
	switch conclusion {
	case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
		return true
	}
	switch state {
	case "FAILURE", "FAILED", "ERROR", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
		return true
	}
	return false
}

func prFeedback(comments, reviews, latestReviews []json.RawMessage) []PRFeedback {
	var feedback []PRFeedback
	seen := map[string]bool{}
	for _, raw := range comments {
		item := decodeMap(raw)
		fb := feedbackFromMap("comment", item)
		appendFeedback(&feedback, seen, fb)
	}
	for _, raw := range reviews {
		item := decodeMap(raw)
		fb := feedbackFromMap("review", item)
		appendFeedback(&feedback, seen, fb)
	}
	for _, raw := range latestReviews {
		item := decodeMap(raw)
		fb := feedbackFromMap("latest_review", item)
		appendFeedback(&feedback, seen, fb)
	}
	return feedback
}

func HumanFeedback(feedback []PRFeedback) []PRFeedback {
	var human []PRFeedback
	for _, fb := range feedback {
		if isBotAuthor(fb.Author) {
			continue
		}
		if strings.TrimSpace(fb.Body) == "" {
			continue
		}
		human = append(human, fb)
	}
	return human
}

func isBotAuthor(author string) bool {
	author = strings.ToLower(strings.TrimSpace(author))
	if author == "" || author == "<nil>" {
		return true
	}
	isBot := author == "bot" ||
		strings.HasSuffix(author, "[bot]") ||
		strings.HasSuffix(author, "-bot") ||
		strings.HasPrefix(author, "bot-") ||
		strings.HasSuffix(author, "_bot") ||
		strings.HasPrefix(author, "bot_") ||
		strings.Contains(author, "bot-") ||
		strings.Contains(author, "-bot")

	isCI := author == "ci" ||
		strings.HasSuffix(author, "-ci") ||
		strings.HasPrefix(author, "ci-") ||
		strings.HasSuffix(author, "_ci") ||
		strings.HasPrefix(author, "ci_") ||
		strings.HasSuffix(author, "[ci]") ||
		strings.Contains(author, "-ci-") ||
		strings.Contains(author, "_ci_")

	isLoop := strings.Contains(author, "loop-o-matic")

	return isBot || isCI || isLoop
}

func decodeMap(raw json.RawMessage) map[string]any {
	var item map[string]any
	_ = json.Unmarshal(raw, &item)
	return item
}

func feedbackFromMap(kind string, item map[string]any) PRFeedback {
	author := ""
	if authorMap, ok := item["author"].(map[string]any); ok {
		author = fmt.Sprint(firstNonEmpty(authorMap["login"], authorMap["name"]))
	}
	if author == "" || author == "<nil>" {
		author = fmt.Sprint(firstNonEmpty(item["author"], item["user"]))
	}
	return PRFeedback{
		Kind:      kind,
		Author:    author,
		State:     fmt.Sprint(firstNonEmpty(item["state"], item["authorAssociation"])),
		Body:      strings.TrimSpace(fmt.Sprint(firstNonEmpty(item["body"], item["bodyText"]))),
		URL:       fmt.Sprint(firstNonEmpty(item["url"], item["htmlUrl"])),
		Path:      fmt.Sprint(firstNonEmpty(item["path"], item["file"])),
		Line:      fmt.Sprint(firstNonEmpty(item["line"], item["originalLine"])),
		CreatedAt: fmt.Sprint(firstNonEmpty(item["createdAt"], item["submittedAt"])),
	}
}

func appendFeedback(feedback *[]PRFeedback, seen map[string]bool, fb PRFeedback) {
	if fb.Body == "" || fb.Body == "<nil>" {
		return
	}
	key := fb.Kind + "|" + fb.Author + "|" + fb.State + "|" + fb.Body + "|" + fb.URL
	if seen[key] {
		return
	}
	seen[key] = true
	*feedback = append(*feedback, fb)
}

func firstNonEmpty(values ...any) any {
	for _, value := range values {
		if fmt.Sprint(value) != "" && fmt.Sprint(value) != "<nil>" {
			return value
		}
	}
	return ""
}

func parsePRNumber(url string) int {
	parts := strings.Split(strings.TrimSpace(url), "/")
	if len(parts) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(parts[len(parts)-1])
	return n
}

func gitRemoteOwnerRepo(ctx context.Context, repoPath string) (string, string) {
	res, err := run.Command(ctx, repoPath, nil, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", ""
	}
	url := strings.TrimSpace(res.Stdout)
	url = strings.TrimSuffix(url, ".git")
	if strings.HasPrefix(url, "git@") {
		parts := strings.SplitN(url, ":", 2)
		if len(parts) == 2 {
			pathParts := strings.Split(parts[1], "/")
			if len(pathParts) >= 2 {
				return pathParts[0], pathParts[1]
			}
		}
	}
	if idx := strings.Index(url, "github.com/"); idx != -1 {
		pathParts := strings.Split(url[idx+len("github.com/"):], "/")
		if len(pathParts) >= 2 {
			return pathParts[0], pathParts[1]
		}
	}
	return "", ""
}
