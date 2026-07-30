package store

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestMilestoneRecommendationMutationsUpdateParentTimestamp(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	task := createTaskForRuntime(t, ctx, s, "task")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{task}, nil)
	if err != nil {
		t.Fatal(err)
	}
	recommendationID, err := s.AddMilestoneRecommendation(ctx, id, "original")
	if err != nil {
		t.Fatal(err)
	}
	const oldTimestamp = "2000-01-01T00:00:00Z"
	if _, err := s.db.ExecContext(ctx, `UPDATE milestones SET updated_at=? WHERE id=?`, oldTimestamp, id); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateMilestoneRecommendation(ctx, id, recommendationID, "updated"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Milestone.UpdatedAt == oldTimestamp || snapshot.Milestone.UpdatedAt != snapshot.Milestone.Recommendations[0].UpdatedAt {
		t.Fatalf("update timestamps = milestone %q recommendation %q", snapshot.Milestone.UpdatedAt, snapshot.Milestone.Recommendations[0].UpdatedAt)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE milestones SET updated_at=? WHERE id=?`, oldTimestamp, id); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveMilestoneRecommendation(ctx, id, recommendationID); err != nil {
		t.Fatal(err)
	}
	snapshot, err = s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Milestone.UpdatedAt == oldTimestamp || len(snapshot.Milestone.Recommendations) != 0 {
		t.Fatalf("remove left timestamp %q and recommendations %#v", snapshot.Milestone.UpdatedAt, snapshot.Milestone.Recommendations)
	}
}

func TestMilestoneRecommendationsAreMonotonicAndImmutableAfterMark(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	task := createTaskForRuntime(t, ctx, s, "task")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{task}, nil)
	if err != nil {
		t.Fatal(err)
	}
	one, err := s.AddMilestoneRecommendation(ctx, id, "one")
	if err != nil || one != "REC-001" {
		t.Fatalf("first recommendation = %q, %v", one, err)
	}
	two, err := s.AddMilestoneRecommendation(ctx, id, "two")
	if err != nil || two != "REC-002" {
		t.Fatalf("second recommendation = %q, %v", two, err)
	}
	if err := s.RemoveMilestoneRecommendation(ctx, id, one); err != nil {
		t.Fatal(err)
	}
	three, err := s.AddMilestoneRecommendation(ctx, id, "three")
	if err != nil || three != "REC-003" {
		t.Fatalf("replacement recommendation = %q, %v", three, err)
	}
	snap, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Milestone.Recommendations; len(got) != 2 || got[0].ID != two || got[0].Position != 1 || got[1].ID != three || got[1].Position != 2 {
		t.Fatalf("recommendations = %#v", got)
	}
	passTask(t, ctx, s, task)
	if err := s.MarkMilestone(ctx, id, "reviewed", "ref"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMilestoneRecommendation(ctx, id, "nope"); err == nil {
		t.Fatal("marked milestone accepted recommendation")
	}
	if err := s.UpdateMilestoneRecommendation(ctx, id, two, "nope"); err == nil {
		t.Fatal("marked milestone updated recommendation")
	}
	if err := s.RemoveMilestoneRecommendation(ctx, id, two); err == nil {
		t.Fatal("marked milestone removed recommendation")
	}
}

func TestMarkSnapshotsAreAtomicAndIndependentOfLiveTasks(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	first := createTaskForRuntime(t, ctx, s, "first")
	second := createTaskForRuntime(t, ctx, s, "second")
	if err := s.AddDependencies(ctx, second, []string{first}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{second}, []string{"recommend"})
	if err != nil {
		t.Fatal(err)
	}
	passTask(t, ctx, s, first)
	passTask(t, ctx, s, second)
	if err := s.MarkMilestone(ctx, id, "complete", "opaque"); err != nil {
		t.Fatal(err)
	}
	marked, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if marked.Milestone.Status != "marked" || len(marked.Scope) != 2 || len(marked.Anchors) != 1 || marked.Anchors[0].ID != second {
		t.Fatalf("marked snapshot = %#v", marked)
	}
	if marked.Scope[0].AttemptCount != 1 || marked.Scope[1].LastCompletionSummary == nil || *marked.Scope[1].LastCompletionSummary != "done" {
		t.Fatalf("snapshot attempt data = %#v", marked.Scope)
	}
	var anchors int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM milestone_anchors WHERE milestone_id=?`, id).Scan(&anchors); err != nil || anchors != 0 {
		t.Fatalf("live anchors = %d, %v", anchors, err)
	}
	if err := s.ReopenTask(ctx, second, "change"); err != nil {
		t.Fatal(err)
	}
	title := "changed"
	priority := 0
	if err := s.UpdateTask(ctx, second, &title, nil, &priority, "change"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, second); err != nil {
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
	after, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := milestoneTaskIDs(after.Scope); strings.Join(got, ",") != strings.Join(milestoneTaskIDs(marked.Scope), ",") || after.Scope[1].Title != marked.Scope[1].Title || after.Scope[1].Priority != marked.Scope[1].Priority {
		t.Fatalf("snapshot changed after live task mutation: before=%#v after=%#v", marked.Scope, after.Scope)
	}
}

func TestMarkRollbackAndConcurrentReopenSerialize(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	task := createTaskForRuntime(t, ctx, s, "task")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{task}, nil)
	if err != nil {
		t.Fatal(err)
	}
	passTask(t, ctx, s, task)
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_snapshot BEFORE INSERT ON milestone_snapshots BEGIN SELECT RAISE(ABORT, 'snapshot rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMilestone(ctx, id, "complete", ""); err == nil {
		t.Fatal("mark succeeded despite snapshot failure")
	}
	var snapshots, anchors int
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM milestone_snapshots WHERE milestone_id=?), (SELECT COUNT(*) FROM milestone_anchors WHERE milestone_id=?)`, id, id).Scan(&snapshots, &anchors); err != nil || snapshots != 0 || anchors != 1 {
		t.Fatalf("failed mark left snapshots=%d anchors=%d err=%v", snapshots, anchors, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER reject_snapshot`); err != nil {
		t.Fatal(err)
	}

}

