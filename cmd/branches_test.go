package cmd

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
)

// callRunEDirect builds the subcommand via addXxxCmd, then invokes its RunE
// with a context that lacks the rcKey. This covers the "RunContext absent"
// error branch inside each wrapper's RunE.
func callRunEDirect(t *testing.T, addFn func(*cobra.Command), args []string) {
	t.Helper()
	root := &cobra.Command{}
	addFn(root)
	if len(root.Commands()) != 1 {
		t.Fatalf("setup: expected 1 subcommand, got %d", len(root.Commands()))
	}
	sub := root.Commands()[0]
	sub.SetContext(context.Background())
	err := sub.RunE(sub, args)
	if err == nil {
		t.Errorf("RunE direct: nil err, want RunContext-absent error")
	}
}

func TestInitCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addInitCmd, nil)
}

func TestSetupCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addSetupCmd, nil)
}

func TestStartCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addStartCmd, nil)
}

func TestStopCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addStopCmd, nil)
}

func TestCleanCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addCleanCmd, nil)
}

func TestInfoCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addInfoCmd, nil)
}

func TestTrustCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addTrustCmd, []string{"/x.yml"})
}

func TestUntrustCmd_RunE_NoContext(t *testing.T) {
	t.Parallel()
	callRunEDirect(t, addUntrustCmd, []string{"/x.yml"})
}

func TestExecute_VersionArgSucceeds(t *testing.T) {
	// Not parallel: Execute mutates version package globals, racing with
	// other tests that call NewRootCmd() (which reads them).
	if err := Execute("test", "abc", "2026-07-04"); err != nil {
		t.Errorf("Execute(version): %v", err)
	}
}

func TestContextWithRC_NilParent(t *testing.T) {
	t.Parallel()
	// Passing a nil parent should not panic; falls back to Background.
	ctx := contextWithRC(nil, nil)
	if ctx == nil {
		t.Fatal("nil ctx")
	}
}

func TestRunContextFromCmd_NilContext(t *testing.T) {
	t.Parallel()
	c := &cobra.Command{}
	// c.Context() returns Background by default in cobra v1.10; explicitly
	// set nil to exercise the nil branch.
	c.SetContext(nil)
	if _, err := RunContextFromCmd(c); err == nil {
		t.Error("nil err, want 'no context' error")
	}
}
