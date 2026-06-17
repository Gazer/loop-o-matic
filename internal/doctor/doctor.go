package doctor

import (
	"context"
	"fmt"
	"os"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/run"
)

func Run(ctx context.Context, cfg *config.Config, configPath string) error {
	fmt.Printf("config: %s\n", configPath)
	checks := []struct {
		name string
		cmd  []string
	}{
		{"git", []string{"git", "--version"}},
		{"gh auth", []string{cfg.GitHub.CLI, "auth", "status"}},
		{"acli", []string{cfg.Jira.CLI, "--version"}},
		{"opencode", []string{cfg.Executor.CLI, "--version"}},
	}
	for _, check := range checks {
		res, err := run.Command(ctx, "", nil, check.cmd[0], check.cmd[1:]...)
		if err != nil {
			fmt.Printf("FAIL %-12s %v\n", check.name, err)
			continue
		}
		out := res.Stdout
		if out == "" {
			out = res.Stderr
		}
		fmt.Printf("OK   %-12s %s\n", check.name, firstLine(out))
	}

	paths := []struct{ name, path string }{
		{"workspace.root", cfg.Workspace.Root},
		{"workspace.bare_repos_root", cfg.Workspace.BareReposRoot},
		{"workspace.log_root", cfg.Workspace.LogRoot},
	}
	for _, p := range paths {
		if err := os.MkdirAll(p.path, 0o755); err != nil {
			fmt.Printf("FAIL %-24s %s: %v\n", p.name, p.path, err)
		} else {
			fmt.Printf("OK   %-24s %s\n", p.name, p.path)
		}
	}
	for name, repo := range cfg.Repos {
		bare := repo.Bare
		if !isAbs(bare) {
			bare = cfg.Workspace.BareReposRoot + string(os.PathSeparator) + bare
		}
		if _, err := os.Stat(bare); err != nil {
			if repo.GitHub != "" {
				fmt.Printf("WARN repo %-18s bare not found, will clone from github: %s\n", name, repo.GitHub)
			} else {
				fmt.Printf("FAIL repo %-18s bare not found and github remote is empty: %s\n", name, bare)
			}
		} else {
			fmt.Printf("OK   repo %-18s %s\n", name, bare)
		}
	}
	return nil
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' || r == '\r' {
			return s[:i]
		}
	}
	return s
}

func isAbs(path string) bool {
	return len(path) > 0 && path[0] == '/'
}
