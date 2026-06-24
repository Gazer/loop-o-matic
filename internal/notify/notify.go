package notify

import (
	"fmt"
	"os/exec"
	"runtime"
)

type Notifier interface {
	Send(issueKey, status string)
}

type NoOp struct{}

func (n *NoOp) Send(_, _ string) {}

type osNotifier struct {
	linux bool
	mac   bool
}

func New() Notifier {
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("notify-send"); err == nil {
			return &osNotifier{linux: true}
		}
	case "darwin":
		if _, err := exec.LookPath("osascript"); err == nil {
			return &osNotifier{mac: true}
		}
	}
	return &NoOp{}
}

func (n *osNotifier) Send(issueKey, status string) {
	summary := fmt.Sprintf("%s is now %s", issueKey, humanStatus(status))
	go n.send(summary)
}

func (n *osNotifier) send(summary string) {
	if n.linux {
		cmd := exec.Command("notify-send", "-a", "loop-o-matic", summary)
		_ = cmd.Run()
		return
	}
	if n.mac {
		script := fmt.Sprintf(`display notification %q with title "loop-o-matic"`, summary)
		cmd := exec.Command("osascript", "-e", script)
		_ = cmd.Run()
	}
}

func humanStatus(status string) string {
	switch status {
	case "created":
		return "created"
	case "preparing_workspace":
		return "preparing workspace"
	case "discovering":
		return "discovering"
	case "implementing":
		return "implementing"
	case "verifying":
		return "verifying"
	case "creating_prs":
		return "creating PRs"
	case "monitoring_ci":
		return "monitoring CI"
	case "waiting_human_review":
		return "waiting for human review"
	case "completed":
		return "done"
	case "failed":
		return "failed"
	case "paused":
		return "paused"
	case "cancelled":
		return "cancelled"
	case "blocked":
		return "blocked"
	default:
		return status
	}
}
