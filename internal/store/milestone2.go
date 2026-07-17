package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linrswa/relo/internal/dag"
	"github.com/linrswa/relo/internal/domain"
)

// TooManyAcceptanceCriteriaWarningLimit is the private validation threshold for
// the currently unspecified "too many acceptance criteria" warning in plan §9.
const TooManyAcceptanceCriteriaWarningLimit = 10

type DependencyEvent struct {
	TaskID       string
	DependencyID string
	Action       string
	Reason       string
}

type ValidationReport struct {
	Errors   []string
	Warnings []string
}

// taskDefinitionMutationError keeps every definition-edit path actionable.
func taskDefinitionMutationError(id, status string) error {
	hint := "stop it first"
	if status == domain.StatusPassed {
		hint = "reopen it first"
	}
	return validation("%s is %s and cannot be modified; %s", id, status, hint)
}

func taskDeleteMutationError(id, status string) error {
	hint := "stop it first"
	if status == domain.StatusPassed {
		hint = "reopen it first"
	}
	return validation("%s is %s and cannot be deleted; %s", id, status, hint)
}

func (s *Store) AddAcceptance(ctx context.Context, taskID, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", validation("acceptance criterion text must not be empty")
	}
	var id string
	err := s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, taskID)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(taskID, t.Status)
		}
		var max int
		if err := tx.tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),-1) FROM acceptance_criteria WHERE task_id=?`, taskID).Scan(&max); err != nil {
			return err
		}
		id = domain.ACID(max + 2)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO acceptance_criteria(task_id,criterion_id,text,position) VALUES(?,?,?,?)`, taskID, id, text, max+1); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, now, taskID)
		return err
	})
	return id, err
}

func (s *Store) UpdateAcceptance(ctx context.Context, taskID, acID, text string) error {
	if strings.TrimSpace(text) == "" {
		return validation("acceptance criterion text must not be empty")
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, taskID)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(taskID, t.Status)
		}
		res, err := tx.tx.ExecContext(ctx, `UPDATE acceptance_criteria SET text=? WHERE task_id=? AND criterion_id=?`, text, taskID, acID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), taskID)
		return err
	})
}

func (s *Store) RemoveAcceptance(ctx context.Context, taskID, acID string) error {
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, taskID)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(taskID, t.Status)
		}
		if len(t.AcceptanceCriteria) <= 1 {
			return validation("task must keep at least one acceptance criterion")
		}
		res, err := tx.tx.ExecContext(ctx, `DELETE FROM acceptance_criteria WHERE task_id=? AND criterion_id=?`, taskID, acID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), taskID)
		return err
	})
}

func (s *Store) AddNote(ctx context.Context, taskID, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", validation("note text must not be empty")
	}
	var id string
	err := s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, taskID)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(taskID, t.Status)
		}
		var max int
		if err := tx.tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),-1) FROM notes WHERE task_id=?`, taskID).Scan(&max); err != nil {
			return err
		}
		id = domain.NoteID(max + 2)
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO notes(task_id,note_id,text,position) VALUES(?,?,?,?)`, taskID, id, text, max+1); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), taskID)
		return err
	})
	return id, err
}

func (s *Store) UpdateNote(ctx context.Context, taskID, noteID, text string) error {
	if strings.TrimSpace(text) == "" {
		return validation("note text must not be empty")
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, taskID)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(taskID, t.Status)
		}
		res, err := tx.tx.ExecContext(ctx, `UPDATE notes SET text=? WHERE task_id=? AND note_id=?`, text, taskID, noteID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), taskID)
		return err
	})
}

func (s *Store) RemoveNote(ctx context.Context, taskID, noteID string) error {
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, taskID)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(taskID, t.Status)
		}
		res, err := tx.tx.ExecContext(ctx, `DELETE FROM notes WHERE task_id=? AND note_id=?`, taskID, noteID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return sql.ErrNoRows
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, time.Now().UTC().Format(time.RFC3339Nano), taskID)
		return err
	})
}

func validateDepIDs(target string, deps []string) error {
	if len(deps) == 0 {
		return validation("at least one dependency ID is required")
	}
	seen := map[string]bool{target: true}
	for _, d := range deps {
		if seen[d] {
			if d == target {
				return validation("task cannot depend on itself")
			}
			return validation("duplicate dependency ID %s", d)
		}
		seen[d] = true
	}
	return nil
}

