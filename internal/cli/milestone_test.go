package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func jsonObject(t *testing.T, raw string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", raw, err)
	}
	return got
}
func requireKeys(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()
	got := make([]string, 0, len(m))
	for key := range m {
		got = append(got, key)
	}
	want := append([]string(nil), keys...)
	// Order does not matter for object fields; maps make sibling-field
	// regressions visible while timestamp values remain deliberately opaque.
	for i := range got {
		for j := i + 1; j < len(got); j++ {
			if got[j] < got[i] {
				got[i], got[j] = got[j], got[i]
			}
		}
	}
	for i := range want {
		for j := i + 1; j < len(want); j++ {
			if want[j] < want[i] {
				want[i], want[j] = want[j], want[i]
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v, want %v; object=%#v", got, want, m)
	}
}
func envelopeData(t *testing.T, raw string) map[string]any {
	t.Helper()
	e := jsonObject(t, raw)
	requireKeys(t, e, "schemaVersion", "ok", "data")
	if e["schemaVersion"] != "relo.output/v1" || e["ok"] != true {
		t.Fatalf("bad envelope: %#v", e)
	}
	d, ok := e["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", e["data"])
	}
	return d
}
func array(t *testing.T, v any) []any {
	t.Helper()
	a, ok := v.([]any)
	if !ok {
		t.Fatalf("expected non-null JSON array, got %#v", v)
	}
	return a
}

func TestCLIMilestoneJSONContractsAndMutations(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	must := func(args ...string) string {
		out, errOut, err := run(t, root, args...)
		if err != nil {
			t.Fatalf("%v: stdout=%s stderr=%s: %v", args, out, errOut, err)
		}
		return out
	}
	fail := func(args ...string) (string, string) {
		out, errOut, err := run(t, root, args...)
		if err == nil || exitCode(err) != 2 {
			t.Fatalf("%v: stdout=%s stderr=%s exit=%v", args, out, errOut, err)
		}
		return out, errOut
	}
	must("init", "--prd", "prd.md", "--goal", "goal")
	must("task", "create", "--title", "second", "--objective", "O", "--accept", "A", "--priority", "20")
	must("task", "create", "--title", "first", "--objective", "O", "--accept", "A", "--priority", "10")
	if got := strings.TrimSpace(must("milestone", "create", "--title", "M", "--reason", "R", "--anchor", "TASK-001", "--recommend", "one")); got != "MILESTONE-001" {
		t.Fatalf("create = %q", got)
	}
	if got := must("graph"); !strings.Contains(got, "Milestone checkpoints (non-gating):") || !strings.Contains(got, "◇ MILESTONE-001  M [planned]") || !strings.Contains(got, "anchors: TASK-001") {
		t.Fatalf("default planned milestone overlay = %q", got)
	}
	if got := must("graph", "--tasks-only"); strings.Contains(got, "Milestone checkpoints") || strings.Contains(got, "MILESTONE-001") {
		t.Fatalf("tasks-only graph contains milestone overlay: %s", got)
	}
	if out, stderr := fail("graph", "--format", "json", "--all-milestones"); stderr != "" || !strings.Contains(out, `"code":"INVALID_ARGUMENT"`) || !strings.Contains(out, "supported only with --format tree") {
		t.Fatalf("JSON all-milestones rejection out=%s stderr=%s", out, stderr)
	}
	if out, stderr := fail("graph", "--format", "json", "--tasks-only"); stderr != "" || !strings.Contains(out, `"code":"INVALID_ARGUMENT"`) || !strings.Contains(out, "supported only with --format tree") {
		t.Fatalf("JSON tasks-only rejection out=%s stderr=%s", out, stderr)
	}
	if _, stderr := fail("graph", "--tasks-only", "--all-milestones"); !strings.Contains(stderr, "cannot be combined") {
		t.Fatalf("mutually exclusive graph flags stderr=%s", stderr)
	}

	// Planned get has the full DTO, non-null arrays, and nullable mark fields.
	data := envelopeData(t, must("milestone", "get", "MILESTONE-001", "--json"))
	requireKeys(t, data, "milestone")
	planned := data["milestone"].(map[string]any)
	requireKeys(t, planned, "id", "title", "reason", "stored_status", "display_status", "anchors", "scope", "recommendations", "mark_summary", "reference", "created_at", "updated_at", "marked_at")
	if planned["stored_status"] != "planned" || planned["display_status"] != "planned" || planned["mark_summary"] != nil || planned["reference"] != nil || planned["marked_at"] != nil {
		t.Fatalf("planned null/status fields = %#v", planned)
	}
	if got := array(t, planned["anchors"]); len(got) != 1 || got[0].(map[string]any)["task_id"] != "TASK-001" || got[0].(map[string]any)["is_anchor"] != true {
		t.Fatalf("planned anchors = %#v", got)
	}
	if got := array(t, planned["scope"]); len(got) != 1 || got[0].(map[string]any)["is_anchor"] != true {
		t.Fatalf("planned scope = %#v", got)
	}
	if got := array(t, planned["recommendations"]); len(got) != 1 || got[0].(map[string]any)["id"] != "REC-001" || got[0].(map[string]any)["position"] != float64(0) {
		t.Fatalf("planned recommendations = %#v", got)
	}

	// List, ready and zero-result arrays retain their exact envelope shape.
	list := envelopeData(t, must("milestone", "list", "--json"))
	requireKeys(t, list, "milestones")
	if got := array(t, list["milestones"]); len(got) != 1 {
		t.Fatalf("list = %#v", got)
	} else {
		requireKeys(t, got[0].(map[string]any), "id", "title", "stored_status", "display_status", "anchor_task_ids", "recommendations", "created_at", "marked_at")
	}
	ready := envelopeData(t, must("milestone", "ready", "--json"))
	requireKeys(t, ready, "milestones")
	if got := array(t, ready["milestones"]); len(got) != 0 {
		t.Fatalf("ready before pass = %#v", got)
	}
	if out := must("milestone", "ready"); out != "" {
		t.Fatalf("empty ready human output = %q", out)
	}
	if out := must("milestone", "list", "--status", "marked", "--json"); len(array(t, envelopeData(t, out)["milestones"])) != 0 {
		t.Fatalf("marked zero list = %s", out)
	}

	// Recommendation IDs never reuse gaps; anchor batches roll back on error.
	if got := strings.TrimSpace(must("milestone", "recommendation", "add", "MILESTONE-001", "--text", "two")); got != "REC-002" {
		t.Fatal(got)
	}
	must("milestone", "recommendation", "remove", "MILESTONE-001", "REC-001")
	if got := strings.TrimSpace(must("milestone", "recommendation", "add", "MILESTONE-001", "--text", "three")); got != "REC-003" {
		t.Fatal(got)
	}
	must("milestone", "recommendation", "update", "MILESTONE-001", "REC-002", "--text", "two updated")
	recommendations := array(t, envelopeData(t, must("milestone", "get", "MILESTONE-001", "--json"))["milestone"].(map[string]any)["recommendations"])
	if len(recommendations) != 2 || recommendations[0].(map[string]any)["id"] != "REC-002" || recommendations[0].(map[string]any)["position"] != float64(1) || recommendations[1].(map[string]any)["id"] != "REC-003" || recommendations[1].(map[string]any)["position"] != float64(2) {
		t.Fatalf("recommendation gap/order = %#v", recommendations)
	}
	fail("milestone", "anchor", "add", "MILESTONE-001", "TASK-002", "TASK-999")
	if got := array(t, envelopeData(t, must("milestone", "get", "MILESTONE-001", "--json"))["milestone"].(map[string]any)["anchors"]); len(got) != 1 {
		t.Fatalf("failed anchor batch changed anchors: %#v", got)
	}
	must("milestone", "anchor", "add", "MILESTONE-001", "TASK-002")
	if got := must("graph"); !strings.Contains(got, "anchors:") || !strings.Contains(got, "TASK-001") || !strings.Contains(got, "TASK-002") {
		t.Fatalf("multi-anchor milestone overlay = %q", got)
	}
	must("milestone", "anchor", "remove", "MILESTONE-001", "TASK-002")

	// Task readiness is independent. Mark rejects pending scope, then succeeds
	// only once its anchor passes and freezes all definition mutations.
	if out := must("task", "ready", "--json"); !strings.Contains(out, "TASK-002") {
		t.Fatalf("milestone changed task frontier: %s", out)
	}
	fail("milestone", "mark", "MILESTONE-001", "--summary", "done")
	must("task", "start", "TASK-001")
	must("task", "pass", "TASK-001", "--summary", "done")
	if got := must("milestone", "ready", "--details"); !strings.Contains(got, "MILESTONE-001\tanchors=TASK-001\tM") || !strings.Contains(got, "REC-002") {
		t.Fatalf("ready details = %q", got)
	}
	ready = envelopeData(t, must("milestone", "ready", "--json"))
	if got := array(t, ready["milestones"]); len(got) != 1 || got[0].(map[string]any)["id"] != "MILESTONE-001" {
		t.Fatalf("ready = %#v", got)
	} else {
		requireKeys(t, got[0].(map[string]any), "id", "title", "stored_status", "display_status", "anchor_task_ids", "recommendations", "created_at", "marked_at")
	}
	status := envelopeData(t, must("status", "--json"))
	requireKeys(t, status, "summary")
	summary := status["summary"].(map[string]any)
	requireKeys(t, summary, "running", "ready", "blocked", "failed", "milestones_ready_to_mark")
	if got := array(t, summary["milestones_ready_to_mark"]); len(got) != 1 || got[0] != "MILESTONE-001" {
		t.Fatalf("status = %#v", summary)
	}
	if out := must("status"); !strings.Contains(out, "Milestones ready to mark: MILESTONE-001") {
		t.Fatalf("status human = %q", out)
	}
	if got := must("graph"); !strings.Contains(got, "◎ MILESTONE-001  M [ready_to_mark]") {
		t.Fatalf("ready milestone overlay = %q", got)
	}
	// graph JSON remains the pre-existing task-only contract.
	graphJSON := must("graph", "--format", "json")
	if strings.Contains(graphJSON, "MILESTONE") || strings.Contains(graphJSON, "milestone") {
		t.Fatalf("graph JSON gained milestone data: %s", graphJSON)
	}
	graph := envelopeData(t, graphJSON)
	requireKeys(t, graph, "project_goal", "nodes", "edges", "summary")
	graphSummary := graph["summary"].(map[string]any)
	requireKeys(t, graphSummary, "running", "ready", "blocked", "failed")
	must("milestone", "mark", "MILESTONE-001", "--summary", "reviewed", "--reference", "ref")
	if got := must("graph"); strings.Contains(got, "Milestone checkpoints") || strings.Contains(got, "MILESTONE-001") {
		t.Fatalf("default graph contains marked history: %q", got)
	}
	if got := must("graph", "--all-milestones"); !strings.Contains(got, "◆ MILESTONE-001  M [marked]") || !strings.Contains(got, "anchors: TASK-001") {
		t.Fatalf("all-milestones marked overlay = %q", got)
	}
	marked := envelopeData(t, must("milestone", "get", "MILESTONE-001", "--json"))["milestone"].(map[string]any)
	requireKeys(t, marked, "id", "title", "reason", "stored_status", "display_status", "anchors", "scope", "recommendations", "mark_summary", "reference", "created_at", "updated_at", "marked_at")
	if marked["stored_status"] != "marked" || marked["display_status"] != "marked" || marked["mark_summary"] != "reviewed" || marked["reference"] != "ref" || marked["marked_at"] == nil || len(array(t, marked["anchors"])) != 1 {
		t.Fatalf("marked DTO = %#v", marked)
	}
	if got := array(t, envelopeData(t, must("milestone", "list", "--status", "marked", "--json"))["milestones"]); len(got) != 1 || got[0].(map[string]any)["marked_at"] == nil {
		t.Fatalf("marked list = %#v", got)
	} else {
		requireKeys(t, got[0].(map[string]any), "id", "title", "stored_status", "display_status", "anchor_task_ids", "recommendations", "created_at", "marked_at")
	}
	for _, args := range [][]string{{"milestone", "update", "MILESTONE-001", "--title", "no"}, {"milestone", "delete", "MILESTONE-001"}, {"milestone", "anchor", "add", "MILESTONE-001", "TASK-002"}, {"milestone", "recommendation", "add", "MILESTONE-001", "--text", "no"}} {
		fail(args...)
	}
	if got := strings.TrimSpace(must("milestone", "create", "--title", "later", "--reason", "R", "--anchor", "TASK-002")); got != "MILESTONE-002" {
		t.Fatalf("second milestone = %q", got)
	}
	if got := must("graph"); strings.Contains(got, "MILESTONE-001") || !strings.Contains(got, "◇ MILESTONE-002  later [planned]") {
		t.Fatalf("default graph did not isolate active milestone: %q", got)
	}
	if got := must("graph", "--all-milestones"); !strings.Contains(got, "◆ MILESTONE-001  M [marked]") || !strings.Contains(got, "◇ MILESTONE-002  later [planned]") {
		t.Fatalf("all-milestones graph did not include active and marked: %q", got)
	}
	ordered := array(t, envelopeData(t, must("milestone", "list", "--json"))["milestones"])
	if len(ordered) != 2 || ordered[0].(map[string]any)["id"] != "MILESTONE-001" || ordered[1].(map[string]any)["id"] != "MILESTONE-002" {
		t.Fatalf("deterministic list order = %#v", ordered)
	}

	// Canonical IDs, required values, and JSON failures use stdout-only error envelopes.
	for _, args := range [][]string{{"milestone", "update", "MILESTONE-1", "--title", "x"}, {"milestone", "recommendation", "update", "MILESTONE-001", "REC-1", "--text", "x"}, {"milestone", "create", "--title", " ", "--reason", "R", "--anchor", "TASK-001"}, {"milestone", "mark", "MILESTONE-001", "--summary", " "}} {
		fail(args...)
	}
	out, stderr := fail("milestone", "update", "MILESTONE-1", "--title", "x", "--json")
	if stderr != "" {
		t.Fatalf("JSON error wrote stderr: %q", stderr)
	}
	errEnv := jsonObject(t, out)
	requireKeys(t, errEnv, "schemaVersion", "ok", "error")
	if errEnv["ok"] != false {
		t.Fatalf("error envelope = %#v", errEnv)
	}
}

func TestCLIMilestoneReviewLifecycleEndToEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	must := func(args ...string) string {
		t.Helper()
		out, stderr, err := run(t, root, args...)
		if err != nil {
			t.Fatalf("%v: stdout=%s stderr=%s: %v", args, out, stderr, err)
		}
		return out
	}
	must("init", "--prd", "prd.md", "--goal", "review lifecycle")

	// The six-task DAG has two checkpoint anchors and downstream work that
	// remains dispatchable while the checkpoint is ready.
	for i, title := range []string{"foundation A", "foundation B", "anchor A", "anchor B", "integration", "follow-up"} {
		if got := strings.TrimSpace(must("task", "create", "--title", title, "--objective", "O", "--accept", "A")); got != fmt.Sprintf("TASK-%03d", i+1) {
			t.Fatalf("created ID = %q", got)
		}
	}
	for _, edge := range [][]string{{"TASK-003", "TASK-001"}, {"TASK-004", "TASK-002"}, {"TASK-005", "TASK-003", "TASK-004"}, {"TASK-006", "TASK-004"}} {
		args := append([]string{"task", "dependency", "add"}, edge...)
		must(append(args, "--reason", "DAG ordering")...)
	}
	if got := strings.TrimSpace(must("milestone", "create", "--title", "integration review", "--reason", "anchors are complete", "--anchor", "TASK-003", "--anchor", "TASK-004", "--recommend", "Review integration boundaries", "--recommend", "Run a refactor sweeper", "--recommend", "Run full tests")); got != "MILESTONE-001" {
		t.Fatalf("milestone ID = %q", got)
	}
	pass := func(id string) {
		t.Helper()
		must("task", "start", id)
		must("task", "pass", id, "--summary", "done")
	}
	for _, id := range []string{"TASK-001", "TASK-002", "TASK-003", "TASK-004"} {
		pass(id)
	}
	if got := strings.TrimSpace(must("milestone", "ready")); got != "MILESTONE-001" {
		t.Fatalf("ready milestone = %q", got)
	}
	if ready := must("task", "ready"); !strings.Contains(ready, "TASK-005") {
		t.Fatalf("ready checkpoint gated downstream work: %q", ready)
	}

	// A reviewer finding is distinct work, so it gets the next identity rather
	// than reopening or renumbering an existing task. Re-anchor after inserting
	// it before the pending downstream integration task.
	if got := strings.TrimSpace(must("task", "create", "--title", "refactor boundary", "--objective", "O", "--accept", "A", "--priority", "15")); got != "TASK-007" {
		t.Fatalf("refactor ID = %q", got)
	}
	must("task", "dependency", "add", "TASK-007", "TASK-004", "--reason", "refactor builds on anchor B")
	must("task", "dependency", "add", "TASK-005", "TASK-007", "--reason", "integration uses refactor")
	blocked := array(t, envelopeData(t, must("status", "--json"))["summary"].(map[string]any)["blocked"])
	if len(blocked) != 1 || blocked[0] != "TASK-005" {
		t.Fatalf("TASK-005 was not blocked after dependency insertion: %#v", blocked)
	}
	must("milestone", "anchor", "add", "MILESTONE-001", "TASK-007")
	if got := must("milestone", "ready"); got != "" {
		t.Fatalf("pending re-anchor remained ready: %q", got)
	}
	pass("TASK-007")
	if got := strings.TrimSpace(must("milestone", "ready")); got != "MILESTONE-001" {
		t.Fatalf("completed re-anchor not ready: %q", got)
	}
	readyTasks := array(t, envelopeData(t, must("status", "--json"))["summary"].(map[string]any)["ready"])
	if len(readyTasks) != 2 || readyTasks[0] != "TASK-005" || readyTasks[1] != "TASK-006" {
		t.Fatalf("TASK-005 was not ready after TASK-007 passed: %#v", readyTasks)
	}

	const markSummary = "Reviewed boundaries; completed distinct refactor and full tests."
	must("milestone", "mark", "MILESTONE-001", "--summary", markSummary, "--reference", "e2e-run")
	marked := must("milestone", "get", "MILESTONE-001", "--json")
	markedMilestone := envelopeData(t, marked)["milestone"].(map[string]any)
	if markedMilestone["stored_status"] != "marked" || markedMilestone["display_status"] != "marked" || markedMilestone["mark_summary"] != markSummary || markedMilestone["reference"] != "e2e-run" {
		t.Fatalf("marked status/metadata = %#v", markedMilestone)
	}
	for _, field := range []string{"created_at", "updated_at", "marked_at"} {
		if timestamp, ok := markedMilestone[field].(string); !ok || timestamp == "" {
			t.Fatalf("marked %s = %#v", field, markedMilestone[field])
		}
	}
	ids := func(field string) []string {
		t.Helper()
		values := array(t, markedMilestone[field])
		out := make([]string, len(values))
		for i, value := range values {
			out[i] = value.(map[string]any)["task_id"].(string)
		}
		return out
	}
	if got := ids("scope"); !reflect.DeepEqual(got, []string{"TASK-007", "TASK-001", "TASK-002", "TASK-003", "TASK-004"}) {
		t.Fatalf("marked scope IDs = %v", got)
	}
	if got := ids("anchors"); !reflect.DeepEqual(got, []string{"TASK-007", "TASK-003", "TASK-004"}) {
		t.Fatalf("marked anchor IDs = %v", got)
	}
	recommendations := array(t, markedMilestone["recommendations"])
	if len(recommendations) != 3 {
		t.Fatalf("marked recommendations = %#v", recommendations)
	}
	for i, want := range []struct{ id, text string }{{"REC-001", "Review integration boundaries"}, {"REC-002", "Run a refactor sweeper"}, {"REC-003", "Run full tests"}} {
		got := recommendations[i].(map[string]any)
		if got["id"] != want.id || got["text"] != want.text {
			t.Fatalf("recommendation %d = %#v, want %#v", i, got, want)
		}
	}

	// The refactor's downstream task is still pending, so reopen is safe. The
	// next CLI invocation reads a fresh store; marked output must remain the
	// exact immutable snapshot despite the live task changing.
	must("task", "reopen", "TASK-007", "--reason", "verify immutable checkpoint history")
	if got := must("milestone", "get", "MILESTONE-001", "--json"); got != marked {
		t.Fatalf("marked history drifted after reopen:\nbefore=%s\nafter=%s", marked, got)
	}
	if got := must("milestone", "get", "MILESTONE-001", "--json"); got != marked {
		t.Fatalf("milestone did not persist across CLI/store restart:\nbefore=%s\nafter=%s", marked, got)
	}
	must("validate")
}

