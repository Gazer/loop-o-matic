package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"loop-o-matic/internal/branch"
	"loop-o-matic/internal/clients"
	"loop-o-matic/internal/config"
	"loop-o-matic/internal/core"
	"loop-o-matic/internal/logging"
	"loop-o-matic/internal/metadata"
	"loop-o-matic/internal/opencode"
	"loop-o-matic/internal/run"
	"loop-o-matic/internal/store"
)

type Engine struct {
	cfg          *config.Config
	store        *store.Store
	logger       *logging.Logger
	git          clients.Git
	gh           clients.GitHub
	jira         clients.Jira
	slots        chan struct{}
	pollingSlots chan struct{}
	running      map[int64]bool
	cancels      map[int64]context.CancelFunc
	mu           sync.Mutex
}

func NewEngine(cfg *config.Config, s *store.Store, logger *logging.Logger) *Engine {
	return &Engine{
		cfg:          cfg,
		store:        s,
		logger:       logger,
		git:          clients.Git{},
		gh:           clients.NewGitHub(cfg.GitHub),
		jira:         clients.NewJira(cfg.Jira),
		slots:        make(chan struct{}, cfg.Daemon.MaxRunningTasks),
		pollingSlots: make(chan struct{}, 10),
		running:      map[int64]bool{},
		cancels:      make(map[int64]context.CancelFunc),
	}
}

func (e *Engine) Tick(ctx context.Context) error {
	loops, err := e.store.ActiveLoops(ctx)
	if err != nil {
		return err
	}
	if len(loops) == 0 {
		e.logger.Debug(ctx, nil, "no active loops")
		return nil
	}
	var wg sync.WaitGroup
	for _, loop := range loops {
		if loop.Status == core.StatusPaused {
			continue
		}
		loop := loop
		if e.requiresLongExecutionSlot(loop.Status) {
			if e.isRunning(loop.ID) {
				continue
			}
			if e.tryAcquireSlot() {
				e.markRunning(loop.ID, true)
				loopCtx, cancel := context.WithCancel(ctx)
				e.registerCancel(loop.ID, cancel)
				go func() {
					defer func() {
						e.deregisterCancel(loop.ID)
						e.releaseSlot()
						e.markRunning(loop.ID, false)
					}()
					e.processLoopSafely(loopCtx, &loop)
				}()
			} else {
				e.logger.Debug(ctx, &loop, "execution slots full; leaving queued for next tick")
			}
			continue
		}
		if e.requiresPollingSlot(loop.Status) {
			if !e.tryAcquirePollingSlot() {
				e.logger.Debug(ctx, &loop, "polling slots full; skipping polling until next tick")
				continue
			}
			wg.Add(1)
			loopCtx, cancel := context.WithCancel(ctx)
			e.registerCancel(loop.ID, cancel)
			go func() {
				defer wg.Done()
				defer e.deregisterCancel(loop.ID)
				defer e.releasePollingSlot()
				e.processLoopSafely(loopCtx, &loop)
			}()
			continue
		}
		wg.Add(1)
		loopCtx, cancel := context.WithCancel(ctx)
		e.registerCancel(loop.ID, cancel)
		go func() {
			defer wg.Done()
			defer e.deregisterCancel(loop.ID)
			e.processLoopSafely(loopCtx, &loop)
		}()
	}
	wg.Wait()
	return nil
}

