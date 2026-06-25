package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"loop-o-matic/internal/app"
	"loop-o-matic/internal/clients"
	"loop-o-matic/internal/config"
	"loop-o-matic/internal/core"
	"loop-o-matic/internal/daemon"
	"loop-o-matic/internal/doctor"
	"loop-o-matic/internal/logging"
	"loop-o-matic/internal/store"
	"loop-o-matic/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func RunLoopCLI(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return runTUI()
	}
	switch args[0] {
	case "doctor":
		cfg, path, err := config.Load()
		if err != nil {
			return err
		}
		return doctor.Run(ctx, cfg, path)
	case "start":
		return startLoop(ctx, args[1:])
	case "task":
		return taskLoop(ctx, args[1:])
	case "status":
		return statusLoop(ctx, args[1:])
	case "logs":
		return logsLoop(ctx, args[1:])
	case "events":
		return eventsLoop(ctx, args[1:])
	case "extra":
		return extraLoop(ctx, args[1:])
	case "delete", "rm":
		return deleteLoop(ctx, args[1:])
	case "pause", "resume", "cancel":
		return updateLoop(ctx, args[0], args[1:])
	default:
		handled, err := maybeRepoShortcut(ctx, args)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		return app.UsageError{Message: "unknown loop command: " + args[0]}
	}
}

func RunLoopdCLI(ctx context.Context, args []string) error {
	if len(args) == 0 {
		printLoopdUsage()
		return nil
	}
	switch args[0] {
	case "start":
		cfg, _, s, logger, cleanup, err := openRuntime()
		if err != nil {
			return err
		}
		defer cleanup()
		return daemon.Start(ctx, cfg, s, logger)
	case "attach":
		cfg, _, err := config.Load()
		if err != nil {
			return err
		}
		issue := ""
		if len(args) > 1 {
			issue = args[1]
		}
		return followLog(ctx, cfg.Workspace.LogRoot, issue)
	default:
		return app.UsageError{Message: "unknown loopd command: " + args[0]}
	}
}

