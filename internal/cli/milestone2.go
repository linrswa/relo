package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"relo/internal/store"

	"github.com/spf13/cobra"
)

type envelope struct {
	SchemaVersion string `json:"schemaVersion"`
	OK            bool   `json:"ok"`
	Data          any    `json:"data,omitempty"`
	Error         any    `json:"error,omitempty"`
}

func okEnvelope(data any) envelope {
	return envelope{SchemaVersion: "relo.output/v1", OK: true, Data: data}
}

func (a *app) taskAcceptanceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "acceptance"}
	cmd.AddCommand(a.taskAcceptanceAddCmd(), a.taskAcceptanceUpdateCmd(), a.taskAcceptanceRemoveCmd())
	return cmd
}
func (a *app) taskAcceptanceAddCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "add TASK-ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&text, "text", "", "")
	return cmd
}
func (a *app) taskAcceptanceUpdateCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "update TASK-ID AC-ID", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.UpdateAcceptance(cmd.Context(), args[0], args[1], text)
	}}
	cmd.Flags().StringVar(&text, "text", "", "")
	return cmd
}
func (a *app) taskAcceptanceRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "remove TASK-ID AC-ID", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.RemoveAcceptance(cmd.Context(), args[0], args[1])
	}}
}

func (a *app) taskNoteCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "note"}
	cmd.AddCommand(a.taskNoteAddCmd(), a.taskNoteUpdateCmd(), a.taskNoteRemoveCmd())
	return cmd
}
func (a *app) taskNoteAddCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "add TASK-ID", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&text, "text", "", "")
	return cmd
}
func (a *app) taskNoteUpdateCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "update TASK-ID NOTE-ID", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.UpdateNote(cmd.Context(), args[0], args[1], text)
	}}
	cmd.Flags().StringVar(&text, "text", "", "")
	return cmd
}
func (a *app) taskNoteRemoveCmd() *cobra.Command {
	return &cobra.Command{Use: "remove TASK-ID NOTE-ID", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.RemoveNote(cmd.Context(), args[0], args[1])
	}}
}

func (a *app) taskDependencyCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "dependency"}
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
		out[parts[0]] = parts[1]
	}
	return out, nil
}
func (a *app) taskDependencyAddCmd() *cobra.Command {
	var reason string
	var reasonForVals []string
	cmd := &cobra.Command{Use: "add TASK-ID DEP-ID...", Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&reason, "reason", "", "")
	cmd.Flags().StringArrayVar(&reasonForVals, "reason-for", nil, "")
	return cmd
}
func (a *app) taskDependencyRemoveCmd() *cobra.Command {
	var reason string
	cmd := &cobra.Command{Use: "remove TASK-ID DEP-ID...", Args: cobra.MinimumNArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.RemoveDependencies(cmd.Context(), args[0], args[1:], reason)
	}}
	cmd.Flags().StringVar(&reason, "reason", "", "")
	return cmd
}
func (a *app) taskDependencyReasonCmd() *cobra.Command {
	var text string
	cmd := &cobra.Command{Use: "reason TASK-ID DEP-ID", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		return s.UpdateDependencyReason(cmd.Context(), args[0], args[1], text)
	}}
	cmd.Flags().StringVar(&text, "text", "", "")
	return cmd
}

func (a *app) taskReadyCmd() *cobra.Command {
	var details, jsonOut bool
	cmd := &cobra.Command{Use: "ready", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, err := a.open(cmd.Context())
		if err != nil {
			return err
		}
		defer s.Close()
		tasks, err := s.ReadyTasks(cmd.Context())
		if err != nil {
			return err
		}
		if jsonOut {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(okEnvelope(map[string]any{"tasks": tasks}))
		}
		for _, t := range tasks {
			if details {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\tpriority=%d\t%s\n", t.ID, t.Priority, t.Title)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), t.ID)
			}
		}
		return nil
	}}
	cmd.Flags().BoolVar(&details, "details", false, "")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "")
	return cmd
}

func (a *app) validateCmd() *cobra.Command {
	return &cobra.Command{Use: "validate", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
		if len(r.Warnings) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "OK")
		}
		return nil
	}}
}
