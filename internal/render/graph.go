package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/linrswa/relo/internal/dag"
	"github.com/linrswa/relo/internal/domain"
)

type GraphPayload struct {
	ProjectGoal string      `json:"project_goal"`
	Nodes       []JSONNode  `json:"nodes"`
	Edges       []JSONEdge  `json:"edges"`
	Summary     JSONSummary `json:"summary"`
}

type JSONNode struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Status        string   `json:"status"`
	DisplayStatus string   `json:"display_status"`
	Symbol        string   `json:"symbol"`
	Priority      int      `json:"priority"`
	CreationOrder int      `json:"creation_order"`
	Dependencies  []string `json:"dependencies"`
}

type JSONEdge struct {
	TaskID       string `json:"task_id"`
	DependencyID string `json:"dependency_id"`
}

type JSONSummary struct {
	Running []string `json:"running"`
	Ready   []string `json:"ready"`
	Blocked []string `json:"blocked"`
	Failed  []string `json:"failed"`
}

func Tree(goal string, g dag.Graph) (string, error) {
	if c := g.Cycle(); len(c) > 0 {
		return "", fmt.Errorf("dependency graph has cycle: %s", strings.Join(c, " -> "))
	}
	parents := parents(g)
	roots := roots(g)
	var b bytes.Buffer
	fmt.Fprintf(&b, "🎯 Goal: %s\n", goal)
	if len(roots) > 0 {
		b.WriteString("│\n")
	}
	seen := map[string]bool{}
	for i, t := range roots {
		renderNode(&b, g, parents, seen, t.ID, "", i == len(roots)-1, "")
	}
	b.WriteString("\n")
	b.WriteString(Status(g))
	return b.String(), nil
}

func Status(g dag.Graph) string {
	s := summary(g)
	var b bytes.Buffer
	fmt.Fprintf(&b, "Running: %s\n", idsOrNone(s.Running))
	fmt.Fprintf(&b, "Ready:   %s\n", idsOrNone(s.Ready))
	fmt.Fprintf(&b, "Blocked: %s\n", idsOrNone(s.Blocked))
	fmt.Fprintf(&b, "Failed:  %s\n", idsOrNone(s.Failed))
	return b.String()
}

func JSON(goal string, g dag.Graph) ([]byte, error) {
	if c := g.Cycle(); len(c) > 0 {
		return nil, fmt.Errorf("dependency graph has cycle: %s", strings.Join(c, " -> "))
	}
	payload := Payload(goal, g)
	return json.MarshalIndent(payload, "", "  ")
}

func Payload(goal string, g dag.Graph) GraphPayload {
	tasks := sortedTasks(g)
	nodes := make([]JSONNode, 0, len(tasks))
	for _, t := range tasks {
		deps := sortedDeps(g, t.ID)
		depIDs := make([]string, 0, len(deps))
		for _, dep := range deps {
			depIDs = append(depIDs, dep.ID)
		}
		sym, display := symbolStatus(g, t.ID)
		nodes = append(nodes, JSONNode{ID: t.ID, Title: t.Title, Status: t.Status, DisplayStatus: display, Symbol: sym, Priority: t.Priority, CreationOrder: t.CreationOrder, Dependencies: depIDs})
	}
	edges := make([]JSONEdge, 0)
	for _, t := range tasks {
		for _, dep := range sortedDeps(g, t.ID) {
			edges = append(edges, JSONEdge{TaskID: t.ID, DependencyID: dep.ID})
		}
	}
	return GraphPayload{ProjectGoal: goal, Nodes: nodes, Edges: edges, Summary: Summary(g)}
}

func Summary(g dag.Graph) JSONSummary {
	s := summary(g)
	return JSONSummary{Running: ids(s.Running), Ready: ids(s.Ready), Blocked: ids(s.Blocked), Failed: ids(s.Failed)}
}

