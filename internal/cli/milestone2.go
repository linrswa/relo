package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"relo/internal/domain"
	"relo/internal/store"

	"github.com/spf13/cobra"
)

var jsonErrorWritten bool

type envelope struct {
	SchemaVersion string `json:"schemaVersion"`
	OK            bool   `json:"ok"`
	Data          any    `json:"data,omitempty"`
	Error         any    `json:"error,omitempty"`
}

func okEnvelope(data any) envelope {
	return envelope{SchemaVersion: "relo.output/v1", OK: true, Data: data}
}

func errorEnvelope(code, message string) envelope {
	return envelope{SchemaVersion: "relo.output/v1", OK: false, Error: map[string]any{"code": code, "message": message, "details": map[string]any{}}}
}

func writeJSONOK(cmd *cobra.Command, data any) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(okEnvelope(data))
}

func writeJSONError(cmd *cobra.Command, enabled bool, code string, err error) {
	if !enabled || err == nil {
		return
	}
	if code == "" {
		code = jsonErrorCode(err)
	}
	jsonErrorWritten = true
	_ = json.NewEncoder(cmd.OutOrStdout()).Encode(errorEnvelope(code, err.Error()))
}

func jsonErrorCode(err error) string {
	var ve store.ValidationError
	if errors.Is(err, sql.ErrNoRows) {
		return "NOT_FOUND"
	}
	if errors.As(err, &ve) {
		return "VALIDATION_ERROR"
	}
	return "OPERATIONAL_ERROR"
}

// projectDTO is the long-standing task-get nested-project contract.
type projectDTO struct {
	Goal    string `json:"goal"`
	PRDPath string `json:"prd_path"`
}

// projectShowDTO deliberately includes the additional project-show hash.
type projectShowDTO struct {
	Goal    string `json:"goal"`
	PRDPath string `json:"prd_path"`
	PRDHash string `json:"prd_hash"`
}

type taskDTO struct {
	ID                 string          `json:"id"`
	Title              string          `json:"title"`
	Status             string          `json:"status"`
	Priority           int             `json:"priority"`
	Objective          string          `json:"objective"`
	AcceptanceCriteria []acDTO         `json:"acceptance_criteria"`
	Dependencies       []dependencyDTO `json:"dependencies"`
	Notes              []noteDTO       `json:"notes"`
}
type taskDetailDTO struct {
	taskDTO
	CreationOrder         int         `json:"creation_order"`
	AttemptCount          int         `json:"attempt_count"`
	LastFailureReason     *string     `json:"last_failure_reason"`
	LastCompletionSummary *string     `json:"last_completion_summary"`
	CreatedAt             string      `json:"created_at"`
	UpdatedAt             string      `json:"updated_at"`
	CurrentAttempt        *attemptDTO `json:"current_attempt"`
}

type taskSummaryDTO struct {
	ID                    string  `json:"id"`
	Title                 string  `json:"title"`
	Status                string  `json:"status"`
	Priority              int     `json:"priority"`
	AttemptCount          int     `json:"attempt_count"`
	LastFailureReason     *string `json:"last_failure_reason"`
	LastCompletionSummary *string `json:"last_completion_summary"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
}
type attemptDTO struct {
	AttemptNumber int     `json:"attempt_number"`
	Status        string  `json:"status"`
	StartedAt     string  `json:"started_at"`
	CompletedAt   *string `json:"completed_at"`
	Summary       *string `json:"summary"`
	Reason        *string `json:"reason"`
}

type acDTO struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type dependencyDTO struct {
	TaskID       string `json:"task_id"`
	DependencyID string `json:"dependency_id"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
	Title        string `json:"title"`
}

type noteDTO struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type taskGetDTO struct {
	Project projectDTO    `json:"project"`
	Task    taskDetailDTO `json:"task"`
}

func toTaskDTO(t domain.Task) taskDTO {
	out := taskDTO{
		ID:                 t.ID,
		Title:              t.Title,
		Status:             t.Status,
		Priority:           t.Priority,
		Objective:          t.Objective,
		AcceptanceCriteria: []acDTO{},
		Dependencies:       []dependencyDTO{},
		Notes:              []noteDTO{},
	}
	for _, ac := range t.AcceptanceCriteria {
		out.AcceptanceCriteria = append(out.AcceptanceCriteria, acDTO{ID: ac.ID, Text: ac.Text})
	}
	for _, d := range t.Dependencies {
		out.Dependencies = append(out.Dependencies, dependencyDTO{TaskID: d.TaskID, DependencyID: d.DependencyID, Status: d.Status, Reason: d.Reason, Title: d.Title})
	}
	for _, n := range t.Notes {
		out.Notes = append(out.Notes, noteDTO{ID: n.ID, Text: n.Text})
	}
	return out
}