func startLoop(ctx context.Context, args []string) error {
	parsed, err := parseStartArgs(args)
	if err != nil {
		return err
	}
	issueKey := parsed.issueKey
	cfg, _, s, logger, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()

	if err := validateRepoScope(cfg, parsed.repos); err != nil {
		return err
	}

	repoExtra := collectRepoExtras(cfg, parsed.repos)
	if repoExtra != "" {
		parsed.extra = appendExtra(repoExtra, parsed.extra)
	}

	jira := clients.NewJira(cfg.Jira)
	fmt.Printf("reading Jira issue %s\n", issueKey)
	issue, err := jira.ViewIssue(ctx, issueKey)
	if err != nil {
		return err
	}
	runID := fmt.Sprintf("%s-%s", issueKey, time.Now().Format("20060102-150405"))
	runDir := filepath.Join(cfg.Workspace.Root, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	ticketPath := filepath.Join(runDir, "ticket.txt")
	if err := os.WriteFile(ticketPath, []byte(issue.Raw), 0o644); err != nil {
		return err
	}
	extraPath := ""
	if strings.TrimSpace(parsed.extra) != "" {
		extraPath = filepath.Join(runDir, "extra-instructions.md")
		if err := os.WriteFile(extraPath, []byte(strings.TrimSpace(parsed.extra)+"\n"), 0o644); err != nil {
			return err
		}
	}
	loop := &core.Loop{
		IssueKey:              issueKey,
		Summary:               firstNonEmpty(issue.Summary, firstNonEmptyLine(issue.Raw)),
		Status:                core.StatusCreated,
		RunDir:                runDir,
		TicketPath:            ticketPath,
		ExtraInstructionsPath: extraPath,
		RepoScope:             strings.Join(parsed.repos, ","),
	}
	if err := s.CreateLoop(ctx, loop); err != nil {
		return err
	}
	logger.Info(ctx, loop, "created loop in %s", runDir)
	fmt.Printf("loop %s created: %s\n", issueKey, runDir)
	fmt.Println("run loopd start in another terminal to process it")
	return nil
}

type startArgs struct {
	issueKey string
	extra    string
	repos    []string
}

func parseStartArgs(args []string) (startArgs, error) {
	var parsed startArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--extra":
			if i+1 >= len(args) {
				return parsed, app.UsageError{Message: "--extra requires text"}
			}
			parsed.extra = appendExtra(parsed.extra, args[i+1])
			i++
		case strings.HasPrefix(arg, "--extra="):
			parsed.extra = appendExtra(parsed.extra, strings.TrimPrefix(arg, "--extra="))
		case arg == "--extra-file":
			if i+1 >= len(args) {
				return parsed, app.UsageError{Message: "--extra-file requires a path"}
			}
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				return parsed, err
			}
			parsed.extra = appendExtra(parsed.extra, string(data))
			i++
		case strings.HasPrefix(arg, "--extra-file="):
			data, err := os.ReadFile(strings.TrimPrefix(arg, "--extra-file="))
			if err != nil {
				return parsed, err
			}
			parsed.extra = appendExtra(parsed.extra, string(data))
		case arg == "--repo":
			if i+1 >= len(args) {
				return parsed, app.UsageError{Message: "--repo requires a repo name"}
			}
			parsed.repos = append(parsed.repos, args[i+1])
			i++
		case strings.HasPrefix(arg, "--repo="):
			parsed.repos = append(parsed.repos, strings.TrimPrefix(arg, "--repo="))
		case arg == "--repos":
			if i+1 >= len(args) {
				return parsed, app.UsageError{Message: "--repos requires comma-separated repo names"}
			}
			parsed.repos = append(parsed.repos, splitRepos(args[i+1])...)
			i++
		case strings.HasPrefix(arg, "--repos="):
			parsed.repos = append(parsed.repos, splitRepos(strings.TrimPrefix(arg, "--repos="))...)
		case strings.HasPrefix(arg, "--"):
			return parsed, app.UsageError{Message: "unknown start flag: " + arg}
		default:
			if parsed.issueKey != "" {
				return parsed, app.UsageError{Message: "usage: loop start ISSUE-123 --repo NAME [--repo NAME2 ...] [--extra text] [--extra-file path]"}
			}
			parsed.issueKey = arg
		}
	}
	parsed.repos = uniqueStrings(parsed.repos)
	if parsed.issueKey == "" {
		return parsed, app.UsageError{Message: "usage: loop start ISSUE-123 --repo NAME [--repo NAME2 ...] [--extra text] [--extra-file path]"}
	}
	if len(parsed.repos) == 0 {
		return parsed, app.UsageError{Message: "usage: loop start ISSUE-123 --repo NAME [--repo NAME2 ...] [--extra text] [--extra-file path]\nAt least one repository must be specified."}
	}
	return parsed, nil
}

func collectRepoExtras(cfg *config.Config, repoNames []string) string {
	var extras []string
	for _, name := range repoNames {
		if repo, ok := cfg.Repos[name]; ok {
			for _, extra := range repo.Extras {
				if trimmed := strings.TrimSpace(extra); trimmed != "" {
					extras = append(extras, fmt.Sprintf("[%s] %s", name, trimmed))
				}
			}
		}
	}
	switch len(extras) {
	case 0:
		return ""
	case 1:
		return extras[0]
	default:
		return strings.Join(extras, "\n\n")
	}
}

func appendExtra(existing, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return next
	}
	return strings.TrimSpace(existing) + "\n\n" + next
}

func taskLoop(ctx context.Context, args []string) error {
	parsed, err := parseTaskArgs(args)
	if err != nil {
		return err
	}
	cfg, _, s, logger, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()
	if err := validateRepoScope(cfg, parsed.repos); err != nil {
		return err
	}
	return createLocalTaskLoop(ctx, cfg, s, logger, parsed)
}

func maybeRepoShortcut(ctx context.Context, args []string) (bool, error) {
	if len(args) < 2 {
		return false, nil
	}
	cfg, _, s, logger, cleanup, err := openRuntime()
	if err != nil {
		return false, nil
	}
	defer cleanup()
	if _, ok := cfg.Repos[args[0]]; !ok {
		return false, nil
	}
	parsed := taskArgs{repos: []string{args[0]}, text: strings.Join(args[1:], " ")}
	return true, createLocalTaskLoop(ctx, cfg, s, logger, parsed)
}