func (e *Engine) tryAcquireSlot() bool {
	select {
	case e.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *Engine) releaseSlot() {
	<-e.slots
}

func (e *Engine) tryAcquirePollingSlot() bool {
	if e.pollingSlots == nil {
		return true
	}
	select {
	case e.pollingSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (e *Engine) releasePollingSlot() {
	if e.pollingSlots != nil {
		<-e.pollingSlots
	}
}

func (e *Engine) isRunning(loopID int64) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running[loopID]
}

func (e *Engine) markRunning(loopID int64, running bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if running {
		e.running[loopID] = true
		return
	}
	delete(e.running, loopID)
}

func (e *Engine) processLoopSafely(ctx context.Context, loop *core.Loop) {
	defer func() {
		if r := recover(); r != nil {
			errStr := fmt.Sprintf("panic: %v", r)
			e.logger.Error(ctx, loop, "PANIC RECOVERED: %s", errStr)
			_ = e.setStatus(context.Background(), loop, core.StatusFailed, errStr)
		}
	}()
	if err := e.ProcessLoop(ctx, loop); err != nil {
		e.logger.Error(ctx, loop, "%v", err)
		_ = e.setStatus(context.Background(), loop, core.StatusFailed, err.Error())
	}
}

func (e *Engine) requiresLongExecutionSlot(status string) bool {
	switch status {
	case core.StatusCreated, core.StatusPreparingWorkspace, core.StatusDiscovering, core.StatusImplementing, core.StatusVerifying, core.StatusCreatingPRs:
		return true
	default:
		return false
	}
}

func (e *Engine) requiresPollingSlot(status string) bool {
	switch status {
	case core.StatusMonitoringCI, core.StatusWaitingHumanReview:
		return true
	default:
		return false
	}
}

func (e *Engine) ProcessLoop(ctx context.Context, loop *core.Loop) error {
	if loop != nil && loop.IssueKey == "PANIC-TRIGGER" {
		panic("intentional panic for testing recovery")
	}
	e.repairLoopSummary(ctx, loop)
	switch loop.Status {
	case core.StatusCreated, core.StatusPreparingWorkspace:
		return e.prepareWorkspace(ctx, loop)
	case core.StatusDiscovering:
		return e.discover(ctx, loop)
	case core.StatusImplementing:
		return e.implement(ctx, loop)
	case core.StatusVerifying:
		return e.verify(ctx, loop)
	case core.StatusCreatingPRs:
		return e.createPRs(ctx, loop)
	case core.StatusMonitoringCI, core.StatusWaitingHumanReview:
		return e.monitorPRs(ctx, loop)
	default:
		e.logger.Debug(ctx, loop, "no action for status %s", loop.Status)
		return nil
	}
}

func (e *Engine) repairLoopSummary(ctx context.Context, loop *core.Loop) {
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
	_ = e.store.UpdateLoopSummary(ctx, loop.ID, summary)
}

func summaryLooksBroken(summary string) bool {
	summary = strings.TrimSpace(summary)
	return summary == "" || summary == "{" || summary == "[" || summary == "null"
}

func (e *Engine) prepareWorkspace(ctx context.Context, loop *core.Loop) error {
	e.logger.Info(ctx, loop, "preparing workspace %s", loop.RunDir)
	if err := e.setStatus(ctx, loop, core.StatusPreparingWorkspace, ""); err != nil {
		return err
	}
	if err := os.MkdirAll(loop.RunDir, 0o755); err != nil {
		return err
	}
	ticketText := ""
	if data, err := os.ReadFile(loop.TicketPath); err == nil {
		ticketText = string(data)
	}

	names := e.repoNamesForLoop(loop)
	existingRepos, _ := e.store.RepoRuns(ctx, loop.ID)
	existingBranches := map[string]string{}
	for _, repo := range existingRepos {
		if repo.Branch != "" {
			existingBranches[repo.RepoName] = repo.Branch
		}
	}
	for _, name := range names {
		repoCfg := e.cfg.Repos[name]
		if strings.TrimSpace(repoCfg.Bare) == "" {
			return fmt.Errorf("repository %q has no 'bare' repository configured (check for typos like 'base' instead of 'bare')", name)
		}
		bare := clients.BarePath(e.cfg.Workspace.BareReposRoot, repoCfg.Bare)
		repoPath := filepath.Join(loop.RunDir, name)
		branchName := existingBranches[name]
		if branchName == "" {
			var err error
			branchName, err = branch.Generate(ctx, e.cfg.Executor, loop, name, ticketText)
			if err != nil {
				e.logger.Error(ctx, loop, "branch naming agent failed for %s, using fallback: %v", name, err)
			}
		}
		branch := branchName
		e.logger.Info(ctx, loop, "%s branch: %s", name, branch)
		needsWorktree, err := e.needsWorktree(ctx, repoPath)
		if err != nil {
			return err
		}
		if needsWorktree {
			if _, err := os.Stat(bare); os.IsNotExist(err) {
				remote := clients.GitHubRemote(repoCfg.GitHub)
				if remote == "" {
					return fmt.Errorf("bare repo %s does not exist and repo %s has no github remote configured", bare, name)
				}
				e.logger.Info(ctx, loop, "cloning bare repo for %s from %s", name, remote)
				if err := e.git.CloneBare(ctx, remote, bare); err != nil {
					return fmt.Errorf("clone bare %s: %w", name, err)
				}
			}
			e.logger.Info(ctx, loop, "fetching %s", name)
			if err := e.git.Fetch(ctx, bare); err != nil {
				return fmt.Errorf("fetch %s: %w", name, err)
			}
			baseRef, err := e.git.ResolveBaseRef(ctx, bare, repoCfg.DefaultBranch)
			if err != nil {
				return fmt.Errorf("resolve base ref %s: %w", name, err)
			}
			e.logger.Info(ctx, loop, "creating worktree for %s from %s", name, baseRef)
			if err := e.git.AddWorktree(ctx, bare, repoPath, branch, baseRef); err != nil {
				return fmt.Errorf("worktree %s: %w", name, err)
			}
		}
		if err := e.store.UpsertRepoRun(ctx, core.RepoRun{LoopID: loop.ID, RepoName: name, Path: repoPath, Branch: branch}); err != nil {
			return err
		}
	}
	return e.setStatus(ctx, loop, core.StatusDiscovering, "")
}

func (e *Engine) needsWorktree(ctx context.Context, repoPath string) (bool, error) {
	entries, err := os.ReadDir(repoPath)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if e.git.IsWorktree(ctx, repoPath) {
		return false, nil
	}
	if len(entries) == 0 {
		if err := os.Remove(repoPath); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, fmt.Errorf("%s exists but is not a git worktree; remove it or choose a new task", repoPath)
}

func (e *Engine) discover(ctx context.Context, loop *core.Loop) error {
	e.logger.Info(ctx, loop, "setting up plan for targeted repos")
	repos, err := e.store.RepoRuns(ctx, loop.ID)
	if err != nil {
		return err
	}
	var plan strings.Builder
	fmt.Fprintf(&plan, "# Plan for %s\n\n", loop.IssueKey)
	fmt.Fprintf(&plan, "## Target Repositories\n\n")
	for _, repo := range repos {
		repo.ImpactScore = 1
		if err := e.store.UpsertRepoRun(ctx, repo); err != nil {
			return err
		}
		fmt.Fprintf(&plan, "- %s (explicitly targeted)\n", repo.RepoName)
		e.logger.Info(ctx, loop, "%s: explicitly targeted", repo.RepoName)
	}
	fmt.Fprintf(&plan, "\n## Next action\n\n")
	fmt.Fprintf(&plan, "Run implementation agent from the loop workspace.\n")
	planPath := filepath.Join(loop.RunDir, "plan.md")
	if err := os.WriteFile(planPath, []byte(plan.String()), 0o644); err != nil {
		return err
	}
	if err := e.store.UpdateLoopPlan(ctx, loop.ID, planPath); err != nil {
		return err
	}
	return e.setStatus(ctx, loop, core.StatusImplementing, "")
}

func (e *Engine) implement(ctx context.Context, loop *core.Loop) error {
	e.logger.Info(ctx, loop, "running implementation agent")
	summaryPath := filepath.Join(loop.RunDir, "verification-summary.md")
	lastSummaryPath := filepath.Join(loop.RunDir, "verification-summary-last.md")
	_ = os.Remove(lastSummaryPath)
	if _, err := os.Stat(summaryPath); err == nil {
		_ = os.Rename(summaryPath, lastSummaryPath)
	}
	promptPath := filepath.Join(loop.RunDir, "implementation-prompt.md")
	prompt := e.implementationPrompt(ctx, loop)
	if err := e.runAgent(ctx, loop, "implementation", promptPath, prompt); err != nil {
		return err
	}
	return e.setStatus(ctx, loop, core.StatusVerifying, "")
}

func (e *Engine) runAgent(ctx context.Context, loop *core.Loop, phase, promptPath, prompt string) error {
	ctx, cancel := context.WithTimeout(ctx, e.cfg.Executor.Timeout.Duration)
	defer cancel()
	if err := os.WriteFile(promptPath, []byte(prompt), 0o644); err != nil {
		return err
	}
	env := []string{
		"LOOP_ISSUE_KEY=" + loop.IssueKey,
		"LOOP_WORKSPACE=" + loop.RunDir,
		"LOOP_TICKET_FILE=" + loop.TicketPath,
		"LOOP_PLAN_FILE=" + loop.PlanPath,
		"LOOP_PROMPT_FILE=" + promptPath,
		"LOOP_PHASE=" + phase,
	}
	cmd := e.cfg.Executor.Command
	if len(cmd) == 0 {
		res, err := opencode.New(e.cfg.Executor).Run(ctx, opencode.RunRequest{
			Dir:                    loop.RunDir,
			Title:                  loop.IssueKey + " " + phase,
			Prompt:                 prompt,
			Env:                    env,
			AutoApprovePermissions: e.cfg.Executor.AutoApprovePermissions != nil && *e.cfg.Executor.AutoApprovePermissions,
		})
		e.logger.Info(ctx, loop, "%s agent finished in %s", phase, res.Duration.Round(time.Second))
		writeCommandLog(loop.RunDir, phase+"-agent.log", res)
		if e.isStopped(context.Background(), loop) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s agent: %w", phase, err)
		}
		return nil
	}
	res, err := run.Command(ctx, loop.RunDir, env, cmd[0], cmd[1:]...)
	e.logger.Info(ctx, loop, "%s agent finished in %s", phase, res.Duration.Round(time.Second))
	writeCommandLog(loop.RunDir, phase+"-agent.log", res)
	if e.isStopped(context.Background(), loop) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s agent: %w", phase, err)
	}
	return nil
}

func (e *Engine) implementationPrompt(ctx context.Context, loop *core.Loop) string {
	repos, _ := e.store.RepoRuns(ctx, loop.ID)
	if strings.HasPrefix(loop.IssueKey, "TASK-") {
		return e.localTaskPrompt(loop, repos)
	}
	return e.jiraTaskPrompt(loop, repos)
}

func (e *Engine) jiraTaskPrompt(loop *core.Loop, repos []core.RepoRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are implementing Jira ticket %s in a multi-repo SDK workspace.\n\n", loop.IssueKey)
	fmt.Fprintf(&b, "Goal: produce a correct, tested code solution. Do not create commits, push branches, create PRs, merge, or comment on Jira; loopd will do those steps after you finish.\n\n")
	fmt.Fprintf(&b, "Inputs:\n")
	fmt.Fprintf(&b, "- Jira ticket snapshot: %s\n", loop.TicketPath)
	fmt.Fprintf(&b, "- Discovery plan: %s\n", loop.PlanPath)
	fmt.Fprintf(&b, "- Workspace root: %s\n\n", loop.RunDir)
	if loop.RepoScope != "" {
		fmt.Fprintf(&b, "Repo scope requested by the user: %s\n\n", loop.RepoScope)
	} else {
		fmt.Fprintf(&b, "Repo scope requested by the user: auto-discover across prepared repositories.\n\n")
	}
	if loop.LastError != "" {
		fmt.Fprintf(&b, "Current blocker/error to fix:\n%s\n\n", loop.LastError)
	}
	writeExtraInstructions(&b, loop)
	writeCIFailureEvidence(&b, loop.RunDir)
	writePRFeedbackEvidence(&b, loop.RunDir)
	writeVerificationFailureEvidence(&b, loop.RunDir)
	fmt.Fprintf(&b, "Repositories:\n")
	for _, repo := range repos {
		fmt.Fprintf(&b, "- %s: %s (impact score %d, changed=%t, pr=%s, checks=%s, review=%s)\n", repo.RepoName, repo.Path, repo.ImpactScore, repo.Changed, repo.PRURL, repo.CIState, repo.ReviewDecision)
	}
	fmt.Fprintf(&b, "\nUseful loop artifacts may exist in the workspace root, including implementation-agent.log, verification-agent.log, implementation-summary.md, and verification-summary.md. Read them when fixing failures.\n")
	fmt.Fprintf(&b, "\nInstructions:\n")
	fmt.Fprintf(&b, "1. Read the Jira snapshot and discovery plan first.\n")
	fmt.Fprintf(&b, "2. Inspect all relevant repositories before editing. SDK parity matters: if a public API or behavior changes in one SDK, check whether equivalent changes are needed in the other SDK repos.\n")
	fmt.Fprintf(&b, "3. Make the smallest correct code changes needed for the ticket.\n")
	fmt.Fprintf(&b, "4. Add or update tests in the affected repos when appropriate.\n")
	fmt.Fprintf(&b, "5. Run focused verification where feasible. A separate verification agent pass will also decide and run broader tests/builds before PR creation.\n")
	fmt.Fprintf(&b, "6. Do not rewrite, reset, squash, drop, amend, or delete existing commits on the loop branch when a PR/remote branch already exists. Add follow-up commits instead; loopd will squash at merge time if needed.\n")
	fmt.Fprintf(&b, "7. Leave changes uncommitted.\n")
	fmt.Fprintf(&b, "8. Write a concise implementation summary to %s.\n", filepath.Join(loop.RunDir, "implementation-summary.md"))
	fmt.Fprintf(&b, "\nIf the ticket is impossible or ambiguous, do not invent requirements. Write the blocker and evidence to implementation-summary.md and exit non-zero.\n")
	return b.String()
}

func (e *Engine) localTaskPrompt(loop *core.Loop, repos []core.RepoRun) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are working on a manually requested development task in a multi-repo workspace.\n\n")
	fmt.Fprintf(&b, "Task ID: %s\n", loop.IssueKey)
	fmt.Fprintf(&b, "User ask:\n%s\n\n", loop.Summary)
	fmt.Fprintf(&b, "Goal: produce a correct, tested code solution for the user's ask. Do not create commits, push branches, create PRs, merge, or comment externally; loopd will do those steps after you finish.\n\n")
	fmt.Fprintf(&b, "Inputs:\n")
	fmt.Fprintf(&b, "- Local task file: %s\n", loop.TicketPath)
	fmt.Fprintf(&b, "- Discovery plan: %s\n", loop.PlanPath)
	fmt.Fprintf(&b, "- Workspace root: %s\n\n", loop.RunDir)
	if loop.RepoScope != "" {
		fmt.Fprintf(&b, "Repo scope requested by the user: %s\n\n", loop.RepoScope)
	} else {
		fmt.Fprintf(&b, "Repo scope requested by the user: auto-discover across prepared repositories.\n\n")
	}
	if loop.LastError != "" {
		fmt.Fprintf(&b, "Current blocker/error to fix:\n%s\n\n", loop.LastError)
	}
	writeExtraInstructions(&b, loop)
	writeCIFailureEvidence(&b, loop.RunDir)
	writePRFeedbackEvidence(&b, loop.RunDir)
	writeVerificationFailureEvidence(&b, loop.RunDir)
	e.writeRepoContext(&b, repos)
	fmt.Fprintf(&b, "\nInstructions:\n")
	fmt.Fprintf(&b, "1. Read the local task file and discovery plan first.\n")
	fmt.Fprintf(&b, "2. Treat the user ask as the source of truth. Do not assume Jira-specific workflow, fields, or acceptance criteria.\n")
	fmt.Fprintf(&b, "3. Inspect the scoped repositories before editing. If no repo scope was provided, determine the affected repo or repos from code and config.\n")
	fmt.Fprintf(&b, "4. Make the smallest correct code changes needed for the ask.\n")
	fmt.Fprintf(&b, "5. Preserve SDK/API parity when the ask touches shared behavior or public API.\n")
	fmt.Fprintf(&b, "6. Add or update tests when appropriate.\n")
	fmt.Fprintf(&b, "7. Run focused verification where feasible. A separate verification agent pass will also decide and run broader tests/builds before PR creation.\n")
	fmt.Fprintf(&b, "8. Do not rewrite, reset, squash, drop, amend, or delete existing commits on the loop branch when a PR/remote branch already exists. Add follow-up commits instead; loopd will squash at merge time if needed.\n")
	fmt.Fprintf(&b, "9. Leave changes uncommitted.\n")
	fmt.Fprintf(&b, "10. Write a concise implementation summary to %s.\n", filepath.Join(loop.RunDir, "implementation-summary.md"))
	fmt.Fprintf(&b, "\nIf the ask is impossible or ambiguous, do not invent requirements. Write the blocker and evidence to implementation-summary.md and exit non-zero.\n")
	return b.String()
}

