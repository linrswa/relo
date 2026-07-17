package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linrswa/relo/internal/domain"
	"github.com/linrswa/relo/internal/render"
	"github.com/linrswa/relo/internal/store"

	"github.com/spf13/cobra"
)

// Version defaults to dev and may be replaced at build time with -ldflags.
var Version = "dev"

type app struct{}

// statusSummaryDTO is deliberately separate from render.JSONSummary: graph
// JSON is a stable task-DAG contract and must not gain milestone fields.
type statusSummaryDTO struct {
	Running               []string `json:"running"`
	Ready                 []string `json:"ready"`
	Blocked               []string `json:"blocked"`
	Failed                []string `json:"failed"`
	MilestonesReadyToMark []string `json:"milestones_ready_to_mark"`
}

func Execute() int {
	jsonErrorWritten = false
	a := &app{}
	cmd := a.rootCmd()
	if _, _, err := cmd.Find(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if err := cmd.Execute(); err != nil {
		jsonMode := argsRequestJSON(os.Args[1:])
		if !jsonErrorWritten && jsonMode {
			_ = json.NewEncoder(os.Stdout).Encode(errorEnvelope(executeJSONErrorCode(err), err.Error()))
		}
		if !jsonMode {
			fmt.Fprintln(os.Stderr, err)
		}
		var ve store.ValidationError
		if errors.As(err, &ve) || errors.Is(err, sql.ErrNoRows) {
			return 2
		}
		return 1
	}
	return 0
}

func executeJSONErrorCode(err error) string {
	var ve store.ValidationError
	if errors.As(err, &ve) {
		return "INVALID_ARGUMENT"
	}
	return jsonErrorCode(err)
}

func argsRequestJSON(args []string) bool {
	for i, arg := range args {
		if arg == "--json" || arg == "--json=true" || arg == "--format=json" {
			return true
		}
		if arg == "--format" && i+1 < len(args) && args[i+1] == "json" {
			return true
		}
	}
	return false
}

func (a *app) rootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "relo",
		Short:        "Manage an agent-owned task dependency graph",
		Long:         "relo manages tasks, dependencies, runtime state, and milestone records. Run `relo --help` to discover commands; projects are found from the current directory or any parent containing .relo/relo.db.",
		Example:      "  relo init --prd docs/prd.md --goal \"Ship the feature\"\n  relo task ready\n  relo task get TASK-001",
		SilenceUsage: true, SilenceErrors: true,
	}
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return store.ValidationError{Message: err.Error()}
	})
	cmd.AddCommand(a.initCmd(), a.projectCmd(), a.taskCmd(), a.milestoneCmd(), a.validateCmd(), a.graphCmd(), a.statusCmd(), a.versionCmd())
	return cmd
}

func (a *app) versionCmd() *cobra.Command {
	return &cobra.Command{Use: "version", Short: "Print the relo version", Args: validationArgs(cobra.NoArgs), Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintln(cmd.OutOrStdout(), Version)
	}}
}

func validationArgs(fn cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return store.ValidationError{Message: err.Error()}
		}
		return nil
	}
}

func requireCanonicalTaskID(id string) error {
	if !isCanonicalTaskID(id) {
		return store.ValidationError{Message: "mutation commands require canonical task ID"}
	}
	return nil
}

func isCanonicalTaskID(id string) bool {
	const prefix = "TASK-"
	if !strings.HasPrefix(id, prefix) || len(id) < len(prefix)+3 {
		return false
	}
	for _, r := range id[len(prefix):] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (a *app) open(ctx context.Context) (*store.Store, error) {
	root, err := store.FindRoot(".")
	if err != nil {
		return nil, err
	}
	s, err := store.Open(store.DBPath(root))
	if err != nil {
		return nil, err
	}
	if err = s.Migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (a *app) initCmd() *cobra.Command {
	var prd, goal string
	cmd := &cobra.Command{Use: "init", Short: "Initialize a relo project", Example: "  relo init --prd docs/prd.md --goal \"Ship the feature\"", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		p, err := store.InitProject(cmd.Context(), ".", prd, goal)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Initialized relo project\nGoal: %s\nPRD: %s\n", p.Goal, p.PRDPath)
		return nil
	}}
	cmd.Flags().StringVar(&prd, "prd", "", "PRD path")
	cmd.Flags().StringVar(&goal, "goal", "", "project goal")
	return cmd
}

func (a *app) projectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "project", Short: "Inspect and update project metadata", Long: "Project metadata records the goal and PRD path/hash."}
	cmd.AddCommand(a.projectShowCmd(), a.projectUpdateCmd(), a.projectRefreshPRDCmd(), a.projectRemoveCmd())
	return cmd
}

