package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestUpgradeCommandHelpAndValidation(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantError  bool
		wantOutput string
	}{
		{"help", []string{"upgrade", "--help"}, false, "--check"},
		{"conflicting flags", []string{"upgrade", "--check", "--version", "v1.2.3"}, true, "--check and --version"},
		{"invalid version", []string{"upgrade", "--version", "latest"}, true, "semantic version"},
		{"positional argument", []string{"upgrade", "extra"}, true, "unknown command"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := (&app{}).rootCmd()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs(test.args)
			_, err := cmd.ExecuteC()
			if (err != nil) != test.wantError {
				t.Fatalf("ExecuteC() err = %v, stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String()+stderr.String()+errorText(err), test.wantOutput) {
				t.Fatalf("output=%q stderr=%q err=%v, want %q", stdout.String(), stderr.String(), err, test.wantOutput)
			}
		})
	}
}

func TestRootHelpIncludesUpgrade(t *testing.T) {
	cmd := (&app{}).rootCmd()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--help"})
	if _, err := cmd.ExecuteC(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "upgrade") {
		t.Fatalf("root help = %q", stdout.String())
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