func (e *Engine) writeRepoContext(b *strings.Builder, repos []core.RepoRun) {
	fmt.Fprintf(b, "Repositories:\n")
	for _, repo := range repos {
		fmt.Fprintf(b, "- %s: %s (impact score %d, changed=%t, pr=%s, checks=%s, review=%s)\n", repo.RepoName, repo.Path, repo.ImpactScore, repo.Changed, repo.PRURL, repo.CIState, repo.ReviewDecision)
	}
	fmt.Fprintf(b, "\nUseful loop artifacts may exist in the workspace root, including implementation-agent.log, verification-agent.log, implementation-summary.md, and verification-summary.md. Read them when fixing failures.\n")
}

func writeCIFailureEvidence(b *strings.Builder, runDir string) {
	path := filepath.Join(runDir, "ci-failures.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return
	}
	if len(text) > 12000 {
		text = text[:12000] + "\n[truncated]"
	}
	fmt.Fprintf(b, "CI failure evidence from previous GitHub checks:\n%s\n\n", text)
	fmt.Fprintf(b, "You must use this CI failure evidence to guide the fix. Do not ignore it.\n\n")
}

func writeExtraInstructions(b *strings.Builder, loop *core.Loop) {
	if loop == nil || strings.TrimSpace(loop.ExtraInstructionsPath) == "" {
		return
	}
	data, err := os.ReadFile(loop.ExtraInstructionsPath)
	if err != nil {
		return
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return
	}
	if len(text) > 12000 {
		text = text[:12000] + "\n[truncated]"
	}
	fmt.Fprintf(b, "Additional user instructions for this loop (not part of Jira):\n%s\n\n", text)
	fmt.Fprintf(b, "These extra instructions are authoritative for this run unless they conflict with safety constraints or repository evidence.\n\n")
}