func (a *app) projectShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{Use: "show", Short: "Show the project goal and PRD metadata", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		defer s.Close()
		p, err := s.Project(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		if jsonOut {
			return writeJSONOK(cmd, map[string]any{"project": projectShowDTO{Goal: p.Goal, PRDPath: p.PRDPath, PRDHash: p.PRDHash}})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Goal: %s\nPRD: %s\nPRD hash: %s\n", p.Goal, p.PRDPath, p.PRDHash)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON")
	return cmd
}

func (a *app) projectUpdateCmd() *cobra.Command {
	var goal, prd string
	cmd := &cobra.Command{Use: "update", Short: "Update project metadata", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		var goalPtr, prdPtr *string
		if cmd.Flags().Changed("goal") {
			if strings.TrimSpace(goal) == "" {
				return store.ValidationError{Message: "--goal must not be empty"}
			}
			goalPtr = &goal
		}
		if cmd.Flags().Changed("prd") {
			if strings.TrimSpace(prd) == "" {
				return store.ValidationError{Message: "--prd must not be empty"}
			}
			prdPtr = &prd
		}
		if goalPtr == nil && prdPtr == nil {
			return store.ValidationError{Message: "at least one of --goal or --prd is required"}
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		p, err := s.UpdateProject(cmd.Context(), goalPtr, prdPtr)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated relo project\nGoal: %s\nPRD: %s\nPRD hash: %s\n", p.Goal, p.PRDPath, p.PRDHash)
		return nil
	}}
	cmd.Flags().StringVar(&goal, "goal", "", "project goal")
	cmd.Flags().StringVar(&prd, "prd", "", "PRD path")
	return cmd
}

func (a *app) projectRefreshPRDCmd() *cobra.Command {
	return &cobra.Command{Use: "refresh-prd", Short: "Refresh the stored PRD hash", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		oldHash, newHash, p, err := s.RefreshPRDHash(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Refreshed PRD hash\nPRD: %s\nOld hash: %s\nNew hash: %s\n", p.PRDPath, oldHash, newHash)
		return nil
	}}
}

func (a *app) projectRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{Use: "remove", Short: "Remove relo project state", Long: "Permanently remove the relo-managed database from the current project. Run this command from the project root. The PRD, source files, and unknown files under .relo are preserved.", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		if !force {
			return store.ValidationError{Message: "--force is required to permanently remove relo project state"}
		}
		root, err := store.FindRoot(".")
		if err != nil {
			return err
		}
		cwd, err := filepath.Abs(".")
		if err != nil {
			return err
		}
		if filepath.Clean(cwd) != filepath.Clean(root) {
			return store.ValidationError{Message: fmt.Sprintf("project remove must run from the project root: %s", root)}
		}
		result, err := store.RemoveProjectState(root)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed relo project state at %s\n", result.Root)
		if !result.MetadataDirRemoved {
			fmt.Fprintf(cmd.OutOrStdout(), "Preserved non-empty metadata directory: %s\n", filepath.Join(result.Root, ".relo"))
		}
		return nil
	}}
	cmd.Flags().BoolVar(&force, "force", false, "confirm permanent removal of relo project state")
	return cmd
}

func (a *app) graphCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{Use: "graph", Short: "Render the task dependency graph", RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut := format == "json"
		if len(args) != 0 {
			err := store.ValidationError{Message: fmt.Sprintf("accepts 0 arg(s), received %d", len(args))}
			writeJSONError(cmd, jsonOut, "INVALID_ARGUMENT", err)
			return err
		}
		if format != "tree" && format != "json" {
			err := store.ValidationError{Message: "--format must be tree or json"}
			writeJSONError(cmd, jsonOut, "INVALID_ARGUMENT", err)
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		defer s.Close()
		p, err := s.Project(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		g, err := s.Graph(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		if c := g.Cycle(); len(c) > 0 {
			err := store.ValidationError{Message: "dependency graph has cycle: " + strings.Join(c, " -> ")}
			writeJSONError(cmd, jsonOut, "VALIDATION_ERROR", err)
			return err
		}
		if jsonOut {
			return writeJSONOK(cmd, render.Payload(p.Goal, g))
		}
		out, err := render.Tree(p.Goal, g)
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), out)
		return nil
	}}
	cmd.Flags().StringVar(&format, "format", "tree", "tree or json output format")
	return cmd
}

