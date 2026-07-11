package dag

import (
	"fmt"
	"sort"

	"relo/internal/domain"
)

type Task struct {
	ID            string
	Priority      int
	CreationOrder int
	Status        string
}

type Graph struct {
	Tasks map[string]Task
	Deps  map[string][]string // task -> dependencies
}

func Less(a, b Task) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if a.CreationOrder != b.CreationOrder {
		return a.CreationOrder < b.CreationOrder
	}
	return a.ID < b.ID
}

func Sort(tasks []Task) { sort.Slice(tasks, func(i, j int) bool { return Less(tasks[i], tasks[j]) }) }

func (g Graph) Cycle() []string {
	seen := map[string]int{}
	var stack []string
	var visit func(string) []string
	visit = func(id string) []string {
		if seen[id] == 1 {
			for i, v := range stack {
				if v == id {
					return append(append([]string{}, stack[i:]...), id)
				}
			}
			return []string{id, id}
		}
		if seen[id] == 2 {
			return nil
		}
		seen[id] = 1
		stack = append(stack, id)
		deps := append([]string{}, g.Deps[id]...)
		sort.Slice(deps, func(i, j int) bool { return Less(g.Tasks[deps[i]], g.Tasks[deps[j]]) })
		for _, dep := range deps {
			if c := visit(dep); len(c) > 0 {
				return c
			}
		}
		stack = stack[:len(stack)-1]
		seen[id] = 2
		return nil
	}
	ids := make([]Task, 0, len(g.Tasks))
	for _, t := range g.Tasks {
		ids = append(ids, t)
	}
	Sort(ids)
	for _, t := range ids {
		if c := visit(t.ID); len(c) > 0 {
			return c
		}
	}
	return nil
}

func (g Graph) Layers() (map[string]int, error) {
	if c := g.Cycle(); len(c) > 0 {
		return nil, fmt.Errorf("dependency graph has cycle: %v", c)
	}
	memo := map[string]int{}
	var layer func(string) int
	layer = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		max := -1
		for _, dep := range g.Deps[id] {
			if l := layer(dep); l > max {
				max = l
			}
		}
		memo[id] = max + 1
		return memo[id]
	}
	for id := range g.Tasks {
		layer(id)
	}
	return memo, nil
}

func (g Graph) TopologicalLayers() ([][]Task, error) {
	layers, err := g.Layers()
	if err != nil {
		return nil, err
	}
	var max int
	for _, l := range layers {
		if l > max {
			max = l
		}
	}
	out := make([][]Task, max+1)
	for id, l := range layers {
		out[l] = append(out[l], g.Tasks[id])
	}
	for i := range out {
		Sort(out[i])
	}
	return out, nil
}

func (g Graph) Ready() []Task {
	var out []Task
	for id, t := range g.Tasks {
		if t.Status != domain.StatusPending {
			continue
		}
		ready := true
		for _, dep := range g.Deps[id] {
			if g.Tasks[dep].Status != domain.StatusPassed {
				ready = false
				break
			}
		}
		if ready {
			out = append(out, t)
		}
	}
	Sort(out)
	return out
}

func (g Graph) Unmet(id string) []Task {
	var out []Task
	for _, dep := range g.Deps[id] {
		if g.Tasks[dep].Status != domain.StatusPassed {
			out = append(out, g.Tasks[dep])
		}
	}
	Sort(out)
	return out
}

func (g Graph) Blocked() map[string][]Task {
	out := map[string][]Task{}
	for id, t := range g.Tasks {
		if t.Status != domain.StatusPending {
			continue
		}
		if unmet := g.Unmet(id); len(unmet) > 0 {
			out[id] = unmet
		}
	}
	return out
}