func writePRFeedbackEvidence(b *strings.Builder, runDir string) {
	path := filepath.Join(runDir, "pr-feedback.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return
	}
	if len(text) > 16000 {
		text = text[:16000] + "\n[truncated]"
	}
	fmt.Fprintf(b, "GitHub PR feedback from previous review/comments:\n%s\n\n", text)
	fmt.Fprintf(b, "You must address this PR feedback explicitly. Do not ignore reviewer comments.\n\n")
}

func writeVerificationFailureEvidence(b *strings.Builder, runDir string) {
	path := filepath.Join(runDir, "verification-summary-last.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return
	}
	if len(text) > 12000 {
		text = text[:12000] + "\n[truncated]"
	}
	fmt.Fprintf(b, "Verification failure evidence from the previous test run:\n%s\n\n", text)
	fmt.Fprintf(b, "You must use this verification failure evidence to guide the fix. Do not ignore it.\n\n")
}

func (e *Engine) verificationPrompt(ctx context.Context, loop *core.Loop) string {
	repos, _ := e.store.RepoRuns(ctx, loop.ID)
	var b strings.Builder
	if strings.HasPrefix(loop.IssueKey, "TASK-") {
		fmt.Fprintf(&b, "You are the test/verification agent for a manually requested development task.\n\n")
		fmt.Fprintf(&b, "Your only job in this opencode run is to verify the implemented changes and report feedback if there are any failures.\n\n")
		fmt.Fprintf(&b, "User ask:\n%s\n\n", loop.Summary)
	} else {
		fmt.Fprintf(&b, "You are the test/verification agent for Jira ticket %s.\n\n", loop.IssueKey)
		fmt.Fprintf(&b, "Your only job in this opencode run is to verify the implemented changes and report feedback if there are any failures.\n\n")
	}
	fmt.Fprintf(&b, "Workspace root: %s\n", loop.RunDir)
	fmt.Fprintf(&b, "Task/ticket file: %s\n", loop.TicketPath)
	fmt.Fprintf(&b, "Plan file: %s\n", loop.PlanPath)
	fmt.Fprintf(&b, "Implementation summary: %s\n\n", filepath.Join(loop.RunDir, "implementation-summary.md"))
	if loop.LastError != "" {
		fmt.Fprintf(&b, "Current blocker/error to fix:\n%s\n\n", loop.LastError)
	}
	e.writeRepoContext(&b, repos)
	fmt.Fprintf(&b, "\nConcrete task for this opencode instance:\n")
	fmt.Fprintf(&b, "Verify that the current uncommitted implementation is correct enough for loopd to commit, push, and open PRs.\n")
	fmt.Fprintf(&b, "\nTest mode instructions:\n")
	fmt.Fprintf(&b, "1. Inspect the actual uncommitted diffs in the affected repositories.\n")
	fmt.Fprintf(&b, "2. Determine the right verification commands from each repo's files, scripts, docs, package metadata, Gradle/Xcode/npm/etc configuration, and the nature of the change.\n")
	fmt.Fprintf(&b, "3. Run the smallest sufficient tests/build/lint/typecheck commands to gain confidence. Prefer focused checks first, broader checks when the change warrants it.\n")
	fmt.Fprintf(&b, "4. Do not modify the code under any circumstances. This agent is a pure critic; if verification fails, simply report the details of the failure in the summary file.\n")
	fmt.Fprintf(&b, "5. Do not expand scope beyond verification. Do not implement unrelated improvements.\n")
	fmt.Fprintf(&b, "6. Do not commit, push, create PRs, merge, or comment externally.\n")
	fmt.Fprintf(&b, "7. Write a concise verification report to %s, including commands run, results, failures found, and any commands intentionally skipped with reasons.\n", filepath.Join(loop.RunDir, "verification-summary.md"))
	fmt.Fprintf(&b, "\nIf verification fails or you cannot verify safely, write the blocker details and failed test results to verification-summary.md and exit non-zero.\n")
	return b.String()
}

