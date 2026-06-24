package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Workspace     WorkspaceConfig       `yaml:"workspace"`
	Daemon        DaemonConfig          `yaml:"daemon"`
	Jira          JiraConfig            `yaml:"jira"`
	GitHub        GitHubConfig          `yaml:"github"`
	Executor      ExecutorConfig        `yaml:"executor"`
	Notifications NotificationsConfig   `yaml:"notifications"`
	Repos         map[string]RepoConfig `yaml:"repos"`
}

type NotificationsConfig struct {
	Enabled bool `yaml:"enabled"`
}

type WorkspaceConfig struct {
	Root          string `yaml:"root"`
	BareReposRoot string `yaml:"bare_repos_root"`
	LogRoot       string `yaml:"log_root"`
	StatePath     string `yaml:"state_path"`
}

type DaemonConfig struct {
	TickInterval    Duration `yaml:"tick_interval"`
	MaxRunningTasks int      `yaml:"max_running_tasks"`
	MaxAutoRetries  int      `yaml:"max_auto_retries"`
}

type JiraConfig struct {
	CLI string `yaml:"cli"`
}

type GitHubConfig struct {
	CLI string `yaml:"cli"`
}

type ExecutorConfig struct {
	CLI                    string   `yaml:"cli"`
	Model                  string   `yaml:"model"`
	Command                []string `yaml:"command"`
	Timeout                Duration `yaml:"timeout"`
	AutoApprovePermissions *bool    `yaml:"auto_approve_permissions"`
}

type RepoConfig struct {
	Bare          string   `yaml:"bare"`
	GitHub        string   `yaml:"github"`
	ForkOwner     string   `yaml:"fork_owner"`
	DefaultBranch string   `yaml:"default_branch"`
	Extras        []string `yaml:"extras,omitempty"`
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var raw string
	if err := value.Decode(&raw); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

func Load() (*Config, string, error) {
	path := os.Getenv("LOOPOMATIC_CONFIG")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, "", err
		}
		path = filepath.Join(home, ".loop-o-matic", "config.yaml")
	}
	path = Expand(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, path, fmt.Errorf("parse config %s: %w", path, err)
	}
	setDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, path, err
	}
	return &cfg, path, nil
}

func setDefaults(cfg *Config) {
	cfg.Workspace.Root = defaultPath(cfg.Workspace.Root, "~/.loop-o-matic/runs")
	cfg.Workspace.BareReposRoot = defaultPath(cfg.Workspace.BareReposRoot, "~/.loop-o-matic/bare-repos")
	cfg.Workspace.LogRoot = defaultPath(cfg.Workspace.LogRoot, "~/.loop-o-matic/logs")
	cfg.Workspace.StatePath = defaultPath(cfg.Workspace.StatePath, "~/.loop-o-matic/state.sqlite")
	if cfg.Daemon.TickInterval.Duration == 0 {
		cfg.Daemon.TickInterval.Duration = 30 * time.Second
	}
	if cfg.Daemon.MaxRunningTasks <= 0 {
		cfg.Daemon.MaxRunningTasks = 2
	}
	if cfg.Daemon.MaxAutoRetries <= 0 {
		cfg.Daemon.MaxAutoRetries = 3
	}
	if cfg.Jira.CLI == "" {
		cfg.Jira.CLI = "acli"
	}
	if cfg.GitHub.CLI == "" {
		cfg.GitHub.CLI = "gh"
	}
	if cfg.Executor.Timeout.Duration == 0 {
		cfg.Executor.Timeout.Duration = time.Hour
	}
	if cfg.Executor.CLI == "" {
		cfg.Executor.CLI = "opencode"
	}
	if cfg.Executor.Model == "" {
		cfg.Executor.Model = "github-copilot/gemini-3.5-flash"
	}
	if cfg.Executor.AutoApprovePermissions == nil {
		v := true
		cfg.Executor.AutoApprovePermissions = &v
	}
	if !cfg.Notifications.Enabled {
		cfg.Notifications.Enabled = true
	}
	for name, repo := range cfg.Repos {
		if repo.DefaultBranch == "" {
			repo.DefaultBranch = "main"
			cfg.Repos[name] = repo
		}
	}
}

func validate(cfg *Config) error {
	if len(cfg.Repos) == 0 {
		return errors.New("config requires at least one repo")
	}
	return nil
}

func defaultPath(value, fallback string) string {
	if value == "" {
		return Expand(fallback)
	}
	return Expand(value)
}

func Expand(path string) string {
	if path == "" || path[0] != '~' {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	if len(path) > 1 && path[1] == '/' {
		return filepath.Join(home, path[2:])
	}
	return path
}