func TestMarkedMilestoneRejectsEveryMutation(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	task := createTaskForRuntime(t, ctx, s, "task")
	extra := createTaskForRuntime(t, ctx, s, "extra")
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{task}, []string{"recommend"})
	if err != nil {
		t.Fatal(err)
	}
	passTask(t, ctx, s, task)
	if err := s.MarkMilestone(ctx, id, "complete", "reference"); err != nil {
		t.Fatal(err)
	}
	title, reason := "new title", "new reason"
	for name, mutate := range map[string]func() error{
		"update definition":     func() error { return s.UpdateMilestone(ctx, id, &title, &reason) },
		"add anchors":           func() error { return s.AddMilestoneAnchors(ctx, id, []string{extra}) },
		"remove anchors":        func() error { return s.RemoveMilestoneAnchors(ctx, id, []string{task}) },
		"add recommendation":    func() error { _, err := s.AddMilestoneRecommendation(ctx, id, "new"); return err },
		"update recommendation": func() error { return s.UpdateMilestoneRecommendation(ctx, id, "REC-001", "new") },
		"remove recommendation": func() error { return s.RemoveMilestoneRecommendation(ctx, id, "REC-001") },
		"delete":                func() error { return s.DeleteMilestone(ctx, id) },
		"mark again":            func() error { return s.MarkMilestone(ctx, id, "again", "") },
	} {
		if err := mutate(); err == nil {
			t.Errorf("marked milestone allowed %s", name)
		}
	}
}