func (e *Engine) verify(ctx context.Context, loop *core.Loop) error {
	e.logger.Info(ctx, loop, "verifying with AI agent")
	if err := e.setStatus(ctx, loop, core.StatusVerifying, ""); err != nil {
		return err
	}
	verificationSummary := filepath.Join(loop.RunDir, "verification-summary.md")
	if _, err := os.Stat(verificationSummary); os.IsNotExist(err) {
		promptPath := filepath.Join(loop.RunDir, "verification-prompt.md")
		prompt := e.verificationPrompt(ctx, loop)
		if err := e.runAgent(ctx, loop, "verification", promptPath, prompt); err != nil {
			e.logger.Error(ctx, loop, "verification agent failed: %v", err)
			msg := fmt.Sprintf("verification agent failed: %v", err)
			if data, readErr := os.ReadFile(verificationSummary); readErr == nil {
				summaryText := strings.TrimSpace(string(data))
				if summaryText != "" {
					lines := strings.Split(summaryText, "\n")
					var snippetLines []string
					for _, line := range lines {
						trimmed := strings.TrimSpace(line)
						if trimmed != "" {
							snippetLines = append(snippetLines, trimmed)
						}
						if len(snippetLines) >= 3 {
							break
						}
					}
					if len(snippetLines) > 0 {
						msg = fmt.Sprintf("verification agent failed: %s", strings.Join(snippetLines, " | "))
						if len(msg) > 400 {
							msg = msg[:397] + "..."
						}
					}
				}
			}
			return e.retryOrPause(ctx, loop, msg)
		}
	} else if err != nil {
		return err
	} else {
		e.logger.Info(ctx, loop, "verification summary exists, skipping verification agent")
	}

	return e.setStatus(ctx, loop, core.StatusCreatingPRs, "")
}

