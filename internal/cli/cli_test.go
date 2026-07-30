package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Clean(filepath.Join(wd, "../.."))
	bin := filepath.Join(t.TempDir(), "relo")
	build := exec.Command("go", "build", "-o", bin, "./cmd/relo")
	build.Dir = repo
	build.Env = append(os.Environ(), "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build relo: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return stdout.String(), stderr.String(), err
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

func TestCLIInitRootDiscoveryTaskCRUD(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatalf("init failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if _, _, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err == nil {
		t.Fatal("repeat init succeeded")
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, sub, "init", "--prd", "prd.md", "--goal", "nested"); err == nil {
		t.Fatal("nested repeat init succeeded")
	}
	if _, err := os.Stat(filepath.Join(sub, ".relo")); err == nil {
		t.Fatal("nested repeat init created .relo directory")
	}
	out, stderr, err := run(t, sub, "task", "create", "--title", "T", "--objective", "O", "--accept", "A")
	if err != nil {
		t.Fatalf("create failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if strings.TrimSpace(out) != "TASK-001" {
		t.Fatalf("create out = %q", out)
	}
	out, stderr, err = run(t, sub, "task", "get", "--title", "T")
	if err != nil {
		t.Fatalf("get by title failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "# TASK-001 T") || !strings.Contains(out, "- [ ] AC-001 A") {
		t.Fatalf("unexpected get output:\n%s", out)
	}
	if out, stderr, err = run(t, sub, "task", "update", "TASK-001", "--priority", "1"); err == nil {
		t.Fatalf("priority without reason succeeded: out=%s stderr=%s", out, stderr)
	}
	if out, stderr, err = run(t, sub, "task", "update", "TASK-001", "--priority", "1", "--reason", "urgent"); err != nil {
		t.Fatalf("priority update failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	out, stderr, err = run(t, sub, "task", "list", "--status", "pending")
	if err != nil {
		t.Fatalf("list failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "TASK-001\tpending\tpriority=1\tT") {
		t.Fatalf("unexpected list output: %s", out)
	}
	if out, stderr, err = run(t, sub, "task", "delete", "TASK-001"); err != nil {
		t.Fatalf("delete failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
}

func TestCLIProjectUpdateRefreshPRDFromSubdirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "prd2.md"), []byte("prd2"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatalf("init failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := run(t, sub, "project", "update", "--goal", "new goal")
	if err != nil {
		t.Fatalf("project goal update failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "Goal: new goal") || !strings.Contains(out, "PRD: prd.md") {
		t.Fatalf("unexpected goal update output: %s", out)
	}
	out, stderr, err = run(t, sub, "project", "update", "--prd", filepath.Join("docs", "prd2.md"))
	if err != nil {
		t.Fatalf("project prd update failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "Goal: new goal") || !strings.Contains(out, "PRD: "+filepath.Join("docs", "prd2.md")) || !strings.Contains(out, "PRD hash:") {
		t.Fatalf("unexpected prd update output: %s", out)
	}
	if out, stderr, err = run(t, sub, "project", "update"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "at least one of --goal or --prd") {
		t.Fatalf("project update without flags out=%s stderr=%s err=%v", out, stderr, err)
	}
	if out, stderr, err = run(t, sub, "project", "update", "--prd", ""); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "--prd must not be empty") {
		t.Fatalf("project update empty prd out=%s stderr=%s err=%v", out, stderr, err)
	}
	if out, stderr, err = run(t, sub, "project", "update", "--goal", "rolled back", "--prd", "missing.md"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "cannot read PRD") {
		t.Fatalf("project update missing prd out=%s stderr=%s err=%v", out, stderr, err)
	}
	out, stderr, err = run(t, sub, "project", "show")
	if err != nil {
		t.Fatalf("project show failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "Goal: new goal") || strings.Contains(out, "rolled back") {
		t.Fatalf("atomic rollback failed, show output: %s", out)
	}

	if err := os.WriteFile(filepath.Join(root, "docs", "prd2.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	out, stderr, err = run(t, sub, "validate")
	if err != nil {
		t.Fatalf("validate changed failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(stderr, "PRD content hash has changed") {
		t.Fatalf("validate did not warn about changed PRD: out=%s stderr=%s", out, stderr)
	}
	out, stderr, err = run(t, sub, "project", "refresh-prd")
	if err != nil {
		t.Fatalf("refresh changed failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "Old hash:") || !strings.Contains(out, "New hash:") {
		t.Fatalf("refresh did not print old/new hash: %s", out)
	}
	out, stderr, err = run(t, sub, "validate")
	if err != nil {
		t.Fatalf("validate refreshed failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if strings.Contains(out+stderr, "PRD content hash has changed") {
		t.Fatalf("validate warning not cleared after refresh: out=%s stderr=%s", out, stderr)
	}
	out, stderr, err = run(t, sub, "project", "refresh-prd")
	if err != nil {
		t.Fatalf("refresh unchanged failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "Old hash:") || !strings.Contains(out, "New hash:") {
		t.Fatalf("unchanged refresh did not print old/new hash: %s", out)
	}
}

func TestCLIProjectRemoveRequiresForceAndProjectRoot(t *testing.T) {
	root := t.TempDir()
	prdPath := filepath.Join(root, "prd.md")
	if err := os.WriteFile(prdPath, []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatalf("init failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	metadataDir := filepath.Join(root, ".relo")
	unknownPath := filepath.Join(metadataDir, "keep.txt")
	if err := os.WriteFile(unknownPath, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	if out, stderr, err := run(t, root, "project", "remove"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "--force is required") {
		t.Fatalf("remove without force out=%s stderr=%s err=%v", out, stderr, err)
	}
	if _, err := os.Stat(filepath.Join(metadataDir, "relo.db")); err != nil {
		t.Fatalf("remove without force changed database: %v", err)
	}

	sub := filepath.Join(root, "nested")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if out, stderr, err := run(t, sub, "project", "remove", "--force"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "must run from the project root") {
		t.Fatalf("nested remove out=%s stderr=%s err=%v", out, stderr, err)
	}
	if _, err := os.Stat(filepath.Join(metadataDir, "relo.db")); err != nil {
		t.Fatalf("nested remove changed database: %v", err)
	}

	out, stderr, err := run(t, root, "project", "remove", "--force")
	if err != nil {
		t.Fatalf("remove failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "Removed relo project state at "+root) || !strings.Contains(out, "Preserved unknown files in metadata directory") {
		t.Fatalf("unexpected remove output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(metadataDir, "relo.db")); !os.IsNotExist(err) {
		t.Fatalf("database remains after remove: %v", err)
	}
	if got, err := os.ReadFile(unknownPath); err != nil || string(got) != "keep" {
		t.Fatalf("unknown metadata file was not preserved: got=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(prdPath); err != nil || string(got) != "prd" {
		t.Fatalf("PRD was not preserved: got=%q err=%v", got, err)
	}
	if out, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "replacement"); err != nil {
		t.Fatalf("reinit after remove failed: out=%s stderr=%s err=%v", out, stderr, err)
	}
}

func TestCLIEmptyReloDoesNotCreateDB(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".relo"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, root, "project", "show"); err == nil {
		t.Fatal("project show succeeded in empty .relo directory")
	}
	if _, err := os.Stat(filepath.Join(root, ".relo", "relo.db")); !os.IsNotExist(err) {
		t.Fatalf("relo.db was created, stat err=%v", err)
	}
}

func TestCLICommandPositionalRejection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "unknown"); err == nil {
		t.Fatal("unknown command succeeded")
	} else if exitCode(err) != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("unexpected unknown command err=%v stderr=%s", err, stderr)
	}
	if _, stderr, err := run(t, root, "init", "extra", "--prd", "prd.md", "--goal", "goal"); err == nil {
		t.Fatal("init accepted positional arg")
	} else if exitCode(err) != 2 || (!strings.Contains(stderr, "unknown command") && !strings.Contains(stderr, "accepts 0 arg")) {
		t.Fatalf("unexpected init positional err=%v stderr=%s", err, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, ".relo", "relo.db")); !os.IsNotExist(err) {
		t.Fatalf("invalid init created db, stat err=%v", err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "list", "extra"); err == nil {
		t.Fatal("task list accepted positional arg")
	} else if exitCode(err) != 2 || (!strings.Contains(stderr, "unknown command") && !strings.Contains(stderr, "accepts 0 arg")) {
		t.Fatalf("unexpected task list positional err=%v stderr=%s", err, stderr)
	}
	if _, stderr, err := run(t, root, "task", "start"); err == nil {
		t.Fatal("task start accepted no IDs")
	} else if exitCode(err) != 2 || !strings.Contains(stderr, "requires at least 1 arg") {
		t.Fatalf("unexpected task start positional err=%v stderr=%s", err, stderr)
	}
	if _, stderr, err := run(t, root, "task", "list", "--bogus"); err == nil {
		t.Fatal("task list accepted invalid flag")
	} else if exitCode(err) != 2 || !strings.Contains(stderr, "unknown flag") {
		t.Fatalf("unexpected invalid flag err=%v stderr=%s", err, stderr)
	}
}

func TestCLIMilestone2Commands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if _, stderr, err := run(t, root, "task", "create", "--title", name, "--objective", "O", "--accept", "A"); err != nil {
			t.Fatal(stderr, err)
		}
	}
	out, stderr, err := run(t, root, "task", "acceptance", "add", "TASK-001", "--text", "B")
	if err != nil || strings.TrimSpace(out) != "AC-002" {
		t.Fatalf("acceptance add out=%s stderr=%s err=%v", out, stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "note", "add", "TASK-001", "--text", "N"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err = run(t, root, "task", "dependency", "add", "TASK-003", "TASK-001", "TASK-002", "--reason-for", "TASK-001=one", "--reason-for", "TASK-002=two")
	if err != nil {
		t.Fatalf("dep add failed out=%s stderr=%s err=%v", out, stderr, err)
	}
	if _, _, err := run(t, root, "task", "dependency", "add", "TASK-001", "TASK-003", "--reason", "cycle"); err == nil {
		t.Fatal("cycle dependency succeeded")
	}
	out, stderr, err = run(t, root, "task", "get", "TASK-003")
	if err != nil {
		t.Fatal(stderr, err)
	}
	if !strings.Contains(out, "TASK-001 (pending): one") || !strings.Contains(out, "TASK-002 (pending): two") || !strings.Contains(out, "## Notes") {
		t.Fatalf("unexpected get output:\n%s", out)
	}
	if _, stderr, err := run(t, root, "task", "dependency", "reason", "TASK-003", "TASK-001", "--text", "updated"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err = run(t, root, "task", "ready", "--json")
	if err != nil {
		t.Fatal(stderr, err)
	}
	var readyEnv struct {
		OK   bool `json:"ok"`
		Data struct {
			Tasks []struct {
				ID                 string `json:"id"`
				Title              string `json:"title"`
				Status             string `json:"status"`
				Priority           int    `json:"priority"`
				Objective          string `json:"objective"`
				AcceptanceCriteria []struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"acceptance_criteria"`
				Dependencies []struct {
					TaskID       string `json:"task_id"`
					DependencyID string `json:"dependency_id"`
					Status       string `json:"status"`
					Reason       string `json:"reason"`
					Title        string `json:"title"`
				} `json:"dependencies"`
				Notes []struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"notes"`
			} `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &readyEnv); err != nil {
		t.Fatalf("invalid ready json %q: %v", out, err)
	}
	if !readyEnv.OK || len(readyEnv.Data.Tasks) != 2 || readyEnv.Data.Tasks[0].ID != "TASK-001" || readyEnv.Data.Tasks[1].ID != "TASK-002" {
		t.Fatalf("unexpected ready json: %#v stdout=%s", readyEnv, out)
	}
	for _, task := range readyEnv.Data.Tasks {
		if task.AcceptanceCriteria == nil || task.Dependencies == nil || task.Notes == nil || task.ID == "TASK-003" {
			t.Fatalf("ready task has unstable arrays or blocked task: %#v stdout=%s", task, out)
		}
	}
	out, stderr, err = run(t, root, "validate")
	if err != nil {
		t.Fatalf("validate failed out=%s stderr=%s err=%v", out, stderr, err)
	}
	if out != "OK\n" || stderr != "" {
		t.Fatalf("expected OK without removed low-value warnings, out=%q stderr=%q", out, stderr)
	}
	if _, stderr, err := run(t, root, "task", "dependency", "remove", "TASK-003", "TASK-001", "TASK-999", "--reason", "bad"); err == nil {
		t.Fatal("mixed dependency remove succeeded")
	} else if !strings.Contains(stderr, "dependency task TASK-999 does not exist") {
		t.Fatalf("unexpected remove stderr: %s", stderr)
	}
	out, stderr, err = run(t, root, "task", "get", "TASK-003")
	if err != nil || !strings.Contains(out, "TASK-001 (pending): updated") || !strings.Contains(out, "TASK-002 (pending): two") {
		t.Fatalf("remove was not rolled back or get failed out=%s stderr=%s err=%v", out, stderr, err)
	}
}

func TestCLIGraphTreeAndJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "Add task priority support"); err != nil {
		t.Fatal(stderr, err)
	}
	for _, name := range []string{"Add priority model", "Prepare UI primitives", "Add priority API", "Add priority badge", "Add priority selector", "Add priority filtering"} {
		if _, stderr, err := run(t, root, "task", "create", "--title", name, "--objective", "O", "--accept", "A"); err != nil {
			t.Fatal(stderr, err)
		}
	}
	deps := [][]string{{"TASK-003", "TASK-001"}, {"TASK-004", "TASK-001", "TASK-002"}, {"TASK-005", "TASK-003", "TASK-004"}, {"TASK-006", "TASK-005"}}
	for _, dep := range deps {
		args := append([]string{"task", "dependency", "add"}, dep...)
		args = append(args, "--reason", "required")
		if _, stderr, err := run(t, root, args...); err != nil {
			t.Fatal(stderr, err)
		}
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-001"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "pass", "TASK-001", "--summary", "done"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-002"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err := run(t, root, "graph")
	if err != nil {
		t.Fatalf("graph failed out=%s stderr=%s err=%v", out, stderr, err)
	}
	for _, want := range []string{"🎯 Goal: Add task priority support", "also requires: TASK-004", "↗ TASK-005  already shown", "Running: TASK-002", "Ready:   TASK-003", "Blocked: TASK-004, TASK-005, TASK-006"} {
		if !strings.Contains(out, want) {
			t.Fatalf("graph output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Milestone checkpoints") {
		t.Fatalf("graph without milestones has an empty checkpoint section:\n%s", out)
	}
	out2, stderr, err := run(t, root, "graph", "--format", "tree")
	if err != nil || out2 != out {
		t.Fatalf("graph tree not deterministic out=%s out2=%s stderr=%s err=%v", out, out2, stderr, err)
	}
	jsonOut, stderr, err := run(t, root, "graph", "--format", "json")
	if err != nil {
		t.Fatalf("graph json failed out=%s stderr=%s err=%v", jsonOut, stderr, err)
	}
	for _, want := range []string{"\"schemaVersion\":\"relo.output/v1\"", "\"ok\":true", "\"project_goal\":\"Add task priority support\"", "\"task_id\":\"TASK-005\"", "\"dependency_id\":\"TASK-004\"", "\"blocked\":[\"TASK-004\",\"TASK-005\",\"TASK-006\"]"} {
		if !strings.Contains(jsonOut, want) {
			t.Fatalf("graph json missing %q:\n%s", want, jsonOut)
		}
	}
}

func TestCLIEndToEndScenario(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("# Agent PRD\n\nBuild the MVP through delegated tasks."), 0644); err != nil {
		t.Fatal(err)
	}
	if out, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "ship delegated MVP"); err != nil || !strings.Contains(out, "Initialized relo project") {
		t.Fatalf("init out=%s stderr=%s err=%v", out, stderr, err)
	}

	creates := []struct {
		title    string
		priority string
	}{
		{title: "Implement storage", priority: "10"},
		{title: "Implement worker", priority: "20"},
		{title: "Add configuration", priority: "30"},
		{title: "Wire integration", priority: "40"},
	}
	for i, c := range creates {
		out, stderr, err := run(t, root, "task", "create", "--title", c.title, "--objective", "Complete "+c.title, "--accept", "done", "--priority", c.priority)
		wantID := []string{"TASK-001", "TASK-002", "TASK-003", "TASK-004"}[i]
		if err != nil || strings.TrimSpace(out) != wantID {
			t.Fatalf("create %s out=%s stderr=%s err=%v", c.title, out, stderr, err)
		}
		if _, stderr, err := run(t, root, "task", "note", "add", wantID, "--text", "created by CLI"); err != nil {
			t.Fatalf("note %s stderr=%s err=%v", wantID, stderr, err)
		}
	}
	if _, stderr, err := run(t, root, "task", "dependency", "add", "TASK-004", "TASK-001", "TASK-002", "--reason", "integration needs completed components"); err != nil {
		t.Fatalf("initial dependency add stderr=%s err=%v", stderr, err)
	}

	if out, stderr, err := run(t, root, "validate"); err != nil || strings.TrimSpace(out) != "OK" || stderr != "" {
		t.Fatalf("validate out=%s stderr=%s err=%v", out, stderr, err)
	}
	graphOut, stderr, err := run(t, root, "graph")
	if err != nil {
		t.Fatalf("graph out=%s stderr=%s err=%v", graphOut, stderr, err)
	}
	for _, want := range []string{"🎯 Goal: ship delegated MVP", "TASK-001", "TASK-002", "TASK-004", "Blocked: TASK-004"} {
		if !strings.Contains(graphOut, want) {
			t.Fatalf("graph missing %q:\n%s", want, graphOut)
		}
	}

	readyOut, stderr, err := run(t, root, "task", "ready")
	if err != nil {
		t.Fatalf("ready out=%s stderr=%s err=%v", readyOut, stderr, err)
	}
	if got := strings.Join(strings.Fields(readyOut), ","); got != "TASK-001,TASK-002,TASK-003" {
		t.Fatalf("initial ready = %q", got)
	}
	if out, stderr, err := run(t, root, "task", "get", "--title", "Implement worker"); err != nil || !strings.Contains(out, "# TASK-002 Implement worker") {
		t.Fatalf("get by title out=%s stderr=%s err=%v", out, stderr, err)
	}
	startOut, stderr, err := run(t, root, "task", "start", "TASK-001", "TASK-002")
	if err != nil || strings.Join(strings.Fields(startOut), ",") != "TASK-001,TASK-002" {
		t.Fatalf("start two out=%s stderr=%s err=%v", startOut, stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "pass", "TASK-001", "--summary", "storage complete"); err != nil {
		t.Fatalf("pass TASK-001 stderr=%s err=%v", stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "stop", "TASK-002", "--reason", "discovered missing configuration dependency"); err != nil {
		t.Fatalf("stop TASK-002 stderr=%s err=%v", stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "dependency", "add", "TASK-002", "TASK-003", "--reason", "worker requires configuration"); err != nil {
		t.Fatalf("add discovered dependency stderr=%s err=%v", stderr, err)
	}
	if out, stderr, err := run(t, root, "validate"); err != nil || strings.TrimSpace(out) != "OK" || stderr != "" {
		t.Fatalf("validate after dependency out=%s stderr=%s err=%v", out, stderr, err)
	}
	readyOut, stderr, err = run(t, root, "task", "ready")
	if err != nil || strings.TrimSpace(readyOut) != "TASK-003" {
		t.Fatalf("ready after stop/dependency out=%s stderr=%s err=%v", readyOut, stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-003"); err != nil {
		t.Fatalf("start TASK-003 stderr=%s err=%v", stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "pass", "TASK-003", "--summary", "configuration complete"); err != nil {
		t.Fatalf("pass TASK-003 stderr=%s err=%v", stderr, err)
	}
	readyOut, stderr, err = run(t, root, "task", "ready")
	if err != nil || strings.TrimSpace(readyOut) != "TASK-002" {
		t.Fatalf("ready after prerequisite out=%s stderr=%s err=%v", readyOut, stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-002"); err != nil {
		t.Fatalf("restart TASK-002 stderr=%s err=%v", stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "pass", "TASK-002", "--summary", "worker complete"); err != nil {
		t.Fatalf("pass TASK-002 stderr=%s err=%v", stderr, err)
	}
	readyOut, stderr, err = run(t, root, "task", "ready")
	if err != nil || strings.TrimSpace(readyOut) != "TASK-004" {
		t.Fatalf("ready integration out=%s stderr=%s err=%v", readyOut, stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-004"); err != nil {
		t.Fatalf("start TASK-004 stderr=%s err=%v", stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "pass", "TASK-004", "--summary", "integration complete"); err != nil {
		t.Fatalf("pass TASK-004 stderr=%s err=%v", stderr, err)
	}

	passedOut, stderr, err := run(t, root, "task", "list", "--status", "passed")
	if err != nil {
		t.Fatalf("list passed out=%s stderr=%s err=%v", passedOut, stderr, err)
	}
	for _, want := range []string{"TASK-001\tpassed", "TASK-002\tpassed", "TASK-003\tpassed", "TASK-004\tpassed"} {
		if !strings.Contains(passedOut, want) {
			t.Fatalf("passed list missing %q:\n%s", want, passedOut)
		}
	}
	statusJSON, stderr, err := run(t, root, "status", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("final status json out=%s stderr=%s err=%v", statusJSON, stderr, err)
	}
	var statusEnv struct {
		SchemaVersion string `json:"schemaVersion"`
		OK            bool   `json:"ok"`
		Data          struct {
			Summary struct {
				Running []string `json:"running"`
				Ready   []string `json:"ready"`
				Blocked []string `json:"blocked"`
				Failed  []string `json:"failed"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(statusJSON), &statusEnv); err != nil {
		t.Fatalf("invalid final status json %q: %v", statusJSON, err)
	}
	if statusEnv.SchemaVersion != "relo.output/v1" || !statusEnv.OK || len(statusEnv.Data.Summary.Running) != 0 || len(statusEnv.Data.Summary.Ready) != 0 || len(statusEnv.Data.Summary.Blocked) != 0 || len(statusEnv.Data.Summary.Failed) != 0 {
		t.Fatalf("unexpected final status json: %#v stdout=%s", statusEnv, statusJSON)
	}
	missingOut, stderr, err := run(t, root, "task", "get", "TASK-999", "--json")
	if err == nil || exitCode(err) != 2 || stderr != "" || !strings.Contains(missingOut, `"schemaVersion":"relo.output/v1"`) || !strings.Contains(missingOut, `"ok":false`) {
		t.Fatalf("json error contract out=%s stderr=%s err=%v", missingOut, stderr, err)
	}
}

func TestCLIStatusAndJSONContracts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	for _, name := range []string{"dep", "down", "run", "fail"} {
		if _, stderr, err := run(t, root, "task", "create", "--title", name, "--objective", "O", "--accept", "A"); err != nil {
			t.Fatal(stderr, err)
		}
	}
	if _, stderr, err := run(t, root, "task", "dependency", "add", "TASK-002", "TASK-001", "--reason", "needs"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-003"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-004"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "fail", "TASK-004", "--reason", "bad"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err := run(t, root, "status")
	if err != nil {
		t.Fatalf("status failed out=%s stderr=%s err=%v", out, stderr, err)
	}
	for _, want := range []string{"Running: TASK-003", "Ready:   TASK-001", "Blocked: TASK-002", "Failed:  TASK-004"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
	jsonOut, stderr, err := run(t, root, "status", "--json")
	if err != nil {
		t.Fatalf("status json failed out=%s stderr=%s err=%v", jsonOut, stderr, err)
	}
	var env struct {
		SchemaVersion string `json:"schemaVersion"`
		OK            bool   `json:"ok"`
		Data          struct {
			Summary struct {
				Running []string `json:"running"`
				Ready   []string `json:"ready"`
				Blocked []string `json:"blocked"`
				Failed  []string `json:"failed"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &env); err != nil {
		t.Fatalf("invalid status json %q: %v", jsonOut, err)
	}
	if env.SchemaVersion != "relo.output/v1" || !env.OK || strings.Join(env.Data.Summary.Running, ",") != "TASK-003" || strings.Join(env.Data.Summary.Ready, ",") != "TASK-001" || strings.Join(env.Data.Summary.Blocked, ",") != "TASK-002" || strings.Join(env.Data.Summary.Failed, ",") != "TASK-004" {
		t.Fatalf("unexpected status json: %#v stdout=%s", env, jsonOut)
	}
	if env.Data.Summary.Running == nil || env.Data.Summary.Ready == nil || env.Data.Summary.Blocked == nil || env.Data.Summary.Failed == nil {
		t.Fatalf("status summary has nil arrays: %#v", env.Data.Summary)
	}
	getOut, stderr, err := run(t, root, "task", "get", "TASK-001", "--json")
	if err != nil {
		t.Fatalf("task get json failed out=%s stderr=%s err=%v", getOut, stderr, err)
	}
	var getEnv struct {
		SchemaVersion string `json:"schemaVersion"`
		OK            bool   `json:"ok"`
		Data          struct {
			Project struct {
				Goal    string `json:"goal"`
				PRDPath string `json:"prd_path"`
			} `json:"project"`
			Task struct {
				ID                 string `json:"id"`
				Title              string `json:"title"`
				Status             string `json:"status"`
				Priority           int    `json:"priority"`
				Objective          string `json:"objective"`
				AcceptanceCriteria []struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"acceptance_criteria"`
				Dependencies []struct {
					TaskID       string `json:"task_id"`
					DependencyID string `json:"dependency_id"`
					Status       string `json:"status"`
					Reason       string `json:"reason"`
					Title        string `json:"title"`
				} `json:"dependencies"`
				Notes []struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"notes"`
			} `json:"task"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(getOut), &getEnv); err != nil {
		t.Fatalf("invalid task get json %q: %v", getOut, err)
	}
	if getEnv.SchemaVersion != "relo.output/v1" || !getEnv.OK || getEnv.Data.Project.Goal != "goal" || getEnv.Data.Project.PRDPath != "prd.md" || getEnv.Data.Task.ID != "TASK-001" || getEnv.Data.Task.Title != "dep" || getEnv.Data.Task.Status != "pending" || getEnv.Data.Task.Priority != 100 || getEnv.Data.Task.Objective != "O" {
		t.Fatalf("unexpected task get json: %#v stdout=%s", getEnv, getOut)
	}
	if len(getEnv.Data.Task.AcceptanceCriteria) != 1 || getEnv.Data.Task.AcceptanceCriteria[0].ID != "AC-001" || getEnv.Data.Task.Dependencies == nil || getEnv.Data.Task.Notes == nil {
		t.Fatalf("task get arrays/ACs not stable: %#v stdout=%s", getEnv.Data.Task, getOut)
	}
	depOut, stderr, err := run(t, root, "task", "get", "TASK-002", "--json")
	if err != nil {
		t.Fatalf("task get dependency json failed out=%s stderr=%s err=%v", depOut, stderr, err)
	}
	getEnv = struct {
		SchemaVersion string `json:"schemaVersion"`
		OK            bool   `json:"ok"`
		Data          struct {
			Project struct {
				Goal    string `json:"goal"`
				PRDPath string `json:"prd_path"`
			} `json:"project"`
			Task struct {
				ID                 string `json:"id"`
				Title              string `json:"title"`
				Status             string `json:"status"`
				Priority           int    `json:"priority"`
				Objective          string `json:"objective"`
				AcceptanceCriteria []struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"acceptance_criteria"`
				Dependencies []struct {
					TaskID       string `json:"task_id"`
					DependencyID string `json:"dependency_id"`
					Status       string `json:"status"`
					Reason       string `json:"reason"`
					Title        string `json:"title"`
				} `json:"dependencies"`
				Notes []struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				} `json:"notes"`
			} `json:"task"`
		} `json:"data"`
	}{}
	if err := json.Unmarshal([]byte(depOut), &getEnv); err != nil {
		t.Fatalf("invalid task get dependency json %q: %v", depOut, err)
	}
	if len(getEnv.Data.Task.Dependencies) != 1 || getEnv.Data.Task.Dependencies[0].TaskID != "TASK-002" || getEnv.Data.Task.Dependencies[0].DependencyID != "TASK-001" || getEnv.Data.Task.Dependencies[0].Status != "pending" || getEnv.Data.Task.Dependencies[0].Reason != "needs" || getEnv.Data.Task.Dependencies[0].Title != "dep" {
		t.Fatalf("dependency DTO missing stable fields: %#v stdout=%s", getEnv.Data.Task.Dependencies, depOut)
	}
	missingOut, stderr, err := run(t, root, "task", "get", "TASK-999", "--json")
	if err == nil || exitCode(err) != 2 || stderr != "" {
		t.Fatalf("missing task succeeded, leaked stderr, or wrong code out=%s stderr=%s err=%v", missingOut, stderr, err)
	}
	var errEnv struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(missingOut), &errEnv); err != nil {
		t.Fatalf("invalid missing task envelope %q: %v", missingOut, err)
	}
	if errEnv.OK || errEnv.Error.Code != "NOT_FOUND" {
		t.Fatalf("unexpected missing task envelope: %#v stdout=%s", errEnv, missingOut)
	}
	badReadyOut, stderr, err := run(t, root, "task", "ready", "extra", "--json")
	if err == nil || exitCode(err) != 2 || stderr != "" {
		t.Fatalf("ready extra succeeded, leaked stderr, or wrong code out=%s stderr=%s err=%v", badReadyOut, stderr, err)
	}
	if err := json.Unmarshal([]byte(badReadyOut), &errEnv); err != nil {
		t.Fatalf("invalid ready arg envelope %q: %v", badReadyOut, err)
	}
	if errEnv.OK || errEnv.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("unexpected ready arg envelope: %#v stdout=%s", errEnv, badReadyOut)
	}
	flagOut, stderr, err := run(t, root, "task", "ready", "--json", "--bogus")
	if err == nil || exitCode(err) != 2 || stderr != "" {
		t.Fatalf("ready bad flag succeeded, leaked stderr, or wrong code out=%s stderr=%s err=%v", flagOut, stderr, err)
	}
	if err := json.Unmarshal([]byte(flagOut), &errEnv); err != nil {
		t.Fatalf("invalid ready flag envelope %q: %v", flagOut, err)
	}
	if errEnv.OK || errEnv.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("unexpected ready flag envelope: %#v stdout=%s", errEnv, flagOut)
	}
	falseOut, stderr, err := run(t, root, "task", "ready", "extra", "--json=false")
	if err == nil || exitCode(err) != 2 || falseOut != "" || !strings.Contains(stderr, "accepts 0 arg") {
		t.Fatalf("--json=false should be human error only out=%s stderr=%s err=%v", falseOut, stderr, err)
	}
}

func TestCLIGraphJSONValidationErrorsAndEmptyShape(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err := run(t, root, "graph", "extra", "--format", "json")
	if err == nil || exitCode(err) != 2 || stderr != "" {
		t.Fatalf("graph positional succeeded, leaked stderr, or wrong code out=%s stderr=%s err=%v", out, stderr, err)
	}
	var env struct {
		SchemaVersion string `json:"schemaVersion"`
		OK            bool   `json:"ok"`
		Error         struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid json error envelope %q: %v", out, err)
	}
	if env.SchemaVersion != "relo.output/v1" || env.OK || env.Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("unexpected error envelope: %#v stdout=%s stderr=%s", env, out, stderr)
	}
	out, stderr, err = run(t, root, "graph", "--format", "yaml")
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("graph invalid format succeeded or wrong code out=%s stderr=%s err=%v", out, stderr, err)
	}
	if out != "" || !strings.Contains(stderr, "--format must be tree or json") {
		t.Fatalf("yaml/invalid format should be human stderr only out=%s stderr=%s", out, stderr)
	}
	out, stderr, err = run(t, root, "graph", "--format", "json")
	if err != nil {
		t.Fatalf("empty graph json failed out=%s stderr=%s err=%v", out, stderr, err)
	}
	var ok struct {
		OK   bool `json:"ok"`
		Data struct {
			Nodes   []any `json:"nodes"`
			Edges   []any `json:"edges"`
			Summary struct {
				Running []string `json:"running"`
				Ready   []string `json:"ready"`
				Blocked []string `json:"blocked"`
				Failed  []string `json:"failed"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &ok); err != nil {
		t.Fatalf("invalid empty graph json %q: %v", out, err)
	}
	if !ok.OK || ok.Data.Nodes == nil || ok.Data.Edges == nil || ok.Data.Summary.Running == nil || ok.Data.Summary.Ready == nil || ok.Data.Summary.Blocked == nil || ok.Data.Summary.Failed == nil {
		t.Fatalf("empty graph has nil/missing arrays: %#v stdout=%s", ok, out)
	}
}

func TestCLIJSONUninitializedError(t *testing.T) {
	root := t.TempDir()
	out, stderr, err := run(t, root, "task", "ready", "--json")
	if err == nil || exitCode(err) != 2 || stderr != "" {
		t.Fatalf("uninitialized json should exit 2 without stderr out=%s stderr=%s err=%v", out, stderr, err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid uninitialized envelope %q: %v", out, err)
	}
	if env.OK || env.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("unexpected uninitialized envelope: %#v stdout=%s", env, out)
	}
}

func TestCLIDependencyReasonFlagEdgeCases(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if _, stderr, err := run(t, root, "task", "create", "--title", name, "--objective", "O", "--accept", "A"); err != nil {
			t.Fatal(stderr, err)
		}
	}
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing reason", []string{"task", "dependency", "add", "TASK-003", "TASK-001"}, "--reason or --reason-for is required"},
		{"mixed reason flags", []string{"task", "dependency", "add", "TASK-003", "TASK-001", "--reason", "all", "--reason-for", "TASK-001=one"}, "--reason and --reason-for are mutually exclusive"},
		{"unrelated reason", []string{"task", "dependency", "add", "TASK-003", "TASK-001", "--reason-for", "TASK-002=two"}, "unrelated dependency TASK-002"},
		{"empty reason", []string{"task", "dependency", "add", "TASK-003", "TASK-001", "--reason-for", "TASK-001="}, "reason for TASK-001 must not be empty"},
		{"bad format", []string{"task", "dependency", "add", "TASK-003", "TASK-001", "--reason-for", "TASK-001"}, "--reason-for must be TASK-ID=TEXT"},
		{"duplicate reason", []string{"task", "dependency", "add", "TASK-003", "TASK-001", "--reason-for", "TASK-001=one", "--reason-for", "TASK-001=again"}, "duplicate --reason-for TASK-001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, stderr, err := run(t, root, tc.args...); err == nil {
				t.Fatal("command succeeded")
			} else if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr, tc.want)
			}
		})
	}
	if _, stderr, err := run(t, root, "task", "dependency", "add", "TASK-003", "TASK-001", "TASK-002", "--reason-for", "TASK-001=one"); err == nil {
		t.Fatal("missing per-dependency reason succeeded")
	} else if !strings.Contains(stderr, "missing --reason-for TASK-002") {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestCLITitleAmbiguityAndCreateValidation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, _, err := run(t, root, "task", "create", "--title", "bad", "--objective", "O"); err == nil {
		t.Fatal("create without AC succeeded")
	}
	for i := 0; i < 2; i++ {
		if _, stderr, err := run(t, root, "task", "create", "--title", "dup", "--objective", "O", "--accept", "A"); err != nil {
			t.Fatal(stderr, err)
		}
	}
	_, stderr, err := run(t, root, "task", "get", "--title", "dup")
	if err == nil {
		t.Fatal("ambiguous title lookup succeeded")
	}
	if !strings.Contains(stderr, "ambiguous") || !strings.Contains(stderr, "TASK-001") || !strings.Contains(stderr, "TASK-002") {
		t.Fatalf("unexpected ambiguity stderr: %s", stderr)
	}
	out, stderr, err := run(t, root, "task", "get", "--title", "dup", "--json")
	if err == nil || exitCode(err) != 2 || stderr != "" {
		t.Fatalf("json ambiguity succeeded, leaked stderr, or wrong code out=%s stderr=%s err=%v", out, stderr, err)
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid ambiguity envelope %q: %v", out, err)
	}
	if env.OK || env.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("unexpected ambiguity envelope: %#v stdout=%s", env, out)
	}
}

func TestCLIRuntimeCommandsAndReadyGating(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	for _, name := range []string{"dep", "down", "other"} {
		if _, stderr, err := run(t, root, "task", "create", "--title", name, "--objective", "O", "--accept", "A"); err != nil {
			t.Fatal(stderr, err)
		}
	}
	if _, stderr, err := run(t, root, "task", "dependency", "add", "TASK-002", "TASK-001", "--reason", "needs"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err := run(t, root, "task", "start", "TASK-001", "TASK-003")
	if err != nil {
		t.Fatalf("start failed out=%s stderr=%s err=%v", out, stderr, err)
	}
	if !strings.Contains(out, "TASK-001") || !strings.Contains(out, "TASK-003") {
		t.Fatalf("unexpected start output: %s", out)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-002"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "blocked") {
		t.Fatalf("blocked start err=%v stderr=%s", err, stderr)
	}
	if _, stderr, err := run(t, root, "task", "pass", "TASK-001", "--summary", "done"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err = run(t, root, "task", "ready")
	if err != nil {
		t.Fatal(stderr, err)
	}
	if !strings.Contains(out, "TASK-002") {
		t.Fatalf("downstream not ready after pass: %s", out)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-002"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "reopen", "TASK-001", "--reason", "redo"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "TASK-002") {
		t.Fatalf("unsafe reopen err=%v stderr=%s", err, stderr)
	}
	if _, stderr, err := run(t, root, "task", "stop", "TASK-002", "TASK-003", "--reason", "pause"); err != nil {
		t.Fatalf("multi stop stderr=%s err=%v", stderr, err)
	}
	out, stderr, err = run(t, root, "task", "list")
	if err != nil {
		t.Fatal(stderr, err)
	}
	if !strings.Contains(out, "TASK-002\tpending") || !strings.Contains(out, "TASK-003\tpending") {
		t.Fatalf("stop did not reset tasks: %s", out)
	}
	if _, stderr, err := run(t, root, "task", "reopen", "TASK-001", "--reason", "redo"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-001"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "fail", "TASK-001"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "--reason is required") {
		t.Fatalf("fail without reason err=%v stderr=%s", err, stderr)
	}
	if _, stderr, err := run(t, root, "task", "fail", "TASK-001", "--reason", "bad"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "retry", "TASK-001"); err != nil {
		t.Fatal(stderr, err)
	}
	out, stderr, err = run(t, root, "task", "list", "--status", "pending")
	if err != nil || !strings.Contains(out, "TASK-001\tpending") {
		t.Fatalf("retry list out=%s stderr=%s err=%v", out, stderr, err)
	}
}

func TestCLIRuntimeAtomicityAndExitCodes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	for _, name := range []string{"one", "two", "three"} {
		if _, stderr, err := run(t, root, "task", "create", "--title", name, "--objective", "O", "--accept", "A"); err != nil {
			t.Fatal(stderr, err)
		}
	}
	if _, stderr, err := run(t, root, "task", "dependency", "add", "TASK-003", "TASK-002", "--reason", "needs"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-001", "TASK-003"); err == nil || exitCode(err) != 2 {
		t.Fatalf("mixed start err=%v stderr=%s", err, stderr)
	}
	out, stderr, err := run(t, root, "task", "list")
	if err != nil {
		t.Fatal(stderr, err)
	}
	if !strings.Contains(out, "TASK-001\tpending") || !strings.Contains(out, "TASK-003\tpending") {
		t.Fatalf("mixed start was not atomic: %s", out)
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-001"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "stop", "TASK-001", "TASK-002", "--reason", "pause"); err == nil || exitCode(err) != 2 {
		t.Fatalf("mixed stop err=%v stderr=%s", err, stderr)
	}
	out, stderr, err = run(t, root, "task", "list")
	if err != nil {
		t.Fatal(stderr, err)
	}
	if !strings.Contains(out, "TASK-001\trunning") || !strings.Contains(out, "TASK-002\tpending") {
		t.Fatalf("mixed stop was not atomic: %s", out)
	}
	if _, stderr, err := run(t, root, "task", "stop", "TASK-001"); err == nil || exitCode(err) != 2 || !strings.Contains(stderr, "--reason is required") {
		t.Fatalf("stop without reason err=%v stderr=%s", err, stderr)
	}
	for _, badID := range []string{"bad-id", "TASK-1", "TASK-01", "TASK-ABC"} {
		if _, stderr, err := run(t, root, "task", "start", badID); err == nil || exitCode(err) != 2 {
			t.Fatalf("bad id %s err=%v stderr=%s", badID, err, stderr)
		}
	}
	if _, stderr, err := run(t, root, "task", "start", "TASK-1000"); err == nil || exitCode(err) != 2 || strings.Contains(stderr, "canonical") {
		t.Fatalf("TASK-1000 should pass canonical validation and fail as missing task, err=%v stderr=%s", err, stderr)
	}
	if _, _, err := run(t, root, "task", "create", "--title", "bad", "--objective-file", "missing.txt", "--accept", "A"); err == nil || exitCode(err) != 1 {
		t.Fatalf("unexpected runtime failure exit: %v", err)
	}
}

func TestCLIProjectAndTaskSummaryJSONContracts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatalf("init: %s: %v", stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "create", "--title", "T", "--objective", "O", "--accept", "A"); err != nil {
		t.Fatalf("create: %s: %v", stderr, err)
	}
	out, stderr, err := run(t, root, "project", "show", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("project show json: out=%s stderr=%s err=%v", out, stderr, err)
	}
	projectData := envelopeData(t, out)
	requireKeys(t, projectData, "project")
	projectFields, ok := projectData["project"].(map[string]any)
	if !ok {
		t.Fatalf("project = %#v", projectData["project"])
	}
	requireKeys(t, projectFields, "goal", "prd_path", "prd_hash")
	if projectFields["goal"] != "goal" || projectFields["prd_path"] != "prd.md" || projectFields["prd_hash"] == "" {
		t.Fatalf("project fields = %#v", projectFields)
	}
	out, stderr, err = run(t, root, "task", "list", "--status", "pending", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("task list json: out=%s stderr=%s err=%v", out, stderr, err)
	}
	var list struct {
		Data struct {
			Tasks []map[string]json.RawMessage `json:"tasks"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	if list.Data.Tasks == nil || len(list.Data.Tasks) != 1 {
		t.Fatalf("task summaries = %#v", list.Data.Tasks)
	}
	summary := make(map[string]any, len(list.Data.Tasks[0]))
	encoded, err := json.Marshal(list.Data.Tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &summary); err != nil {
		t.Fatal(err)
	}
	requireKeys(t, summary, "id", "title", "status", "priority", "attempt_count", "last_failure_reason", "last_completion_summary", "created_at", "updated_at")
	if summary["id"] != "TASK-001" || summary["title"] != "T" || summary["status"] != "pending" || summary["attempt_count"] != float64(0) || summary["last_failure_reason"] != nil || summary["last_completion_summary"] != nil {
		t.Fatalf("summary = %#v", summary)
	}
	for _, flag := range []string{"-v", "--version"} {
		out, stderr, err = run(t, root, flag)
		if err != nil || stderr != "" || out != "dev\n" {
			t.Fatalf("%s = out=%q stderr=%q err=%v", flag, out, stderr, err)
		}
	}
	_, _, err = run(t, root, "version")
	if exitCode(err) != 2 {
		t.Fatalf("version command exit = %d", exitCode(err))
	}
}

func TestCLIJSONTaskDetailLifecycleAndErrors(t *testing.T) {
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
	must("task", "create", "--title", "T", "--objective", "O", "--accept", "A")
	get := func() map[string]any { return envelopeData(t, must("task", "get", "TASK-001", "--json")) }
	assertDetail := func(wantStatus string, attempts float64, current any, failure, completion any) {
		d := get()
		requireKeys(t, d, "project", "task")
		project := d["project"].(map[string]any)
		requireKeys(t, project, "goal", "prd_path")
		task := d["task"].(map[string]any)
		requireKeys(t, task, "id", "title", "status", "priority", "objective", "acceptance_criteria", "dependencies", "notes", "creation_order", "attempt_count", "last_failure_reason", "last_completion_summary", "created_at", "updated_at", "current_attempt")
		if task["status"] != wantStatus || task["attempt_count"] != attempts || task["current_attempt"] != current || task["last_failure_reason"] != failure || task["last_completion_summary"] != completion {
			t.Fatalf("detail=%#v", task)
		}
	}
	assertDetail("pending", 0, nil, nil, nil)
	must("task", "start", "TASK-001")
	d := get()
	task := d["task"].(map[string]any)
	attempt := task["current_attempt"].(map[string]any)
	requireKeys(t, attempt, "attempt_number", "status", "started_at", "completed_at", "summary", "reason")
	if attempt["attempt_number"] != float64(1) || attempt["status"] != "running" || attempt["started_at"] == "" || attempt["completed_at"] != nil || attempt["summary"] != nil || attempt["reason"] != nil {
		t.Fatalf("running attempt=%#v", attempt)
	}
	must("task", "fail", "TASK-001", "--reason", "bad")
	assertDetail("failed", 1, nil, "bad", nil)
	must("task", "retry", "TASK-001")
	must("task", "start", "TASK-001")
	must("task", "pass", "TASK-001", "--summary", "done")
	assertDetail("passed", 2, nil, "bad", "done")

	out, stderr, err := run(t, t.TempDir(), "project", "show", "--json")
	if err == nil || stderr != "" {
		t.Fatalf("project JSON error: out=%q stderr=%q err=%v", out, stderr, err)
	}
	if e := jsonObject(t, out); e["ok"] != false {
		t.Fatalf("error envelope=%#v", e)
	}
	out, stderr, err = run(t, root, "task", "list", "--status", "invalid", "--json")
	if err == nil || stderr != "" {
		t.Fatalf("list JSON error: out=%q stderr=%q err=%v", out, stderr, err)
	}
	if e := jsonObject(t, out); e["ok"] != false {
		t.Fatalf("error envelope=%#v", e)
	}
	out, stderr, err = run(t, root, "task", "list", "--status", "running", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("empty list: %s %v", stderr, err)
	}
	data := envelopeData(t, out)
	if tasks := array(t, data["tasks"]); len(tasks) != 0 {
		t.Fatalf("empty tasks=%#v", tasks)
	}
}

func TestCLIObjectiveHelpRecoveryAndWarnings(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("prd"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "objective.txt"), []byte("file objective"), 0644); err != nil {
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
	fail := func(args ...string) string {
		_, stderr, err := run(t, root, args...)
		if exitCode(err) != 2 {
			t.Fatalf("%v exit=%d stderr=%s", args, exitCode(err), stderr)
		}
		return stderr
	}
	fail("task", "create", "--title", "bad", "--objective", "x", "--objective-file", "objective.txt", "--accept", "A")
	fail("task", "create", "--title", "bad", "--accept", "A")
	if out := must("task", "list", "--json"); len(array(t, envelopeData(t, out)["tasks"])) != 0 {
		t.Fatal("rejected create mutated store")
	}
	must("task", "create", "--title", "T", "--objective", "original", "--accept", "A")
	fail("task", "update", "TASK-001", "--objective", "x", "--objective-file", "objective.txt")
	out := must("task", "get", "TASK-001", "--json")
	if envelopeData(t, out)["task"].(map[string]any)["objective"] != "original" {
		t.Fatal("rejected update mutated objective")
	}
	if _, _, err := run(t, root, "task", "update", "TASK-001", "--objective-file", "missing"); err == nil {
		t.Fatal("missing objective file succeeded")
	}
	out = must("task", "get", "TASK-001", "--json")
	if envelopeData(t, out)["task"].(map[string]any)["objective"] != "original" {
		t.Fatal("failed file update mutated objective")
	}

	must("task", "start", "TASK-001")
	if got := fail("task", "note", "add", "TASK-001", "--text", "n"); !strings.Contains(got, "stop it first") {
		t.Fatalf("running hint=%s", got)
	}
	must("task", "stop", "TASK-001", "--reason", "pause")
	if got := fail("task", "pass", "TASK-001", "--summary", "x"); !strings.Contains(got, "start") {
		t.Fatalf("pending pass hint=%s", got)
	}
	must("task", "create", "--title", "blocked", "--objective", "O", "--accept", "A")
	must("task", "dependency", "add", "TASK-002", "TASK-001", "--reason", "needs")
	if got := fail("task", "start", "TASK-002"); !strings.Contains(got, "pass dependencies") {
		t.Fatalf("blocked start hint=%s", got)
	}
	must("task", "start", "TASK-001")
	must("task", "pass", "TASK-001", "--summary", "ok")
	must("task", "start", "TASK-002")
	if got := fail("task", "reopen", "TASK-001", "--reason", "redo"); !strings.Contains(got, "descendants") {
		t.Fatalf("reopen hint=%s", got)
	}
	if err := os.WriteFile(filepath.Join(root, "prd.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	out, stderr, err := run(t, root, "validate")
	if err != nil || out != "OK\n" || !strings.Contains(stderr, "WARNING:") {
		t.Fatalf("validate warning: out=%q stderr=%q err=%v", out, stderr, err)
	}
	out, stderr, err = run(t, root, "--help")
	if err != nil || !strings.Contains(out, "-v, --version") || !strings.Contains(out, "manages") || stderr != "" {
		t.Fatalf("root help=%q stderr=%q", out, stderr)
	}
	out, _, _ = run(t, root, "task", "dependency", "--help")
	if !strings.Contains(out, "dependent") {
		t.Fatalf("dependency group help=%q", out)
	}
	out, _, _ = run(t, root, "task", "dependency", "add", "--help")
	if !strings.Contains(out, "Example") {
		t.Fatalf("dependency add help=%q", out)
	}
	out, _, _ = run(t, root, "task", "create", "--help")
	if !strings.Contains(out, "exactly one") {
		t.Fatalf("create help=%q", out)
	}
}

func TestCLIDefaultHelpAndCompletionCommands(t *testing.T) {
	root := t.TempDir()
	out, stderr, err := run(t, root, "help", "task")
	if err != nil || stderr != "" || !strings.Contains(out, "relo task [command]") {
		t.Fatalf("help task: out=%q stderr=%q err=%v", out, stderr, err)
	}

	out, stderr, err = run(t, root, "completion", "bash")
	if err != nil || stderr != "" || !strings.Contains(out, "__start_relo") {
		t.Fatalf("completion bash: out=%q stderr=%q err=%v", out, stderr, err)
	}

	out, stderr, err = run(t, root, "__complete", "ta")
	if err != nil || !strings.Contains(out, "task\t") || !strings.Contains(out, ":4") || !strings.Contains(stderr, "Completion ended with directive") {
		t.Fatalf("shell completion request: out=%q stderr=%q err=%v", out, stderr, err)
	}

	out, stderr, err = run(t, root, "__completeNoDesc", "ta")
	if err != nil || !strings.Contains(out, "task\n") || strings.Contains(out, "task\t") || !strings.Contains(out, ":4") || !strings.Contains(stderr, "Completion ended with directive") {
		t.Fatalf("description-free completion request: out=%q stderr=%q err=%v", out, stderr, err)
	}

	for _, request := range []string{"__complete", "__completeNoDesc"} {
		out, stderr, err = run(t, root, request)
		if exitCode(err) != 2 || out != "" || !strings.Contains(stderr, "requires at least 1 arg") {
			t.Fatalf("malformed %s request: out=%q stderr=%q err=%v", request, out, stderr, err)
		}
	}
}