func parseReasons(deps []string, shared string, reasonFor map[string]string) (map[string]string, error) {
	if strings.TrimSpace(shared) != "" && len(reasonFor) > 0 {
		return nil, validation("--reason and --reason-for are mutually exclusive")
	}
	out := map[string]string{}
	if strings.TrimSpace(shared) != "" {
		for _, d := range deps {
			out[d] = shared
		}
		return out, nil
	}
	if len(reasonFor) == 0 {
		return nil, validation("--reason or --reason-for is required")
	}
	want := map[string]bool{}
	for _, d := range deps {
		want[d] = true
	}
	for id, r := range reasonFor {
		if !want[id] {
			return nil, validation("--reason-for specified unrelated dependency %s", id)
		}
		if strings.TrimSpace(r) == "" {
			return nil, validation("reason for %s must not be empty", id)
		}
		out[id] = r
	}
	for _, d := range deps {
		if _, ok := out[d]; !ok {
			return nil, validation("missing --reason-for %s", d)
		}
	}
	return out, nil
}

func (s *Store) AddDependencies(ctx context.Context, target string, deps []string, sharedReason string, reasonFor map[string]string) error {
	reasons, err := parseReasons(deps, sharedReason, reasonFor)
	if err != nil {
		return err
	}
	if err := validateDepIDs(target, deps); err != nil {
		return err
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, target)
		if err != nil {
			if err == sql.ErrNoRows {
				return validation("task %s does not exist", target)
			}
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(target, t.Status)
		}
		if err := requireExistingTasks(ctx, tx.tx, "dependency task", deps); err != nil {
			return err
		}
		for _, id := range deps {
			var n int
			if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies WHERE task_id=? AND dependency_id=?`, target, id).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return validation("dependency %s -> %s already exists", target, id)
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, id := range deps {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO dependencies(task_id,dependency_id,reason,created_at) VALUES(?,?,?,?)`, target, id, reasons[id], now); err != nil {
				return err
			}
		}
		g, err := tx.graph(ctx)
		if err != nil {
			return err
		}
		if c := g.Cycle(); len(c) > 0 {
			return validation("dependency graph would contain cycle: %s", strings.Join(c, " -> "))
		}
		for _, id := range deps {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO dependency_events(task_id,dependency_id,action,reason,created_at) VALUES(?,?,'added',?,?)`, target, id, reasons[id], now); err != nil {
				return err
			}
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, now, target)
		return err
	})
}

func (s *Store) RemoveDependencies(ctx context.Context, target string, deps []string, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return validation("--reason is required")
	}
	if err := validateDepIDs(target, deps); err != nil {
		return err
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, target)
		if err != nil {
			if err == sql.ErrNoRows {
				return validation("task %s does not exist", target)
			}
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(target, t.Status)
		}
		if err := requireExistingTasks(ctx, tx.tx, "dependency task", deps); err != nil {
			return err
		}
		for _, id := range deps {
			var n int
			if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies WHERE task_id=? AND dependency_id=?`, target, id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return validation("dependency %s -> %s does not exist", target, id)
			}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		for _, id := range deps {
			if _, err := tx.tx.ExecContext(ctx, `DELETE FROM dependencies WHERE task_id=? AND dependency_id=?`, target, id); err != nil {
				return err
			}
		}
		for _, id := range deps {
			if _, err := tx.tx.ExecContext(ctx, `INSERT INTO dependency_events(task_id,dependency_id,action,reason,created_at) VALUES(?,?,'removed',?,?)`, target, id, reason, now); err != nil {
				return err
			}
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, now, target)
		return err
	})
}

