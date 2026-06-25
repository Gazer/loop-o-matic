package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/core"
	"loop-o-matic/internal/opencode"
	"loop-o-matic/internal/run"
)

var conventionalTitle = regexp.MustCompile(`^(feat|fix|chore|docs|refactor|test|perf|ci|build)(\([a-z0-9-]+\))?: .+`)

type Metadata struct {
	Title      string `json:"title"`
	CommitBody string `json:"commit_body"`
	PRBody     string `json:"pr_body"`
	Model      string `json:"model"`
}

func Generate(ctx context.Context, cfg config.ExecutorConfig, loop *core.Loop, repo core.RepoRun, baseBranch, existingPRTitle, existingPRBody string) (Metadata, error) {
	evidence := CollectEvidence(ctx, loop, repo, baseBranch)
	prompt := Prompt(loop, repo, baseBranch, cfg.Model, existingPRTitle, existingPRBody, evidence)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	const maxAttempts = 3
	var lastOutput string
	for attempt := 0; attempt < maxAttempts; attempt++ {
		res, err := opencode.New(cfg).Run(ctx, opencode.RunRequest{Dir: repo.Path, Title: loop.IssueKey + " commit pr metadata", Prompt: prompt})
		lastOutput = res.Stdout
		if err != nil {
			return Metadata{}, fmt.Errorf("opencode agent failed to generate PR metadata (attempt %d/%d): %w\nAgent output:\n%s", attempt+1, maxAttempts, err, res.Stdout)
		}
		meta, err := parse(res.Stdout)
		if err != nil {
			if attempt < maxAttempts-1 {
				continue
			}
			return Metadata{}, fmt.Errorf("failed to parse PR metadata from agent output (attempt %d/%d): %w\nAgent output:\n%s", attempt+1, maxAttempts, err, res.Stdout)
		}
		meta = sanitize(meta)
		if !ValidTitle(meta.Title) {
			if attempt < maxAttempts-1 {
				continue
			}
			return Metadata{}, fmt.Errorf("agent generated invalid conventional title: %q (attempt %d/%d)\nAgent output:\n%s", meta.Title, attempt+1, maxAttempts, res.Stdout)
		}
		if strings.TrimSpace(meta.PRBody) == "" {
			if attempt < maxAttempts-1 {
				continue
			}
			return Metadata{}, fmt.Errorf("agent generated empty PR body (attempt %d/%d)\nAgent output:\n%s", attempt+1, maxAttempts, res.Stdout)
		}
		if strings.TrimSpace(meta.CommitBody) == "" {
			if attempt < maxAttempts-1 {
				continue
			}
			return Metadata{}, fmt.Errorf("agent generated empty commit body (attempt %d/%d)\nAgent output:\n%s", attempt+1, maxAttempts, res.Stdout)
		}
		meta.Model = cfg.Model
		meta.CommitBody = EnsureCommitFooter(meta.CommitBody, cfg.Model, loop.IssueKey)
		meta.PRBody = EnsurePRFooter(meta.PRBody, cfg.Model)
		write(loop, repo, meta)
		return meta, nil
	}
	return Metadata{}, fmt.Errorf("failed to generate PR metadata after %d attempts\nLast agent output:\n%s", maxAttempts, lastOutput)
}

type Evidence struct {
	TicketSummary         string
	ExtraInstructions     string
	Plan                  string
	ImplementationSummary string
	VerificationSummary   string
	DiffStat              string
	Diff                  string
}

func CollectEvidence(ctx context.Context, loop *core.Loop, repo core.RepoRun, baseBranch string) Evidence {
	rangeSpec := baseBranch + "...HEAD"
	return Evidence{
		TicketSummary:         readText(loop.TicketPath, 12000),
		ExtraInstructions:     readText(loop.ExtraInstructionsPath, 12000),
		Plan:                  readText(loop.PlanPath, 12000),
		ImplementationSummary: readText(filepath.Join(loop.RunDir, "implementation-summary.md"), 16000),
		VerificationSummary:   readText(filepath.Join(loop.RunDir, "verification-summary.md"), 16000),
		DiffStat:              firstNonEmpty(gitOutput(ctx, repo.Path, "diff", "--stat", rangeSpec), gitOutput(ctx, repo.Path, "show", "--stat", "--format=medium", "HEAD")),
		Diff:                  firstNonEmpty(gitOutput(ctx, repo.Path, "diff", "--find-renames", "--find-copies", rangeSpec), gitOutput(ctx, repo.Path, "show", "--find-renames", "--find-copies", "--format=medium", "HEAD")),
	}
}

