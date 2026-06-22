package clients

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"loop-o-matic/internal/run"
)

type Git struct{}

func (Git) CloneBare(ctx context.Context, remoteURL, bareRepo string) error {
	if err := os.MkdirAll(filepath.Dir(bareRepo), 0o755); err != nil {
		return err
	}
	_, err := run.Command(ctx, "", nil, "git", "clone", "--bare", remoteURL, bareRepo)
	return err
}

func (Git) Fetch(ctx context.Context, bareRepo string) error {
	_, err := run.Command(ctx, "", nil, "git", "--git-dir", bareRepo, "fetch", "--all", "--prune")
	return err
}

func (Git) AddWorktree(ctx context.Context, bareRepo, worktreePath, branch, baseRef string) error {
	_, err := run.Command(ctx, "", nil, "git", "--git-dir", bareRepo, "worktree", "add", "-B", branch, worktreePath, baseRef)
	return err
}

func (Git) ResolveBaseRef(ctx context.Context, bareRepo, preferredBranch string) (string, error) {
	candidates := []string{}
	if preferredBranch != "" {
		candidates = append(candidates, preferredBranch, "refs/heads/"+preferredBranch, "origin/"+preferredBranch)
	}
	if head, err := bareHEAD(ctx, bareRepo); err == nil && head != "" {
		candidates = append(candidates, head)
	}
	candidates = append(candidates, "main", "refs/heads/main", "master", "refs/heads/master", "origin/main", "origin/master")

	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if refExists(ctx, bareRepo, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not resolve base ref in %s; tried %s", bareRepo, strings.Join(candidates, ", "))
}

func bareHEAD(ctx context.Context, bareRepo string) (string, error) {
	res, err := run.Command(ctx, "", nil, "git", "--git-dir", bareRepo, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

func refExists(ctx context.Context, bareRepo, ref string) bool {
	_, err := run.Command(ctx, "", nil, "git", "--git-dir", bareRepo, "rev-parse", "--verify", ref+"^{commit}")
	return err == nil
}

func (Git) StatusPorcelain(ctx context.Context, repoPath string) (string, error) {
	res, err := run.Command(ctx, repoPath, nil, "git", "status", "--porcelain")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

func (Git) IsWorktree(ctx context.Context, repoPath string) bool {
	res, err := run.Command(ctx, repoPath, nil, "git", "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(res.Stdout) == "true"
}

func (Git) HasChanges(ctx context.Context, repoPath string) (bool, error) {
	status, err := (Git{}).StatusPorcelain(ctx, repoPath)
	return status != "", err
}

func (Git) HasCommitsAhead(ctx context.Context, repoPath, baseBranch string) (bool, error) {
	baseRef, err := worktreeBaseRef(ctx, repoPath, baseBranch)
	if err != nil {
		return false, err
	}
	res, err := run.Command(ctx, repoPath, nil, "git", "rev-list", "--count", baseRef+"..HEAD")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(res.Stdout) != "0", nil
}

func worktreeBaseRef(ctx context.Context, repoPath, preferredBranch string) (string, error) {
	candidates := []string{}
	if preferredBranch != "" {
		candidates = append(candidates, "origin/"+preferredBranch, preferredBranch, "refs/remotes/origin/"+preferredBranch, "refs/heads/"+preferredBranch)
	}
	candidates = append(candidates, "origin/main", "main", "refs/remotes/origin/main", "refs/heads/main", "origin/master", "master", "refs/remotes/origin/master", "refs/heads/master")
	seen := map[string]bool{}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if worktreeRefExists(ctx, repoPath, candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not resolve worktree base ref; tried %s", strings.Join(candidates, ", "))
}

func worktreeRefExists(ctx context.Context, repoPath, ref string) bool {
	_, err := run.Command(ctx, repoPath, nil, "git", "rev-parse", "--verify", ref+"^{commit}")
	return err == nil
}

func (Git) CommitAll(ctx context.Context, repoPath, subject, body string) error {
	if _, err := run.Command(ctx, repoPath, nil, "git", "add", "-A"); err != nil {
		return err
	}
	args := []string{"commit", "-m", subject}
	if strings.TrimSpace(body) != "" {
		args = append(args, "-m", body)
	}
	_, err := run.Command(ctx, repoPath, nil, "git", args...)
	return err
}

func (Git) Push(ctx context.Context, repoPath, branch string) error {
	_, err := run.Command(ctx, repoPath, nil, "git", "push", "-u", "origin", branch)
	if err != nil && isNonFastForward(err) {
		_, err = run.Command(ctx, repoPath, nil, "git", "push", "--force-with-lease", "-u", "origin", branch)
	}
	return err
}

func (Git) PushResult(ctx context.Context, repoPath, branch string) (run.Result, error) {
	res, err := run.Command(ctx, repoPath, nil, "git", "push", "-u", "origin", branch)
	if err != nil && isNonFastForward(err) {
		res, err = run.Command(ctx, repoPath, nil, "git", "push", "--force-with-lease", "-u", "origin", branch)
	}
	return res, err
}

type MergeConflictError struct {
	Conflicts []string
	Output    string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("git merge failed with conflicts on files: %s", strings.Join(e.Conflicts, ", "))
}

func (Git) SyncWithBase(ctx context.Context, repoPath, baseBranch string) error {
	if _, err := run.Command(ctx, repoPath, nil, "git", "fetch", "origin", baseBranch); err != nil {
		return err
	}
	res, err := run.Command(ctx, repoPath, nil, "git", "merge", "--no-edit", "FETCH_HEAD")
	if err != nil {
		var conflicts []string
		if diffRes, diffErr := run.Command(ctx, repoPath, nil, "git", "diff", "--name-only", "--diff-filter=U"); diffErr == nil {
			for _, line := range strings.Split(diffRes.Stdout, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					conflicts = append(conflicts, line)
				}
			}
		}
		if abortRes, abortErr := run.Command(context.Background(), repoPath, nil, "git", "merge", "--abort"); abortErr != nil {
			return fmt.Errorf("git merge failed: %v (additionally, git merge --abort failed: %v, stdout: %s, stderr: %s)", err, abortErr, abortRes.Stdout, abortRes.Stderr)
		}
		if len(conflicts) > 0 {
			return &MergeConflictError{Conflicts: conflicts, Output: res.Stdout + "\n" + res.Stderr}
		}
		return fmt.Errorf("git merge failed with conflicts (merge aborted successfully): %w", err)
	}
	return nil
}

func isNonFastForward(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "non-fast-forward") || strings.Contains(message, "fetch first") || strings.Contains(message, "tip of your current branch is behind")
}

func (Git) RemoteURL(ctx context.Context, repoPath, remote string) (string, error) {
	res, err := run.Command(ctx, repoPath, nil, "git", "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

func GitHubOwnerFromRemote(remoteURL string) string {
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return ""
	}
	remoteURL = strings.TrimSuffix(remoteURL, ".git")
	if strings.HasPrefix(remoteURL, "git@") {
		parts := strings.SplitN(remoteURL, ":", 2)
		if len(parts) != 2 {
			return ""
		}
		path := strings.Split(parts[1], "/")
		if len(path) >= 2 {
			return path[0]
		}
	}
	if strings.Contains(remoteURL, "github.com/") {
		idx := strings.Index(remoteURL, "github.com/")
		path := strings.Split(remoteURL[idx+len("github.com/"):], "/")
		if len(path) >= 2 {
			return path[0]
		}
	}
	return ""
}

func (Git) RemoveWorktree(ctx context.Context, repoPath string, force bool) error {
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		return nil
	}
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, repoPath)
	_, err := run.Command(ctx, "", nil, "git", args...)
	return err
}

func BarePath(root, bare string) string {
	if filepath.IsAbs(bare) {
		return bare
	}
	return filepath.Join(root, bare)
}

func GitHubRemote(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") || strings.HasPrefix(value, "git@") {
		return value
	}
	return "git@github.com:" + strings.TrimSuffix(value, ".git") + ".git"
}
