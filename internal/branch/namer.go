package branch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/core"
	"loop-o-matic/internal/opencode"
)

var validBranch = regexp.MustCompile(`^[a-z]+/[a-z0-9]+(?:-[a-z0-9]+)*$`)

func Generate(ctx context.Context, cfg config.ExecutorConfig, loop *core.Loop, repoName, ticketText string) (string, error) {
	prompt := Prompt(loop, repoName, ticketText)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var lastRaw string
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		name, raw, err := runNamePrompt(ctx, cfg, loop, prompt)
		lastRaw = raw
		if err != nil {
			return "", fmt.Errorf("opencode agent failed to generate branch name (attempt %d/%d): %w\nAgent output:\n%s", attempt+1, maxAttempts, err, raw)
		}
		if name == "" || !Valid(name) {
			if attempt < maxAttempts-1 {
				continue
			}
			return "", fmt.Errorf("agent generated invalid branch name: %q (attempt %d/%d)\nAgent output:\n%s", name, attempt+1, maxAttempts, raw)
		}
		_ = os.WriteFile(filepath.Join(loop.RunDir, repoName+"-branch-name.txt"), []byte(name+"\n"), 0o644)
		return name, nil
	}
	return "", fmt.Errorf("failed to generate branch name after %d attempts\nLast agent output:\n%s", maxAttempts, lastRaw)
}

func runNamePrompt(ctx context.Context, cfg config.ExecutorConfig, loop *core.Loop, prompt string) (string, string, error) {
	res, err := opencode.New(cfg).Run(ctx, opencode.RunRequest{Dir: loop.RunDir, Title: loop.IssueKey + " branch name", Prompt: prompt})
	debugPath := filepath.Join(loop.RunDir, "branch-name-raw-output.txt")
	_ = os.WriteFile(debugPath, []byte(res.Stdout), 0o644)
	if err != nil {
		return "", res.Stdout, err
	}
	raw := firstLine(res.Stdout)
	return Sanitize(raw), raw, nil
}

func Prompt(loop *core.Loop, repoName, ticketText string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Generate exactly one git branch name for this development task.\n")
	fmt.Fprintf(&b, "CRITICAL: You MUST return a non-empty branch name. Never return empty output.\n")
	fmt.Fprintf(&b, "Return only the branch name. No markdown. No explanation. No quotes. Just the branch name.\n\n")
	fmt.Fprintf(&b, "Repository: %s\n", repoName)
	fmt.Fprintf(&b, "Issue/task id: %s\n", loop.IssueKey)
	fmt.Fprintf(&b, "Summary: %s\n", loop.Summary)
	fmt.Fprintf(&b, "Task context:\n%s\n\n", ticketText)
	fmt.Fprintf(&b, "The user request may be written in Spanish or another language. Translate and summarize the meaning in English. Never copy non-English words from the request into the branch name.\n\n")
	fmt.Fprintf(&b, "Rules:\n")
	fmt.Fprintf(&b, "- Include the work type: feature, fix, chore, docs, refactor, test, perf, ci, build.\n")
	fmt.Fprintf(&b, "- Use conventional commit type as the prefix.\n")
	fmt.Fprintf(&b, "- Use feat for new behavior, new API surface, new callbacks/listeners, new integration points, or behavior that users/integrators can observe.\n")
	fmt.Fprintf(&b, "- Use fix only for bug fixes.\n")
	fmt.Fprintf(&b, "- Use refactor only when the change is purely internal and does not change behavior, public API, integration contracts, callbacks, listeners, or tests' expected behavior.\n")
	fmt.Fprintf(&b, "- When unsure between feat and refactor, choose feat.\n")
	fmt.Fprintf(&b, "- Include the corresponding ticket/story id if it exists. Use lower case. For local TASK-* ids, include the task id in lower case.\n")
	fmt.Fprintf(&b, "- Include a short descriptive summary in English, in imperative present tense.\n")
	fmt.Fprintf(&b, "- Do not copy the user's raw request text when it is not English.\n")
	fmt.Fprintf(&b, "- Use hyphens for separating words.\n")
	fmt.Fprintf(&b, "- Use this exact format: {type}/{jira_ticket}-{title_description}\n")
	fmt.Fprintf(&b, "- Use only lowercase letters, digits, hyphens, and one slash after the type.\n")
	fmt.Fprintf(&b, "- Keep it concise.\n\n")
	fmt.Fprintf(&b, "Examples:\nfeat/mobile-1234-collect-network-api-error\nfix/sup-1234-sr-masking-not-working\n")
	return b.String()
}

func Sanitize(value string) string {
	value = firstLine(value)
	value = strings.Trim(value, "` \t\r\n")
	value = strings.ToLower(value)
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	prefix := slug(parts[0])
	rest := slug(parts[1])
	if prefix == "" || rest == "" {
		return ""
	}
	return prefix + "/" + rest
}

func Valid(value string) bool {
	return validBranch.MatchString(value)
}

func slug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastHyphen := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ">") {
			continue
		}
		if strings.HasPrefix(line, "```") {
			continue
		}
		if !strings.Contains(line, "/") {
			continue
		}
		return line
	}
	return ""
}