func toTaskGetDTO(snap store.TaskReadSnapshot) taskGetDTO {
	task := taskDetailDTO{taskDTO: toTaskDTO(snap.Task), CreationOrder: snap.Task.CreationOrder, AttemptCount: snap.Task.AttemptCount, LastFailureReason: snap.Task.LastFailureReason, LastCompletionSummary: snap.Task.LastCompletionSummary, CreatedAt: snap.Task.CreatedAt, UpdatedAt: snap.Task.UpdatedAt}
	if snap.CurrentAttempt != nil {
		a := snap.CurrentAttempt
		task.CurrentAttempt = &attemptDTO{AttemptNumber: a.AttemptNumber, Status: a.Status, StartedAt: a.StartedAt, CompletedAt: a.CompletedAt, Summary: a.Summary, Reason: a.Reason}
	}
	return taskGetDTO{Project: projectDTO{Goal: snap.Project.Goal, PRDPath: snap.Project.PRDPath}, Task: task}
}

func toTaskSummariesDTO(tasks []domain.Task) []taskSummaryDTO {
	out := make([]taskSummaryDTO, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskSummaryDTO{t.ID, t.Title, t.Status, t.Priority, t.AttemptCount, t.LastFailureReason, t.LastCompletionSummary, t.CreatedAt, t.UpdatedAt})
	}
	return out
}

func toTasksDTO(tasks []domain.Task) []taskDTO {
	out := make([]taskDTO, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toTaskDTO(t))
	}
	return out
}

func (a *app) taskAcceptanceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "acceptance", Short: "Manage acceptance criteria"}
	cmd.AddCommand(a.taskAcceptanceAddCmd(), a.taskAcceptanceUpdateCmd(), a.taskAcceptanceRemoveCmd())
	return cmd
}
func (a *app) taskAcceptanceAddCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "add TASK-ID", Short: "Add an acceptance criterion", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		id, err := s.AddAcceptance(cmd.Context(), args[0], text)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), id)
		return nil
	}}
	cmd.Flags().StringVar(&text, "text", "", "text content")
	return cmd
}
func (a *app) taskAcceptanceUpdateCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "update TASK-ID AC-ID", Short: "Update an acceptance criterion", Args: validationArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskID(args[0]); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.UpdateAcceptance(cmd.Context(), args[0], args[1], text)
	}}
	cmd.Flags().StringVar(&text, "text", "", "text content")
	return cmd
}
func (a *app) taskAcceptanceRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "remove TASK-ID AC-ID", Short: "Remove an acceptance criterion", Args: validationArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskID(args[0]); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.RemoveAcceptance(cmd.Context(), args[0], args[1])
	}}
}

func (a *app) taskNoteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "note", Short: "Manage task notes"}
	cmd.AddCommand(a.taskNoteAddCmd(), a.taskNoteUpdateCmd(), a.taskNoteRemoveCmd())
	return cmd
}
func (a *app) taskNoteAddCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "add TASK-ID", Short: "Add a task note", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		id, err := s.AddNote(cmd.Context(), args[0], text)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), id)
		return nil
	}}
	cmd.Flags().StringVar(&text, "text", "", "text content")
	return cmd
}
func (a *app) taskNoteUpdateCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "update TASK-ID NOTE-ID", Short: "Update a task note", Args: validationArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskID(args[0]); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.UpdateNote(cmd.Context(), args[0], args[1], text)
	}}
	cmd.Flags().StringVar(&text, "text", "", "text content")
	return cmd
}
func (a *app) taskNoteRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "remove TASK-ID NOTE-ID", Short: "Remove a task note", Args: validationArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskID(args[0]); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.RemoveNote(cmd.Context(), args[0], args[1])
	}}
}