func Prompt(loop *core.Loop, repo core.RepoRun, baseBranch, model, existingPRTitle, existingPRBody string, evidence Evidence) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Generate commit and pull request metadata for this repository change.\n")
	fmt.Fprintf(&b, "CRITICAL: You MUST return valid JSON with non-empty title, commit_body, and pr_body fields. Never return empty output.\n")
	fmt.Fprintf(&b, "Return JSON only. No markdown. No explanation. No quotes around the JSON.\n\n")
	fmt.Fprintf(&b, "Required JSON shape:\n{\"title\":\"...\",\"commit_body\":\"...\",\"pr_body\":\"...\"}\n\n")
	fmt.Fprintf(&b, "Repository: %s\n", repo.RepoName)
	fmt.Fprintf(&b, "Repo path: %s\n", repo.Path)
	fmt.Fprintf(&b, "Base branch: %s\n", baseBranch)
	fmt.Fprintf(&b, "Issue/task id: %s\n", loop.IssueKey)
	fmt.Fprintf(&b, "Summary: %s\n", loop.Summary)
	fmt.Fprintf(&b, "Important: the user request may be in Spanish or another language. Translate and summarize the actual change in English. Never copy non-English words from the request into title, commit_body, or pr_body.\n")
	fmt.Fprintf(&b, "Model used to generate code: %s\n\n", model)
	if strings.TrimSpace(existingPRTitle) != "" || strings.TrimSpace(existingPRBody) != "" {
		fmt.Fprintf(&b, "Existing PR metadata already exists and should be re-evaluated against the current change. Treat the PR title as stable context for the ticket; focus on updating the PR body to reflect the full accumulated change set.\n")
		writeSection(&b, "Current PR Title", existingPRTitle)
		writeSection(&b, "Current PR Body", existingPRBody)
	}
	fmt.Fprintf(&b, "Evidence to use when writing human-readable metadata:\n")
	writeSection(&b, "Ticket / Task", evidence.TicketSummary)
	writeSection(&b, "Additional User Instructions", evidence.ExtraInstructions)
	writeSection(&b, "Plan", evidence.Plan)
	writeSection(&b, "Implementation Summary", evidence.ImplementationSummary)
	writeSection(&b, "Verification Summary", evidence.VerificationSummary)
	writeSection(&b, "Git Diff Stat", evidence.DiffStat)
	writeSection(&b, "Git Diff Summary", evidence.Diff)
	fmt.Fprintf(&b, "Rules for title:\n")
	fmt.Fprintf(&b, "- Follow Conventional Commits exactly: type(optional-scope): short description.\n")
	fmt.Fprintf(&b, "- Allowed types: feat, fix, chore, docs, refactor, test, perf, ci, build.\n")
	fmt.Fprintf(&b, "- Always write it in English.\n")
	fmt.Fprintf(&b, "- Do not copy the raw user request if it is not English; summarize the implemented change in English.\n")
	fmt.Fprintf(&b, "- Use the implementation summary to generate the short description. 70 chars or less.\n")
	fmt.Fprintf(&b, "- Use imperative present tense.\n")
	fmt.Fprintf(&b, "- Use feat for new behavior, new API surface, new callbacks/listeners, new integration points, or behavior that users/integrators can observe.\n")
	fmt.Fprintf(&b, "- Use fix only for bug fixes.\n")
	fmt.Fprintf(&b, "- Use refactor only when the change is purely internal and does not change behavior, public API, integration contracts, callbacks, listeners, or tests' expected behavior.\n")
	fmt.Fprintf(&b, "- When unsure between feat and refactor, choose feat.\n")
	fmt.Fprintf(&b, "- Keep the title concise and readable.\n")
	fmt.Fprintf(&b, "- Do not include markdown in title.\n\n")
	fmt.Fprintf(&b, "Rules for commit_body:\n")
	fmt.Fprintf(&b, "- Always write it in English.\n")
	fmt.Fprintf(&b, "- 1-4 concise lines explaining what changed and why.\n")
	fmt.Fprintf(&b, "- Do not include closing/referencing keywords like \"Fixes\", \"Addresses\", \"Closes\", \"Implements\", \"Addressed\", or \"Resolved\" followed by the ticket/task ID.\n")
	fmt.Fprintf(&b, "- End with this footer exactly, preserving the model value: Co-authored-by: loop-o-matic\nGenerated-with: %s\n\n", model)
	fmt.Fprintf(&b, "Rules for pr_body:\n")
	fmt.Fprintf(&b, "- Always write it in English.\n")
	fmt.Fprintf(&b, "- Be detailed and extensive enough for a reviewer to understand the full change without opening every file.\n")
	fmt.Fprintf(&b, "- Include these sections: Summary, Detailed Changes, Verification, Risks / Notes.\n")
	fmt.Fprintf(&b, "- Detailed Changes should list meaningful behavior/code changes, grouped by area/file when possible.\n")
	fmt.Fprintf(&b, "- Verification should summarize commands/results from the provided Verification Summary evidence.\n")
	fmt.Fprintf(&b, "- Do not include local filesystem paths in the PR body. The reviewer cannot access local files.\n")
	fmt.Fprintf(&b, "- Do not include closing/referencing keywords like \"Fixes\", \"Addresses\", \"Closes\", \"Implements\", \"Addressed\", or \"Resolved\" followed by the ticket/task ID in the summary or footer.\n")
	fmt.Fprintf(&b, "- Convert all evidence into prose intended for a human reviewer.\n")
	fmt.Fprintf(&b, "- End with this footer exactly, preserving the model value: Co-authored-by: loop-o-matic\nGenerated-with: %s\n", model)
	fmt.Fprintf(&b, "\nInspect the diff and summaries before answering.\n")
	return b.String()
}

