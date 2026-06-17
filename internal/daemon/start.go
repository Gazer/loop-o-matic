package daemon

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"loop-o-matic/internal/config"
	"loop-o-matic/internal/logging"
	"loop-o-matic/internal/store"
)

func Start(ctx context.Context, cfg *config.Config, s *store.Store, logger *logging.Logger) error {
	lock, err := AcquireLock(cfg.Workspace.LogRoot)
	if err != nil {
		return err
	}
	defer lock.Release()

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	engine := NewEngine(cfg, s, logger)
	engine.StartMonitor(ctx)
	logger.Info(ctx, nil, "loopd started with max_running_tasks=%d", cfg.Daemon.MaxRunningTasks)
	if err := engine.Tick(ctx); err != nil {
		logger.Error(ctx, nil, "%v", err)
	}
	ticker := time.NewTicker(cfg.Daemon.TickInterval.Duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info(context.Background(), nil, "loopd stopped")
			return nil
		case <-ticker.C:
			if err := engine.Tick(ctx); err != nil {
				logger.Error(ctx, nil, "%v", err)
			}
		}
	}
}