func (a *app) taskDependencyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "dependency", Short: "Manage prerequisite edges", Long: "The first task ID is the dependent target; each later ID is a prerequisite that must pass before it can start."}
	cmd.AddCommand(a.taskDependencyAddCmd(), a.taskDependencyRemoveCmd(), a.taskDependencyReasonCmd())
	return cmd
}
func parseReasonFor(vals []string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range vals {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return nil, store.ValidationError{Message: "--reason-for must be TASK-ID=TEXT"}
		}
		if _, ok := out[parts[0]]; ok {
			return nil, store.ValidationError{Message: "duplicate --reason-for " + parts[0]}
		}
		if err := requireCanonicalTaskID(parts[0]); err != nil {
			return nil, err
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}
func (a *app) taskDependencyAddCmd() *cobra.Command {
	var reason string
	var reasonForVals []string
	cmd := &cobra.Command{Use: "add TASK-ID DEP-ID...", Short: "Add prerequisites to a task", Example: "  relo task dependency add TASK-002 TASK-001 --reason \"TASK-001 must pass first\"", Args: validationArgs(cobra.MinimumNArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		reasonFor, err := parseReasonFor(reasonForVals)
		if err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.AddDependencies(cmd.Context(), args[0], args[1:], reason, reasonFor)
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "shared reason for every dependency; use --reason-for instead")
	cmd.Flags().StringArrayVar(&reasonForVals, "reason-for", nil, "per-dependency TASK-ID=TEXT alternative to --reason")
	return cmd
}
func (a *app) taskDependencyRemoveCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{Use: "remove TASK-ID DEP-ID...", Short: "Remove task prerequisites", Args: validationArgs(cobra.MinimumNArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.RemoveDependencies(cmd.Context(), args[0], args[1:], reason)
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "required reason for this mutation")
	return cmd
}
func (a *app) taskDependencyReasonCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "reason TASK-ID DEP-ID", Short: "Change a dependency reason", Args: validationArgs(cobra.ExactArgs(2)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.UpdateDependencyReason(cmd.Context(), args[0], args[1], text)
	}}
	cmd.Flags().StringVar(&text, "text", "", "text content")
	return cmd
}

func (a *app) taskReadyCmd() *cobra.Command {
	var details, jsonOut bool
	cmd := &cobra.Command{Use: "ready", Short: "List all pending tasks whose dependencies passed", Example: "  relo task ready --details", RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			err := store.ValidationError{Message: fmt.Sprintf("accepts 0 arg(s), received %d", len(args))}
			writeJSONError(cmd, jsonOut, "INVALID_ARGUMENT", err)
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		defer s.Close()
		snap, err := s.ReadyReadSnapshot(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		if jsonOut {
			return writeJSONOK(cmd, map[string]any{"tasks": toTasksDTO(snap.Tasks)})
		}
		for _, t := range snap.Tasks {
			if details {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\tpriority=%d\t%s\n", t.ID, t.Priority, t.Title)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), t.ID)
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&details, "details", false, "include task details")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON")
	return cmd
}

func (a *app) taskStartCmd() *cobra.Command {
	return &cobra.Command{Use: "start TASK-ID...", Short: "Atomically start ready pending tasks", Args: validationArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		started, err := s.StartTasks(cmd.Context(), args)
		if err != nil {
			return err
		}
		for _, id := range started {
			fmt.Fprintln(cmd.OutOrStdout(), id)
		}
		return nil
	}}
}

func (a *app) taskStopCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{Use: "stop TASK-ID...", Short: "Stop running tasks", Args: validationArgs(cobra.MinimumNArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.StopTasks(cmd.Context(), args, reason)
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "required reason for this mutation")
	return cmd
}

func (a *app) taskPassCmd() *cobra.Command {
	var summary string
	cmd := &cobra.Command{Use: "pass TASK-ID", Short: "Mark a running task passed", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.PassTask(cmd.Context(), args[0], summary)
	}}
	cmd.Flags().StringVar(&summary, "summary", "", "completion summary")
	return cmd
}

func (a *app) taskFailCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{Use: "fail TASK-ID", Short: "Mark a running task failed", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.FailTask(cmd.Context(), args[0], reason)
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "required reason for this mutation")
	return cmd
}

func (a *app) taskRetryCmd() *cobra.Command {
	return &cobra.Command{Use: "retry TASK-ID", Short: "Return a failed task to pending", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.RetryTask(cmd.Context(), args[0])
	}}
}

func (a *app) taskReopenCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{Use: "reopen TASK-ID", Short: "Return a passed task to pending", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskIDs(args); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.ReopenTask(cmd.Context(), args[0], reason)
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "required reason for this mutation")
	return cmd
}

func requireCanonicalTaskIDs(ids []string) error {
	for _, id := range ids {
		if err := requireCanonicalTaskID(id); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) validateCmd() *cobra.Command {
	return &cobra.Command{Use: "validate", Short: "Validate project consistency", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		r, err := s.Validate(cmd.Context())
		if err != nil {
			return err
		}
		for _, e := range r.Errors {
			fmt.Fprintln(cmd.ErrOrStderr(), "ERROR: "+e)
		}
		for _, w := range r.Warnings {
			fmt.Fprintln(cmd.ErrOrStderr(), "WARNING: "+w)
		}
		if len(r.Errors) > 0 {
			return store.ValidationError{Message: "validation failed"}
		}
		fmt.Fprintln(cmd.OutOrStdout(), "OK")
		return nil
	}}
}
