package cli_test

import (
	"bytes"
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
	if !strings.Contains(out, `"schemaVersion":"relo.output/v1"`) || !strings.Contains(out, `"ok":true`) || !strings.Contains(out, "TASK-001") || strings.Contains(out, "TASK-003") {
		t.Fatalf("unexpected ready json: %s", out)
	}
	out, stderr, err = run(t, root, "validate")
	if err != nil {
		t.Fatalf("validate failed out=%s stderr=%s err=%v", out, stderr, err)
	}
	if out != "" || !strings.Contains(stderr, "WARNING: TASK-002 has no notes") {
		t.Fatalf("expected warning on stderr only, out=%q stderr=%q", out, stderr)
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
