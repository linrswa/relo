package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linrswa/relo/internal/domain"

	_ "modernc.org/sqlite"
)

var ErrValidation = errors.New("validation error")

type ValidationError struct{ Message string }

func (e ValidationError) Error() string        { return e.Message }
func validation(msg string, args ...any) error { return ValidationError{fmt.Sprintf(msg, args...)} }

const currentSchemaVersion = 2

type Store struct {
	db   *sql.DB
	root string

	// testAfterWriteLock is used only by package tests to deterministically
	// coordinate competing writers after BEGIN IMMEDIATE has acquired its lock.
	testAfterWriteLock func()
}

type Tx struct{ tx *sql.Tx }

func (s *Store) WithReadTx(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	if err := fn(&Tx{tx: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return err
	}
	return nil
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, root: filepath.Dir(filepath.Dir(path))}, nil
}
func (s *Store) Close() error { return s.db.Close() }

func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if st, err := os.Stat(filepath.Join(dir, ".relo", "relo.db")); err == nil && !st.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", validation("not inside an initialized relo project (missing .relo/relo.db)")
		}
		dir = parent
	}
}
func DBPath(root string) string { return filepath.Join(root, ".relo", "relo.db") }

type RemoveProjectResult struct {
	Root               string
	MetadataDirRemoved bool
}

// RemoveProjectState deletes only relo-managed database files. Unknown files
// under .relo are preserved so a destructive project removal cannot erase
// caller-owned data.
func RemoveProjectState(root string) (RemoveProjectResult, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return RemoveProjectResult{}, err
	}
	metadataDir := filepath.Join(absRoot, ".relo")
	st, err := os.Lstat(metadataDir)
	if err != nil {
		if os.IsNotExist(err) {
			return RemoveProjectResult{}, validation("not an initialized relo project (missing .relo/relo.db)")
		}
		return RemoveProjectResult{}, err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return RemoveProjectResult{}, validation("refusing to remove project state through non-directory %s", metadataDir)
	}

	dbPath := DBPath(absRoot)
	st, err = os.Lstat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			return RemoveProjectResult{}, validation("not an initialized relo project (missing .relo/relo.db)")
		}
		return RemoveProjectResult{}, err
	}
	if !st.Mode().IsRegular() {
		return RemoveProjectResult{}, validation("refusing to remove non-regular database file %s", dbPath)
	}

	managedPaths := []string{dbPath + "-shm", dbPath + "-wal", dbPath}
	existingPaths := make([]string, 0, len(managedPaths))
	for _, path := range managedPaths {
		st, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return RemoveProjectResult{}, err
		}
		if !st.Mode().IsRegular() {
			return RemoveProjectResult{}, validation("refusing to remove non-regular database file %s", path)
		}
		existingPaths = append(existingPaths, path)
	}
	for _, path := range existingPaths {
		if err := os.Remove(path); err != nil {
			return RemoveProjectResult{}, err
		}
	}

	result := RemoveProjectResult{Root: absRoot}
	entries, err := os.ReadDir(metadataDir)
	if err != nil {
		if os.IsNotExist(err) {
			result.MetadataDirRemoved = true
			return result, nil
		}
		return RemoveProjectResult{}, err
	}
	if len(entries) == 0 {
		if err := os.Remove(metadataDir); err != nil && !os.IsNotExist(err) {
			return RemoveProjectResult{}, err
		}
		result.MetadataDirRemoved = true
	}
	return result, nil
}