func TestCLIMilestoneHelpAndMarkedMutationHints(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	must := func(args ...string) string {
		out, stderr, err := run(t, root, args...)
		if err != nil {
			t.Fatalf("%v: %s %v", args, stderr, err)
		}
		return out
	}
	must("init", "--prd", "prd.md", "--goal", "goal")
	must("task", "create", "--title", "anchor", "--objective", "O", "--accept", "A")
	must("milestone", "create", "--title", "Release", "--reason", "track", "--anchor", "TASK-001", "--recommend", "review")
	for _, args := range [][]string{{"milestone", "--help"}, {"milestone", "get", "--help"}} {
		out := must(args...)
		for _, phrase := range []string{"passed anchor", "transitive"} {
			if !strings.Contains(out, phrase) {
				t.Fatalf("%v missing %q: %s", args, phrase, out)
			}
		}
		if !strings.Contains(out, "never change task") && !strings.Contains(out, "do not gate") {
			t.Fatalf("%v missing non-gating semantics: %s", args, out)
		}
	}
	out := must("milestone", "get", "MILESTONE-001")
	for _, phrase := range []string{"Readiness: passed anchors", "transitive dependency scope", "do not gate task readiness or start"} {
		if !strings.Contains(out, phrase) {
			t.Fatalf("human get missing %q: %s", phrase, out)
		}
	}
	must("task", "start", "TASK-001")
	must("task", "pass", "TASK-001", "--summary", "done")
	must("milestone", "mark", "MILESTONE-001", "--summary", "done")
	cases := [][]string{
		{"milestone", "update", "MILESTONE-001", "--title", "new"},
		{"milestone", "delete", "MILESTONE-001"},
		{"milestone", "anchor", "add", "MILESTONE-001", "TASK-001"},
		{"milestone", "anchor", "remove", "MILESTONE-001", "TASK-001"},
		{"milestone", "recommendation", "add", "MILESTONE-001", "--text", "new"},
		{"milestone", "recommendation", "update", "MILESTONE-001", "REC-001", "--text", "new"},
		{"milestone", "recommendation", "remove", "MILESTONE-001", "REC-001"},
		{"milestone", "mark", "MILESTONE-001", "--summary", "again"},
	}
	for _, args := range cases {
		_, stderr, err := run(t, root, args...)
		if exitCode(err) != 2 || !strings.Contains(stderr, "immutable") || !strings.Contains(stderr, "successor") {
			t.Fatalf("%v: exit=%d stderr=%q", args, exitCode(err), stderr)
		}
	}
}
