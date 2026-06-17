package logging

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"loop-o-matic/internal/core"
	"loop-o-matic/internal/store"
)

type Logger struct {
	store     *store.Store
	logRoot   string
	loopdFile *os.File
	mu        sync.Mutex
}

func New(s *store.Store, logRoot string) (*Logger, error) {
	if err := os.MkdirAll(logRoot, 0o755); err != nil {
		return nil, err
	}
	loopdPath := filepath.Join(logRoot, "loopd.log")
	f, err := os.OpenFile(loopdPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{store: s, logRoot: logRoot, loopdFile: f}, nil
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loopdFile != nil {
		err := l.loopdFile.Close()
		l.loopdFile = nil
		return err
	}
	return nil
}

func (l *Logger) Info(ctx context.Context, loop *core.Loop, msg string, args ...any) {
	l.write(ctx, loop, "info", msg, args...)
}

func (l *Logger) Error(ctx context.Context, loop *core.Loop, msg string, args ...any) {
	l.write(ctx, loop, "error", msg, args...)
}

func (l *Logger) Debug(ctx context.Context, loop *core.Loop, msg string, args ...any) {
	l.write(ctx, loop, "debug", msg, args...)
}

func (l *Logger) write(ctx context.Context, loop *core.Loop, level, msg string, args ...any) {
	message := fmt.Sprintf(msg, args...)
	issue := "loopd"
	loopID := int64(0)
	if loop != nil {
		issue = loop.IssueKey
		loopID = loop.ID
	}
	line := fmt.Sprintf("%s [%s] [%s] %s\n", time.Now().Format(time.RFC3339), issue, strings.ToUpper(level), message)

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loopdFile != nil {
		_, _ = l.loopdFile.WriteString(line)
	} else {
		_ = appendFile(filepath.Join(l.logRoot, "loopd.log"), line)
	}
	if loop != nil {
		_ = appendFile(filepath.Join(l.logRoot, loop.IssueKey+".log"), line)
	}
	if l.store != nil {
		_ = l.store.AddEvent(ctx, core.Event{LoopID: loopID, IssueKey: issue, Level: level, Message: message})
	}
	fmt.Print(line)
}

func appendFile(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
}
