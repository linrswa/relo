package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"relo/internal/dag"
	"relo/internal/domain"
)

// MilestoneReadSnapshot is a consistent view of a milestone and its live
// dependency closure. Scope and Anchors contain full task records so callers
// need not assemble them from a different database snapshot.
type MilestoneReadSnapshot struct {
	Milestone   domain.Milestone
	Anchors     []domain.Task
	Scope       []domain.Task
	ReadyToMark bool
}

func milestoneIDs(ids []string) error {
	if len(ids) == 0 {
		return validation("at least one anchor task ID is required")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return validation("anchor task ID must not be empty")
		}
		if seen[id] {
			return validation("duplicate anchor task ID %s", id)
		}
		seen[id] = true
	}
	return nil
}

// CreateMilestone atomically creates a planned milestone, its anchors, and
// optional create-time recommendations. Recommendation mutation is otherwise
// intentionally not part of this task.
func (s *Store) CreateMilestone(ctx context.Context, title, reason string, anchors, recommendations []string) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", validation("milestone title must not be empty")
	}
	if strings.TrimSpace(reason) == "" {
		return "", validation("milestone reason must not be empty")
	}
	if err := milestoneIDs(anchors); err != nil {
		return "", err
	}
	for _, text := range recommendations {
		if strings.TrimSpace(text) == "" {
			return "", validation("recommendation text must not be empty")
		}
	}

	var id string
	err := s.WithWriteTx(ctx, func(tx *Tx) error {
		if err := requireExistingTasks(ctx, tx.tx, "anchor task", anchors); err != nil {
			return err
		}
		p, err := tx.Project(ctx)
		if err != nil {
			return err
		}
		id = domain.MilestoneID(p.NextMilestoneSequence)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO milestones(id,title,reason,status,creation_order,next_recommendation_sequence,created_at,updated_at) VALUES(?,?,?,'planned',?,?,?,?)`, id, title, reason, p.NextMilestoneSequence, len(recommendations)+1, now, now); err != nil {
			return err
		}
		for _, taskID := range anchors {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO milestone_anchors(milestone_id,task_id,created_at) VALUES(?,?,?)`, id, taskID, now); err != nil {
				return err
			}
		}
		for i, text := range recommendations {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO milestone_recommendations(milestone_id,recommendation_id,text,position,created_at,updated_at) VALUES(?,?,?,?,?,?)`, id, domain.RecommendationID(i+1), text, i, now, now); err != nil {
				return err
			}
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE projects SET next_milestone_sequence=?,updated_at=? WHERE id=1`, p.NextMilestoneSequence+1, now)
		return err
	})
	return id, err
}

