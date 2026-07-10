package cli_test

import (
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
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), string(ee.Stderr), err
		}
		return string(out), "", err
	}
	return string(out), "", nil
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
	if _, stderr, err := run(t, root, "init", "extra", "--prd", "prd.md", "--goal", "goal"); err == nil {
		t.Fatal("init accepted positional arg")
	} else if !strings.Contains(stderr, "unknown command") && !strings.Contains(stderr, "accepts 0 arg") {
		t.Fatalf("unexpected init positional stderr: %s", stderr)
	}
	if _, err := os.Stat(filepath.Join(root, ".relo", "relo.db")); !os.IsNotExist(err) {
		t.Fatalf("invalid init created db, stat err=%v", err)
	}
	if _, stderr, err := run(t, root, "init", "--prd", "prd.md", "--goal", "goal"); err != nil {
		t.Fatal(stderr, err)
	}
	if _, stderr, err := run(t, root, "task", "list", "extra"); err == nil {
		t.Fatal("task list accepted positional arg")
	} else if !strings.Contains(stderr, "unknown command") && !strings.Contains(stderr, "accepts 0 arg") {
		t.Fatalf("unexpected task list positional stderr: %s", stderr)
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
