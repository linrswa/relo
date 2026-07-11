package store

import (
	"context"
	"strings"
	"time"

	"relo/internal/domain"
)

func (s *Store) StartTasks(ctx context.Context, ids []string) ([]string, error) {
	ids, err := uniqueTaskIDs(ids)
	if err != nil {
		return nil, err
	}
	var started []string
	err = s.WithWriteTx(ctx, func(tx *Tx) error {
		if err := requireExistingTasks(ctx, tx.tx, "task", ids); err != nil {
			return err
		}
		g, err := tx.graph(ctx)
		if err != nil {
			return err
		}
		ready := map[string]bool{}
		for _, t := range g.Ready() {
			ready[t.ID] = true
		}
		tasks := make([]*domain.Task, 0, len(ids))
		for _, id := range ids {
			t, err := getTask(ctx, tx.tx, id)
			if err != nil {
				return err
			}
			if t.Status != domain.StatusPending {
				return validation("%s is %s and cannot be started", id, t.Status)
			}
			if !ready[id] {
				return validation("%s is blocked by unmet dependencies", id)
			}
			if n, err := openAttemptCount(ctx, tx.tx, id); err != nil {
				return err
			} else if n != 0 {
				return validation("%s already has %d running attempt(s)", id, n)
			}
			tasks = append(tasks, t)
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		started = started[:0]
		for _, t := range tasks {
			attempt := t.AttemptCount + 1
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO attempts(task_id,attempt_number,status,started_at) VALUES(?,?,'running',?)`, t.ID, attempt, now); err != nil {
				return err
			}
			if _, err := tx.tx.ExecContext(ctx, `UPDATE tasks SET status='running', attempt_count=?, updated_at=? WHERE id=?`, attempt, now, t.ID); err != nil {
				return err
			}
			started = append(started, t.ID)
		}
		return nil
	})
	return started, err
}

func (s *Store) StopTasks(ctx context.Context, ids []string, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return validation("--reason is required")
	}
	ids, err := uniqueTaskIDs(ids)
	if err != nil {
		return err
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		if err := requireExistingTasks(ctx, tx.tx, "task", ids); err != nil {
			return err
		}
		for _, id := range ids {
			t, err := getTask(ctx, tx.tx, id)
			if err != nil {
				return err
			}
			if t.Status != domain.StatusRunning {
				return validation("%s is %s and cannot be stopped", id, t.Status)
			}
			if err := requireExactlyOneOpenAttempt(ctx, tx.tx, id); err != nil {
				return err
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, id := range ids {
			if _, err := tx.tx.ExecContext(ctx, `UPDATE attempts SET status='interrupted', completed_at=?, reason=? WHERE task_id=? AND status='running'`, now, reason, id); err != nil {
				return err
			}
			if _, err := tx.tx.ExecContext(ctx, `UPDATE tasks SET status='pending', updated_at=? WHERE id=?`, now, id); err != nil {
				return err
			}
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO task_events(task_id,event_type,reason,created_at) VALUES(?,'stopped',?,?)`, id, reason, now); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) PassTask(ctx context.Context, id, summary string) error {
	return s.closeRunningTask(ctx, id, domain.StatusPassed, summary, "")
}

func (s *Store) FailTask(ctx context.Context, id, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return validation("--reason is required")
	}
	return s.closeRunningTask(ctx, id, domain.StatusFailed, "", reason)
}

func (s *Store) RetryTask(ctx context.Context, id string) error {
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		if err := requireExistingTasks(ctx, tx.tx, "task", []string{id}); err != nil {
			return err
		}
		t, err := getTask(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if t.Status != domain.StatusFailed {
			return validation("%s is %s and cannot be retried", id, t.Status)
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET status='pending', updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), id)
		return err
	})
}

func (s *Store) ReopenTask(ctx context.Context, id, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return validation("--reason is required")
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		if err := requireExistingTasks(ctx, tx.tx, "task", []string{id}); err != nil {
			return err
		}
		t, err := getTask(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if t.Status != domain.StatusPassed {
			return validation("%s is %s and cannot be reopened", id, t.Status)
		}
		affected, err := downstreamWithStatus(ctx, tx.tx, id, []string{domain.StatusRunning, domain.StatusPassed})
		if err != nil {
			return err
		}
		if len(affected) > 0 {
			return validation("%s cannot be reopened because downstream task(s) are running or passed: %s", id, strings.Join(affected, ", "))
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.tx.ExecContext(ctx, `UPDATE tasks SET status='pending', updated_at=? WHERE id=?`, now, id); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `INSERT INTO task_events(task_id,event_type,reason,created_at) VALUES(?,'reopened',?,?)`, id, reason, now)
		return err
	})
}

func (s *Store) closeRunningTask(ctx context.Context, id, status, summary, reason string) error {
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		if err := requireExistingTasks(ctx, tx.tx, "task", []string{id}); err != nil {
			return err
		}
		t, err := getTask(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if t.Status != domain.StatusRunning {
			return validation("%s is %s and cannot transition to %s", id, t.Status, status)
		}
		if err := requireExactlyOneOpenAttempt(ctx, tx.tx, id); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if status == domain.StatusPassed {
			if _, err := tx.tx.ExecContext(ctx, `UPDATE attempts SET status='passed', completed_at=?, summary=? WHERE task_id=? AND status='running'`, now, summary, id); err != nil {
				return err
			}
			_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET status='passed', last_completion_summary=?, updated_at=? WHERE id=?`, summary, now, id)
			return err
		}
		if _, err := tx.tx.ExecContext(ctx, `UPDATE attempts SET status='failed', completed_at=?, reason=? WHERE task_id=? AND status='running'`, now, reason, id); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET status='failed', last_failure_reason=?, updated_at=? WHERE id=?`, reason, now, id)
		return err
	})
}

func uniqueTaskIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, validation("at least one task ID is required")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, validation("task ID must not be empty")
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func openAttemptCount(ctx context.Context, q queryer, id string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE task_id=? AND status='running'`, id).Scan(&n)
	return n, err
}

func requireExactlyOneOpenAttempt(ctx context.Context, q queryer, id string) error {
	n, err := openAttemptCount(ctx, q, id)
	if err != nil {
		return err
	}
	if n != 1 {
		return validation("%s has %d running attempt(s); expected 1", id, n)
	}
	return nil
}

func downstreamWithStatus(ctx context.Context, q queryer, id string, statuses []string) ([]string, error) {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(statuses)), ",")
	args := []any{id}
	for _, st := range statuses {
		args = append(args, st)
	}
	rows, err := q.QueryContext(ctx, `SELECT d.task_id FROM dependencies d JOIN tasks t ON t.id=d.task_id WHERE d.dependency_id=? AND t.status IN (`+placeholders+`) ORDER BY t.priority,t.creation_order,t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