func (e *Engine) createPRs(ctx context.Context, loop *core.Loop) error {
	e.logger.Info(ctx, loop, "opening PRs for affected repos")
	if err := e.setStatus(ctx, loop, core.StatusCreatingPRs, ""); err != nil {
		return err
	}
	repos, err := e.store.RepoRuns(ctx, loop.ID)
	if err != nil {
		return err
	}
	changedCount := 0
	for _, repo := range repos {
		hasPendingChanges, err := e.git.HasChanges(ctx, repo.Path)
		if err != nil {
			return err
		}
		hasCommits, err := e.git.HasCommitsAhead(ctx, repo.Path, e.cfg.Repos[repo.RepoName].DefaultBranch)
		if err != nil {
			return fmt.Errorf("check commits ahead %s: %w", repo.RepoName, err)
		}
		repo.Changed = repo.Changed || hasPendingChanges || hasCommits || repo.PRURL != ""
		if !repo.Changed {
			_ = e.store.UpsertRepoRun(ctx, repo)
			continue
		}
		changedCount++
		e.logger.Info(ctx, loop, "%s is affected", repo.RepoName)
		meta, err := metadata.Generate(ctx, e.cfg.Executor, loop, repo, e.cfg.Repos[repo.RepoName].DefaultBranch)
		if err != nil {
			e.logger.Info(ctx, loop, "metadata agent output was not usable for %s; using fallback metadata: %v", repo.RepoName, err)
		}
		if hasPendingChanges {
			if err := e.git.CommitAll(ctx, repo.Path, meta.Title, meta.CommitBody); err != nil {
				return fmt.Errorf("commit %s: %w", repo.RepoName, err)
			}
			hasCommits = true
			if err := e.git.Push(ctx, repo.Path, repo.Branch); err != nil {
				return fmt.Errorf("push %s: %w", repo.RepoName, err)
			}
			e.logger.Info(ctx, loop, "%s pushed branch %s", repo.RepoName, repo.Branch)
		}
		if repo.PRURL == "" {
			if !hasCommits {
				return e.setStatus(ctx, loop, core.StatusBlocked, repo.RepoName+" has no commits ahead of base branch")
			}
			if !hasPendingChanges {
				if err := e.git.Push(ctx, repo.Path, repo.Branch); err != nil {
					return fmt.Errorf("push %s: %w", repo.RepoName, err)
				}
				e.logger.Info(ctx, loop, "%s pushed branch %s", repo.RepoName, repo.Branch)
			}
			head := e.prHead(ctx, repo)
			url, number, err := e.gh.CreatePR(ctx, repo.Path, e.cfg.Repos[repo.RepoName].DefaultBranch, head, meta.Title, meta.PRBody)
			if err != nil {
				if strings.Contains(err.Error(), "must be a collaborator") || strings.Contains(err.Error(), "createPullRequest") {
					msg := "GitHub refused PR creation because the authenticated user is not a collaborator on the target repository. Branch was pushed; create the PR manually or use a repo/fork where gh can create PRs."
					e.logger.Error(ctx, loop, "%s", msg)
					return e.setStatus(ctx, loop, core.StatusBlocked, msg)
				}
				return fmt.Errorf("create PR %s: %w", repo.RepoName, err)
			}
			repo.PRURL = url
			repo.PRNumber = number
			e.logger.Info(ctx, loop, "%s PR created: %s", repo.RepoName, url)
		}
		if err := e.store.UpsertRepoRun(ctx, repo); err != nil {
			return err
		}
	}
	if changedCount == 0 {
		return e.setStatus(ctx, loop, core.StatusBlocked, "executor produced no changes")
	}
	return e.setStatus(ctx, loop, core.StatusMonitoringCI, "")
}

func (e *Engine) prHead(ctx context.Context, repo core.RepoRun) string {
	repoCfg := e.cfg.Repos[repo.RepoName]
	owner := strings.TrimSpace(repoCfg.ForkOwner)
	if owner == "" {
		if remoteURL, err := e.git.RemoteURL(ctx, repo.Path, "origin"); err == nil {
			owner = clients.GitHubOwnerFromRemote(remoteURL)
		}
	}
	if owner == "" {
		return repo.Branch
	}
	return owner + ":" + repo.Branch
}

func (e *Engine) monitorPRs(ctx context.Context, loop *core.Loop) error {
	e.logger.Info(ctx, loop, "monitoring PR checks and reviews")
	repos, err := e.store.RepoRuns(ctx, loop.ID)
	if err != nil {
		return err
	}
	changed := 0
	approved := 0
	checksOK := 0
	var prLinks []string
	for _, repo := range repos {
		if !repo.Changed {
			continue
		}
		changed++
		if repo.PRNumber == 0 {
			return e.setStatus(ctx, loop, core.StatusCreatingPRs, "")
		}
		status, err := e.gh.PRStatus(ctx, repo.Path, repo.PRNumber)
		if err != nil {
			e.logger.Error(ctx, loop, "failed to get PR status for %s: %v. Will retry in next loop tick.", repo.RepoName, err)
			continue
		}
		repo.CIState = status.ChecksState
		repo.ReviewDecision = status.ReviewDecision
		humanFeedback := clients.HumanFeedback(status.Feedback)
		feedbackHash := feedbackHash(humanFeedback)
		newHumanFeedback := feedbackHash != "" && feedbackHash != repo.FeedbackHash
		repo.FeedbackHash = feedbackHash
		if err := e.store.UpsertRepoRun(ctx, repo); err != nil {
			return err
		}
		prLinks = append(prLinks, repo.PRURL)
		e.logger.Info(ctx, loop, "%s PR status: checks=%s review=%s merge=%s merged=%v", repo.RepoName, repo.CIState, repo.ReviewDecision, status.MergeState, status.Merged)
		if status.Merged {
			e.logger.Info(ctx, loop, "%s PR is already merged; no further monitoring needed", repo.RepoName)
			checksOK++
			approved++
			continue
		}
		if status.IsOutOfDate() {
			if err := e.syncPRBranch(ctx, loop, repo); err != nil {
				message := fmt.Sprintf("failed to sync %s with base branch: %v. Paused for human intervention.", repo.RepoName, err)
				e.logger.Error(ctx, loop, "%s", message)
				return e.store.UpdateLoopStatusAndRetry(ctx, loop.ID, core.StatusPaused, message, loop.AutoRetryCount)
			}
			return e.setStatus(ctx, loop, core.StatusMonitoringCI, "")
		}
		if strings.EqualFold(repo.CIState, "success") {
			checksOK++
		}
		if strings.EqualFold(repo.ReviewDecision, "APPROVED") {
			approved++
		}
		if strings.EqualFold(repo.ReviewDecision, "CHANGES_REQUESTED") {
			message := e.writePRFeedback(loop, repo, status, humanFeedback)
			return e.retryOrPause(ctx, loop, message)
		}
		if newHumanFeedback {
			message := e.writePRFeedback(loop, repo, status, humanFeedback)
			return e.retryOrPause(ctx, loop, message)
		}
		if repo.CIState != "success" && repo.CIState != "cancelled" && repo.CIState != "unknown" && repo.CIState != "pending" {
			message := e.writeCIFailures(loop, repo, status)
			return e.retryOrPause(ctx, loop, message)
		}
	}
	if changed > 0 && checksOK == changed && approved == changed {
		if !strings.HasPrefix(loop.IssueKey, "TASK-") {
			body := fmt.Sprintf("Loop completed for %s.\n\nPRs:\n- %s", loop.IssueKey, strings.Join(prLinks, "\n- "))
			if err := e.jira.AddComment(ctx, loop.IssueKey, body); err != nil {
				e.logger.Error(ctx, loop, "failed to comment Jira: %v", err)
			}
		}
		e.logger.Info(ctx, loop, "completed: CI green and human review approved")
		return e.setStatus(ctx, loop, core.StatusCompleted, "")
	}
	if changed > 0 && checksOK == changed {
		return e.setStatus(ctx, loop, core.StatusWaitingHumanReview, "")
	}
	return e.setStatus(ctx, loop, core.StatusMonitoringCI, "")
}

