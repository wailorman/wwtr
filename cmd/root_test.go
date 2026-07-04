package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionFlag_PrintsLongVersion(t *testing.T) {
	t.Parallel()
	root := NewRootCmd()
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetErr(buf)
	// Invoke the `version` subcommand (always runs RunE); cobra's --version
	// short-circuits before PersistentPreRunE so it is less useful for the test.
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"wwtr version", "commit:", "built:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestPersistentFlags_Bound(t *testing.T) {
	t.Parallel()
	root := NewRootCmd()
	// Wire all global flags through argv and observe they parse without error
	// into a command that does nothing harmful.
	root.SetArgs([]string{
		"version",
		"--config", "/tmp/x.yml",
		"--force", "--skip", "--dry-run",
		"--no-hooks", "--yes", "--no-state", "-v",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute with global flags: %v", err)
	}
}

func TestRunContextFromCmd_NotSet(t *testing.T) {
	t.Parallel()
	// A freshly built command without PersistentPreRunE has no RC.
	root := NewRootCmd()
	if _, err := RunContextFromCmd(root); err == nil {
		t.Fatal("expected error when RunContext absent from context")
	}
}