func renderNode(b *bytes.Buffer, g dag.Graph, ps map[string][]dag.Task, seen map[string]bool, id, parentID string, last bool, prefix string) {
	connector := "├── "
	nextPrefix := prefix + "│   "
	if last {
		connector = "└── "
		nextPrefix = prefix + "    "
	}
	if seen[id] {
		fmt.Fprintf(b, "%s%s↗ %s  already shown\n", prefix, connector, id)
		return
	}
	seen[id] = true
	t := g.Tasks[id]
	sym, _ := symbolStatus(g, id)
	fmt.Fprintf(b, "%s%s%s %s  %s\n", prefix, connector, sym, t.ID, t.Title)

	also := sortedDeps(g, id)
	filtered := also[:0]
	for _, dep := range also {
		if dep.ID != parentID {
			filtered = append(filtered, dep)
		}
	}
	children := ps[id]
	total := len(filtered) + len(children)
	idx := 0
	for _, dep := range filtered {
		idx++
		c := "├── "
		if idx == total {
			c = "└── "
		}
		fmt.Fprintf(b, "%s%salso requires: %s\n", nextPrefix, c, dep.ID)
	}
	for i, child := range children {
		idx++
		renderNode(b, g, ps, seen, child.ID, id, i == len(children)-1 && idx == total, nextPrefix)
	}
}

func parents(g dag.Graph) map[string][]dag.Task {
	out := map[string][]dag.Task{}
	for id, deps := range g.Deps {
		for _, dep := range deps {
			out[dep] = append(out[dep], g.Tasks[id])
		}
	}
	for id := range out {
		dag.Sort(out[id])
	}
	return out
}

func roots(g dag.Graph) []dag.Task {
	var out []dag.Task
	for id, t := range g.Tasks {
		if len(g.Deps[id]) == 0 {
			out = append(out, t)
		}
	}
	dag.Sort(out)
	return out
}

func sortedTasks(g dag.Graph) []dag.Task {
	out := make([]dag.Task, 0, len(g.Tasks))
	for _, t := range g.Tasks {
		out = append(out, t)
	}
	dag.Sort(out)
	return out
}

func sortedDeps(g dag.Graph, id string) []dag.Task {
	out := make([]dag.Task, 0, len(g.Deps[id]))
	for _, dep := range g.Deps[id] {
		if t, ok := g.Tasks[dep]; ok {
			out = append(out, t)
		}
	}
	dag.Sort(out)
	return out
}

type buckets struct{ Running, Ready, Blocked, Failed []dag.Task }

func summary(g dag.Graph) buckets {
	var b buckets
	for _, t := range g.Tasks {
		switch t.Status {
		case domain.StatusRunning:
			b.Running = append(b.Running, t)
		case domain.StatusFailed:
			b.Failed = append(b.Failed, t)
		case domain.StatusPending:
			if isReady(g, t.ID) {
				b.Ready = append(b.Ready, t)
			} else {
				b.Blocked = append(b.Blocked, t)
			}
		}
	}
	dag.Sort(b.Running)
	dag.Sort(b.Ready)
	dag.Sort(b.Blocked)
	dag.Sort(b.Failed)
	return b
}

func isReady(g dag.Graph, id string) bool {
	for _, dep := range g.Deps[id] {
		if g.Tasks[dep].Status != domain.StatusPassed {
			return false
		}
	}
	return true
}

func symbolStatus(g dag.Graph, id string) (string, string) {
	switch g.Tasks[id].Status {
	case domain.StatusPassed:
		return "✅", "passed"
	case domain.StatusRunning:
		return "🟡", "running"
	case domain.StatusFailed:
		return "❌", "failed"
	default:
		if isReady(g, id) {
			return "🔵", "ready"
		}
		return "⏸", "blocked"
	}
}

func ids(tasks []dag.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func idsOrNone(tasks []dag.Task) string {
	if len(tasks) == 0 {
		return "none"
	}
	return strings.Join(ids(tasks), ", ")
}
