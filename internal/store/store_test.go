package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newProject(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := InitProject(context.Background(), root, "prd.md", "goal"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(DBPath(root))
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestInitProjectMigrateAndRepeatGuard(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	defer s.Close()

	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("user_version = %d, want 1", version)
	}
	var wal string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&wal); err != nil {
		t.Fatal(err)
	}
	if wal != "wal" {
		t.Fatalf("journal_mode = %q, want wal", wal)
	}
	if _, err := InitProject(ctx, root, "prd.md", "again"); err == nil {
		t.Fatal("repeat init succeeded")
	}
}

func TestMigrateRejectsUnsupportedFutureVersion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".relo"), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(DBPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA user_version=2`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "unsupported database schema version") {
		t.Fatalf("Migrate err = %v, want unsupported version", err)
	}
	_ = s.Close()
}

func TestWithWriteTxRollbackAndWholeClosureRetry(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	attempts := 0
	err := s.WithWriteTx(ctx, func(tx *Tx) error {
		attempts++
		if attempts == 2 {
			var count int
			if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				t.Fatalf("retry saw unrolled-back rows: %d", count)
			}
		}
		now := "2026-01-01T00:00:00Z"
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO tasks(id,title,objective,priority,creation_order,status,created_at,updated_at) VALUES('TASK-001','title','objective',100,1,'pending',?,?)`, now, now); err != nil {
			return err
		}
		if attempts == 1 {
			return errors.New("database is locked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("tasks = %d, want 1", count)
	}
}

func TestCreateGetListUpdateDeleteTask(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	id, err := s.CreateTask(ctx, "title", "objective", []string{"ac"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if id != "TASK-001" {
		t.Fatalf("id = %s", id)
	}
	task, err := s.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.AcceptanceCriteria) != 1 || task.AcceptanceCriteria[0].ID != "AC-001" {
		t.Fatalf("unexpected ACs: %#v", task.AcceptanceCriteria)
	}
	prio := 5
	if err := s.UpdateTask(ctx, id, nil, nil, &prio, "more urgent"); err != nil {
		t.Fatal(err)
	}
	var events int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_events WHERE task_id=? AND event_type='priority_changed'`, id).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("priority events = %d, want 1", events)
	}
	tasks, err := s.ListTasks(ctx, "pending")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Priority != 5 {
		t.Fatalf("unexpected list: %#v", tasks)
	}
	if err := s.DeleteTask(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestCreateValidationAndRollback(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	if _, err := s.CreateTask(ctx, "title", "objective", nil, 100); err == nil {
		t.Fatal("create without AC succeeded")
	}
	if _, err := s.CreateTask(ctx, "title", "objective", []string{"ac"}, -1); err == nil {
		t.Fatal("create with negative priority succeeded")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("tasks after failed creates = %d, want 0", count)
	}
}

func TestTitleAmbiguityAndMutationRestrictions(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	id1, err := s.CreateTask(ctx, "same", "one", []string{"ac"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(ctx, "same", "two", []string{"ac"}, 100); err != nil {
		t.Fatal(err)
	}
	_, ids, err := s.GetTaskByTitle(ctx, "same")
	if err == nil || len(ids) != 2 {
		t.Fatalf("expected title ambiguity, ids=%v err=%v", ids, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET status='running' WHERE id=?`, id1); err != nil {
		t.Fatal(err)
	}
	newTitle := "new"
	if err := s.UpdateTask(ctx, id1, &newTitle, nil, nil, ""); err == nil {
		t.Fatal("updated running task")
	}
	if err := s.DeleteTask(ctx, id1); err == nil {
		t.Fatal("deleted running task")
	}
}

func TestForeignKeyAndDownstreamDeleteRestriction(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	id1, _ := s.CreateTask(ctx, "one", "objective", []string{"ac"}, 100)
	id2, _ := s.CreateTask(ctx, "two", "objective", []string{"ac"}, 100)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES(?,?,?,datetime('now'))`, id2, "TASK-999", "missing"); err == nil {
		t.Fatal("foreign key allowed missing dependency")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES(?,?,?,datetime('now'))`, id2, id1, "needed"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, id1); err == nil {
		t.Fatal("deleted task with downstream dependency")
	} else {
		var ve ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("err type = %T, want ValidationError", err)
		}
	}
}
