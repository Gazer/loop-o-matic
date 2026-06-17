package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFollowLogGracefulCancellation(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")
	if err := os.WriteFile(logPath, []byte("line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := followLog(ctx, tempDir, "test")
	if err == nil {
		t.Fatal("expected context deadline exceeded or cancelled error, got nil")
	}
	if err != context.DeadlineExceeded && err != context.Canceled {
		t.Fatalf("expected deadline exceeded or canceled, got: %v", err)
	}
}