func TestMarkedSnapshotIsFullyStableAndDoesNotLoadLiveAnchors(t *testing.T) {
	ctx := context.Background()
	s, root := newProject(t)
	rootTask := createTaskForRuntime(t, ctx, s, "root")
	anchor := createTaskForRuntime(t, ctx, s, "anchor")
	if err := s.AddDependencies(ctx, anchor, []string{rootTask}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{anchor}, []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	passTask(t, ctx, s, rootTask)
	passTask(t, ctx, s, anchor)
	if err := s.MarkMilestone(ctx, id, "complete", "opaque"); err != nil {
		t.Fatal(err)
	}
	before, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Milestone.Snapshots) != 2 || before.Milestone.Snapshots[0].ScopePosition != 0 || before.Milestone.Snapshots[1].ScopePosition != 1 || !before.Milestone.Snapshots[1].IsAnchor || before.Milestone.Snapshots[0].IsAnchor || before.Milestone.Snapshots[0].CapturedAt == "" || before.Milestone.MarkSummary == nil || before.Milestone.MarkedAt == nil {
		t.Fatalf("incomplete marked snapshot: %#v", before.Milestone)
	}
	// This corrupt live row must be ignored by marked reads; snapshots are the
	// only source for scope and anchor history.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO milestone_anchors(milestone_id,task_id,created_at) VALUES(?,?,?)`, id, "TASK-999", "corrupt"); err != nil {
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
	if err := s.ReopenTask(ctx, anchor, "change"); err != nil {
		t.Fatal(err)
	}
	title, priority := "changed", 0
	if err := s.UpdateTask(ctx, anchor, &title, nil, &priority, "change"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTask(ctx, anchor); err != nil {
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
	after, err := s.MilestoneReadSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("marked snapshot changed:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func TestMarkRollbackAfterLaterSnapshotFailure(t *testing.T) {
	ctx := context.Background()
	s, _ := newProject(t)
	defer s.Close()
	first := createTaskForRuntime(t, ctx, s, "first")
	second := createTaskForRuntime(t, ctx, s, "second")
	if err := s.AddDependencies(ctx, second, []string{first}, "needs", nil); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateMilestone(ctx, "title", "reason", []string{second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	passTask(t, ctx, s, first)
	passTask(t, ctx, s, second)
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER reject_second_snapshot BEFORE INSERT ON milestone_snapshots WHEN NEW.task_id = '`+second+`' BEGIN SELECT RAISE(ABORT, 'later snapshot rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkMilestone(ctx, id, "complete", ""); err == nil {
		t.Fatal("mark succeeded despite later snapshot failure")
	}
	var snapshots, anchors int
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM milestone_snapshots WHERE milestone_id=?), (SELECT COUNT(*) FROM milestone_anchors WHERE milestone_id=?), (SELECT status FROM milestones WHERE id=?)`, id, id, id).Scan(&snapshots, &anchors, &status); err != nil {
		t.Fatal(err)
	}
	if snapshots != 0 || anchors != 1 || status != "planned" {
		t.Fatalf("later failure left snapshots=%d anchors=%d status=%s", snapshots, anchors, status)
	}
}

func TestMarkSerializesWithReopenAndAnchorMutation(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*Store, string, string) {
		s, _ := newProject(t)
		task := createTaskForRuntime(t, ctx, s, "task")
		extra := createTaskForRuntime(t, ctx, s, "extra")
		id, err := s.CreateMilestone(ctx, "title", "reason", []string{task}, nil)
		if err != nil {
			t.Fatal(err)
		}
		passTask(t, ctx, s, task)
		return s, id, extra
	}
	blockFirstWriter := func(s *Store) (chan struct{}, chan struct{}) {
		locked, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		s.testAfterWriteLock = func() { once.Do(func() { close(locked); <-release }) }
		return locked, release
	}
	t.Run("reopen first makes mark fail", func(t *testing.T) {
		s, id, _ := setup(t)
		defer s.Close()
		locked, release := blockFirstWriter(s)
		reopenDone := make(chan error, 1)
		go func() { reopenDone <- s.ReopenTask(ctx, "TASK-001", "change") }()
		<-locked
		markDone := make(chan error, 1)
		go func() { markDone <- s.MarkMilestone(ctx, id, "complete", "") }()
		close(release)
		if err := <-reopenDone; err != nil {
			t.Fatal(err)
		}
		if err := <-markDone; err == nil {
			t.Fatal("mark used stale pre-reopen state")
		}
	})
	t.Run("mark first snapshots before reopen", func(t *testing.T) {
		s, id, _ := setup(t)
		defer s.Close()
		locked, release := blockFirstWriter(s)
		markDone := make(chan error, 1)
		go func() { markDone <- s.MarkMilestone(ctx, id, "complete", "") }()
		<-locked
		reopenDone := make(chan error, 1)
		go func() { reopenDone <- s.ReopenTask(ctx, "TASK-001", "change") }()
		close(release)
		if err := <-markDone; err != nil {
			t.Fatal(err)
		}
		if err := <-reopenDone; err != nil {
			t.Fatal(err)
		}
		snap, err := s.MilestoneReadSnapshot(ctx, id)
		if err != nil || len(snap.Scope) != 1 || snap.Scope[0].Status != "passed" {
			t.Fatalf("mark did not preserve pre-reopen snapshot: %#v, %v", snap, err)
		}
	})
	t.Run("anchor mutation first makes mark fail", func(t *testing.T) {
		s, id, extra := setup(t)
		defer s.Close()
		locked, release := blockFirstWriter(s)
		anchorDone := make(chan error, 1)
		go func() { anchorDone <- s.AddMilestoneAnchors(ctx, id, []string{extra}) }()
		<-locked
		markDone := make(chan error, 1)
		go func() { markDone <- s.MarkMilestone(ctx, id, "complete", "") }()
		close(release)
		if err := <-anchorDone; err != nil {
			t.Fatal(err)
		}
		if err := <-markDone; err == nil {
			t.Fatal("mark used stale pre-anchor state")
		}
	})
	t.Run("mark first makes anchor mutation fail", func(t *testing.T) {
		s, id, extra := setup(t)
		defer s.Close()
		locked, release := blockFirstWriter(s)
		markDone := make(chan error, 1)
		go func() { markDone <- s.MarkMilestone(ctx, id, "complete", "") }()
		<-locked
		anchorDone := make(chan error, 1)
		go func() { anchorDone <- s.AddMilestoneAnchors(ctx, id, []string{extra}) }()
		close(release)
		if err := <-markDone; err != nil {
			t.Fatal(err)
		}
		if err := <-anchorDone; err == nil {
			t.Fatal("anchor mutation succeeded after mark")
		}
		snap, err := s.MilestoneReadSnapshot(ctx, id)
		if err != nil || len(snap.Scope) != 1 || snap.Scope[0].ID != "TASK-001" {
			t.Fatalf("stale or invalid snapshot: %#v, %v", snap, err)
		}
	})
}