type taskArgs struct {
	repos []string
	text  string
}

func parseTaskArgs(args []string) (taskArgs, error) {
	var parsed taskArgs
	var textParts []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--repo":
			if i+1 >= len(args) {
				return parsed, app.UsageError{Message: "--repo requires a repo name"}
			}
			parsed.repos = append(parsed.repos, args[i+1])
			i++
		case strings.HasPrefix(arg, "--repo="):
			parsed.repos = append(parsed.repos, strings.TrimPrefix(arg, "--repo="))
		case arg == "--repos":
			if i+1 >= len(args) {
				return parsed, app.UsageError{Message: "--repos requires comma-separated repo names"}
			}
			parsed.repos = append(parsed.repos, splitRepos(args[i+1])...)
			i++
		case strings.HasPrefix(arg, "--repos="):
			parsed.repos = append(parsed.repos, splitRepos(strings.TrimPrefix(arg, "--repos="))...)
		case strings.HasPrefix(arg, "--"):
			return parsed, app.UsageError{Message: "unknown task flag: " + arg}
		default:
			textParts = append(textParts, arg)
		}
	}
	parsed.repos = uniqueStrings(parsed.repos)
	parsed.text = strings.TrimSpace(strings.Join(textParts, " "))
	if len(parsed.repos) == 0 {
		return parsed, app.UsageError{Message: "usage: loop task --repo NAME [--repo NAME2 ...] <task text>\nAt least one repository must be specified."}
	}
	if parsed.text == "" {
		return parsed, app.UsageError{Message: "usage: loop task --repo NAME [--repo NAME2 ...] <task text>"}
	}
	return parsed, nil
}

func createLocalTaskLoop(ctx context.Context, cfg *config.Config, s *store.Store, logger *logging.Logger, parsed taskArgs) error {
	issueKey := fmt.Sprintf("TASK-%s", time.Now().Format("20060102-150405"))
	runDir := filepath.Join(cfg.Workspace.Root, issueKey)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	scopeText := "auto: all configured repos"
	if len(parsed.repos) > 0 {
		scopeText = strings.Join(parsed.repos, ", ")
	}
	ticket := fmt.Sprintf("Local task %s\n\nRequested work:\n%s\n\nRepo scope:\n%s\n", issueKey, parsed.text, scopeText)
	ticketPath := filepath.Join(runDir, "ticket.txt")
	if err := os.WriteFile(ticketPath, []byte(ticket), 0o644); err != nil {
		return err
	}
	extraPath := ""
	if repoExtra := collectRepoExtras(cfg, parsed.repos); repoExtra != "" {
		extraPath = filepath.Join(runDir, "extra-instructions.md")
		if err := os.WriteFile(extraPath, []byte(repoExtra+"\n"), 0o644); err != nil {
			return err
		}
	}
	loop := &core.Loop{
		IssueKey:              issueKey,
		Summary:               parsed.text,
		Status:                core.StatusCreated,
		RunDir:                runDir,
		TicketPath:            ticketPath,
		ExtraInstructionsPath: extraPath,
		RepoScope:             strings.Join(parsed.repos, ","),
	}
	if err := s.CreateLoop(ctx, loop); err != nil {
		return err
	}
	logger.Info(ctx, loop, "created local task loop in %s", runDir)
	fmt.Printf("task loop %s created: %s\n", issueKey, runDir)
	fmt.Printf("repo scope: %s\n", scopeText)
	fmt.Println("run loopd start in another terminal to process it")
	return nil
}

func validateRepoScope(cfg *config.Config, repos []string) error {
	for _, repo := range repos {
		if _, ok := cfg.Repos[repo]; !ok {
			return app.UsageError{Message: fmt.Sprintf("unknown repo %q. configured repos: %s", repo, strings.Join(sortedRepoNames(cfg.Repos), ", "))}
		}
	}
	return nil
}

