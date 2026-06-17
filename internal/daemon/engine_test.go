package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/core"
	"loop-o-matic/internal/logging"
	"loop-o-matic/internal/store"
)

func TestSlotClassification(t *testing.T) {
	e := &Engine{}
	if !e.requiresLongExecutionSlot(core.StatusImplementing) {
		t.Fatal("implementing should consume a long execution slot")
	}
	if !e.requiresPollingSlot(core.StatusMonitoringCI) {
		t.Fatal("monitoring_ci polling should consume a short slot")
	}
	if e.requiresLongExecutionSlot(core.StatusMonitoringCI) {
		t.Fatal("monitoring_ci should not hold a long slot between ticks")
	}
	if e.requiresPollingSlot(core.StatusCompleted) {
		t.Fatal("completed should not consume slots")
	}
}

func TestRetryOrPausePausesAfterLimit(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	loop := &core.Loop{IssueKey: "TASK-1", Summary: "test", Status: core.StatusMonitoringCI, RunDir: t.TempDir(), AutoRetryCount: 3}
	if err := s.CreateLoop(ctx, loop); err != nil {
		t.Fatal(err)
	}
	e := &Engine{cfg: &config.Config{Daemon: config.DaemonConfig{MaxAutoRetries: 3}}, store: s}
	if err := e.retryOrPause(ctx, loop, "ci failed"); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetLoopByID(ctx, loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != core.StatusPaused {
		t.Fatalf("expected paused, got %s", updated.Status)
	}
}

func TestVerifyTransitionsToCreatingPRs(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	logger, err := logging.New(s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	runDir := t.TempDir()
	summaryPath := filepath.Join(runDir, "verification-summary.md")
	if err := os.WriteFile(summaryPath, []byte("Verified!"), 0o644); err != nil {
		t.Fatal(err)
	}

	loop := &core.Loop{
		IssueKey:   "TASK-1",
		Summary:    "test",
		Status:     core.StatusVerifying,
		RunDir:     runDir,
		TicketPath: filepath.Join(runDir, "ticket.md"),
		PlanPath:   filepath.Join(runDir, "plan.md"),
	}
	if err := s.CreateLoop(ctx, loop); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Daemon: config.DaemonConfig{MaxAutoRetries: 3},
	}
	e := &Engine{cfg: cfg, store: s, logger: logger}

	if err := e.verify(ctx, loop); err != nil {
		t.Fatal(err)
	}

	updated, err := s.GetLoopByID(ctx, loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != core.StatusCreatingPRs {
		t.Fatalf("expected status creating_prs, got %s", updated.Status)
	}
}

func TestVerifyRetriesOnFailure(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	logger, err := logging.New(s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	runDir := t.TempDir()
	loop := &core.Loop{
		IssueKey:   "TASK-1",
		Summary:    "test",
		Status:     core.StatusVerifying,
		RunDir:     runDir,
		TicketPath: filepath.Join(runDir, "ticket.md"),
		PlanPath:   filepath.Join(runDir, "plan.md"),
	}
	if err := s.CreateLoop(ctx, loop); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Daemon: config.DaemonConfig{MaxAutoRetries: 3},
		Executor: config.ExecutorConfig{
			Command: []string{"false"},
			Timeout: config.Duration{Duration: 10 * time.Second},
		},
	}
	e := &Engine{cfg: cfg, store: s, logger: logger}

	if err := e.verify(ctx, loop); err != nil {
		t.Fatal(err)
	}

	updated, err := s.GetLoopByID(ctx, loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != core.StatusImplementing {
		t.Fatalf("expected status implementing, got %s", updated.Status)
	}
	if updated.AutoRetryCount != 1 {
		t.Fatalf("expected auto retry count 1, got %d", updated.AutoRetryCount)
	}
}

func TestPanicRecovery(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	logger, err := logging.New(s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	loop := &core.Loop{
		IssueKey:   "PANIC-TRIGGER",
		Summary:    "test panic recovery",
		Status:     core.StatusVerifying,
		RunDir:     t.TempDir(),
		TicketPath: filepath.Join(t.TempDir(), "ticket.md"),
		PlanPath:   filepath.Join(t.TempDir(), "plan.md"),
	}
	if err := s.CreateLoop(ctx, loop); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Daemon: config.DaemonConfig{MaxAutoRetries: 3},
	}
	e := &Engine{cfg: cfg, store: s, logger: logger}

	// This should recover from the panic inside ProcessLoop
	e.processLoopSafely(ctx, loop)

	updated, err := s.GetLoopByID(ctx, loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != core.StatusFailed {
		t.Fatalf("expected status failed, got %s", updated.Status)
	}
	if !testing.Short() && updated.LastError == "" {
		t.Fatal("expected last error to be set, got empty")
	}
}

func TestLastActiveStatusUpdate(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	loop := &core.Loop{
		IssueKey: "TASK-2",
		Summary:  "test last active status",
		Status:   core.StatusCreated,
	}
	if err := s.CreateLoop(ctx, loop); err != nil {
		t.Fatal(err)
	}

	if loop.LastActiveStatus != core.StatusCreated {
		t.Fatalf("expected initial last active status to be created, got %s", loop.LastActiveStatus)
	}

	// Move to implementing (active status)
	if err := s.UpdateLoopStatus(ctx, loop.ID, core.StatusImplementing, ""); err != nil {
		t.Fatal(err)
	}

	updated, err := s.GetLoopByID(ctx, loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastActiveStatus != core.StatusImplementing {
		t.Fatalf("expected last active status to update to implementing, got %s", updated.LastActiveStatus)
	}

	// Pause loop (non-active status)
	if err := s.UpdateLoopStatus(ctx, loop.ID, core.StatusPaused, ""); err != nil {
		t.Fatal(err)
	}

	updated, err = s.GetLoopByID(ctx, loop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != core.StatusPaused {
		t.Fatalf("expected status paused, got %s", updated.Status)
	}
	if updated.LastActiveStatus != core.StatusImplementing {
		t.Fatalf("expected last active status to remain implementing, got %s", updated.LastActiveStatus)
	}
}

func TestCentralMonitorCancellation(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	logger, err := logging.New(s, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()

	loop := &core.Loop{
		IssueKey: "TASK-3",
		Summary:  "test monitor",
		Status:   core.StatusImplementing,
	}
	if err := s.CreateLoop(ctx, loop); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Daemon: config.DaemonConfig{MaxAutoRetries: 3},
	}
	e := NewEngine(cfg, s, logger)

	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	e.registerCancel(loop.ID, cancel)

	// Update status to paused (stopped state)
	if err := s.UpdateLoopStatus(ctx, loop.ID, core.StatusPaused, ""); err != nil {
		t.Fatal(err)
	}

	// Trigger central monitor check
	e.checkRunningLoops(ctx)

	// Verify loopCtx is cancelled
	select {
	case <-loopCtx.Done():
		// Success!
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected registered context to be cancelled by central monitor")
	}
}