func (s *Store) UpdateDependencyReason(ctx context.Context, target, dep, text string) error {
	if strings.TrimSpace(text) == "" {
		return validation("dependency reason must not be empty")
	}
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, target)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(target, t.Status)
		}
		if _, err := getTask(ctx, tx.tx, dep); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		res, err := tx.tx.ExecContext(ctx, `UPDATE dependencies SET reason=? WHERE task_id=? AND dependency_id=?`, text, target, dep)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return validation("dependency %s -> %s does not exist", target, dep)
		}
		if _, err := tx.tx.ExecContext(ctx, `INSERT INTO dependency_events(task_id,dependency_id,action,reason,created_at) VALUES(?,?,'reason_updated',?,?)`, target, dep, text, now); err != nil {
			return err
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET updated_at=? WHERE id=?`, now, target)
		return err
	})
}

func requireExistingTasks(ctx context.Context, q queryer, label string, ids []string) error {
	var missing []string
	for _, id := range ids {
		var n int
		if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE id=?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if len(missing) == 1 {
		return validation("%s %s does not exist", label, missing[0])
	}
	return validation("%ss do not exist: %s", label, strings.Join(missing, ", "))
}

func (tx *Tx) graph(ctx context.Context) (dag.Graph, error)   { return loadGraph(ctx, tx.tx) }
func (s *Store) graph(ctx context.Context) (dag.Graph, error) { return s.Graph(ctx) }

type Attempt struct {
	AttemptNumber int
	Status        string
	StartedAt     string
	CompletedAt   *string
	Summary       *string
	Reason        *string
}

type TaskReadSnapshot struct {
	Project        domain.Project
	Task           domain.Task
	CurrentAttempt *Attempt
}

type ReadyReadSnapshot struct {
	Graph dag.Graph
	Tasks []domain.Task
}

// GraphReadSnapshot contains every database-backed value rendered by graph.
// It is intentionally assembled in one read transaction so task states and
// milestone readiness cannot come from different commits.
type GraphReadSnapshot struct {
	Project    domain.Project
	Graph      dag.Graph
	Milestones []MilestoneReadSnapshot
}

// StatusReadSnapshot contains every database-backed value rendered by status.
// It is intentionally assembled in one read transaction.
type StatusReadSnapshot struct {
	Graph      dag.Graph
	Milestones []MilestoneReadSnapshot
}

func (s *Store) Graph(ctx context.Context) (dag.Graph, error) {
	var g dag.Graph
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		var err error
		g, err = tx.graph(ctx)
		return err
	})
	return g, err
}

func (s *Store) GraphReadSnapshot(ctx context.Context, includeMilestones bool) (GraphReadSnapshot, error) {
	var snap GraphReadSnapshot
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		project, err := tx.Project(ctx)
		if err != nil {
			return err
		}
		graph, err := tx.graph(ctx)
		if err != nil {
			return err
		}
		snap.Project = *project
		snap.Graph = graph
		if includeMilestones {
			if s.testAfterGraphRead != nil {
				s.testAfterGraphRead()
			}
			snap.Milestones, err = listMilestoneReadSnapshotsTx(ctx, tx, "")
			if err != nil {
				return err
			}
		}
		return nil
	})
	return snap, err
}

func (s *Store) TaskReadSnapshot(ctx context.Context, id string) (TaskReadSnapshot, error) {
	var snap TaskReadSnapshot
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		p, err := tx.Project(ctx)
		if err != nil {
			return err
		}
		t, err := tx.GetTask(ctx, id)
		if err != nil {
			return err
		}
		snap.Project, snap.Task = *p, *t
		var attempt Attempt
		err = tx.tx.QueryRowContext(ctx, `SELECT attempt_number,status,started_at,completed_at,summary,reason FROM attempts WHERE task_id=? AND status='running'`, id).Scan(&attempt.AttemptNumber, &attempt.Status, &attempt.StartedAt, &attempt.CompletedAt, &attempt.Summary, &attempt.Reason)
		if err == nil {
			snap.CurrentAttempt = &attempt
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return nil
	})
	return snap, err
}

func (s *Store) TaskReadSnapshotByTitle(ctx context.Context, title string) (TaskReadSnapshot, error) {
	var snap TaskReadSnapshot
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		p, err := tx.Project(ctx)
		if err != nil {
			return err
		}
		t, _, err := tx.GetTaskByTitle(ctx, title)
		if err != nil {
			return err
		}
		snap.Project, snap.Task = *p, *t
		var attempt Attempt
		err = tx.tx.QueryRowContext(ctx, `SELECT attempt_number,status,started_at,completed_at,summary,reason FROM attempts WHERE task_id=? AND status='running'`, t.ID).Scan(&attempt.AttemptNumber, &attempt.Status, &attempt.StartedAt, &attempt.CompletedAt, &attempt.Summary, &attempt.Reason)
		if err == nil {
			snap.CurrentAttempt = &attempt
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		return nil
	})
	return snap, err
}

func (s *Store) StatusReadSnapshot(ctx context.Context) (StatusReadSnapshot, error) {
	var snap StatusReadSnapshot
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		g, err := tx.graph(ctx)
		if err != nil {
			return err
		}
		milestones, err := readyMilestonesTx(ctx, tx)
		if err != nil {
			return err
		}
		snap.Graph = g
		snap.Milestones = milestones
		return nil
	})
	return snap, err
}

func (s *Store) ReadyReadSnapshot(ctx context.Context) (ReadyReadSnapshot, error) {
	var snap ReadyReadSnapshot
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		g, err := tx.graph(ctx)
		if err != nil {
			return err
		}
		snap.Graph = g
		ready := g.Ready()
		snap.Tasks = make([]domain.Task, 0, len(ready))
		for _, rt := range ready {
			t, err := tx.GetTask(ctx, rt.ID)
			if err != nil {
				return err
			}
			snap.Tasks = append(snap.Tasks, *t)
		}
		return nil
	})
	return snap, err
}

func loadGraph(ctx context.Context, q queryer) (dag.Graph, error) {
	g := dag.Graph{Tasks: map[string]dag.Task{}, Deps: map[string][]string{}}
	rows, err := q.QueryContext(ctx, `SELECT id,title,priority,creation_order,status FROM tasks`)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var t dag.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Priority, &t.CreationOrder, &t.Status); err != nil {
			return g, err
		}
		g.Tasks[t.ID] = t
	}
	if err := rows.Err(); err != nil {
		return g, err
	}
	rows, err = q.QueryContext(ctx, `SELECT task_id,dependency_id FROM dependencies`)
	if err != nil {
		return g, err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return g, err
		}
		g.Deps[a] = append(g.Deps[a], b)
	}
	return g, rows.Err()
}

func (s *Store) ReadyTasks(ctx context.Context) ([]domain.Task, error) {
	snap, err := s.ReadyReadSnapshot(ctx)
	return snap.Tasks, err
}

type missingDependencyEndpoint struct {
	TaskID       string
	DependencyID string
	MissingSide  string
}

func missingDependencyEndpoints(ctx context.Context, q queryer) ([]missingDependencyEndpoint, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT d.task_id, d.dependency_id,
			CASE
				WHEN task.id IS NULL AND dep.id IS NULL THEN 'task and dependency endpoints'
				WHEN task.id IS NULL THEN 'task endpoint'
				ELSE 'dependency endpoint'
			END
		FROM dependencies d
		LEFT JOIN tasks task ON task.id = d.task_id
		LEFT JOIN tasks dep ON dep.id = d.dependency_id
		WHERE task.id IS NULL OR dep.id IS NULL
		ORDER BY d.task_id, d.dependency_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []missingDependencyEndpoint
	for rows.Next() {
		var e missingDependencyEndpoint
		if err := rows.Scan(&e.TaskID, &e.DependencyID, &e.MissingSide); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) Validate(ctx context.Context) (ValidationReport, error) {
	var r ValidationReport
	var p *domain.Project
	err := s.WithReadTx(ctx, func(tx *Tx) error {
		var err error
		p, err = tx.Project(ctx)
		if err != nil {
			return err
		}
		if strings.TrimSpace(p.Goal) == "" {
			r.Errors = append(r.Errors, "project goal is empty")
		}
		rows, err := tx.tx.QueryContext(ctx, `SELECT id FROM tasks ORDER BY priority,creation_order,id`)
		if err != nil {
			return err
		}
		var tasks []domain.Task
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			t, err := tx.GetTask(ctx, id)
			if err != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("task %s cannot be read: %v", id, err))
				continue
			}
			tasks = append(tasks, *t)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		titles := map[string]int{}
		for _, t := range tasks {
			titles[t.Title]++
			if strings.TrimSpace(t.Title) == "" {
				r.Errors = append(r.Errors, fmt.Sprintf("%s title is empty", t.ID))
			}
			if strings.TrimSpace(t.Objective) == "" {
				r.Errors = append(r.Errors, fmt.Sprintf("%s objective is empty", t.ID))
			}
			if len(t.AcceptanceCriteria) == 0 {
				r.Errors = append(r.Errors, fmt.Sprintf("%s has no acceptance criteria", t.ID))
			}
			if len(t.AcceptanceCriteria) > TooManyAcceptanceCriteriaWarningLimit {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s has too many acceptance criteria (%d > %d)", t.ID, len(t.AcceptanceCriteria), TooManyAcceptanceCriteriaWarningLimit))
			}
		}
		for title, n := range titles {
			if n > 1 {
				r.Warnings = append(r.Warnings, fmt.Sprintf("title lookup is ambiguous for %q", title))
			}
		}
		missing, err := missingDependencyEndpoints(ctx, tx.tx)
		if err != nil {
			return err
		}
		for _, edge := range missing {
			r.Errors = append(r.Errors, fmt.Sprintf("dependency %s -> %s references missing %s", edge.TaskID, edge.DependencyID, edge.MissingSide))
		}
		g, err := tx.graph(ctx)
		if err != nil {
			return err
		}
		if c := g.Cycle(); len(c) > 0 {
			r.Errors = append(r.Errors, "dependency graph has cycle: "+strings.Join(c, " -> "))
		}
		for _, t := range tasks {
			if t.Status == domain.StatusRunning || t.Status == domain.StatusPassed {
				for _, unmet := range g.Unmet(t.ID) {
					if unmet.ID != "" {
						r.Errors = append(r.Errors, fmt.Sprintf("%s task %s has unmet dependency %s", t.Status, t.ID, unmet.ID))
					}
				}
			}
		}
		return validateMilestones(ctx, tx, &r)
	})
	if err != nil {
		return r, err
	}
	prdPath := p.PRDPath
	if !filepath.IsAbs(prdPath) {
		prdPath = filepath.Join(s.root, prdPath)
	}
	if _, err := os.Stat(prdPath); err != nil {
		r.Errors = append(r.Errors, fmt.Sprintf("PRD does not exist: %s", p.PRDPath))
	} else if h, err := HashFile(prdPath); err == nil && h != p.PRDHash {
		r.Warnings = append(r.Warnings, "PRD content hash has changed")
	}
	return r, nil
}

// validateMilestones checks persisted checkpoint invariants without attempting
// to reconstruct a marked milestone from mutable live dependencies.
func validateMilestones(ctx context.Context, tx *Tx, r *ValidationReport) error {
	var next int
	if err := tx.tx.QueryRowContext(ctx, `SELECT next_milestone_sequence FROM projects WHERE id=1`).Scan(&next); err != nil {
		return err
	}
	rows, err := tx.tx.QueryContext(ctx, `SELECT id,title,reason,status,next_recommendation_sequence,marked_at,mark_summary FROM milestones ORDER BY creation_order,id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	maxMilestone := 0
	for rows.Next() {
		var id, title, reason, status string
		var recNext int
		var markedAt, summary *string
		if err := rows.Scan(&id, &title, &reason, &status, &recNext, &markedAt, &summary); err != nil {
			return err
		}
		if n, ok := canonicalSuffix(id, "MILESTONE-"); ok && n > maxMilestone {
			maxMilestone = n
		}
		if strings.TrimSpace(title) == "" {
			r.Errors = append(r.Errors, fmt.Sprintf("%s title is empty", id))
		}
		if strings.TrimSpace(reason) == "" {
			r.Errors = append(r.Errors, fmt.Sprintf("%s reason is empty", id))
		}
		if recNext <= 0 {
			r.Errors = append(r.Errors, fmt.Sprintf("%s next recommendation sequence is non-positive", id))
		}
		var anchors, snapshots, snapshotAnchors int
		if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM milestone_anchors WHERE milestone_id=?`, id).Scan(&anchors); err != nil {
			return err
		}
		if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN is_anchor=1 THEN 1 ELSE 0 END),0) FROM milestone_snapshots WHERE milestone_id=?`, id).Scan(&snapshots, &snapshotAnchors); err != nil {
			return err
		}
		if status == domain.MilestoneStatusPlanned {
			if anchors == 0 {
				r.Errors = append(r.Errors, fmt.Sprintf("planned milestone %s has no live anchors", id))
			}
			if snapshots != 0 {
				r.Errors = append(r.Errors, fmt.Sprintf("planned milestone %s has snapshot rows", id))
			}
			if markedAt != nil || summary != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("planned milestone %s has mark data", id))
			}
		}
		if status == domain.MilestoneStatusMarked {
			if anchors != 0 {
				r.Errors = append(r.Errors, fmt.Sprintf("marked milestone %s has live anchors", id))
			}
			if markedAt == nil || strings.TrimSpace(value(markedAt)) == "" || summary == nil || strings.TrimSpace(value(summary)) == "" {
				r.Errors = append(r.Errors, fmt.Sprintf("marked milestone %s lacks marked timestamp or summary", id))
			}
			if snapshots == 0 || snapshotAnchors == 0 {
				r.Errors = append(r.Errors, fmt.Sprintf("marked milestone %s lacks snapshot or anchor snapshot", id))
			}
		}
		if status != domain.MilestoneStatusPlanned && status != domain.MilestoneStatusMarked {
			r.Errors = append(r.Errors, fmt.Sprintf("%s has invalid status %s", id, status))
		}
		if err := validateMilestoneRows(ctx, tx, id, recNext, r); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	badAnchors, err := tx.tx.QueryContext(ctx, `
			SELECT a.milestone_id,a.task_id FROM milestone_anchors a
			LEFT JOIN tasks t ON t.id=a.task_id WHERE t.id IS NULL
			ORDER BY a.milestone_id,a.task_id`)
	if err != nil {
		return err
	}
	for badAnchors.Next() {
		var milestoneID, taskID string
		if err := badAnchors.Scan(&milestoneID, &taskID); err != nil {
			badAnchors.Close()
			return err
		}
		r.Errors = append(r.Errors, fmt.Sprintf("planned anchor %s -> %s references missing task", milestoneID, taskID))
	}
	if err := badAnchors.Err(); err != nil {
		badAnchors.Close()
		return err
	}
	badAnchors.Close()
	duplicates, err := tx.tx.QueryContext(ctx, `SELECT milestone_id,task_id FROM milestone_anchors GROUP BY milestone_id,task_id HAVING COUNT(*) > 1 ORDER BY milestone_id,task_id`)
	if err != nil {
		return err
	}
	for duplicates.Next() {
		var milestoneID, taskID string
		if err := duplicates.Scan(&milestoneID, &taskID); err != nil {
			duplicates.Close()
			return err
		}
		r.Errors = append(r.Errors, fmt.Sprintf("planned milestone %s has duplicate anchor %s", milestoneID, taskID))
	}
	if err := duplicates.Err(); err != nil {
		duplicates.Close()
		return err
	}
	duplicates.Close()
	if next <= 0 || next <= maxMilestone {
		r.Errors = append(r.Errors, "next milestone sequence is invalid")
	}
	return nil
}
func value(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func canonicalSuffix(id, prefix string) (int, bool) {
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(id, prefix)
	if len(suffix) < 3 {
		return 0, false
	}
	n := 0
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return 0, false
		}
		digit := int(c - '0')
		if n > (int(^uint(0)>>1)-digit)/10 {
			return 0, false
		}
		n = n*10 + digit
	}
	return n, true
}
func validateMilestoneRows(ctx context.Context, tx *Tx, id string, next int, r *ValidationReport) error {
	rows, err := tx.tx.QueryContext(ctx, `SELECT recommendation_id,text,position FROM milestone_recommendations WHERE milestone_id=? ORDER BY position,recommendation_id`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	max := 0
	positions := map[int]bool{}
	recommendationIDs := map[string]bool{}
	for rows.Next() {
		var rid, text string
		var pos int
		if err := rows.Scan(&rid, &text, &pos); err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			r.Errors = append(r.Errors, fmt.Sprintf("%s recommendation %s text is empty", id, rid))
		}
		if recommendationIDs[rid] {
			r.Errors = append(r.Errors, fmt.Sprintf("%s has duplicate recommendation ID %s", id, rid))
		}
		recommendationIDs[rid] = true
		if pos < 0 || positions[pos] {
			r.Errors = append(r.Errors, fmt.Sprintf("%s recommendation positions are invalid", id))
		}
		positions[pos] = true
		if n, ok := canonicalSuffix(rid, "REC-"); !ok {
			r.Errors = append(r.Errors, fmt.Sprintf("%s has invalid recommendation ID %s", id, rid))
		} else if n > max {
			max = n
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if next <= max {
		r.Errors = append(r.Errors, fmt.Sprintf("%s next recommendation sequence is invalid", id))
	}
	rows, err = tx.tx.QueryContext(ctx, `SELECT scope_position,priority,creation_order,is_anchor,status,attempt_number FROM milestone_snapshots WHERE milestone_id=? ORDER BY scope_position`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	expected := 0
	for rows.Next() {
		var pos, priority, order, anchor, attempt int
		var status string
		if err := rows.Scan(&pos, &priority, &order, &anchor, &status, &attempt); err != nil {
			return err
		}
		if pos != expected {
			r.Errors = append(r.Errors, fmt.Sprintf("%s snapshot scope positions are not contiguous", id))
			expected = pos
		}
		expected++
		if priority < 0 || order <= 0 {
			r.Errors = append(r.Errors, fmt.Sprintf("%s snapshot priority or creation order is invalid", id))
		}
		if anchor != 0 && anchor != 1 {
			r.Errors = append(r.Errors, fmt.Sprintf("%s snapshot anchor flag is invalid", id))
		}
		if status != domain.StatusPassed || attempt <= 0 {
			r.Errors = append(r.Errors, fmt.Sprintf("%s snapshot status or attempt is invalid", id))
		}
	}
	return rows.Err()
}
