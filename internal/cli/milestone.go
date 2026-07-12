package cli

import (
	"fmt"
	"strings"

	"github.com/linrswa/relo/internal/domain"
	"github.com/linrswa/relo/internal/store"

	"github.com/spf13/cobra"
)

type milestoneTaskDTO struct {
	TaskID        string `json:"task_id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Priority      int    `json:"priority"`
	CreationOrder int    `json:"creation_order"`
	AttemptNumber int    `json:"attempt_number"`
	IsAnchor      bool   `json:"is_anchor"`
}
type recommendationDTO struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Position int    `json:"position"`
}
type milestoneDTO struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	Reason          string              `json:"reason"`
	StoredStatus    string              `json:"stored_status"`
	DisplayStatus   string              `json:"display_status"`
	Anchors         []milestoneTaskDTO  `json:"anchors"`
	Scope           []milestoneTaskDTO  `json:"scope"`
	Recommendations []recommendationDTO `json:"recommendations"`
	MarkSummary     *string             `json:"mark_summary"`
	Reference       *string             `json:"reference"`
	CreatedAt       string              `json:"created_at"`
	UpdatedAt       string              `json:"updated_at"`
	MarkedAt        *string             `json:"marked_at"`
}
type milestoneSummaryDTO struct {
	ID              string              `json:"id"`
	Title           string              `json:"title"`
	StoredStatus    string              `json:"stored_status"`
	DisplayStatus   string              `json:"display_status"`
	AnchorTaskIDs   []string            `json:"anchor_task_ids"`
	Recommendations []recommendationDTO `json:"recommendations"`
	CreatedAt       string              `json:"created_at"`
	MarkedAt        *string             `json:"marked_at"`
}

func milestoneDisplay(s store.MilestoneReadSnapshot) string {
	if s.ReadyToMark {
		return "ready_to_mark"
	}
	return s.Milestone.Status
}
func milestoneTask(t domain.Task, anchor bool) milestoneTaskDTO {
	return milestoneTaskDTO{t.ID, t.Title, t.Status, t.Priority, t.CreationOrder, t.AttemptCount, anchor}
}
func milestoneRecommendations(rs []domain.Recommendation) []recommendationDTO {
	out := make([]recommendationDTO, 0, len(rs))
	for _, r := range rs {
		out = append(out, recommendationDTO{r.ID, r.Text, r.Position})
	}
	return out
}
func toMilestoneDTO(s store.MilestoneReadSnapshot) milestoneDTO {
	anchors := make([]milestoneTaskDTO, 0, len(s.Anchors))
	anchorIDs := map[string]bool{}
	for _, t := range s.Anchors {
		anchorIDs[t.ID] = true
		anchors = append(anchors, milestoneTask(t, true))
	}
	scope := make([]milestoneTaskDTO, 0, len(s.Scope))
	for _, t := range s.Scope {
		scope = append(scope, milestoneTask(t, anchorIDs[t.ID]))
	}
	m := s.Milestone
	return milestoneDTO{m.ID, m.Title, m.Reason, m.Status, milestoneDisplay(s), anchors, scope, milestoneRecommendations(m.Recommendations), m.MarkSummary, m.Reference, m.CreatedAt, m.UpdatedAt, m.MarkedAt}
}
func toMilestoneSummaryDTO(s store.MilestoneReadSnapshot) milestoneSummaryDTO {
	ids := make([]string, 0, len(s.Anchors))
	for _, t := range s.Anchors {
		ids = append(ids, t.ID)
	}
	m := s.Milestone
	return milestoneSummaryDTO{m.ID, m.Title, m.Status, milestoneDisplay(s), ids, milestoneRecommendations(m.Recommendations), m.CreatedAt, m.MarkedAt}
}
func requireCanonicalMilestoneID(id string) error {
	if !domain.IsCanonicalMilestoneID(id) {
		return store.ValidationError{Message: "mutation commands require canonical milestone ID"}
	}
	return nil
}
func requireCanonicalRecommendationID(id string) error {
	if !domain.IsCanonicalRecommendationID(id) {
		return store.ValidationError{Message: "mutation commands require canonical recommendation ID"}
	}
	return nil
}

func (a *app) milestoneCmd() *cobra.Command {
	c := &cobra.Command{Use: "milestone", Short: "Record non-gating delivery milestones", Long: "Milestone readiness uses passed anchor tasks. Marking validates each anchor and its transitive dependency scope, but milestones never change task ready, dependency legality, or start behavior."}
	c.AddCommand(a.milestoneCreateCmd(), a.milestoneGetCmd(), a.milestoneListCmd(), a.milestoneReadyCmd(), a.milestoneUpdateCmd(), a.milestoneDeleteCmd(), a.milestoneMarkCmd(), a.milestoneAnchorCmd(), a.milestoneRecommendationCmd())
	return c
}
func (a *app) milestoneCreateCmd() *cobra.Command {
	var title, reason string
	var anchors, recs []string
	c := &cobra.Command{Use: "create", Short: "Create a planned milestone", Example: "  relo milestone create --title \"Release 1\" --reason \"Track release readiness\" --anchor TASK-001", Args: validationArgs(cobra.NoArgs), RunE: func(c *cobra.Command, args []string) error {
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		id, e := s.CreateMilestone(c.Context(), title, reason, anchors, recs)
		if e == nil {
			fmt.Fprintln(c.OutOrStdout(), id)
		}
		return e
	}}
	c.Flags().StringVar(&title, "title", "", "milestone title")
	c.Flags().StringVar(&reason, "reason", "", "why this milestone is recorded")
	c.Flags().StringArrayVar(&anchors, "anchor", nil, "anchor task in any existing state (repeatable)")
	c.Flags().StringArrayVar(&recs, "recommend", nil, "non-binding recommendation text (repeatable)")
	return c
}
func (a *app) milestoneGetCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{Use: "get MILESTONE-ID", Short: "Show a milestone and its validated scope", Example: "  relo milestone get MILESTONE-001", Long: "Readiness is based on passed anchors. Marking validates anchors plus transitive dependencies; milestones are informational and do not gate tasks.", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(c *cobra.Command, args []string) error {
		s, e := a.open(c.Context())
		if e != nil {
			writeJSONError(c, jsonOut, "", e)
			return e
		}
		defer s.Close()
		x, e := s.MilestoneReadSnapshot(c.Context(), args[0])
		if e != nil {
			writeJSONError(c, jsonOut, "", e)
			return e
		}
		if jsonOut {
			return writeJSONOK(c, map[string]any{"milestone": toMilestoneDTO(x)})
		}
		renderMilestone(c, x)
		return nil
	}}
	c.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON")
	return c
}
func (a *app) milestoneListCmd() *cobra.Command {
	var status string
	var jsonOut bool
	c := &cobra.Command{Use: "list", Short: "List milestones", Args: validationArgs(cobra.NoArgs), RunE: func(c *cobra.Command, args []string) error {
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		xs, e := s.ListMilestoneReadSnapshots(c.Context(), status)
		if e != nil {
			writeJSONError(c, jsonOut, "", e)
			return e
		}
		if jsonOut {
			out := make([]milestoneSummaryDTO, 0, len(xs))
			for _, x := range xs {
				out = append(out, toMilestoneSummaryDTO(x))
			}
			return writeJSONOK(c, map[string]any{"milestones": out})
		}
		for _, x := range xs {
			fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", x.Milestone.ID, x.Milestone.Status, x.Milestone.Title)
		}
		return nil
	}}
	c.Flags().StringVar(&status, "status", "", "filter by stored status")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON")
	return c
}
func (a *app) milestoneReadyCmd() *cobra.Command {
	var details, jsonOut bool
	c := &cobra.Command{Use: "ready", Short: "List milestones ready to mark", Example: "  relo milestone ready --details", Args: validationArgs(cobra.NoArgs), RunE: func(c *cobra.Command, args []string) error {
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		xs, e := s.ReadyMilestones(c.Context())
		if e != nil {
			writeJSONError(c, jsonOut, "", e)
			return e
		}
		if jsonOut {
			out := make([]milestoneSummaryDTO, 0, len(xs))
			for _, x := range xs {
				out = append(out, toMilestoneSummaryDTO(x))
			}
			return writeJSONOK(c, map[string]any{"milestones": out})
		}
		for _, x := range xs {
			if !details {
				fmt.Fprintln(c.OutOrStdout(), x.Milestone.ID)
				continue
			}
			ids := make([]string, 0, len(x.Anchors))
			for _, t := range x.Anchors {
				ids = append(ids, t.ID)
			}
			fmt.Fprintf(c.OutOrStdout(), "%s\tanchors=%s\t%s\n", x.Milestone.ID, strings.Join(ids, ","), x.Milestone.Title)
			for _, r := range x.Milestone.Recommendations {
				fmt.Fprintf(c.OutOrStdout(), "  recommend %s: %s\n", r.ID, r.Text)
			}
		}
		return nil
	}}
	c.Flags().BoolVar(&details, "details", false, "include anchors and recommendations")
	c.Flags().BoolVar(&jsonOut, "json", false, "emit relo.output/v1 JSON")
	return c
}
func (a *app) milestoneUpdateCmd() *cobra.Command {
	var title, reason string
	c := &cobra.Command{Use: "update MILESTONE-ID", Short: "Update a planned milestone", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(c *cobra.Command, args []string) error {
		if e := requireCanonicalMilestoneID(args[0]); e != nil {
			return e
		}
		var t, r *string
		if c.Flags().Changed("title") {
			t = &title
		}
		if c.Flags().Changed("reason") {
			r = &reason
		}
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		return s.UpdateMilestone(c.Context(), args[0], t, r)
	}}
	c.Flags().StringVar(&title, "title", "", "milestone title")
	c.Flags().StringVar(&reason, "reason", "", "why this milestone is recorded")
	return c
}
func (a *app) milestoneDeleteCmd() *cobra.Command {
	return &cobra.Command{Use: "delete MILESTONE-ID", Short: "Delete a planned milestone", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(c *cobra.Command, args []string) error {
		if e := requireCanonicalMilestoneID(args[0]); e != nil {
			return e
		}
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		return s.DeleteMilestone(c.Context(), args[0])
	}}
}
func (a *app) milestoneMarkCmd() *cobra.Command {
	var summary, reference string
	c := &cobra.Command{Use: "mark MILESTONE-ID", Short: "Mark a ready milestone", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(c *cobra.Command, args []string) error {
		if e := requireCanonicalMilestoneID(args[0]); e != nil {
			return e
		}
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		return s.MarkMilestone(c.Context(), args[0], summary, reference)
	}}
	c.Flags().StringVar(&summary, "summary", "", "completion or mark summary")
	c.Flags().StringVar(&reference, "reference", "", "optional mark reference")
	return c
}
func (a *app) milestoneAnchorCmd() *cobra.Command {
	c := &cobra.Command{Use: "anchor", Short: "Manage milestone anchors"}
	for _, add := range []bool{true, false} {
		add := add
		use := "remove MILESTONE-ID TASK-ID..."
		if add {
			use = "add MILESTONE-ID TASK-ID..."
		}
		c.AddCommand(&cobra.Command{Use: use, Short: "Change milestone anchors", Args: validationArgs(cobra.MinimumNArgs(2)), RunE: func(c *cobra.Command, args []string) error {
			if e := requireCanonicalMilestoneID(args[0]); e != nil {
				return e
			}
			if e := requireCanonicalTaskIDs(args[1:]); e != nil {
				return e
			}
			s, e := a.open(c.Context())
			if e != nil {
				return e
			}
			defer s.Close()
			if add {
				return s.AddMilestoneAnchors(c.Context(), args[0], args[1:])
			}
			return s.RemoveMilestoneAnchors(c.Context(), args[0], args[1:])
		}})
	}
	return c
}
func (a *app) milestoneRecommendationCmd() *cobra.Command {
	c := &cobra.Command{Use: "recommendation", Short: "Manage milestone recommendations"}
	var text string
	add := &cobra.Command{Use: "add MILESTONE-ID", Short: "Add a recommendation", Args: validationArgs(cobra.ExactArgs(1)), RunE: func(c *cobra.Command, args []string) error {
		if e := requireCanonicalMilestoneID(args[0]); e != nil {
			return e
		}
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		id, e := s.AddMilestoneRecommendation(c.Context(), args[0], text)
		if e == nil {
			fmt.Fprintln(c.OutOrStdout(), id)
		}
		return e
	}}
	add.Flags().StringVar(&text, "text", "", "text content")
	var updateText string
	update := &cobra.Command{Use: "update MILESTONE-ID REC-ID", Short: "Update a recommendation", Args: validationArgs(cobra.ExactArgs(2)), RunE: func(c *cobra.Command, args []string) error {
		if e := requireCanonicalMilestoneID(args[0]); e != nil {
			return e
		}
		if e := requireCanonicalRecommendationID(args[1]); e != nil {
			return e
		}
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		return s.UpdateMilestoneRecommendation(c.Context(), args[0], args[1], updateText)
	}}
	update.Flags().StringVar(&updateText, "text", "", "replacement recommendation text")
	remove := &cobra.Command{Use: "remove MILESTONE-ID REC-ID", Short: "Remove a recommendation", Args: validationArgs(cobra.ExactArgs(2)), RunE: func(c *cobra.Command, args []string) error {
		if e := requireCanonicalMilestoneID(args[0]); e != nil {
			return e
		}
		if e := requireCanonicalRecommendationID(args[1]); e != nil {
			return e
		}
		s, e := a.open(c.Context())
		if e != nil {
			return e
		}
		defer s.Close()
		return s.RemoveMilestoneRecommendation(c.Context(), args[0], args[1])
	}}
	c.AddCommand(add, update, remove)
	return c
}
func renderMilestone(c *cobra.Command, s store.MilestoneReadSnapshot) {
	m := s.Milestone
	fmt.Fprintf(c.OutOrStdout(), "# %s %s\n\nStatus: %s\nReason: %s\n\nReadiness: passed anchors. Marking validates anchor and transitive dependency scope. Milestones do not gate task readiness or start.\n\n## Anchors\n", m.ID, m.Title, milestoneDisplay(s), m.Reason)
	for _, t := range s.Anchors {
		fmt.Fprintf(c.OutOrStdout(), "- %s %s  %s\n", t.ID, t.Status, t.Title)
	}
	fmt.Fprintln(c.OutOrStdout(), "\n## Scope")
	for _, t := range s.Scope {
		fmt.Fprintf(c.OutOrStdout(), "- %s %s  %s\n", t.ID, t.Status, t.Title)
	}
	fmt.Fprintln(c.OutOrStdout(), "\n## Recommendations")
	for _, r := range m.Recommendations {
		fmt.Fprintf(c.OutOrStdout(), "- %s %s\n", r.ID, r.Text)
	}
	summary, ref := "none", "none"
	if m.MarkSummary != nil {
		summary = *m.MarkSummary
	}
	if m.Reference != nil {
		ref = *m.Reference
	}
	fmt.Fprintf(c.OutOrStdout(), "\nMark summary: %s\nReference: %s\n", summary, ref)
}