func splitRepos(value string) []string {
	parts := strings.Split(value, ",")
	repos := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			repos = append(repos, part)
		}
	}
	return repos
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func sortedRepoNames(repos map[string]config.RepoConfig) []string {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func statusLoop(ctx context.Context, args []string) error {
	cfg, _, s, _, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()
	_ = cfg
	if len(args) == 1 && args[0] == "--all" {
		loops, err := s.ListLoops(ctx)
		if err != nil {
			return err
		}
		for _, loop := range loops {
			fmt.Printf("%-12s %-24s %s\n", loop.IssueKey, loop.Status, loop.UpdatedAt.Local().Format(time.RFC3339))
		}
		return nil
	}
	if len(args) != 1 {
		return app.UsageError{Message: "usage: loop status ISSUE-123 | loop status --all"}
	}
	loop, err := s.GetLatestLoopByIssue(ctx, args[0])
	if store.IsNotFound(err) {
		return store.FormatNotFound(args[0])
	}
	if err != nil {
		return err
	}
	repairLoopSummary(ctx, s, loop)
	fmt.Printf("issue:     %s\n", loop.IssueKey)
	fmt.Printf("status:    %s\n", loop.Status)
	fmt.Printf("summary:   %s\n", loop.Summary)
	fmt.Printf("retries:   %d\n", loop.AutoRetryCount)
	fmt.Printf("run dir:   %s\n", loop.RunDir)
	fmt.Printf("ticket:    %s\n", loop.TicketPath)
	if loop.RepoScope != "" {
		fmt.Printf("repos:     %s\n", loop.RepoScope)
	}
	if loop.PlanPath != "" {
		fmt.Printf("plan:      %s\n", loop.PlanPath)
	}
	if loop.LastError != "" {
		fmt.Printf("error:     %s\n", loop.LastError)
	}
	repos, err := s.RepoRuns(ctx, loop.ID)
	if err != nil {
		return err
	}
	if len(repos) > 0 {
		fmt.Println("repos:")
		for _, repo := range repos {
			fmt.Printf("  %-18s impact=%d changed=%t pr=%s checks=%s review=%s\n", repo.RepoName, repo.ImpactScore, repo.Changed, repo.PRURL, repo.CIState, repo.ReviewDecision)
		}
	}
	return nil
}

func logsLoop(ctx context.Context, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return app.UsageError{Message: "usage: loop logs ISSUE-123 [--follow]"}
	}
	follow := len(args) == 2 && args[1] == "--follow"
	if len(args) == 2 && !follow {
		return app.UsageError{Message: "usage: loop logs ISSUE-123 [--follow]"}
	}
	cfg, _, err := config.Load()
	if err != nil {
		return err
	}
	if follow {
		return followLog(ctx, cfg.Workspace.LogRoot, args[0])
	}
	return printLog(cfg.Workspace.LogRoot, args[0])
}

func eventsLoop(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return app.UsageError{Message: "usage: loop events ISSUE-123"}
	}
	_, _, s, _, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()
	events, err := s.Events(ctx, args[0], 200)
	if err != nil {
		return err
	}
	for _, event := range events {
		fmt.Printf("%s [%s] %s\n", event.CreatedAt.Local().Format(time.RFC3339), strings.ToUpper(event.Level), event.Message)
	}
	return nil
}

func updateLoop(ctx context.Context, command string, args []string) error {
	if len(args) != 1 {
		return app.UsageError{Message: "usage: loop " + command + " ISSUE-123"}
	}
	_, _, s, logger, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()
	loop, err := s.GetLatestLoopByIssue(ctx, args[0])
	if store.IsNotFound(err) {
		return store.FormatNotFound(args[0])
	}
	if err != nil {
		return err
	}
	status := core.StatusPaused
	if command == "resume" {
		if loop.LastActiveStatus != "" && loop.LastActiveStatus != core.StatusPaused {
			status = loop.LastActiveStatus
			if status == core.StatusCreatingPRs {
				status = core.StatusImplementing
			}
		} else if loop.Status == core.StatusBlocked || loop.Status == core.StatusFailed {
			status = core.StatusImplementing
		} else {
			status = core.StatusCreated
			if _, err := os.Stat(filepath.Join(loop.RunDir, "verification-summary.md")); err == nil {
				status = core.StatusVerifying
			} else if loop.PlanPath != "" {
				status = core.StatusImplementing
			}
		}
	}
	if command == "cancel" {
		status = core.StatusCancelled
	}
	if err := s.UpdateLoopStatus(ctx, loop.ID, status, ""); err != nil {
		return err
	}
	if command == "resume" {
		_ = s.ResetAutoRetryCount(ctx, loop.ID)
	}
	logger.Info(ctx, loop, "%s loop", command)
	fmt.Printf("%s -> %s\n", loop.IssueKey, status)
	return nil
}

