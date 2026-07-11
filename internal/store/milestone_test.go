package store

import (
	"context"
	"strings"
	"testing"

	"relo/internal/dag"
	"relo/internal/domain"
)

func passTask(t *testing.T, ctx context.Context, s *Store, id string) {
	t.Helper()
	if _, err := s.StartTasks(ctx, []string{id}); err != nil {
		t.Fatal(err)
	}
	if err := s.PassTask(ctx, id, "done"); err != nil {
		t.Fatal(err)
	}
}

func milestoneTaskIDs(tasks []domain.Task) []string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}
	return ids
}

func TestMilestoneScopeReadyAndDeterministicOrdering(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()

	// A <- B and A <- C; D depends on both B and C. D and B are overlapping
	// anchors, making this a true diamond with a redundant anchor. A has the
	// lower priority, proving scope uses the task comparator rather than walk order.
	a, _ := s.CreateTask(ctx, "a", "objective", []string{"ac"}, 30)
	b, _ := s.CreateTask(ctx, "b", "objective", []string{"ac"}, 20)
	c, _ := s.CreateTask(ctx, "c", "objective", []string{"ac"}, 10)
	d, _ := s.CreateTask(ctx, "d", "objective", []string{"ac"}, 40)
	unrelated, _ := s.CreateTask(ctx, "unrelated", "objective", []string{"ac"}, 50)
	if err := s.AddDependencies(ctx, b, []string{a}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependencies(ctx, c, []string{a}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddDependencies(ctx, d, []string{b, c}, "needs", nil); err != nil {
		t.Fatal(err)
	}

	before, err := s.ReadyTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateMilestone(ctx, "integration", "review", []string{d, b}, []string{"run tests"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.ReadyTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(milestoneTaskIDs(after), ","), strings.Join(milestoneTaskIDs(before), ","); got != want {
		t.Fatalf("milestone changed task ready frontier: before=%s after=%s", want, got)
	}
	snap, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ReadyToMark {
		t.Fatal("pending anchors made milestone ready")
	}
	if got, want := strings.Join(milestoneTaskIDs(snap.Scope), ","), strings.Join([]string{c, b, a, d}, ","); got != want {
		t.Fatalf("scope = %s, want %s", got, want)
	}
	if got, want := strings.Join(milestoneTaskIDs(snap.Anchors), ","), strings.Join([]string{b, d}, ","); got != want {
		t.Fatalf("anchors = %s, want %s", got, want)
	}
	if len(snap.Milestone.Recommendations) != 1 || snap.Milestone.Recommendations[0].ID != "REC-001" {
		t.Fatalf("create-time recommendations = %#v", snap.Milestone.Recommendations)
	}

	passTask(t, ctx, s, a)
	passTask(t, ctx, s, b)
	passTask(t, ctx, s, c)
	passTask(t, ctx, s, d)
	ready, err := s.ReadyMilestones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].Milestone.ID != id || !ready[0].ReadyToMark {
		t.Fatalf("ready milestones = %#v", ready)
	}
	// A milestone does not enter the task ready frontier.
	tasks, err := s.ReadyTasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := milestoneTaskIDs(tasks); len(got) != 1 || got[0] != unrelated {
		t.Fatalf("ready tasks = %v, want unrelated pending task %s", got, unrelated)
	}
}

func TestMilestoneAnchorMutationsAreAtomicAndKeepAnchor(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	a := createTaskForRuntime(t, ctx, s, "a")
	b := createTaskForRuntime(t, ctx, s, "b")
	c := createTaskForRuntime(t, ctx, s, "c")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{a}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AddMilestoneAnchors(ctx, id, []string{b, "TASK-999"}); err == nil {
		t.Fatal("add with missing task succeeded")
	}
	snap, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := milestoneTaskIDs(snap.Anchors); len(got) != 1 || got[0] != a {
		t.Fatalf("anchors changed after rollback: %v", got)
	}
	newTitle := "updated"
	if err := s.UpdateMilestone(ctx, id, &newTitle, nil); err != nil {
		t.Fatal(err)
	}
	if snap, err := s.MilestoneReadSnapshot(ctx, id); err != nil || snap.Milestone.Title != newTitle {
		t.Fatalf("updated milestone = %#v, err=%v", snap.Milestone, err)
	}
	if err := s.AddMilestoneAnchors(ctx, id, []string{b, c}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMilestoneAnchors(ctx, id, []string{a, "TASK-999"}); err == nil {
		t.Fatal("remove with missing anchor succeeded")
	}
	snap, err = s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(milestoneTaskIDs(snap.Anchors), ","); got != strings.Join([]string{a, b, c}, ",") {
		t.Fatalf("anchors changed after remove rollback: %s", got)
	}
	if err := s.RemoveMilestoneAnchors(ctx, id, []string{a, b, c}); err == nil || !strings.Contains(err.Error(), "keep at least one") {
		t.Fatalf("remove all error = %v", err)
	}
}

func TestMilestoneCreateAndAnchorBatchesRollBack(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	a := createTaskForRuntime(t, ctx, s, "a")
	b := createTaskForRuntime(t, ctx, s, "b")

	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_milestone_anchor BEFORE INSERT ON milestone_anchors BEGIN SELECT RAISE(ABORT, 'anchor rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMilestone(ctx, "title", "reason", []string{a}, nil); err == nil {
		t.Fatal("create succeeded despite anchor failure")
	}
	var milestones int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM milestones`).Scan(&milestones); err != nil {
		t.Fatal(err)
	}
	if milestones != 0 {
		t.Fatalf("milestones after failed create = %d, want 0", milestones)
	}
	p, err := s.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.NextMilestoneSequence != 1 {
		t.Fatalf("next milestone sequence after failed create = %d, want 1", p.NextMilestoneSequence)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER reject_milestone_anchor`); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMilestoneAnchors(ctx, id, []string{b, a}); err == nil {
		t.Fatal("batch with existing anchor succeeded")
	}
	snap, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := milestoneTaskIDs(snap.Anchors); len(got) != 1 || got[0] != a {
		t.Fatalf("anchors after existing-anchor batch rollback = %v", got)
	}
	if err := s.AddMilestoneAnchors(ctx, id, []string{b, b}); err == nil {
		t.Fatal("batch with duplicate anchor succeeded")
	}
	snap, err = s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := milestoneTaskIDs(snap.Anchors); len(got) != 1 || got[0] != a {
		t.Fatalf("anchors after duplicate-anchor batch rollback = %v", got)
	}
}

func TestDeleteMilestoneCascadesLiveRowsAndRestartPersists(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	a := createTaskForRuntime(t, ctx, s, "a")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{a}, []string{"recommend"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(DBPath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Anchors) != 1 || snap.Anchors[0].ID != a || len(snap.Milestone.Recommendations) != 1 {
		t.Fatalf("restarted milestone = %#v", snap)
	}
	if err := s.DeleteMilestone(ctx, id); err != nil {
		t.Fatal(err)
	}
	var milestones, anchors, recommendations int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM milestones), (SELECT COUNT(*) FROM milestone_anchors), (SELECT COUNT(*) FROM milestone_recommendations)`).Scan(&milestones, &anchors, &recommendations); err != nil {
		t.Fatal(err)
	}
	if milestones != 0 || anchors != 0 || recommendations != 0 {
		t.Fatalf("delete did not cascade: milestones=%d anchors=%d recommendations=%d", milestones, anchors, recommendations)
	}
}

func TestMilestoneReadsRejectPersistedGraphCorruptionOutsideScope(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	anchor := createTaskForRuntime(t, ctx, s, "anchor")
	unrelated := createTaskForRuntime(t, ctx, s, "unrelated")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{anchor}, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES(?,?,?,datetime('now'))`, unrelated, "TASK-999", "corrupt"); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneReadSnapshot(ctx, id); err == nil || !strings.Contains(err.Error(), unrelated+" -> TASK-999") {
		t.Fatalf("missing dependency endpoint read error = %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM dependencies WHERE task_id=? AND dependency_id='TASK-999'`, unrelated); err != nil {
		t.Fatal(err)
	}
	conn, err = s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES('TASK-998',?,?,datetime('now'))`, unrelated, "corrupt"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MilestoneReadSnapshot(ctx, id); err == nil || !strings.Contains(err.Error(), "TASK-998 -> "+unrelated) {
		t.Fatalf("missing task endpoint read error = %v", err)
	}
}

func TestMilestoneReadsRejectPersistedCycleOutsideScope(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	anchor := createTaskForRuntime(t, ctx, s, "anchor")
	one := createTaskForRuntime(t, ctx, s, "one")
	two := createTaskForRuntime(t, ctx, s, "two")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{anchor}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range [][2]string{{one, two}, {two, one}} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES(?,?,?,datetime('now'))`, edge[0], edge[1], "corrupt"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.MilestoneReadSnapshot(ctx, id); err == nil || !strings.Contains(err.Error(), "dependency graph has cycle") {
		t.Fatalf("cycle read error = %v", err)
	}
}

func TestMilestoneScopeRejectsMissingReferencesAndCycles(t *testing.T) {
	missing := dag.Graph{Tasks: map[string]dag.Task{"TASK-001": {ID: "TASK-001"}}, Deps: map[string][]string{"TASK-001": {"TASK-999"}}}
	if _, err := milestoneScope(missing, map[string]bool{"TASK-001": true}); err == nil || !strings.Contains(err.Error(), "TASK-999") {
		t.Fatalf("missing scope error = %v", err)
	}
	cycle := dag.Graph{Tasks: map[string]dag.Task{"TASK-001": {ID: "TASK-001"}, "TASK-002": {ID: "TASK-002"}}, Deps: map[string][]string{"TASK-001": {"TASK-002"}, "TASK-002": {"TASK-001"}}}
	if got := cycle.Cycle(); len(got) == 0 {
		t.Fatal("cycle graph was not detected")
	}
}

func TestDeleteTaskReportsPlannedMilestoneAnchors(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	task := createTaskForRuntime(t, ctx, s, "task")
	m1, err := s.CreateMilestone(ctx, "one", "reason", []string{task}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := s.CreateMilestone(ctx, "two", "reason", []string{task}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, task); err == nil || !strings.Contains(err.Error(), m1+", "+m2) {
		t.Fatalf("delete anchored task error = %v", err)
	}
	if err := s.DeleteMilestone(ctx, m1); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMilestone(ctx, m2); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, task); err != nil {
		t.Fatal(err)
	}
}
