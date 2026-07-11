package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"relo/internal/dag"
	"relo/internal/domain"
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
			return validation("%s is %s and cannot be modified", taskID, t.Status)
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
			return validation("%s is %s and cannot be modified", taskID, t.Status)
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
			return validation("%s is %s and cannot be modified", taskID, t.Status)
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
			return validation("%s is %s and cannot be modified", taskID, t.Status)
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
			return validation("%s is %s and cannot be modified", taskID, t.Status)
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
			return validation("%s is %s and cannot be modified", taskID, t.Status)
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
			return validation("%s is %s and cannot be modified", target, t.Status)
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
			return validation("%s is %s and cannot be modified", target, t.Status)
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
			return validation("%s is %s and cannot be modified", target, t.Status)
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

type TaskReadSnapshot struct {
	Project domain.Project
	Task    domain.Task
}

type ReadyReadSnapshot struct {
	Graph dag.Graph
	Tasks []domain.Task
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
		snap.Project = *p
		snap.Task = *t
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
		snap.Project = *p
		snap.Task = *t
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

func (s *Store) missingDependencyEndpoints(ctx context.Context) ([]missingDependencyEndpoint, error) {
	rows, err := s.db.QueryContext(ctx, `
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
	p, err := s.Project(ctx)
	if err != nil {
		return r, err
	}
	if strings.TrimSpace(p.Goal) == "" {
		r.Errors = append(r.Errors, "project goal is empty")
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
	tasks, err := s.ListTasks(ctx, "")
	if err != nil {
		return r, err
	}
	priorityCounts := map[int]int{}
	titles := map[string]int{}
	for _, t := range tasks {
		priorityCounts[t.Priority]++
		titles[t.Title]++
		full, err := s.GetTask(ctx, t.ID)
		if err != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("task %s cannot be read: %v", t.ID, err))
			continue
		}
		if strings.TrimSpace(t.Title) == "" {
			r.Errors = append(r.Errors, fmt.Sprintf("%s title is empty", t.ID))
		}
		if strings.TrimSpace(t.Objective) == "" {
			r.Errors = append(r.Errors, fmt.Sprintf("%s objective is empty", t.ID))
		}
		if len(full.AcceptanceCriteria) == 0 {
			r.Errors = append(r.Errors, fmt.Sprintf("%s has no acceptance criteria", t.ID))
		}
		if len(full.AcceptanceCriteria) > TooManyAcceptanceCriteriaWarningLimit {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s has too many acceptance criteria (%d > %d)", t.ID, len(full.AcceptanceCriteria), TooManyAcceptanceCriteriaWarningLimit))
		}
		if len(full.Notes) == 0 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s has no notes", t.ID))
		}
	}
	for p, n := range priorityCounts {
		if n > 1 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("multiple tasks use priority %d", p))
		}
	}
	for title, n := range titles {
		if n > 1 {
			r.Warnings = append(r.Warnings, fmt.Sprintf("title lookup is ambiguous for %q", title))
		}
	}
	missing, err := s.missingDependencyEndpoints(ctx)
	if err != nil {
		return r, err
	}
	for _, edge := range missing {
		r.Errors = append(r.Errors, fmt.Sprintf("dependency %s -> %s references missing %s", edge.TaskID, edge.DependencyID, edge.MissingSide))
	}
	g, err := s.graph(ctx)
	if err != nil {
		return r, err
	}
	if c := g.Cycle(); len(c) > 0 {
		r.Errors = append(r.Errors, "dependency graph has cycle: "+strings.Join(c, " -> "))
	}
	for _, t := range tasks {
		if t.Status == domain.StatusRunning || t.Status == domain.StatusPassed {
			for _, unmet := range g.Unmet(t.ID) {
				if unmet.ID == "" {
					continue
				}
				r.Errors = append(r.Errors, fmt.Sprintf("%s task %s has unmet dependency %s", t.Status, t.ID, unmet.ID))
			}
		}
	}
	return r, nil
}