func deleteLoop(ctx context.Context, args []string) error {
	if len(args) < 1 || len(args) > 3 {
		return app.UsageError{Message: "usage: loop delete ISSUE-123 [--force] [--keep-workspace]"}
	}
	issueKey := args[0]
	force := false
	keepWorkspace := false
	for _, arg := range args[1:] {
		switch arg {
		case "--force":
			force = true
		case "--keep-workspace":
			keepWorkspace = true
		default:
			return app.UsageError{Message: "unknown delete flag: " + arg}
		}
	}
	cfg, _, s, logger, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()
	loop, err := s.GetLatestLoopByIssue(ctx, issueKey)
	if store.IsNotFound(err) {
		return store.FormatNotFound(issueKey)
	}
	if err != nil {
		return err
	}
	if !force && !core.IsTerminalStatus(loop.Status) && loop.Status != core.StatusPaused {
		return app.UsageError{Message: fmt.Sprintf("loop %s is %s; cancel/pause it first or use --force", loop.IssueKey, loop.Status)}
	}
	// Update status to cancelled first, so daemon stops processing it
	_ = s.UpdateLoopStatus(ctx, loop.ID, core.StatusCancelled, "Deleted by user")
	time.Sleep(500 * time.Millisecond) // Give daemon a chance to stop

	repos, err := s.RepoRuns(ctx, loop.ID)
	if err != nil {
		return err
	}
	git := clients.Git{}
	for _, repo := range repos {
		if repo.Path == "" {
			continue
		}
		if !force {
			changed, err := git.HasChanges(ctx, repo.Path)
			if err == nil && changed {
				return app.UsageError{Message: fmt.Sprintf("repo %s has uncommitted changes; use --force to delete", repo.RepoName)}
			}
		}
		if !keepWorkspace {
			if err := git.RemoveWorktree(ctx, repo.Path, force); err != nil {
				if force {
					logger.Error(ctx, loop, "failed to remove worktree %s: %v", repo.RepoName, err)
				} else {
					return fmt.Errorf("failed to remove worktree %s; retry with --force to discard changes: %w", repo.RepoName, err)
				}
			}
		}
	}
	logPath := logPath(cfg.Workspace.LogRoot, issueKey)
	if err := s.DeleteLoop(ctx, loop.ID, loop.IssueKey); err != nil {
		return err
	}
	if !keepWorkspace {
		_ = os.RemoveAll(loop.RunDir)
	}
	_ = os.Remove(logPath)
	fmt.Printf("deleted loop %s\n", issueKey)
	return nil
}

func extraLoop(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return app.UsageError{Message: "usage: loop extra ISSUE-123 <text> | loop extra ISSUE-123 --file path"}
	}
	issueKey := args[0]
	extra, err := parseExtraArgs(args[1:])
	if err != nil {
		return err
	}
	_, _, s, logger, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()
	loop, err := s.GetLatestLoopByIssue(ctx, issueKey)
	if store.IsNotFound(err) {
		return store.FormatNotFound(issueKey)
	}
	if err != nil {
		return err
	}
	path := loop.ExtraInstructionsPath
	if strings.TrimSpace(path) == "" {
		path = filepath.Join(loop.RunDir, "extra-instructions.md")
		if err := s.UpdateLoopExtraInstructionsPath(ctx, loop.ID, path); err != nil {
			return err
		}
		loop.ExtraInstructionsPath = path
	}
	entry := fmt.Sprintf("\n\n## %s\n\n%s\n", time.Now().Format(time.RFC3339), strings.TrimSpace(extra))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(entry); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	logger.Info(ctx, loop, "added extra instructions")
	fmt.Printf("extra instructions appended to %s\n", path)
	return nil
}

