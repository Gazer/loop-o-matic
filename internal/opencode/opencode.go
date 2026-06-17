package opencode

import (
	"context"
	"fmt"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/run"
)

type Client struct {
	cfg config.ExecutorConfig
}

type RunRequest struct {
	Dir                    string
	Title                  string
	Prompt                 string
	Env                    []string
	AutoApprovePermissions bool
}

func New(cfg config.ExecutorConfig) Client {
	return Client{cfg: cfg}
}

func (c Client) Run(ctx context.Context, req RunRequest) (run.Result, error) {
	if c.cfg.CLI == "" {
		return run.Result{}, fmt.Errorf("opencode cli is required")
	}
	cmd := []string{c.cfg.CLI, "run", "--model", c.cfg.Model}
	if req.Dir != "" {
		cmd = append(cmd, "--dir", req.Dir)
	}
	if req.Title != "" {
		cmd = append(cmd, "--title", req.Title)
	}
	if req.AutoApprovePermissions {
		cmd = append(cmd, "--dangerously-skip-permissions")
	}
	cmd = append(cmd, req.Prompt)
	return run.Command(ctx, req.Dir, req.Env, cmd[0], cmd[1:]...)
}
