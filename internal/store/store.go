package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"loop-o-matic/internal/core"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(10 * time.Minute)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=10000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS loops (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_key TEXT NOT NULL,
			summary TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			last_active_status TEXT NOT NULL DEFAULT '',
			run_dir TEXT NOT NULL,
			ticket_path TEXT NOT NULL DEFAULT '',
			plan_path TEXT NOT NULL DEFAULT '',
			repo_scope TEXT NOT NULL DEFAULT '',
			extra_instructions_path TEXT NOT NULL DEFAULT '',
			auto_retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_loops_issue_key ON loops(issue_key)`,
		`CREATE TABLE IF NOT EXISTS repo_runs (
			loop_id INTEGER NOT NULL,
			repo_name TEXT NOT NULL,
			path TEXT NOT NULL,
			branch TEXT NOT NULL,
			impact_score INTEGER NOT NULL DEFAULT 0,
			changed INTEGER NOT NULL DEFAULT 0,
			pr_url TEXT NOT NULL DEFAULT '',
			pr_number INTEGER NOT NULL DEFAULT 0,
			ci_state TEXT NOT NULL DEFAULT '',
			review_decision TEXT NOT NULL DEFAULT '',
			feedback_hash TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(loop_id, repo_name)
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			loop_id INTEGER NOT NULL DEFAULT 0,
			issue_key TEXT NOT NULL DEFAULT '',
			level TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.ensureColumn(ctx, "loops", "repo_scope", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "loops", "last_active_status", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "loops", "auto_retry_count", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "loops", "extra_instructions_path", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := s.ensureColumn(ctx, "repo_runs", "feedback_hash", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensureColumn(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+column+` `+definition)
	return err
}

func (s *Store) CreateLoop(ctx context.Context, loop *core.Loop) error {
	now := time.Now().UTC()
	loop.CreatedAt = now
	loop.UpdatedAt = now
	if loop.LastActiveStatus == "" {
		loop.LastActiveStatus = loop.Status
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO loops(issue_key, summary, status, last_active_status, run_dir, ticket_path, plan_path, repo_scope, extra_instructions_path, auto_retry_count, last_error, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		loop.IssueKey, loop.Summary, loop.Status, loop.LastActiveStatus, loop.RunDir, loop.TicketPath, loop.PlanPath, loop.RepoScope, loop.ExtraInstructionsPath, loop.AutoRetryCount, loop.LastError, ts(now), ts(now))
	if err != nil {
		return err
	}
	loop.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetLatestLoopByIssue(ctx context.Context, issueKey string) (*core.Loop, error) {
	row := s.db.QueryRowContext(ctx, selectLoopSQL+` WHERE issue_key = ? ORDER BY id DESC LIMIT 1`, issueKey)
	return scanLoop(row)
}

func (s *Store) GetLoopByID(ctx context.Context, id int64) (*core.Loop, error) {
	row := s.db.QueryRowContext(ctx, selectLoopSQL+` WHERE id = ?`, id)
	return scanLoop(row)
}

func (s *Store) ListLoops(ctx context.Context) ([]core.Loop, error) {
	rows, err := s.db.QueryContext(ctx, selectLoopSQL+` ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var loops []core.Loop
	for rows.Next() {
		loop, err := scanLoopRows(rows)
		if err != nil {
			return nil, err
		}
		loops = append(loops, *loop)
	}
	return loops, rows.Err()
}

func (s *Store) ActiveLoops(ctx context.Context) ([]core.Loop, error) {
	rows, err := s.db.QueryContext(ctx, selectLoopSQL+` WHERE status NOT IN (?, ?, ?, ?) ORDER BY id ASC`, core.StatusCompleted, core.StatusFailed, core.StatusCancelled, core.StatusBlocked)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var loops []core.Loop
	for rows.Next() {
		loop, err := scanLoopRows(rows)
		if err != nil {
			return nil, err
		}
		loops = append(loops, *loop)
	}
	return loops, rows.Err()
}

func isActiveStatus(status string) bool {
	switch status {
	case core.StatusCreated, core.StatusPreparingWorkspace, core.StatusDiscovering, core.StatusImplementing, core.StatusVerifying, core.StatusCreatingPRs, core.StatusMonitoringCI, core.StatusWaitingHumanReview:
		return true
	}
	return false
}

func (s *Store) UpdateLoopStatus(ctx context.Context, id int64, status, lastError string) error {
	var err error
	if isActiveStatus(status) {
		_, err = s.db.ExecContext(ctx, `UPDATE loops SET status = ?, last_active_status = ?, last_error = ?, updated_at = ? WHERE id = ?`, status, status, lastError, ts(time.Now().UTC()), id)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE loops SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`, status, lastError, ts(time.Now().UTC()), id)
	}
	return err
}

func (s *Store) UpdateLoopStatusIfNotStopped(ctx context.Context, id int64, status, lastError string) error {
	var err error
	if isActiveStatus(status) {
		_, err = s.db.ExecContext(ctx, `UPDATE loops SET status = ?, last_active_status = ?, last_error = ?, updated_at = ? WHERE id = ? AND status NOT IN (?, ?, ?)`, status, status, lastError, ts(time.Now().UTC()), id, core.StatusCompleted, core.StatusCancelled, core.StatusPaused)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE loops SET status = ?, last_error = ?, updated_at = ? WHERE id = ? AND status NOT IN (?, ?, ?)`, status, lastError, ts(time.Now().UTC()), id, core.StatusCompleted, core.StatusCancelled, core.StatusPaused)
	}
	return err
}

func (s *Store) UpdateLoopStatusAndRetry(ctx context.Context, id int64, status, lastError string, retryCount int) error {
	var err error
	if isActiveStatus(status) {
		_, err = s.db.ExecContext(ctx, `UPDATE loops SET status = ?, last_active_status = ?, last_error = ?, auto_retry_count = ?, updated_at = ? WHERE id = ? AND status NOT IN (?, ?, ?)`, status, status, lastError, retryCount, ts(time.Now().UTC()), id, core.StatusCompleted, core.StatusCancelled, core.StatusPaused)
	} else {
		_, err = s.db.ExecContext(ctx, `UPDATE loops SET status = ?, last_error = ?, auto_retry_count = ?, updated_at = ? WHERE id = ? AND status NOT IN (?, ?, ?)`, status, lastError, retryCount, ts(time.Now().UTC()), id, core.StatusCompleted, core.StatusCancelled, core.StatusPaused)
	}
	return err
}

func (s *Store) ResetAutoRetryCount(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE loops SET auto_retry_count = 0, updated_at = ? WHERE id = ?`, ts(time.Now().UTC()), id)
	return err
}

func (s *Store) UpdateLoopPlan(ctx context.Context, id int64, planPath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE loops SET plan_path = ?, updated_at = ? WHERE id = ?`, planPath, ts(time.Now().UTC()), id)
	return err
}

func (s *Store) UpdateLoopSummary(ctx context.Context, id int64, summary string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE loops SET summary = ?, updated_at = ? WHERE id = ?`, summary, ts(time.Now().UTC()), id)
	return err
}

func (s *Store) UpdateLoopExtraInstructionsPath(ctx context.Context, id int64, path string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE loops SET extra_instructions_path = ?, updated_at = ? WHERE id = ?`, path, ts(time.Now().UTC()), id)
	return err
}

func (s *Store) UpsertRepoRun(ctx context.Context, repo core.RepoRun) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO repo_runs(loop_id, repo_name, path, branch, impact_score, changed, pr_url, pr_number, ci_state, review_decision, feedback_hash)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(loop_id, repo_name) DO UPDATE SET path=excluded.path, branch=excluded.branch, impact_score=excluded.impact_score, changed=excluded.changed, pr_url=excluded.pr_url, pr_number=excluded.pr_number, ci_state=excluded.ci_state, review_decision=excluded.review_decision, feedback_hash=excluded.feedback_hash`,
		repo.LoopID, repo.RepoName, repo.Path, repo.Branch, repo.ImpactScore, boolInt(repo.Changed), repo.PRURL, repo.PRNumber, repo.CIState, repo.ReviewDecision, repo.FeedbackHash)
	return err
}

func (s *Store) RepoRuns(ctx context.Context, loopID int64) ([]core.RepoRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT loop_id, repo_name, path, branch, impact_score, changed, pr_url, pr_number, ci_state, review_decision, feedback_hash FROM repo_runs WHERE loop_id = ? ORDER BY repo_name`, loopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var repos []core.RepoRun
	for rows.Next() {
		var repo core.RepoRun
		var changed int
		if err := rows.Scan(&repo.LoopID, &repo.RepoName, &repo.Path, &repo.Branch, &repo.ImpactScore, &changed, &repo.PRURL, &repo.PRNumber, &repo.CIState, &repo.ReviewDecision, &repo.FeedbackHash); err != nil {
			return nil, err
		}
		repo.Changed = changed != 0
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (s *Store) AddEvent(ctx context.Context, event core.Event) error {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(loop_id, issue_key, level, message, created_at) VALUES(?,?,?,?,?)`, event.LoopID, event.IssueKey, event.Level, event.Message, ts(event.CreatedAt))
	return err
}

func (s *Store) Events(ctx context.Context, issueKey string, limit int) ([]core.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, loop_id, issue_key, level, message, created_at FROM events WHERE issue_key = ? ORDER BY id DESC LIMIT ?`, issueKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []core.Event
	for rows.Next() {
		var e core.Event
		var created string
		if err := rows.Scan(&e.ID, &e.LoopID, &e.IssueKey, &e.Level, &e.Message, &created); err != nil {
			return nil, err
		}
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, e)
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return events, rows.Err()
}

func (s *Store) DeleteLoop(ctx context.Context, loopID int64, issueKey string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM repo_runs WHERE loop_id = ?`, loopID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE loop_id = ? OR issue_key = ?`, loopID, issueKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM loops WHERE id = ?`, loopID); err != nil {
		return err
	}
	return tx.Commit()
}

type scanner interface{ Scan(dest ...any) error }

const selectLoopSQL = `SELECT id, issue_key, summary, status, last_active_status, run_dir, ticket_path, plan_path, repo_scope, extra_instructions_path, auto_retry_count, last_error, created_at, updated_at FROM loops`

func scanLoop(row scanner) (*core.Loop, error) {
	loop, err := scanLoopRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return loop, err
}

func scanLoopRows(row scanner) (*core.Loop, error) {
	var loop core.Loop
	var created, updated string
	if err := row.Scan(&loop.ID, &loop.IssueKey, &loop.Summary, &loop.Status, &loop.LastActiveStatus, &loop.RunDir, &loop.TicketPath, &loop.PlanPath, &loop.RepoScope, &loop.ExtraInstructionsPath, &loop.AutoRetryCount, &loop.LastError, &created, &updated); err != nil {
		return nil, err
	}
	loop.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	loop.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &loop, nil
}

func ts(t time.Time) string { return t.Format(time.RFC3339Nano) }
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func IsNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func FormatNotFound(issue string) error { return fmt.Errorf("loop not found for issue %s", issue) }