func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func InitProject(ctx context.Context, root, prd, goal string) (*domain.Project, error) {
	if strings.TrimSpace(goal) == "" {
		return nil, validation("--goal is required")
	}
	if strings.TrimSpace(prd) == "" {
		return nil, validation("--prd is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if existing, err := FindRoot(absRoot); err == nil {
		s, e := Open(DBPath(existing))
		if e == nil {
			defer s.Close()
			if p, e2 := s.Project(ctx); e2 == nil {
				return nil, validation("project already initialized at %s: goal=%q prd=%s", existing, p.Goal, p.PRDPath)
			}
		}
		return nil, validation("project already initialized at %s", existing)
	}
	absPRD := prd
	if !filepath.IsAbs(absPRD) {
		absPRD = filepath.Join(absRoot, prd)
	}
	hash, err := HashFile(absPRD)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absRoot, ".relo"), 0755); err != nil {
		return nil, err
	}
	s, err := Open(DBPath(absRoot))
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if err := s.Migrate(ctx); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err = s.WithWriteTx(ctx, func(tx *Tx) error {
		if p, err := tx.Project(ctx); err == nil {
			return validation("project already initialized: goal=%q prd=%s", p.Goal, p.PRDPath)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err := tx.tx.ExecContext(ctx, `INSERT INTO projects(id,goal,prd_path,prd_hash,next_task_sequence,created_at,updated_at) VALUES(1,?,?,?,?,?,?)`, goal, prd, hash, 1, now, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Project(ctx)
}

func (s *Store) Migrate(ctx context.Context) error {
	return s.withImmediateTx(ctx, func(tx *sql.Tx) error {
		var v int
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
			return err
		}
		if v > currentSchemaVersion {
			return validation("unsupported database schema version %d (supported %d)", v, currentSchemaVersion)
		}
		for v < currentSchemaVersion {
			var migration string
			switch v {
			case 0:
				migration = schemaV1
			case 1:
				migration = schemaV2
			default:
				return validation("unsupported database schema version %d (supported %d)", v, currentSchemaVersion)
			}
			if _, err := tx.ExecContext(ctx, migration); err != nil {
				return err
			}
			v++
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d`, v)); err != nil {
				return err
			}
		}
		return nil
	})
}

const schemaV1 = `
CREATE TABLE projects (id INTEGER PRIMARY KEY CHECK (id = 1), goal TEXT NOT NULL CHECK (goal <> ''), prd_path TEXT NOT NULL CHECK (prd_path <> ''), prd_hash TEXT NOT NULL CHECK (prd_hash <> ''), next_task_sequence INTEGER NOT NULL CHECK (next_task_sequence > 0), created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE tasks (id TEXT PRIMARY KEY, title TEXT NOT NULL CHECK (title <> ''), objective TEXT NOT NULL CHECK (objective <> ''), priority INTEGER NOT NULL DEFAULT 100 CHECK (priority >= 0), creation_order INTEGER NOT NULL UNIQUE, status TEXT NOT NULL CHECK (status IN ('pending','running','passed','failed')), attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0), last_failure_reason TEXT, last_completion_summary TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE acceptance_criteria (task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, criterion_id TEXT NOT NULL, text TEXT NOT NULL CHECK (text <> ''), position INTEGER NOT NULL CHECK (position >= 0), PRIMARY KEY (task_id, criterion_id), UNIQUE (task_id, position));
CREATE TABLE notes (task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, note_id TEXT NOT NULL, text TEXT NOT NULL CHECK (text <> ''), position INTEGER NOT NULL CHECK (position >= 0), PRIMARY KEY (task_id, note_id), UNIQUE (task_id, position));
CREATE TABLE dependencies (task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, dependency_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT, reason TEXT NOT NULL CHECK (reason <> ''), created_at TEXT NOT NULL, PRIMARY KEY (task_id, dependency_id), CHECK (task_id <> dependency_id));
CREATE TABLE attempts (id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, attempt_number INTEGER NOT NULL CHECK (attempt_number > 0), status TEXT NOT NULL CHECK (status IN ('running','passed','failed','interrupted')), started_at TEXT NOT NULL, completed_at TEXT, summary TEXT, reason TEXT, UNIQUE (task_id, attempt_number));
CREATE TABLE dependency_events (id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, dependency_id TEXT NOT NULL, action TEXT NOT NULL CHECK (action IN ('added','removed','reason_updated')), reason TEXT NOT NULL CHECK (reason <> ''), created_at TEXT NOT NULL);
CREATE TABLE task_events (id INTEGER PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE, event_type TEXT NOT NULL CHECK (event_type IN ('priority_changed','stopped','reopened')), reason TEXT NOT NULL CHECK (reason <> ''), created_at TEXT NOT NULL);
CREATE UNIQUE INDEX one_running_attempt_per_task ON attempts(task_id) WHERE status = 'running';
`

const schemaV2 = `
ALTER TABLE projects ADD COLUMN next_milestone_sequence INTEGER NOT NULL DEFAULT 1 CHECK (next_milestone_sequence > 0);
CREATE TABLE milestones (id TEXT PRIMARY KEY, title TEXT NOT NULL CHECK (title <> ''), reason TEXT NOT NULL CHECK (reason <> ''), status TEXT NOT NULL CHECK (status IN ('planned','marked')), creation_order INTEGER NOT NULL UNIQUE, next_recommendation_sequence INTEGER NOT NULL DEFAULT 1 CHECK (next_recommendation_sequence > 0), mark_summary TEXT, reference TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, marked_at TEXT, CHECK ((status = 'planned' AND marked_at IS NULL AND mark_summary IS NULL) OR (status = 'marked' AND marked_at IS NOT NULL AND mark_summary IS NOT NULL AND mark_summary <> '')));
CREATE TABLE milestone_anchors (milestone_id TEXT NOT NULL REFERENCES milestones(id) ON DELETE CASCADE, task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT, created_at TEXT NOT NULL, PRIMARY KEY (milestone_id, task_id));
CREATE TABLE milestone_recommendations (milestone_id TEXT NOT NULL REFERENCES milestones(id) ON DELETE CASCADE, recommendation_id TEXT NOT NULL, text TEXT NOT NULL CHECK (text <> ''), position INTEGER NOT NULL CHECK (position >= 0), created_at TEXT NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY (milestone_id, recommendation_id), UNIQUE (milestone_id, position));
CREATE TABLE milestone_snapshots (milestone_id TEXT NOT NULL REFERENCES milestones(id) ON DELETE RESTRICT, task_id TEXT NOT NULL, task_title TEXT NOT NULL, priority INTEGER NOT NULL CHECK (priority >= 0), creation_order INTEGER NOT NULL CHECK (creation_order > 0), scope_position INTEGER NOT NULL CHECK (scope_position >= 0), is_anchor INTEGER NOT NULL CHECK (is_anchor IN (0,1)), attempt_number INTEGER NOT NULL CHECK (attempt_number > 0), status TEXT NOT NULL CHECK (status = 'passed'), completion_summary TEXT, task_updated_at TEXT NOT NULL, captured_at TEXT NOT NULL, PRIMARY KEY (milestone_id, task_id), UNIQUE (milestone_id, scope_position));`

func (s *Store) WithWriteTx(ctx context.Context, fn func(*Tx) error) error {
	return s.withImmediateTx(ctx, func(tx *sql.Tx) error { return fn(&Tx{tx: tx}) })
}

func (s *Store) withImmediateTx(ctx context.Context, fn func(*sql.Tx) error) error {
	var last error
	for i := 0; i < 5; i++ {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
		if err != nil {
			if isRetryableBusy(err) {
				last = err
				sleepBeforeRetry(i)
				continue
			}
			return err
		}
		if s.testAfterWriteLock != nil {
			s.testAfterWriteLock()
		}
		err = fn(tx)
		if err != nil {
			_ = tx.Rollback()
			if isRetryableBusy(err) {
				last = err
				sleepBeforeRetry(i)
				continue
			}
			return err
		}
		if err = tx.Commit(); err != nil {
			_ = tx.Rollback()
			if isRetryableBusy(err) {
				last = err
				sleepBeforeRetry(i)
				continue
			}
			return err
		}
		return nil
	}
	if last != nil {
		return fmt.Errorf("database busy: %w", last)
	}
	return fmt.Errorf("database busy")
}

func isRetryableBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "busy") || strings.Contains(msg, "locked")
}

func sleepBeforeRetry(attempt int) { time.Sleep(time.Duration(attempt+1) * 50 * time.Millisecond) }

func (s *Store) Project(ctx context.Context) (*domain.Project, error) { return project(ctx, s.db) }
func (tx *Tx) Project(ctx context.Context) (*domain.Project, error)   { return project(ctx, tx.tx) }

func (s *Store) UpdateProject(ctx context.Context, goal *string, prd *string) (*domain.Project, error) {
	if goal == nil && prd == nil {
		return nil, validation("at least one of --goal or --prd is required")
	}
	if goal != nil && strings.TrimSpace(*goal) == "" {
		return nil, validation("--goal must not be empty")
	}
	if prd != nil && strings.TrimSpace(*prd) == "" {
		return nil, validation("--prd must not be empty")
	}
	err := s.WithWriteTx(ctx, func(tx *Tx) error {
		p, err := tx.Project(ctx)
		if err != nil {
			return err
		}
		newGoal := p.Goal
		newPRDPath := p.PRDPath
		newPRDHash := p.PRDHash
		if goal != nil {
			newGoal = *goal
		}
		if prd != nil {
			resolved, hash, err := s.resolveAndHashPRD(*prd)
			if err != nil {
				return err
			}
			newPRDPath = resolved
			newPRDHash = hash
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = tx.tx.ExecContext(ctx, `UPDATE projects SET goal=?, prd_path=?, prd_hash=?, updated_at=? WHERE id=1`, newGoal, newPRDPath, newPRDHash, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.Project(ctx)
}

func (s *Store) RefreshPRDHash(ctx context.Context) (oldHash, newHash string, p *domain.Project, err error) {
	err = s.WithWriteTx(ctx, func(tx *Tx) error {
		current, err := tx.Project(ctx)
		if err != nil {
			return err
		}
		oldHash = current.PRDHash
		path := current.PRDPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(s.root, path)
		}
		newHash, err = HashFile(path)
		if err != nil {
			return validation("cannot read current PRD %q: %v", current.PRDPath, err)
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = tx.tx.ExecContext(ctx, `UPDATE projects SET prd_hash=?, updated_at=? WHERE id=1`, newHash, now)
		return err
	})
	if err != nil {
		return "", "", nil, err
	}
	p, err = s.Project(ctx)
	return oldHash, newHash, p, err
}

func (s *Store) resolveAndHashPRD(prd string) (string, string, error) {
	cleaned := filepath.Clean(prd)
	abs := cleaned
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.root, cleaned)
	}
	hash, err := HashFile(abs)
	if err != nil {
		return "", "", validation("cannot read PRD %q relative to project root %s: %v", prd, s.root, err)
	}
	persist := cleaned
	if filepath.IsAbs(cleaned) {
		if rel, err := filepath.Rel(s.root, cleaned); err == nil && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." {
			persist = rel
		}
	}
	return persist, hash, nil
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func project(ctx context.Context, q queryer) (*domain.Project, error) {
	p := &domain.Project{}
	err := q.QueryRowContext(ctx, `SELECT goal,prd_path,prd_hash,next_task_sequence,next_milestone_sequence,created_at,updated_at FROM projects WHERE id=1`).Scan(&p.Goal, &p.PRDPath, &p.PRDHash, &p.NextTaskSequence, &p.NextMilestoneSequence, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func (s *Store) CreateTask(ctx context.Context, title, objective string, accepts []string, priority int) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", validation("--title is required")
	}
	if strings.TrimSpace(objective) == "" {
		return "", validation("--objective or --objective-file is required")
	}
	if len(accepts) == 0 {
		return "", validation("at least one --accept is required")
	}
	for _, a := range accepts {
		if strings.TrimSpace(a) == "" {
			return "", validation("acceptance criterion must not be empty")
		}
	}
	if priority < 0 {
		return "", validation("--priority must be >= 0")
	}
	var id string
	err := s.WithWriteTx(ctx, func(tx *Tx) error {
		p, err := tx.Project(ctx)
		if err != nil {
			return err
		}
		id = domain.TaskID(p.NextTaskSequence)
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if _, err = tx.tx.ExecContext(ctx, `INSERT INTO tasks(id,title,objective,priority,creation_order,status,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,?)`, id, title, objective, priority, p.NextTaskSequence, now, now); err != nil {
			return err
		}
		for i, a := range accepts {
			if _, err = tx.tx.ExecContext(ctx, `INSERT INTO acceptance_criteria(task_id,criterion_id,text,position) VALUES(?,?,?,?)`, id, domain.ACID(i+1), a, i); err != nil {
				return err
			}
		}
		_, err = tx.tx.ExecContext(ctx, `UPDATE projects SET next_task_sequence=?, updated_at=? WHERE id=1`, p.NextTaskSequence+1, now)
		return err
	})
	return id, err
}

func (s *Store) GetTask(ctx context.Context, id string) (*domain.Task, error) {
	return getTask(ctx, s.db, id)
}
func (tx *Tx) GetTask(ctx context.Context, id string) (*domain.Task, error) {
	return getTask(ctx, tx.tx, id)
}
func (s *Store) GetTaskByTitle(ctx context.Context, title string) (*domain.Task, []string, error) {
	return getTaskByTitle(ctx, s.db, title)
}
func (tx *Tx) GetTaskByTitle(ctx context.Context, title string) (*domain.Task, []string, error) {
	return getTaskByTitle(ctx, tx.tx, title)
}
func getTaskByTitle(ctx context.Context, q queryer, title string) (*domain.Task, []string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM tasks WHERE title=? ORDER BY priority, creation_order, id`, title)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(ids) == 0 {
		return nil, nil, sql.ErrNoRows
	}
	if len(ids) > 1 {
		return nil, ids, validation("title %q is ambiguous: %s", title, strings.Join(ids, ", "))
	}
	t, err := getTask(ctx, q, ids[0])
	return t, nil, err
}
func getTask(ctx context.Context, q queryer, id string) (*domain.Task, error) {
	t := &domain.Task{}
	err := q.QueryRowContext(ctx, `SELECT id,title,objective,priority,creation_order,status,attempt_count,last_failure_reason,last_completion_summary,created_at,updated_at FROM tasks WHERE id=?`, id).Scan(&t.ID, &t.Title, &t.Objective, &t.Priority, &t.CreationOrder, &t.Status, &t.AttemptCount, &t.LastFailureReason, &t.LastCompletionSummary, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT criterion_id,text,position FROM acceptance_criteria WHERE task_id=? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ac domain.AcceptanceCriterion
		if err := rows.Scan(&ac.ID, &ac.Text, &ac.Position); err != nil {
			return nil, err
		}
		t.AcceptanceCriteria = append(t.AcceptanceCriteria, ac)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT note_id,text,position FROM notes WHERE task_id=? ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n domain.Note
		if err := rows.Scan(&n.ID, &n.Text, &n.Position); err != nil {
			return nil, err
		}
		t.Notes = append(t.Notes, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, `SELECT d.task_id,d.dependency_id,d.reason,d.created_at,t.status,t.title FROM dependencies d JOIN tasks t ON t.id=d.dependency_id WHERE d.task_id=? ORDER BY t.priority,t.creation_order,t.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d domain.Dependency
		if err := rows.Scan(&d.TaskID, &d.DependencyID, &d.Reason, &d.CreatedAt, &d.Status, &d.Title); err != nil {
			return nil, err
		}
		t.Dependencies = append(t.Dependencies, d)
	}
	return t, rows.Err()
}

func (s *Store) ListTasks(ctx context.Context, status string) ([]domain.Task, error) {
	args := []any{}
	sqls := `SELECT id,title,objective,priority,creation_order,status,attempt_count,last_failure_reason,last_completion_summary,created_at,updated_at FROM tasks`
	if status != "" {
		switch status {
		case "pending", "running", "passed", "failed":
		default:
			return nil, validation("--status must be pending, running, passed, or failed")
		}
		sqls += ` WHERE status=?`
		args = append(args, status)
	}
	sqls += ` ORDER BY priority, creation_order, id`
	rows, err := s.db.QueryContext(ctx, sqls, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Task
	for rows.Next() {
		var t domain.Task
		if err := rows.Scan(&t.ID, &t.Title, &t.Objective, &t.Priority, &t.CreationOrder, &t.Status, &t.AttemptCount, &t.LastFailureReason, &t.LastCompletionSummary, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) UpdateTask(ctx context.Context, id string, title *string, objective *string, priority *int, reason string) error {
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if !domain.CanModifyDefinition(t.Status) {
			return taskDefinitionMutationError(id, t.Status)
		}
		if priority != nil && *priority < 0 {
			return validation("--priority must be >= 0")
		}
		if priority != nil && strings.TrimSpace(reason) == "" {
			return validation("--reason is required when changing priority")
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		changed := false
		if title != nil {
			if strings.TrimSpace(*title) == "" {
				return validation("title must not be empty")
			}
			_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET title=?, updated_at=? WHERE id=?`, *title, now, id)
			if err != nil {
				return err
			}
			changed = true
		}
		if objective != nil {
			if strings.TrimSpace(*objective) == "" {
				return validation("objective must not be empty")
			}
			_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET objective=?, updated_at=? WHERE id=?`, *objective, now, id)
			if err != nil {
				return err
			}
			changed = true
		}
		if priority != nil {
			_, err = tx.tx.ExecContext(ctx, `UPDATE tasks SET priority=?, updated_at=? WHERE id=?`, *priority, now, id)
			if err != nil {
				return err
			}
			_, err = tx.tx.ExecContext(ctx, `INSERT INTO task_events(task_id,event_type,reason,created_at) VALUES(?,'priority_changed',?,?)`, id, reason, now)
			if err != nil {
				return err
			}
			changed = true
		}
		if !changed {
			return validation("no updates requested")
		}
		return nil
	})
}

func (s *Store) DeleteTask(ctx context.Context, id string) error {
	return s.WithWriteTx(ctx, func(tx *Tx) error {
		t, err := getTask(ctx, tx.tx, id)
		if err != nil {
			return err
		}
		if !domain.CanDelete(t.Status) {
			return taskDeleteMutationError(id, t.Status)
		}
		rows, err := tx.tx.QueryContext(ctx, `SELECT m.id FROM milestone_anchors a JOIN milestones m ON m.id=a.milestone_id WHERE a.task_id=? AND m.status='planned' ORDER BY m.creation_order,m.id`, id)
		if err != nil {
			return err
		}
		var milestoneIDs []string
		for rows.Next() {
			var milestoneID string
			if err := rows.Scan(&milestoneID); err != nil {
				rows.Close()
				return err
			}
			milestoneIDs = append(milestoneIDs, milestoneID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if len(milestoneIDs) > 0 {
			return validation("%s is anchored by planned milestone(s): %s", id, strings.Join(milestoneIDs, ", "))
		}
		var n int
		if err = tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dependencies WHERE dependency_id=?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return validation("%s is required by downstream task(s) and cannot be deleted", id)
		}
		_, err = tx.tx.ExecContext(ctx, `DELETE FROM tasks WHERE id=?`, id)
		return err
	})
}