func parseExtraArgs(args []string) (string, error) {
	var parts []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--file" || arg == "--extra-file":
			if i+1 >= len(args) {
				return "", app.UsageError{Message: arg + " requires a path"}
			}
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				return "", err
			}
			parts = append(parts, string(data))
			i++
		case strings.HasPrefix(arg, "--file="):
			data, err := os.ReadFile(strings.TrimPrefix(arg, "--file="))
			if err != nil {
				return "", err
			}
			parts = append(parts, string(data))
		case strings.HasPrefix(arg, "--"):
			return "", app.UsageError{Message: "unknown extra flag: " + arg}
		default:
			parts = append(parts, arg)
		}
	}
	extra := strings.TrimSpace(strings.Join(parts, " "))
	if extra == "" {
		return "", app.UsageError{Message: "extra instructions cannot be empty"}
	}
	return extra, nil
}

func openRuntime() (*config.Config, string, *store.Store, *logging.Logger, func(), error) {
	cfg, path, err := config.Load()
	if err != nil {
		return nil, "", nil, nil, nil, err
	}
	s, err := store.Open(cfg.Workspace.StatePath)
	if err != nil {
		return nil, "", nil, nil, nil, err
	}
	logger, err := logging.New(s, cfg.Workspace.LogRoot)
	if err != nil {
		s.Close()
		return nil, "", nil, nil, nil, err
	}
	cleanup := func() {
		_ = logger.Close()
		_ = s.Close()
	}
	return cfg, path, s, logger, cleanup, nil
}

func printLog(logRoot, issue string) error {
	path := logPath(logRoot, issue)
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(os.Stdout, f)
	return err
}

func followLog(ctx context.Context, logRoot, issue string) error {
	path := logPath(logRoot, issue)
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		f, err := os.Open(path)
		if err == nil {
			stat, statErr := f.Stat()
			if statErr == nil && stat.Size() < offset {
				offset = 0
			}
			if _, seekErr := f.Seek(offset, io.SeekStart); seekErr == nil {
				n, _ := io.Copy(os.Stdout, f)
				offset += n
			}
			_ = f.Close()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func logPath(logRoot, issue string) string {
	if issue == "" {
		return filepath.Join(logRoot, "loopd.log")
	}
	return filepath.Join(logRoot, issue+".log")
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 120 {
				return line[:120]
			}
			return line
		}
	}
	return ""
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

func repairLoopSummary(ctx context.Context, s *store.Store, loop *core.Loop) {
	if loop == nil || !summaryLooksBroken(loop.Summary) {
		return
	}
	data, err := os.ReadFile(loop.TicketPath)
	if err != nil {
		return
	}
	summary := clients.SummaryFromRaw(string(data))
	if strings.TrimSpace(summary) == "" {
		return
	}
	loop.Summary = summary
	_ = s.UpdateLoopSummary(ctx, loop.ID, summary)
}

func summaryLooksBroken(summary string) bool {
	summary = strings.TrimSpace(summary)
	return summary == "" || summary == "{" || summary == "[" || summary == "null"
}

func runTUI() error {
	cfg, _, s, logger, cleanup, err := openRuntime()
	if err != nil {
		return err
	}
	defer cleanup()
	_ = logger

	p := tea.NewProgram(tui.New(cfg, s), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui error: %w", err)
	}
	return nil
}

func printLoopUsage() {
	fmt.Println("usage: loop <doctor|start|task|status|logs|events|pause|resume|cancel|delete>")
	fmt.Println("       loop start ISSUE-123 --repo NAME [--repo NAME2 ...] [--extra text] [--extra-file path]")
	fmt.Println("       loop extra ISSUE-123 <text> | loop extra ISSUE-123 --file path")
	fmt.Println("       loop task --repo NAME [--repo NAME2 ...] <task text>")
	fmt.Println("       loop <repo-name> <task text>")
	fmt.Println("       loop delete ISSUE-123 [--force] [--keep-workspace]")
}

func printLoopdUsage() {
	fmt.Println("usage: loopd <start|attach>")
}