func (s *Store) UpdateMilestone(ctx context.Context, id string, title, reason *string) error {
	if title == nil && reason == nil {
		return validation("at least one milestone update is required")
	}
	if title != nil && strings.TrimSpace(*title) == "" {
		return validation("milestone title must not be empty")
	}
	if reason != nil && strings.TrimSpace(*reason) == "" {
		return validation("milestone reason must not be empty")
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		m, err := getMilestone(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if !domain.CanModifyMilestone(m.Status) {
			return validation("%s is %s and cannot be modified", id, m.Status)
		}
		newTitle, newReason := m.Title, m.Reason
		if title != nil {
			newTitle = *title
		}
		if reason != nil {
			newReason = *reason
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE milestones SET title=?,reason=?,updated_at=? WHERE id=?`, newTitle, newReason, time.Now().UTC().Format(time.RFC3339Nano), id)
		return err
	})
}

func (s *Store) DeleteMilestone(ctx context.Context, id string) error {
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		m, err := getMilestone(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if !domain.CanModifyMilestone(m.Status) {
			return validation("%s is %s and cannot be deleted", id, m.Status)
		}
		_, err = tx.tx.ExecContext(ctx, `DELETE FROM milestones WHERE id=?`, id)
		return err
	})
}

func (s *Store) AddMilestoneAnchors(ctx context.Context, id string, taskIDs []string) error {
	if err := milestoneIDs(taskIDs); err != nil {
		return err
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		m, err := getMilestone(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if !domain.CanModifyMilestone(m.Status) {
			return validation("%s is %s and cannot be modified", id, m.Status)
		}
		if err := requireExistingTasks(ctx, tx.tx, "anchor task", taskIDs); err != nil {
			return err
		}
		for _, taskID := range taskIDs {
			var n int
			if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM milestone_anchors WHERE milestone_id=? AND task_id=?`, id, taskID).Scan(&n); err != nil {
				return err
			}
			if n != 0 {
				return validation("task %s is already an anchor of %s", taskID, id)
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, taskID := range taskIDs {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO milestone_anchors(milestone_id,task_id,created_at) VALUES(?,?,?)`, id, taskID, now); err != nil {
				return err
			}
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE milestones SET updated_at=? WHERE id=?`, now, id)
		return err
	})
}

func (s *Store) RemoveMilestoneAnchors(ctx context.Context, id string, taskIDs []string) error {
	if err := milestoneIDs(taskIDs); err != nil {
		return err
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		m, err := getMilestone(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if !domain.CanModifyMilestone(m.Status) {
			return validation("%s is %s and cannot be modified", id, m.Status)
		}
		var total int
		if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM milestone_anchors WHERE milestone_id=?`, id).Scan(&total); err != nil {
			return err
		}
		if total-len(taskIDs) < 1 {
			return validation("milestone must keep at least one anchor")
		}
		for _, taskID := range taskIDs {
			var n int
			if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM milestone_anchors WHERE milestone_id=? AND task_id=?`, id, taskID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return validation("task %s is not an anchor of %s", taskID, id)
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, taskID := range taskIDs {
			if _, err := tx.tx.ExecContext(ctx, `DELETE FROM milestone_anchors WHERE milestone_id=? AND task_id=?`, id, taskID); err != nil {
				return err
			}
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE milestones SET updated_at=? WHERE id=?`, now, id)
		return err
	})
}

func (s *Store) MilestoneReadSnapshot(ctx context.Context, id string) (MilestoneReadSnapshot, error) {
	var out MilestoneReadSnapshot
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		m, err := getMilestone(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		out.Milestone = *m
		return populateMilestoneSnapshot(ctx, tx, &out)
	})
	return out, err
}

func (s *Store) ListMilestoneReadSnapshots(ctx context.Context, status string) ([]MilestoneReadSnapshot, error) {
	if status != "" && status != domain.MilestoneStatusPlanned && status != domain.MilestoneStatusMarked {
		return nil, validation("milestone status must be planned or marked")
	}
	out := []MilestoneReadSnapshot{}
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		q := `SELECT id FROM milestones`
		args := []any{}
		if status != "" {
			q += ` WHERE status=?`
			args = append(args, status)
		}
		q += ` ORDER BY creation_order,id`
		rows, err := tx.tx.QueryContext(ctx, q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			m, err := getMilestone(ctx, tx.tx, id)
			if err != nil {
				return err
			}
			snap := MilestoneReadSnapshot{Milestone: *m}
			if err := populateMilestoneSnapshot(ctx, tx, &snap); err != nil {
				return err
			}
			out = append(out, snap)
		}
		return rows.Err()
	})
	return out, err
}

func (s *Store) ReadyMilestones(ctx context.Context) ([]MilestoneReadSnapshot, error) {
	all, err := s.ListMilestoneReadSnapshots(ctx, domain.MilestoneStatusPlanned)
	if err != nil {
		return nil, err
	}
	out := make([]MilestoneReadSnapshot, 0, len(all))
	for _, snap := range all {
		if snap.ReadyToMark {
			out = append(out, snap)
		}
	}
	return out, nil
}

func getMilestone(ctx context.Context, q queryer, id string) (*domain.Milestone, error) {
	m := &domain.Milestone{}
	err := q.QueryRowContext(ctx, `SELECT id,title,reason,status,creation_order,next_recommendation_sequence,mark_summary,reference,created_at,updated_at,marked_at FROM milestones WHERE id=?`, id).Scan(&m.ID, &m.Title, &m.Reason, &m.Status, &m.CreationOrder, &m.NextRecommendationSequence, &m.MarkSummary, &m.Reference, &m.CreatedAt, &m.UpdatedAt, &m.MarkedAt)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT milestone_id,task_id,created_at FROM milestone_anchors WHERE milestone_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a domain.MilestoneAnchor
		if err := rows.Scan(&a.MilestoneID, &a.TaskID, &a.CreatedAt); err != nil {
			return nil, err
		}
		m.Anchors = append(m.Anchors, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT milestone_id,recommendation_id,text,position,created_at,updated_at FROM milestone_recommendations WHERE milestone_id=? ORDER BY position,recommendation_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.Recommendation
		if err := rows.Scan(&r.MilestoneID, &r.ID, &r.Text, &r.Position, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		m.Recommendations = append(m.Recommendations, r)
	}
	return m, rows.Err()
}

func populateMilestoneSnapshot(ctx context.Context, tx *Tx, out *MilestoneReadSnapshot) error {
	// Marked snapshot rendering belongs to TASK-003. Planned milestones are the
	// only mutable/live form implemented here.
	if out.Milestone.Status != domain.MilestoneStatusPlanned {
		return nil
	}
	g, err := tx.graph(ctx)
	if err != nil {
		return err
	}
	if err := validateGraphEndpoints(g); err != nil {
		return err
	}
	if cycle := g.Cycle(); len(cycle) > 0 {
		return validation("dependency graph has cycle: %s", strings.Join(cycle, " -> "))
	}
	anchorIDs := make(map[string]bool, len(out.Milestone.Anchors))
	for _, a := range out.Milestone.Anchors {
		anchorIDs[a.TaskID] = true
	}
	if len(anchorIDs) == 0 {
		return validation("planned milestone %s has no anchors", out.Milestone.ID)
	}
	ids, err := milestoneScope(g, anchorIDs)
	if err != nil {
		return err
	}
	for _, id := range ids {
		t, err := tx.GetTask(ctx, id)
		if err != nil {
			return err
		}
		out.Scope = append(out.Scope, *t)
		if anchorIDs[id] {
			out.Anchors = append(out.Anchors, *t)
		}
	}
	out.ReadyToMark = true
	for _, t := range out.Anchors {
		if t.Status != domain.StatusPassed {
			out.ReadyToMark = false
			break
		}
	}
	return nil
}

// validateGraphEndpoints rejects persisted corruption before algorithms such as
// Cycle traverse or sort graph nodes. It intentionally checks the complete
// loaded graph, rather than just an anchor's closure.
func validateGraphEndpoints(g dag.Graph) error {
	var edges []string
	for taskID, deps := range g.Deps {
		for _, dependencyID := range deps {
			edges = append(edges, taskID+"\x00"+dependencyID)
		}
	}
	sort.Strings(edges)
	for _, edge := range edges {
		parts := strings.SplitN(edge, "\x00", 2)
		_, taskOK := g.Tasks[parts[0]]
		_, dependencyOK := g.Tasks[parts[1]]
		if !taskOK || !dependencyOK {
			return validation("dependency %s -> %s references missing endpoint", parts[0], parts[1])
		}
	}
	return nil
}

func milestoneScope(g dag.Graph, anchors map[string]bool) ([]string, error) {
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if seen[id] {
			return nil
		}
		if _, ok := g.Tasks[id]; !ok {
			return validation("milestone scope references missing task %s", id)
		}
		seen[id] = true
		for _, dep := range g.Deps[id] {
			if _, ok := g.Tasks[dep]; !ok {
				return validation("milestone scope references missing task %s", dep)
			}
			if err := visit(dep); err != nil {
				return err
			}
		}
		return nil
	}
	for id := range anchors {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	tasks := make([]dag.Task, 0, len(seen))
	for id := range seen {
		tasks = append(tasks, g.Tasks[id])
	}
	dag.Sort(tasks)
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out, nil
}