func (a *app) statusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{Use: "status", Short: "Show task and milestone status", RunE: func(cmd *cobra.Command, args []string) error {
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
		snapshot, err := s.StatusReadSnapshot(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		g := snapshot.Graph
		if c := g.Cycle(); len(c) > 0 {
			err := store.ValidationError{Message: "dependency graph has cycle: " + strings.Join(c, " -> ")}
			writeJSONError(cmd, jsonOut, "VALIDATION_ERROR", err)
			return err
		}
		ids := make([]string, 0, len(snapshot.Milestones))
		for _, milestone := range snapshot.Milestones {
			ids = append(ids, milestone.Milestone.ID)
		}
		graphSummary := render.Summary(g)
		summary := statusSummaryDTO{Running: graphSummary.Running, Ready: graphSummary.Ready, Blocked: graphSummary.Blocked, Failed: graphSummary.Failed, MilestonesReadyToMark: ids}
		if jsonOut {
			return writeJSONOK(cmd, map[string]any{"summary": summary})
		}
		milestoneText := strings.Join(ids, ", ")
		if milestoneText == "" {
			milestoneText = "none"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s\nMilestones ready to mark: %s\n", render.Status(g), milestoneText)
		return nil
	}}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON")
	return cmd
}

func (a *app) taskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Create, inspect, and run tasks", Long: "Tasks are mutable only while pending or failed. Stop running tasks before editing; reopen passed tasks before editing."}
	cmd.AddCommand(a.taskCreateCmd(), a.taskGetCmd(), a.taskListCmd(), a.taskUpdateCmd(), a.taskDeleteCmd(), a.taskAcceptanceCmd(), a.taskNoteCmd(), a.taskDependencyCmd(), a.taskReadyCmd(), a.taskStartCmd(), a.taskStopCmd(), a.taskPassCmd(), a.taskFailCmd(), a.taskRetryCmd(), a.taskReopenCmd())
	return cmd
}

func (a *app) taskCreateCmd() *cobra.Command {
	var title, objective, objectiveFile string
	var accepts []string
	var priority int
	cmd := &cobra.Command{Use: "create", Short: "Create a pending task", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("objective") == cmd.Flags().Changed("objective-file") {
			return store.ValidationError{Message: "provide exactly one of --objective or --objective-file"}
		}
		if cmd.Flags().Changed("objective-file") {
			b, err := os.ReadFile(objectiveFile)
			if err != nil {
				return err
			}
			objective = string(b)
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		id, err := s.CreateTask(cmd.Context(), title, objective, accepts, priority)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), id)
		return nil
	}}
	cmd.Flags().StringVar(&title, "title", "", "task title")
	cmd.Flags().StringVar(&objective, "objective", "", "task objective (exactly one objective source is required)")
	cmd.Flags().StringVar(&objectiveFile, "objective-file", "", "read the task objective from this file")
	cmd.Flags().StringArrayVar(&accepts, "accept", nil, "acceptance criterion (repeat at least once)")
	cmd.Flags().IntVar(&priority, "priority", 100, "priority; lower values sort first")
	return cmd
}

