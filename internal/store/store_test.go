package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func createTaskForRuntime(t *testing.T, ctx context.Context, s *Store, title string) string {
	t.Helper()
	id, err := s.CreateTask(ctx, title, "objective", []string{"ac"}, 100)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func taskStatus(t *testing.T, ctx context.Context, s *Store, id string) string {
	t.Helper()
	task, err := s.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return task.Status
}

func TestReadSnapshotsAssembleConsistentPayloads(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	dep := createTaskForRuntime(t, ctx, s, "dep")
	down := createTaskForRuntime(t, ctx, s, "down")
	if _, err := s.AddNote(ctx, dep, "note"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependencies(ctx, down, []string{dep}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	snap, err := s.TaskReadSnapshot(ctx, down)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Project.Goal != "goal" || snap.Project.PRDPath != "prd.md" || snap.Task.ID != down || len(snap.Task.AcceptanceCriteria) != 1 || len(snap.Task.Dependencies) != 1 {
		t.Fatalf("unexpected task snapshot: %#v", snap)
	}
	if snap.Task.Dependencies[0].DependencyID != dep || snap.Task.Dependencies[0].Reason != "needs" || snap.Task.Dependencies[0].Title != "dep" || snap.Task.Dependencies[0].Status != "pending" {
		t.Fatalf("unexpected dependency snapshot: %#v", snap.Task.Dependencies[0])
	}
	titleSnap, err := s.TaskReadSnapshotByTitle(ctx, "down")
	if err != nil || titleSnap.Task.ID != down || titleSnap.Project.Goal != "goal" {
		t.Fatalf("title snapshot = %#v err=%v", titleSnap, err)
	}
	ready, err := s.ReadyReadSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready.Graph.Tasks) != 2 || len(ready.Tasks) != 1 || ready.Tasks[0].ID != dep || len(ready.Tasks[0].Notes) != 1 {
		t.Fatalf("unexpected ready snapshot: %#v", ready)
	}
}

func TestInitProjectMigrateAndRepeatGuard(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	defer s.Close()

	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("user_version = %d, want 2", version)
	}
	p, err := s.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.NextMilestoneSequence != 1 {
		t.Fatalf("next milestone sequence = %d, want 1", p.NextMilestoneSequence)
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

func TestProjectUpdateGoalPRDAndRollback(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	defer s.Close()

	p, err := s.UpdateProject(ctx, stringPtr("new goal"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Goal != "new goal" || p.PRDPath != "prd.md" {
		t.Fatalf("goal update project = %#v", p)
	}
	if _, err := s.UpdateProject(ctx, stringPtr(""), nil); err == nil || !strings.Contains(err.Error(), "--goal must not be empty") {
		t.Fatalf("empty goal err = %v", err)
	}
	if _, err := s.UpdateProject(ctx, nil, nil); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("no flags err = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "prd2.md"), []byte("prd2"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err = s.UpdateProject(ctx, nil, stringPtr("docs/prd2.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := HashFile(filepath.Join(root, "docs", "prd2.md"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Goal != "new goal" || p.PRDPath != filepath.Join("docs", "prd2.md") || p.PRDHash != wantHash {
		t.Fatalf("prd update project = %#v want hash %s", p, wantHash)
	}

	before := *p
	if _, err := s.UpdateProject(ctx, stringPtr("rolled back"), stringPtr("missing.md")); err == nil || !strings.Contains(err.Error(), "cannot read PRD") {
		t.Fatalf("missing prd err = %v", err)
	}
	after, err := s.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Goal != before.Goal || after.PRDPath != before.PRDPath || after.PRDHash != before.PRDHash {
		t.Fatalf("project changed after rollback: before=%#v after=%#v", before, after)
	}
}

func stringPtr(s string) *string { return &s }

func TestRefreshPRDHashChangedAndUnchanged(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	defer s.Close()

	before, err := s.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	oldHash, newHash, p, err := s.RefreshPRDHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if oldHash != before.PRDHash || newHash == oldHash || p.PRDHash != newHash {
		t.Fatalf("changed refresh old=%s new=%s project=%#v before=%#v", oldHash, newHash, p, before)
	}
	report, err := s.Validate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(report.Warnings, "\n"), "PRD content hash has changed") {
		t.Fatalf("refresh did not clear PRD warning: %#v", report.Warnings)
	}

	beforeUnchanged := p.UpdatedAt
	time.Sleep(time.Millisecond)
	oldHash, newHash, p, err = s.RefreshPRDHash(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if oldHash != newHash || p.PRDHash != newHash || p.UpdatedAt == beforeUnchanged {
		t.Fatalf("unchanged refresh old=%s new=%s project=%#v previous updated_at=%s", oldHash, newHash, p, beforeUnchanged)
	}
}

func TestMigrateV1PreservesDataAndV2IsNoOp(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".relo"), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(DBPath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.ExecContext(ctx, schemaV1+`
		INSERT INTO projects(id,goal,prd_path,prd_hash,next_task_sequence,created_at,updated_at) VALUES(1,'goal','prd.md','hash',3,'project-created','project-updated');
		INSERT INTO tasks(id,title,objective,priority,creation_order,status,attempt_count,last_failure_reason,last_completion_summary,created_at,updated_at) VALUES('TASK-001','title','objective',7,1,'passed',1,'old failure','completed','task-created','task-updated');
		INSERT INTO tasks(id,title,objective,priority,creation_order,status,created_at,updated_at) VALUES('TASK-002','dependency','dependency objective',8,2,'pending','dependency-created','dependency-updated');
		INSERT INTO acceptance_criteria(task_id,criterion_id,text,position) VALUES('TASK-001','AC-001','first criterion',0),('TASK-001','AC-002','second criterion',1);
		INSERT INTO notes(task_id,note_id,text,position) VALUES('TASK-001','NOTE-001','a note',0);
		INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES('TASK-001','TASK-002','needed','dependency-created');
		INSERT INTO attempts(task_id,attempt_number,status,started_at,completed_at,summary,reason) VALUES('TASK-001',1,'passed','attempt-started','attempt-completed','done','attempt reason');
		INSERT INTO dependency_events(task_id,dependency_id,action,reason,created_at) VALUES('TASK-001','TASK-002','added','needed','dependency-event-created');
		INSERT INTO task_events(task_id,event_type,reason,created_at) VALUES('TASK-001','reopened','task reason','task-event-created');
		PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.Project(ctx)
	if err != nil || p.Goal != "goal" || p.PRDPath != "prd.md" || p.PRDHash != "hash" || p.NextTaskSequence != 3 || p.NextMilestoneSequence != 1 || p.CreatedAt != "project-created" || p.UpdatedAt != "project-updated" {
		t.Fatalf("migrated project = %#v, err=%v", p, err)
	}
	var title, objective, status, failure, summary, created, updated string
	var priority, order, attempts int
	if err := s.db.QueryRowContext(ctx, `SELECT title,objective,priority,creation_order,status,attempt_count,last_failure_reason,last_completion_summary,created_at,updated_at FROM tasks WHERE id='TASK-001'`).Scan(&title, &objective, &priority, &order, &status, &attempts, &failure, &summary, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if title != "title" || objective != "objective" || priority != 7 || order != 1 || status != "passed" || attempts != 1 || failure != "old failure" || summary != "completed" || created != "task-created" || updated != "task-updated" {
		t.Fatalf("migrated task fields were not preserved")
	}
	var attemptNumber int
	var attemptStatus, started, completed, attemptSummary, reason string
	if err := s.db.QueryRowContext(ctx, `SELECT attempt_number,status,started_at,completed_at,summary,reason FROM attempts WHERE task_id='TASK-001'`).Scan(&attemptNumber, &attemptStatus, &started, &completed, &attemptSummary, &reason); err != nil {
		t.Fatal(err)
	}
	if attemptNumber != 1 || attemptStatus != "passed" || started != "attempt-started" || completed != "attempt-completed" || attemptSummary != "done" || reason != "attempt reason" {
		t.Fatalf("migrated attempt fields were not preserved")
	}
	var criteria, note, dependency, dependencyEvent, taskEvent string
	if err := s.db.QueryRowContext(ctx, `SELECT group_concat(criterion_id || ':' || text || ':' || position, '|') FROM (SELECT * FROM acceptance_criteria WHERE task_id='TASK-001' ORDER BY position)`).Scan(&criteria); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT note_id || ':' || text || ':' || position FROM notes WHERE task_id='TASK-001'`).Scan(&note); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT dependency_id || ':' || reason || ':' || created_at FROM dependencies WHERE task_id='TASK-001'`).Scan(&dependency); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT dependency_id || ':' || action || ':' || reason || ':' || created_at FROM dependency_events WHERE task_id='TASK-001'`).Scan(&dependencyEvent); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT event_type || ':' || reason || ':' || created_at FROM task_events WHERE task_id='TASK-001'`).Scan(&taskEvent); err != nil {
		t.Fatal(err)
	}
	if criteria != "AC-001:first criterion:0|AC-002:second criterion:1" || note != "NOTE-001:a note:0" || dependency != "TASK-002:needed:dependency-created" || dependencyEvent != "TASK-002:added:needed:dependency-event-created" || taskEvent != "reopened:task reason:task-event-created" {
		t.Fatalf("migration lost related values: criteria=%q note=%q dependency=%q dependency event=%q task event=%q", criteria, note, dependency, dependencyEvent, taskEvent)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("v2 migration was not a no-op: %v", err)
	}
}

func TestMigrateV2FailureRollsBackSchemaAndVersion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".relo"), 0755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(DBPath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.ExecContext(ctx, schemaV1+`
		INSERT INTO projects(id,goal,prd_path,prd_hash,next_task_sequence,created_at,updated_at) VALUES(1,'goal','prd','hash',1,'now','now');
		CREATE TABLE milestones (poison INTEGER);
		PRAGMA user_version=1;`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("migration with conflicting v2 DDL succeeded")
	}
	var version, milestoneColumn, v2Tables int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('projects') WHERE name='next_milestone_sequence'`).Scan(&milestoneColumn); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('milestone_anchors','milestone_recommendations','milestone_snapshots')`).Scan(&v2Tables); err != nil {
		t.Fatal(err)
	}
	if version != 1 || milestoneColumn != 0 || v2Tables != 0 {
		t.Fatalf("failed migration was not rolled back: version=%d milestone column=%d v2 tables=%d", version, milestoneColumn, v2Tables)
	}
}

func TestMigrationV2SchemaConstraints(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	reject := func(statement string) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("constraint allowed: %s", statement)
		}
	}
	for _, statement := range []string{
		`INSERT INTO projects(id,goal,prd_path,prd_hash,next_task_sequence,next_milestone_sequence,created_at,updated_at) VALUES(2,'g','p','h',1,0,'now','now')`,
		`INSERT INTO milestones(id,title,reason,status,creation_order,next_recommendation_sequence,created_at,updated_at) VALUES('MILESTONE-001','title','reason','planned',1,0,'now','now')`,
		`INSERT INTO milestones(id,title,reason,status,creation_order,mark_summary,created_at,updated_at) VALUES('MILESTONE-001','title','reason','planned',1,'summary','now','now')`,
		`INSERT INTO milestones(id,title,reason,status,creation_order,marked_at,created_at,updated_at) VALUES('MILESTONE-001','title','reason','planned',1,'now','now','now')`,
		`INSERT INTO milestones(id,title,reason,status,creation_order,marked_at,created_at,updated_at) VALUES('MILESTONE-001','title','reason','marked',1,'now','now','now')`,
		`INSERT INTO milestones(id,title,reason,status,creation_order,mark_summary,created_at,updated_at) VALUES('MILESTONE-001','title','reason','marked',1,'summary','now','now')`,
	} {
		reject(statement)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO milestones(id,title,reason,status,creation_order,created_at,updated_at) VALUES('MILESTONE-001','title','reason','planned',1,'now','now')`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`INSERT INTO milestone_recommendations(milestone_id,recommendation_id,text,position,created_at,updated_at) VALUES('MILESTONE-001','REC-001','',0,'now','now')`,
		`INSERT INTO milestone_recommendations(milestone_id,recommendation_id,text,position,created_at,updated_at) VALUES('MILESTONE-001','REC-001','text',-1,'now','now')`,
		`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','task',-1,1,0,1,1,'passed','now','now')`,
		`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','task',0,0,0,1,1,'passed','now','now')`,
		`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','task',0,1,-1,1,1,'passed','now','now')`,
		`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','task',0,1,0,2,1,'passed','now','now')`,
		`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','task',0,1,0,1,0,'passed','now','now')`,
		`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','task',0,1,0,1,1,'pending','now','now')`,
	} {
		reject(statement)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO milestone_recommendations(milestone_id,recommendation_id,text,position,created_at,updated_at) VALUES('MILESTONE-001','REC-001','text',0,'now','now')`); err != nil {
		t.Fatal(err)
	}
	reject(`INSERT INTO milestone_recommendations(milestone_id,recommendation_id,text,position,created_at,updated_at) VALUES('MILESTONE-001','REC-001','other',1,'now','now')`)
	reject(`INSERT INTO milestone_recommendations(milestone_id,recommendation_id,text,position,created_at,updated_at) VALUES('MILESTONE-001','REC-002','other',0,'now','now')`)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','task',0,1,0,1,1,'passed','now','now')`); err != nil {
		t.Fatal(err)
	}
	reject(`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-001','other',0,2,1,0,2,'passed','now','now')`)
	reject(`INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-002','other',0,2,0,0,2,'passed','now','now')`)
}

func TestMigrationV2ForeignKeyDeleteSemantics(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	taskID := createTaskForRuntime(t, ctx, s, "anchor")
	if _, err := s.db.ExecContext(ctx, `INSERT INTO milestones(id,title,reason,status,creation_order,mark_summary,marked_at,created_at,updated_at) VALUES('MILESTONE-001','title','reason','marked',1,'summary','now','now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO milestone_anchors(milestone_id,task_id,created_at) VALUES('MILESTONE-001',?,'now')`, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO milestone_recommendations(milestone_id,recommendation_id,text,position,created_at,updated_at) VALUES('MILESTONE-001','REC-001','text',0,'now','now')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO milestone_snapshots(milestone_id,task_id,task_title,priority,creation_order,scope_position,is_anchor,attempt_number,status,task_updated_at,captured_at) VALUES('MILESTONE-001','TASK-999','gone task',0,1,0,0,1,'passed','now','now')`); err != nil {
		t.Fatalf("snapshot task_id unexpectedly has a task FK: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, taskID); err == nil {
		t.Fatal("anchor task delete was not restricted")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM milestones WHERE id='MILESTONE-001'`); err == nil {
		t.Fatal("milestone delete with snapshot was not restricted")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM milestone_snapshots WHERE milestone_id='MILESTONE-001'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM milestones WHERE id='MILESTONE-001'`); err != nil {
		t.Fatal(err)
	}
	var anchors, recommendations int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM milestone_anchors), (SELECT COUNT(*) FROM milestone_recommendations)`).Scan(&anchors, &recommendations); err != nil {
		t.Fatal(err)
	}
	if anchors != 0 || recommendations != 0 {
		t.Fatalf("milestone delete did not cascade: anchors=%d recommendations=%d", anchors, recommendations)
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
	if _, err := s.db.ExecContext(ctx, `PRAGMA user_version=3`); err != nil {
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

func TestGraphIncludesTitlesAndDependencies(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	id1, err := s.CreateTask(ctx, "one", "objective", []string{"ac"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := s.CreateTask(ctx, "two", "objective", []string{"ac"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependencies(ctx, id2, []string{id1}, "required", nil); err != nil {
		t.Fatal(err)
	}
	g, err := s.Graph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if g.Tasks[id1].Title != "one" || g.Tasks[id2].Title != "two" {
		t.Fatalf("graph titles = %#v", g.Tasks)
	}
	if len(g.Deps[id2]) != 1 || g.Deps[id2][0] != id1 {
		t.Fatalf("graph deps = %#v", g.Deps)
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

func TestStartTaskIncrementsAttemptAndCreatesRunningAttempt(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	id := createTaskForRuntime(t, ctx, s, "start")
	started, err := s.StartTasks(ctx, []string{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0] != id {
		t.Fatalf("started = %#v", started)
	}
	task, err := s.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "running" || task.AttemptCount != 1 {
		t.Fatalf("task status/count = %s/%d", task.Status, task.AttemptCount)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE task_id=? AND attempt_number=1 AND status='running' AND completed_at IS NULL`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("running attempts = %d, want 1", n)
	}
}

func TestPassFailStopRequireOpenAttemptAndCloseItAtomically(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	passID := createTaskForRuntime(t, ctx, s, "pass")
	if _, err := s.StartTasks(ctx, []string{passID}); err != nil {
		t.Fatal(err)
	}
	if err := s.PassTask(ctx, passID, "done"); err != nil {
		t.Fatal(err)
	}
	var closed, withSummary int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), SUM(CASE WHEN summary='done' THEN 1 ELSE 0 END) FROM attempts WHERE task_id=? AND status='passed' AND completed_at IS NOT NULL`, passID).Scan(&closed, &withSummary); err != nil {
		t.Fatal(err)
	}
	if taskStatus(t, ctx, s, passID) != "passed" || closed != 1 || withSummary != 1 {
		t.Fatalf("pass did not close attempt")
	}
	failID := createTaskForRuntime(t, ctx, s, "fail")
	if _, err := s.StartTasks(ctx, []string{failID}); err != nil {
		t.Fatal(err)
	}
	if err := s.FailTask(ctx, failID, ""); err == nil {
		t.Fatal("fail without reason succeeded")
	}
	if err := s.FailTask(ctx, failID, "bad"); err != nil {
		t.Fatal(err)
	}
	var failedWithReason int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE task_id=? AND status='failed' AND reason='bad'`, failID).Scan(&failedWithReason); err != nil {
		t.Fatal(err)
	}
	if taskStatus(t, ctx, s, failID) != "failed" || failedWithReason != 1 {
		t.Fatalf("fail status/reason = %s/%d", taskStatus(t, ctx, s, failID), failedWithReason)
	}
	stopID := createTaskForRuntime(t, ctx, s, "stop")
	if _, err := s.StartTasks(ctx, []string{stopID}); err != nil {
		t.Fatal(err)
	}
	if err := s.StopTasks(ctx, []string{stopID}, "pause"); err != nil {
		t.Fatal(err)
	}
	var stoppedEvents int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_events WHERE task_id=? AND event_type='stopped' AND reason='pause'`, stopID).Scan(&stoppedEvents); err != nil {
		t.Fatal(err)
	}
	if taskStatus(t, ctx, s, stopID) != "pending" || stoppedEvents != 1 {
		t.Fatalf("stop status/events = %s/%d", taskStatus(t, ctx, s, stopID), stoppedEvents)
	}
	if err := s.PassTask(ctx, stopID, "no open"); err == nil {
		t.Fatal("pass without open attempt succeeded")
	}
}

func TestMissingRuntimeTaskIDsReturnValidationErrors(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	cases := map[string]func() error{
		"pass":   func() error { return s.PassTask(ctx, "TASK-999", "done") },
		"fail":   func() error { return s.FailTask(ctx, "TASK-999", "bad") },
		"retry":  func() error { return s.RetryTask(ctx, "TASK-999") },
		"reopen": func() error { return s.ReopenTask(ctx, "TASK-999", "redo") },
	}
	for name, fn := range cases {
		err := fn()
		var ve ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "task TASK-999 does not exist") {
			t.Fatalf("%s err = %T %v, want missing task ValidationError", name, err, err)
		}
	}
}

func TestStopRequiresReason(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	id := createTaskForRuntime(t, ctx, s, "stop reason")
	if _, err := s.StartTasks(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	if err := s.StopTasks(ctx, []string{id}, ""); err == nil {
		t.Fatal("stop without reason succeeded")
	}
	if taskStatus(t, ctx, s, id) != "running" {
		t.Fatal("stop without reason mutated task")
	}
}

func TestRetryResetsFailedToPending(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	id := createTaskForRuntime(t, ctx, s, "retry")
	if err := s.RetryTask(ctx, id); err == nil {
		t.Fatal("retry pending succeeded")
	}
	if _, err := s.StartTasks(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	if err := s.FailTask(ctx, id, "bad"); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryTask(ctx, id); err != nil {
		t.Fatal(err)
	}
	if taskStatus(t, ctx, s, id) != "pending" {
		t.Fatalf("retry status = %s", taskStatus(t, ctx, s, id))
	}
}

func TestReopenRejectsRunningOrPassedDownstream(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	dep := createTaskForRuntime(t, ctx, s, "dep")
	down := createTaskForRuntime(t, ctx, s, "down")
	if err := s.AddDependencies(ctx, down, []string{dep}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartTasks(ctx, []string{dep}); err != nil {
		t.Fatal(err)
	}
	if err := s.PassTask(ctx, dep, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartTasks(ctx, []string{down}); err != nil {
		t.Fatal(err)
	}
	err := s.ReopenTask(ctx, dep, "redo")
	if err == nil || !strings.Contains(err.Error(), down) {
		t.Fatalf("reopen err = %v, want affected downstream", err)
	}
	if taskStatus(t, ctx, s, dep) != "passed" {
		t.Fatal("blocked reopen mutated dependency")
	}
}

func TestReopenAllowsPendingDownstream(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	dep := createTaskForRuntime(t, ctx, s, "dep")
	down := createTaskForRuntime(t, ctx, s, "down")
	if err := s.AddDependencies(ctx, down, []string{dep}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartTasks(ctx, []string{dep}); err != nil {
		t.Fatal(err)
	}
	if err := s.PassTask(ctx, dep, "done"); err != nil {
		t.Fatal(err)
	}
	if err := s.ReopenTask(ctx, dep, "redo"); err != nil {
		t.Fatal(err)
	}
	var reopenedEvents int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_events WHERE task_id=? AND event_type='reopened' AND reason='redo'`, dep).Scan(&reopenedEvents); err != nil {
		t.Fatal(err)
	}
	if reopenedEvents != 1 {
		t.Fatalf("reopened events = %d, want 1", reopenedEvents)
	}
	if taskStatus(t, ctx, s, dep) != "pending" || taskStatus(t, ctx, s, down) != "pending" {
		t.Fatalf("unexpected statuses")
	}
}

func TestMultiStartAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	ready := createTaskForRuntime(t, ctx, s, "ready")
	dep := createTaskForRuntime(t, ctx, s, "dep")
	blocked := createTaskForRuntime(t, ctx, s, "blocked")
	if err := s.AddDependencies(ctx, blocked, []string{dep}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartTasks(ctx, []string{ready, blocked}); err == nil {
		t.Fatal("mixed start succeeded")
	}
	if taskStatus(t, ctx, s, ready) != "pending" || taskStatus(t, ctx, s, blocked) != "pending" {
		t.Fatal("multi-start was not atomic")
	}
	started, err := s.StartTasks(ctx, []string{ready, ready})
	if err != nil || len(started) != 1 {
		t.Fatalf("dedup start = %#v, %v", started, err)
	}
}

func TestMultiStopAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	a := createTaskForRuntime(t, ctx, s, "a")
	b := createTaskForRuntime(t, ctx, s, "b")
	if _, err := s.StartTasks(ctx, []string{a}); err != nil {
		t.Fatal(err)
	}
	if err := s.StopTasks(ctx, []string{a, b}, "pause"); err == nil {
		t.Fatal("mixed stop succeeded")
	}
	if taskStatus(t, ctx, s, a) != "running" || taskStatus(t, ctx, s, b) != "pending" {
		t.Fatal("multi-stop was not atomic")
	}
}

func TestConcurrentStartsDoNotCreateDuplicateRunningAttempt(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	defer s.Close()
	id := createTaskForRuntime(t, ctx, s, "race")
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ss, err := Open(DBPath(root))
			if err != nil {
				errs <- err
				return
			}
			defer ss.Close()
			_, err = ss.StartTasks(ctx, []string{id})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful starts = %d, want 1", successes)
	}
	var running int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE task_id=? AND status='running'`, id).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if running != 1 {
		t.Fatalf("running attempts = %d, want 1", running)
	}
}

func TestOneOpenAttemptInvariantOnPassFailStopCorruption(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	missing := createTaskForRuntime(t, ctx, s, "missing open")
	if _, err := s.db.ExecContext(ctx, `UPDATE tasks SET status='running' WHERE id=?`, missing); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"pass": func() error { return s.PassTask(ctx, missing, "done") },
		"fail": func() error { return s.FailTask(ctx, missing, "bad") },
		"stop": func() error { return s.StopTasks(ctx, []string{missing}, "pause") },
	} {
		if err := fn(); err == nil {
			t.Fatalf("%s with no open attempt succeeded", name)
		}
	}

	id := createTaskForRuntime(t, ctx, s, "corrupt")
	if _, err := s.StartTasks(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP INDEX one_running_attempt_per_task`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO attempts(task_id,attempt_number,status,started_at) VALUES(?,2,'running','now')`, id); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"pass": func() error { return s.PassTask(ctx, id, "done") },
		"fail": func() error { return s.FailTask(ctx, id, "bad") },
		"stop": func() error { return s.StopTasks(ctx, []string{id}, "pause") },
	} {
		if err := fn(); err == nil {
			t.Fatalf("%s with two open attempts succeeded", name)
		}
	}
}