func ValidTitle(title string) bool {
	return conventionalTitle.MatchString(strings.TrimSpace(title))
}

func parse(output string) (Metadata, error) {
	output = strings.TrimSpace(output)
	output = strings.TrimPrefix(output, "```json")
	output = strings.TrimPrefix(output, "```")
	output = strings.TrimSuffix(output, "```")
	output = strings.TrimSpace(output)
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start == -1 || end == -1 || end < start {
		return Metadata{}, fmt.Errorf("metadata output did not contain JSON")
	}
	var meta Metadata
	if err := json.Unmarshal([]byte(output[start:end+1]), &meta); err != nil {
		return Metadata{}, err
	}
	return meta, nil
}

func sanitize(meta Metadata) Metadata {
	meta.Title = strings.TrimSpace(strings.ReplaceAll(meta.Title, "\n", " "))
	meta.CommitBody = strings.TrimSpace(meta.CommitBody)
	meta.PRBody = strings.TrimSpace(meta.PRBody)
	return meta
}

func EnsurePRFooter(body, model string) string {
	body = strings.TrimSpace(body)
	footer := fmt.Sprintf("Co-authored-by: loop-o-matic\nGenerated-with: %s", model)
	body = strings.TrimSpace(strings.ReplaceAll(body, "Co-authored-by: loop-o-matic", ""))
	lines := strings.Split(body, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Generated-with:") {
			continue
		}
		filtered = append(filtered, line)
	}
	body = strings.TrimSpace(strings.Join(filtered, "\n"))
	if body == "" {
		return footer
	}
	return body + "\n\n" + footer
}

func EnsureCommitFooter(body, model, issueKey string) string {
	body = strings.TrimSpace(body)
	footer := fmt.Sprintf("Co-authored-by: loop-o-matic\nGenerated-with: %s", model)

	if body == "" {
		return footer
	}

	body = strings.TrimSpace(strings.ReplaceAll(body, "Co-authored-by: loop-o-matic", ""))
	lines := strings.Split(body, "\n")
	filtered := lines[:0]
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Generated-with:") {
			continue
		}
		filtered = append(filtered, line)
	}
	body = strings.TrimSpace(strings.Join(filtered, "\n"))
	if body == "" {
		return footer
	}
	return body + "\n\n" + footer
}

func toJSON(meta Metadata) string {
	data, _ := json.MarshalIndent(meta, "", "  ")
	return string(data)
}

func write(loop *core.Loop, repo core.RepoRun, meta Metadata) {
	_ = os.WriteFile(filepath.Join(loop.RunDir, repo.RepoName+"-commit-pr-metadata.json"), []byte(toJSON(meta)+"\n"), 0o644)
}

func readText(path string, limit int) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if limit > 0 && len(text) > limit {
		return text[:limit] + "\n[truncated]"
	}
	return text
}

func gitOutput(ctx context.Context, dir string, args ...string) string {
	res, err := run.Command(ctx, dir, nil, "git", args...)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(res.Stdout)
	if len(text) > 20000 {
		return text[:20000] + "\n[truncated]"
	}
	return text
}

func writeSection(b *strings.Builder, title, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "Not available."
	}
	fmt.Fprintf(b, "\n--- %s ---\n%s\n", title, content)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
