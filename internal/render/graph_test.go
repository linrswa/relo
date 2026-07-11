package render

import (
	"encoding/json"
	"strings"
	"testing"

	"relo/internal/dag"
	"relo/internal/domain"
)

func fixtureGraph() dag.Graph {
	return dag.Graph{
		Tasks: map[string]dag.Task{
			"TASK-001": {ID: "TASK-001", Title: "Add priority model", Priority: 1, CreationOrder: 1, Status: domain.StatusPassed},
			"TASK-002": {ID: "TASK-002", Title: "Prepare UI primitives", Priority: 2, CreationOrder: 2, Status: domain.StatusRunning},
			"TASK-003": {ID: "TASK-003", Title: "Add priority API", Priority: 3, CreationOrder: 3, Status: domain.StatusPending},
			"TASK-004": {ID: "TASK-004", Title: "Add priority badge", Priority: 4, CreationOrder: 4, Status: domain.StatusPending},
			"TASK-005": {ID: "TASK-005", Title: "Add priority selector", Priority: 5, CreationOrder: 5, Status: domain.StatusPending},
			"TASK-006": {ID: "TASK-006", Title: "Add priority filtering", Priority: 6, CreationOrder: 6, Status: domain.StatusPending},
		},
		Deps: map[string][]string{
			"TASK-003": {"TASK-001"},
			"TASK-004": {"TASK-001", "TASK-002"},
			"TASK-005": {"TASK-003", "TASK-004"},
			"TASK-006": {"TASK-005"},
		},
	}
}

func TestTreeMultiParentGolden(t *testing.T) {
	got, err := Tree("Add task priority support", fixtureGraph())
	if err != nil {
		t.Fatal(err)
	}
	want := "🎯 Goal: Add task priority support\n" +
		"│\n" +
		"├── ✅ TASK-001  Add priority model\n" +
		"│   ├── 🔵 TASK-003  Add priority API\n" +
		"│   │   └── ⏸ TASK-005  Add priority selector\n" +
		"│   │       ├── also requires: TASK-004\n" +
		"│   │       └── ⏸ TASK-006  Add priority filtering\n" +
		"│   └── ⏸ TASK-004  Add priority badge\n" +
		"│       ├── also requires: TASK-002\n" +
		"│       └── ↗ TASK-005  already shown\n" +
		"└── 🟡 TASK-002  Prepare UI primitives\n" +
		"    └── ↗ TASK-004  already shown\n" +
		"\n" +
		"Running: TASK-002\n" +
		"Ready:   TASK-003\n" +
		"Blocked: TASK-004, TASK-005, TASK-006\n" +
		"Failed:  none\n"
	if got != want {
		t.Fatalf("tree mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestTreeDeterministicAcrossDependencyOrder(t *testing.T) {
	g1 := fixtureGraph()
	g2 := fixtureGraph()
	g2.Deps["TASK-004"] = []string{"TASK-002", "TASK-001"}
	g2.Deps["TASK-005"] = []string{"TASK-004", "TASK-003"}
	out1, err := Tree("Add task priority support", g1)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := Tree("Add task priority support", g2)
	if err != nil {
		t.Fatal(err)
	}
	if out1 != out2 {
		t.Fatalf("tree changed with dependency order\n%s\n%s", out1, out2)
	}
}

func TestEmptyTree(t *testing.T) {
	got, err := Tree("goal", dag.Graph{Tasks: map[string]dag.Task{}, Deps: map[string][]string{}})
	if err != nil {
		t.Fatal(err)
	}
	want := "🎯 Goal: goal\n" +
		"\n" +
		"Running: none\n" +
		"Ready:   none\n" +
		"Blocked: none\n" +
		"Failed:  none\n"
	if got != want {
		t.Fatalf("empty tree mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestTreeUsesComparatorPriority(t *testing.T) {
	g := dag.Graph{Tasks: map[string]dag.Task{
		"TASK-001": {ID: "TASK-001", Title: "slow", Priority: 20, CreationOrder: 1, Status: domain.StatusPending},
		"TASK-002": {ID: "TASK-002", Title: "fast", Priority: 10, CreationOrder: 2, Status: domain.StatusPending},
	}, Deps: map[string][]string{}}
	got, err := Tree("goal", g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "├── 🔵 TASK-002  fast\n└── 🔵 TASK-001  slow") {
		t.Fatalf("tree did not sort by priority:\n%s", got)
	}
}

func TestStatusSummaryOrder(t *testing.T) {
	g := fixtureGraph()
	s := Summary(g)
	if strings.Join(s.Running, ",") != "TASK-002" || strings.Join(s.Ready, ",") != "TASK-003" || strings.Join(s.Blocked, ",") != "TASK-004,TASK-005,TASK-006" || len(s.Failed) != 0 {
		t.Fatalf("unexpected summary: %#v", s)
	}
	want := "Running: TASK-002\nReady:   TASK-003\nBlocked: TASK-004, TASK-005, TASK-006\nFailed:  none\n"
	if got := Status(g); got != want {
		t.Fatalf("status mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestJSONPayloadDeterministicSlices(t *testing.T) {
	b, err := JSON("goal", fixtureGraph())
	if err != nil {
		t.Fatal(err)
	}
	var p GraphPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Nodes[0].ID != "TASK-001" || p.Nodes[0].Dependencies == nil || len(p.Nodes[0].Dependencies) != 0 {
		t.Fatalf("unexpected first node: %#v", p.Nodes[0])
	}
	if strings.Join(p.Summary.Blocked, ",") != "TASK-004,TASK-005,TASK-006" {
		t.Fatalf("blocked summary = %#v", p.Summary.Blocked)
	}
}

func TestJSONPayloadEmptyAndNoEdgeShape(t *testing.T) {
	empty := Payload("goal", dag.Graph{Tasks: map[string]dag.Task{}, Deps: map[string][]string{}})
	if empty.Nodes == nil || empty.Edges == nil || empty.Summary.Running == nil || empty.Summary.Ready == nil || empty.Summary.Blocked == nil || empty.Summary.Failed == nil {
		t.Fatalf("empty payload has nil slices: %#v", empty)
	}
	noEdge := Payload("goal", dag.Graph{Tasks: map[string]dag.Task{
		"TASK-001": {ID: "TASK-001", Title: "one", Priority: 1, CreationOrder: 1, Status: domain.StatusPending},
	}, Deps: map[string][]string{}})
	if len(noEdge.Nodes) != 1 || noEdge.Nodes[0].Dependencies == nil || len(noEdge.Nodes[0].Dependencies) != 0 || noEdge.Edges == nil || len(noEdge.Edges) != 0 {
		t.Fatalf("no-edge payload shape = %#v", noEdge)
	}
}