func (e *Engine) syncPRBranch(ctx context.Context, loop *core.Loop, repo core.RepoRun) error {
	base := e.cfg.Repos[repo.RepoName].DefaultBranch
	if base == "" {
		base = "main"
	}
	e.logger.Info(ctx, loop, "%s branch is out-of-date; syncing with origin/%s", repo.RepoName, base)
	if err := e.git.SyncWithBase(ctx, repo.Path, base); err != nil {
		if conflictErr, ok := err.(*clients.MergeConflictError); ok {
			writeMergeConflictsReport(loop.RunDir, repo.RepoName, base, conflictErr)
		}
		return err
	}
	res, err := e.git.PushResult(ctx, repo.Path, repo.Branch)
	if err != nil {
		return err
	}
	pushOutput := strings.TrimSpace(res.Stdout + "\n" + res.Stderr)
	if strings.Contains(strings.ToLower(pushOutput), "everything up-to-date") {
		e.logger.Info(ctx, loop, "%s synced with FETCH_HEAD from origin/%s; push reported everything up-to-date for %s", repo.RepoName, base, repo.Branch)
	} else {
		e.logger.Info(ctx, loop, "%s synced with FETCH_HEAD from origin/%s and pushed %s", repo.RepoName, base, repo.Branch)
	}
	return nil
}

func writeMergeConflictsReport(runDir, repoName, baseBranch string, err *clients.MergeConflictError) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Merge Conflicts Detected\n\n")
	fmt.Fprintf(&b, "A merge conflict was detected when syncing `%s` with origin `%s`.\n\n", repoName, baseBranch)
	fmt.Fprintf(&b, "## Conflicted Files\n\n")
	for _, file := range err.Conflicts {
		fmt.Fprintf(&b, "- `%s`\n", file)
	}
	fmt.Fprintf(&b, "\n## Git Merge Output\n\n```\n%s\n```\n", strings.TrimSpace(err.Output))
	path := filepath.Join(runDir, "merge-conflicts.md")
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

func (e *Engine) writePRFeedback(loop *core.Loop, repo core.RepoRun, status clients.PRStatus, feedback []clients.PRFeedback) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# GitHub PR Feedback\n\n")
	fmt.Fprintf(&b, "GitHub review feedback was detected for `%s` on PR %s.\n\n", repo.RepoName, repo.PRURL)
	fmt.Fprintf(&b, "Review decision: `%s`\n\n", repo.ReviewDecision)
	if len(feedback) == 0 {
		fmt.Fprintf(&b, "No comment bodies were available from GitHub. Inspect the PR review page for details.\n")
	} else {
		fmt.Fprintf(&b, "## Feedback Items\n\n")
		for _, fb := range feedback {
			author := firstNonEmptyString(fb.Author, "unknown")
			state := firstNonEmptyString(fb.State, "comment")
			fmt.Fprintf(&b, "### %s by @%s (%s)\n\n", fb.Kind, author, state)
			if fb.Path != "" && fb.Path != "<nil>" {
				fmt.Fprintf(&b, "File: `%s`", fb.Path)
				if fb.Line != "" && fb.Line != "<nil>" {
					fmt.Fprintf(&b, " line `%s`", fb.Line)
				}
				fmt.Fprintf(&b, "\n\n")
			}
			fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(fb.Body))
			if fb.URL != "" && fb.URL != "<nil>" {
				fmt.Fprintf(&b, "Link: %s\n\n", fb.URL)
			}
		}
	}
	fmt.Fprintf(&b, "## Next Agent Task\n\n")
	fmt.Fprintf(&b, "Read this file, inspect the repository changes, address every reviewer concern that applies, rerun relevant verification, and update the implementation summary.\n")
	path := filepath.Join(loop.RunDir, "pr-feedback.md")
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
	return "changes requested on GitHub review for " + repo.RepoName + ": details written to " + path
}

func feedbackHash(feedback []clients.PRFeedback) string {
	if len(feedback) == 0 {
		return ""
	}
	var b strings.Builder
	for _, fb := range feedback {
		fmt.Fprintf(&b, "%s|%s|%s|%s|%s\n", fb.Kind, fb.Author, fb.State, fb.Body, fb.URL)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum[:])
}

