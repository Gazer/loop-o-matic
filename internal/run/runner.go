package run

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type BoundedWriter struct {
	mu     sync.Mutex
	Buffer bytes.Buffer
	Limit  int
}

func (w *BoundedWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n = len(p)
	if w.Buffer.Len() >= w.Limit {
		return n, nil
	}
	allowed := w.Limit - w.Buffer.Len()
	if allowed > len(p) {
		w.Buffer.Write(p)
	} else {
		w.Buffer.Write(p[:allowed])
		w.Buffer.WriteString("\n[output truncated due to size limit]\n")
	}
	return n, nil
}

func (w *BoundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.Buffer.String()
}

type Result struct {
	Command  []string
	Dir      string
	Stdout   string
	Stderr   string
	Duration time.Duration
}

func Command(ctx context.Context, dir string, env []string, name string, args ...string) (Result, error) {
	started := time.Now()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout := &BoundedWriter{Limit: 5 * 1024 * 1024}
	stderr := &BoundedWriter{Limit: 5 * 1024 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		res := Result{Command: append([]string{name}, args...), Dir: dir, Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(started)}
		return res, fmt.Errorf("%s failed: %w", strings.Join(res.Command, " "), err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case <-ctx.Done():
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			select {
			case err = <-done:
			case <-time.After(5 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				err = <-done
			}
		}
		err = ctx.Err()
	case err = <-done:
	}
	res := Result{Command: append([]string{name}, args...), Dir: dir, Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(started)}
	if err != nil {
		return res, fmt.Errorf("%s failed: %w\n%s", strings.Join(res.Command, " "), err, strings.TrimSpace(res.Stderr))
	}
	return res, nil
}
