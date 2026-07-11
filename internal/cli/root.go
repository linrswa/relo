package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"relo/internal/domain"
	"relo/internal/store"

	"github.com/spf13/cobra"
)

type app struct{}

func Execute() int {
	a := &app{}
	cmd := a.rootCmd()
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var ve store.ValidationError
		if errors.As(err, &ve) || errors.Is(err, sql.ErrNoRows) {
			return 2
		}
		return 1
	}
	return 0
}

func (a *app) rootCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "relo", SilenceUsage: true, SilenceErrors: true}
	cmd.AddCommand(a.initCmd(), a.projectCmd(), a.taskCmd(), a.validateCmd())
	return cmd
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
	cmd := &cobra.Command{Use: "init", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd := &cobra.Command{Use: "project"}
	cmd.AddCommand(&cobra.Command{Use: "show", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		p, err := s.Project(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Goal: %s\nPRD: %s\nPRD hash: %s\n", p.Goal, p.PRDPath, p.PRDHash)
		return nil
	}})
	return cmd
}

func (a *app) taskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task"}
	cmd.AddCommand(a.taskCreateCmd(), a.taskGetCmd(), a.taskListCmd(), a.taskUpdateCmd(), a.taskDeleteCmd(), a.taskAcceptanceCmd(), a.taskNoteCmd(), a.taskDependencyCmd(), a.taskReadyCmd())
	return cmd
}

func (a *app) taskCreateCmd() *cobra.Command {
	var title, objective, objectiveFile string
	var accepts []string
	var priority int
	cmd := &cobra.Command{Use: "create", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if objectiveFile != "" {
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
	cmd.Flags().StringVar(&title, "title", "", "")
	cmd.Flags().StringVar(&objective, "objective", "", "")
	cmd.Flags().StringVar(&objectiveFile, "objective-file", "", "")
	cmd.Flags().StringArrayVar(&accepts, "accept", nil, "")
	cmd.Flags().IntVar(&priority, "priority", 100, "")
	return cmd
}

func (a *app) taskGetCmd() *cobra.Command {
	var title string
	cmd := &cobra.Command{Use: "get [TASK-ID]", Args: func(cmd *cobra.Command, args []string) error {
		if title == "" && len(args) != 1 {
			return store.ValidationError{Message: "provide TASK-ID or --title"}
		}
		if title != "" && len(args) != 0 {
			return store.ValidationError{Message: "--title cannot be combined with TASK-ID"}
		}
		return nil
	}, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		var t *domain.Task
		if title != "" {
			tt, _, err := s.GetTaskByTitle(cmd.Context(), title)
			if err != nil {
				return err
			}
			t = tt
		} else {
			t, err = s.GetTask(cmd.Context(), args[0])
			if err != nil {
				return err
			}
		}
		p, _ := s.Project(cmd.Context())
		renderTask(cmd.OutOrStdout(), p, t)
		return nil
	}}
	cmd.Flags().StringVar(&title, "title", "", "")
	return cmd
}

func (a *app) taskListCmd() *cobra.Command {
	var status string
	cmd := &cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		tasks, err := s.ListTasks(cmd.Context(), status)
		if err != nil {
			return err
		}
		for _, t := range tasks {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\tpriority=%d\t%s\n", t.ID, t.Status, t.Priority, t.Title)
		}
		return nil
	}}
	cmd.Flags().StringVar(&status, "status", "", "")
	return cmd
}

func (a *app) taskUpdateCmd() *cobra.Command {
	var title, objective, objectiveFile, reason string
	var priority int
	cmd := &cobra.Command{Use: "update TASK-ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !strings.HasPrefix(args[0], "TASK-") {
			return store.ValidationError{Message: "mutation commands require canonical task ID"}
		}
		var tp, op *string
		var pp *int
		if cmd.Flags().Changed("title") {
			tp = &title
		}
		if objectiveFile != "" {
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
	cmd.Flags().StringVar(&title, "title", "", "")
	cmd.Flags().StringVar(&objective, "objective", "", "")
	cmd.Flags().StringVar(&objectiveFile, "objective-file", "", "")
	cmd.Flags().IntVar(&priority, "priority", 100, "")
	cmd.Flags().StringVar(&reason, "reason", "", "")
	return cmd
}

func (a *app) taskDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "delete TASK-ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !strings.HasPrefix(args[0], "TASK-") {
			return store.ValidationError{Message: "delete requires canonical task ID"}
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