func (a *app) taskGetCmd() *cobra.Command {
	var title string
	var jsonOut bool
	cmd := &cobra.Command{Use: "get [TASK-ID]", Short: "Show one task in detail", Example: "  relo task get TASK-001\n  relo task get --title \"Design API\"", RunE: func(cmd *cobra.Command, args []string) error {
		if title == "" && len(args) != 1 {
			err := store.ValidationError{Message: "provide TASK-ID or --title"}
			writeJSONError(cmd, jsonOut, "INVALID_ARGUMENT", err)
			return err
		}
		if title != "" && len(args) != 0 {
			err := store.ValidationError{Message: "--title cannot be combined with TASK-ID"}
			writeJSONError(cmd, jsonOut, "INVALID_ARGUMENT", err)
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		defer s.Close()
		var snap store.TaskReadSnapshot
		if title != "" {
			snap, err = s.TaskReadSnapshotByTitle(cmd.Context(), title)
		} else {
			snap, err = s.TaskReadSnapshot(cmd.Context(), args[0])
		}
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		if jsonOut {
			return writeJSONOK(cmd, toTaskGetDTO(snap))
		}
		renderTask(cmd.OutOrStdout(), &snap.Project, &snap.Task)
		return nil
	}}
	cmd.Flags().StringVar(&title, "title", "", "exact task title for read-only lookup")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON")
	return cmd
}

func (a *app) taskListCmd() *cobra.Command {
	var status string
	var jsonOut bool
	cmd := &cobra.Command{Use: "list", Short: "List task summaries", Args: validationArgs(cobra.NoArgs), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		defer s.Close()
		tasks, err := s.ListTasks(cmd.Context(), status)
		if err != nil {
			writeJSONError(cmd, jsonOut, "", err)
			return err
		}
		if jsonOut {
			return writeJSONOK(cmd, map[string]any{"tasks": toTaskSummariesDTO(tasks)})
		}
		for _, t := range tasks {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\tpriority=%d\t%s\n", t.ID, t.Status, t.Priority, t.Title)
		}
		return nil
	}}
	cmd.Flags().StringVar(&status, "status", "", "stored status: pending, running, passed, or failed")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON task summaries")
	return cmd
}

func (a *app) taskUpdateCmd() *cobra.Command {
	var title, objective, objectiveFile, reason string
	var priority int
	cmd := &cobra.Command{Use: "update TASK-ID", Short: "Update a pending or failed task", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskID(args[0]); err != nil {
			return err
		}
		var tp, op *string
		var pp *int
		if cmd.Flags().Changed("title") {
			tp = &title
		}
		if cmd.Flags().Changed("objective") && cmd.Flags().Changed("objective-file") {
			return store.ValidationError{Message: "--objective and --objective-file cannot be combined"}
		}
		if cmd.Flags().Changed("objective-file") {
			b, err := os.ReadFile(objectiveFile)
			if err != nil {
				return err
			}
			objective = string(b)
			op = &objective
		} else if cmd.Flags().Changed("objective") {
			op = &objective
		}
		if cmd.Flags().Changed("priority") {
			pp = &priority
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.UpdateTask(cmd.Context(), args[0], tp, op, pp, reason)
	}}
	cmd.Flags().StringVar(&title, "title", "", "new task title")
	cmd.Flags().StringVar(&objective, "objective", "", "new task objective")
	cmd.Flags().StringVar(&objectiveFile, "objective-file", "", "read new objective from this file")
	cmd.Flags().IntVar(&priority, "priority", 100, "new priority; requires --reason")
	cmd.Flags().StringVar(&reason, "reason", "", "reason required when changing priority")
	return cmd
}

func (a *app) taskDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "delete TASK-ID", Short: "Delete a pending or failed task", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireCanonicalTaskID(args[0]); err != nil {
			return err
		}
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.DeleteTask(cmd.Context(), args[0])
	}}
	return cmd
}

type writer interface{ Write([]byte) (int, error) }

func renderTask(w writer, p *domain.Project, t *domain.Task) {
	if p != nil {
		fmt.Fprintf(w, "# %s %s\n\nProject goal: %s\nSource PRD: %s\n\n", t.ID, t.Title, p.Goal, p.PRDPath)
	} else {
		fmt.Fprintf(w, "# %s %s\n\n", t.ID, t.Title)
	}
	fmt.Fprintf(w, "Status: %s\nPriority: %d\n\n## Objective\n%s\n\n## Acceptance Criteria\n", t.Status, t.Priority, strings.TrimSpace(t.Objective))
	for _, ac := range t.AcceptanceCriteria {
		fmt.Fprintf(w, "- [ ] %s %s\n", ac.ID, ac.Text)
	}
	fmt.Fprintln(w, "\n## Dependencies")
	if len(t.Dependencies) == 0 {
		fmt.Fprintln(w, "None")
	} else {
		for _, d := range t.Dependencies {
			fmt.Fprintf(w, "- %s (%s): %s\n", d.DependencyID, d.Status, d.Reason)
		}
	}
	fmt.Fprintln(w, "\n## Notes")
	if len(t.Notes) == 0 {
		fmt.Fprintln(w, "None")
	} else {
		for _, n := range t.Notes {
			fmt.Fprintf(w, "- %s %s\n", n.ID, n.Text)
		}
	}
}