func (e *Engine) writeCIFailures(loop *core.Loop, repo core.RepoRun, status clients.PRStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# CI Failures\n\n")
	fmt.Fprintf(&b, "GitHub CI failed for `%s` on PR %s.\n\n", repo.RepoName, repo.PRURL)
	fmt.Fprintf(&b, "Overall check state: `%s`\n", repo.CIState)
	fmt.Fprintf(&b, "Review decision: `%s`\n\n", repo.ReviewDecision)
	if len(status.FailingChecks) == 0 {
		fmt.Fprintf(&b, "No individual failing checks were available from GitHub. Inspect the PR checks page for details.\n")
	} else {
		fmt.Fprintf(&b, "## Failing Checks\n\n")
		for _, check := range status.FailingChecks {
			name := strings.TrimSpace(check.Name)
			if name == "" || name == "<nil>" {
				name = "Unnamed check"
			}
			state := firstNonEmptyString(check.Conclusion, check.State, "unknown")
			if check.Link != "" && check.Link != "<nil>" {
				fmt.Fprintf(&b, "- **%s**: `%s` - %s\n", name, state, check.Link)
			} else {
				fmt.Fprintf(&b, "- **%s**: `%s`\n", name, state)
			}
		}
	}
	fmt.Fprintf(&b, "\n## Next Agent Task\n\n")
	fmt.Fprintf(&b, "Read this file, inspect the repository changes, reproduce or infer the failure locally, fix the implementation, and rerun relevant verification.\n")
	path := filepath.Join(loop.RunDir, "ci-failures.md")
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
	return "CI checks are failing for " + repo.RepoName + ": details written to " + path
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func (e *Engine) repoNamesForLoop(loop *core.Loop) []string {
	if strings.TrimSpace(loop.RepoScope) == "" {
		return sortedRepoNames(e.cfg.Repos)
	}
	var names []string
	for _, part := range strings.Split(loop.RepoScope, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if _, ok := e.cfg.Repos[name]; ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (e *Engine) setStatus(ctx context.Context, loop *core.Loop, status, lastError string) error {
	if loop == nil {
		return nil
	}
	current, err := e.store.GetLoopByID(ctx, loop.ID)
	if err == nil && (current.Status == core.StatusCancelled || current.Status == core.StatusPaused || current.Status == core.StatusCompleted) {
		loop.Status = current.Status
		return nil
	}
	loop.Status = status
	loop.LastError = lastError
	return e.store.UpdateLoopStatusIfNotStopped(ctx, loop.ID, status, lastError)
}

func (e *Engine) retryOrPause(ctx context.Context, loop *core.Loop, reason string) error {
	current, err := e.store.GetLoopByID(ctx, loop.ID)
	if err == nil {
		loop.AutoRetryCount = current.AutoRetryCount
	}
	nextCount := loop.AutoRetryCount + 1
	if nextCount > e.cfg.Daemon.MaxAutoRetries {
		message := fmt.Sprintf("auto retry limit reached (%d/%d). Last blocker: %s. Paused for human intervention; use loop resume %s to continue.", e.cfg.Daemon.MaxAutoRetries, e.cfg.Daemon.MaxAutoRetries, reason, loop.IssueKey)
		loop.Status = core.StatusPaused
		loop.LastError = message
		if e.logger != nil {
			e.logger.Info(ctx, loop, "%s", message)
		}
		return e.store.UpdateLoopStatusAndRetry(ctx, loop.ID, core.StatusPaused, message, loop.AutoRetryCount)
	}
	message := fmt.Sprintf("auto retry %d/%d: %s", nextCount, e.cfg.Daemon.MaxAutoRetries, reason)
	loop.Status = core.StatusImplementing
	loop.LastError = message
	loop.AutoRetryCount = nextCount
	if e.logger != nil {
		e.logger.Info(ctx, loop, "%s", message)
	}
	return e.store.UpdateLoopStatusAndRetry(ctx, loop.ID, core.StatusImplementing, message, nextCount)
}

func (e *Engine) registerCancel(loopID int64, cancel context.CancelFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancels[loopID] = cancel
}

func (e *Engine) deregisterCancel(loopID int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.cancels, loopID)
}

func (e *Engine) StartMonitor(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.checkRunningLoops(ctx)
			}
		}
	}()
}

func (e *Engine) checkRunningLoops(ctx context.Context) {
	e.mu.Lock()
	ids := make([]int64, 0, len(e.cancels))
	for id := range e.cancels {
		ids = append(ids, id)
	}
	e.mu.Unlock()

	for _, id := range ids {
		current, err := e.store.GetLoopByID(ctx, id)
		isStopped := err != nil || current.Status == core.StatusCancelled || current.Status == core.StatusPaused || current.Status == core.StatusCompleted
		if isStopped {
			e.mu.Lock()
			cancel, ok := e.cancels[id]
			if ok {
				e.logger.Info(ctx, current, "stopping running command centrally because loop was paused, cancelled, or deleted")
				cancel()
			}
			e.mu.Unlock()
		}
	}
}

func (e *Engine) isStopped(ctx context.Context, loop *core.Loop) bool {
	current, err := e.store.GetLoopByID(ctx, loop.ID)
	if err != nil {
		return true
	}
	return current.Status == core.StatusCancelled || current.Status == core.StatusPaused || current.Status == core.StatusCompleted
}

func writeCommandLog(runDir, name string, res run.Result) {
	var b strings.Builder
	fmt.Fprintf(&b, "$ %s\n", strings.Join(res.Command, " "))
	if res.Dir != "" {
		fmt.Fprintf(&b, "dir: %s\n", res.Dir)
	}
	fmt.Fprintf(&b, "duration: %s\n\n", res.Duration)
	fmt.Fprintf(&b, "--- stdout ---\n%s\n", res.Stdout)
	fmt.Fprintf(&b, "--- stderr ---\n%s\n", res.Stderr)
	_ = os.WriteFile(filepath.Join(runDir, name), []byte(b.String()), 0o644)
}

func sortedRepoNames(repos map[string]config.RepoConfig) []string {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
