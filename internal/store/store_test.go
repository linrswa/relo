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

func TestAcceptanceNotesDependenciesReadyValidateAndRollback(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	defer s.Close()

	id1, _ := s.CreateTask(ctx, "one", "objective", []string{"ac"}, 2)
	id2, _ := s.CreateTask(ctx, "two", "objective", []string{"ac"}, 1)
	id3, _ := s.CreateTask(ctx, "three", "objective", []string{"ac"}, 1)

	ac2, err := s.AddAcceptance(ctx, id1, "second")
	if err != nil || ac2 != "AC-002" {
		t.Fatalf("AddAcceptance = %s %v", ac2, err)
	}
	if err := s.UpdateAcceptance(ctx, id1, ac2, "updated"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAcceptance(ctx, id1, ac2); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAcceptance(ctx, id1, "AC-001"); err == nil {
		t.Fatal("removed final AC")
	}
	note, err := s.AddNote(ctx, id1, "remember")
	if err != nil || note != "NOTE-001" {
		t.Fatalf("AddNote = %s %v", note, err)
	}
	if err := s.UpdateNote(ctx, id1, note, "updated note"); err != nil {
		t.Fatal(err)
	}
	task, err := s.GetTask(ctx, id1)
	if err != nil || len(task.Notes) != 1 || task.Notes[0].Text != "updated note" {
		t.Fatalf("notes not in task get: %#v err=%v", task, err)
	}

	if err := s.AddDependencies(ctx, id3, []string{id1, id2}, "", map[string]string{id1: "schema", id2: "api"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependencies(ctx, id3, []string{id1}, "dup", nil); err == nil {
		t.Fatal("duplicate dependency add succeeded")
	}
	if err := s.AddDependencies(ctx, id1, []string{id3}, "cycle", nil); err == nil {
		t.Fatal("cycle add succeeded")
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies WHERE task_id=? AND dependency_id=?`, id1, id3).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cycle rollback left %d edge(s)", count)
	}
	if err := s.UpdateDependencyReason(ctx, id3, id1, "new reason"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveDependencies(ctx, id3, []string{id1, "TASK-999"}, "remove"); err == nil {
		t.Fatal("mixed valid/missing remove succeeded")
	} else if !strings.Contains(err.Error(), "dependency task TASK-999 does not exist") {
		t.Fatalf("remove err = %v, want missing ID validation", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies WHERE task_id=?`, id3).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("batch rollback count = %d, want 2", count)
	}
	if err := s.RemoveDependencies(ctx, id3, []string{id1, id2}, "remove"); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependency_events WHERE task_id=?`, id3).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 { // 2 add, 1 reason update, 2 remove
		t.Fatalf("dependency events = %d", count)
	}

	ready, err := s.ReadyTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 3 || ready[0].ID != id2 || ready[1].ID != id3 || ready[2].ID != id1 {
		t.Fatalf("ready ordering = %#v", ready)
	}
	if _, err := s.AddNote(ctx, id1, "another"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := s.Validate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 0 {
		t.Fatalf("validate errors = %v", report.Errors)
	}
	joined := strings.Join(report.Warnings, "\n")
	if !strings.Contains(joined, "PRD content hash has changed") || !strings.Contains(joined, "has no notes") || !strings.Contains(joined, "multiple tasks use priority 1") {
		t.Fatalf("warnings = %v", report.Warnings)
	}
}

func TestDependencyMissingIDsReturnValidationErrors(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	id1, _ := s.CreateTask(ctx, "one", "objective", []string{"ac"}, 100)
	if err := s.AddDependencies(ctx, id1, []string{"TASK-999"}, "missing", nil); err == nil {
		t.Fatal("add missing dependency succeeded")
	} else {
		var ve ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "dependency task TASK-999 does not exist") {
			t.Fatalf("add err = %T %v, want actionable ValidationError", err, err)
		}
	}
	if err := s.AddDependencies(ctx, "TASK-999", []string{id1}, "missing target", nil); err == nil {
		t.Fatal("add missing target succeeded")
	} else {
		var ve ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "task TASK-999 does not exist") {
			t.Fatalf("add target err = %T %v, want actionable ValidationError", err, err)
		}
	}
	if err := s.RemoveDependencies(ctx, id1, []string{"TASK-999"}, "missing"); err == nil {
		t.Fatal("remove missing dependency succeeded")
	} else {
		var ve ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "dependency task TASK-999 does not exist") {
			t.Fatalf("remove err = %T %v, want actionable ValidationError", err, err)
		}
	}
}

func TestValidateDetectsUnmetDependenciesForRunningAndPassedTasks(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	dep, _ := s.CreateTask(ctx, "dep", "objective", []string{"ac"}, 100)
	running, _ := s.CreateTask(ctx, "running", "objective", []string{"ac"}, 100)
	passed, _ := s.CreateTask(ctx, "passed", "objective", []string{"ac"}, 100)
	if err := s.AddDependencies(ctx, running, []string{dep}, "needed", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependencies(ctx, passed, []string{dep}, "needed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET status='running' WHERE id=?`, running); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET status='passed' WHERE id=?`, passed); err != nil {
		t.Fatal(err)
	}
	report, err := s.Validate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Errors, "\n")
	if !strings.Contains(joined, "running task "+running+" has unmet dependency "+dep) {
		t.Fatalf("missing running unmet dependency error: %v", report.Errors)
	}
	if !strings.Contains(joined, "passed task "+passed+" has unmet dependency "+dep) {
		t.Fatalf("missing passed unmet dependency error: %v", report.Errors)
	}
}

func TestValidateDetectsMissingDependencyEndpointsWithForeignKeysOff(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	id1, _ := s.CreateTask(ctx, "one", "objective", []string{"ac"}, 100)
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES(?,?,?,datetime('now'))`, id1, "TASK-999", "corrupt"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES(?,?,?,datetime('now'))`, "TASK-998", id1, "corrupt"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	report, err := s.Validate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.Errors, "\n")
	if !strings.Contains(joined, "dependency "+id1+" -> TASK-999 references missing dependency endpoint") || !strings.Contains(joined, "dependency TASK-998 -> "+id1+" references missing task endpoint") {
		t.Fatalf("missing endpoint errors = %v", report.Errors)
	}
}

func TestDefinitionMutationRestrictionsCoverMilestone2Mutations(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	id1, _ := s.CreateTask(ctx, "one", "objective", []string{"ac"}, 100)
	id2, _ := s.CreateTask(ctx, "two", "objective", []string{"ac"}, 100)
	if err := s.AddDependencies(ctx, id1, []string{id2}, "needed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET status='passed' WHERE id=?`, id1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddAcceptance(ctx, id1, "new"); err == nil {
		t.Fatal("added acceptance to passed task")
	}
	if _, err := s.AddNote(ctx, id1, "new"); err == nil {
		t.Fatal("added note to passed task")
	}
	if err := s.UpdateDependencyReason(ctx, id1, id2, "changed"); err == nil {
		t.Fatal("updated dependency reason on passed task")
	}
	if err := s.RemoveDependencies(ctx, id1, []string{id2}, "remove"); err == nil {
		t.Fatal("removed dependency from passed task")
	}
	if err := s.AddDependencies(ctx, id1, []string{"TASK-999"}, "new", nil); err == nil {
		t.Fatal("added dependency to passed task")
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
