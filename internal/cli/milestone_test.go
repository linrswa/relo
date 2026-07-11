package cli_test

import (
	"encoding/json"
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
	// graph remains the pre-existing contract and never gains status-only data.
	graph := envelopeData(t, must("graph", "--format", "json"))
	graphSummary := graph["summary"].(map[string]any)
	requireKeys(t, graphSummary, "running", "ready", "blocked", "failed")
	must("milestone", "mark", "MILESTONE-001", "--summary", "reviewed", "--reference", "ref")
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
