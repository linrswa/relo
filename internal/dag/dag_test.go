package dag

import (
	"testing"

	"relo/internal/domain"
)

func TestReadyBlockedLayersAndCycle(t *testing.T) {
	g := Graph{Tasks: map[string]Task{
		"TASK-001": {ID: "TASK-001", Priority: 2, CreationOrder: 1, Status: domain.StatusPassed},
		"TASK-002": {ID: "TASK-002", Priority: 1, CreationOrder: 2, Status: domain.StatusPending},
		"TASK-003": {ID: "TASK-003", Priority: 1, CreationOrder: 3, Status: domain.StatusPending},
	}, Deps: map[string][]string{
		"TASK-002": {"TASK-001"},
		"TASK-003": {"TASK-002"},
	}}
	ready := g.Ready()
	if len(ready) != 1 || ready[0].ID != "TASK-002" {
		t.Fatalf("ready = %#v", ready)
	}
	blocked := g.Blocked()
	if len(blocked["TASK-003"]) != 1 || blocked["TASK-003"][0].ID != "TASK-002" {
		t.Fatalf("blocked = %#v", blocked)
	}
	layers, err := g.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if layers["TASK-001"] != 0 || layers["TASK-002"] != 1 || layers["TASK-003"] != 2 {
		t.Fatalf("layers = %#v", layers)
	}
	g.Deps["TASK-001"] = []string{"TASK-003"}
	if c := g.Cycle(); len(c) == 0 {
		t.Fatal("expected cycle")
	}
}

func TestComparatorIsDeterministic(t *testing.T) {
	tasks := []Task{{ID: "TASK-003", Priority: 1, CreationOrder: 2}, {ID: "TASK-001", Priority: 1, CreationOrder: 1}, {ID: "TASK-002", Priority: 1, CreationOrder: 1}}
	Sort(tasks)
	got := []string{tasks[0].ID, tasks[1].ID, tasks[2].ID}
	want := []string{"TASK-001", "TASK-002", "TASK-003"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted IDs = %v", got)
		}
	}
}
