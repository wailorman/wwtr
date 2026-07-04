package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/runcontext"
)

// wantSubcommands lists every subcommand cobra should have registered.
var wantSubcommands = []string{
	"init", "setup", "start", "stop", "clean", "info", "trust [path]", "untrust [path]", "version",
}

func TestRootCmd_AllSubcommandsRegistered(t *testing.T) {
	t.Parallel()
	root := NewRootCmd()
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Use] = true
	}
	for _, want := range wantSubcommands {
		if !got[want] {
			t.Errorf("subcommand %q not registered; got: %v", want, keysOf(got))
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRootCmd_GlobalFlagsStillParse(t *testing.T) {
	t.Parallel()
	root := NewRootCmd()
	root.SetArgs([]string{"version", "--config", "/tmp/x.yml", "--force", "--yes", "--no-state"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// fakeRC is a small builder for tests that need a RunContext wired entirely
// to fakes. The cobra command is replaced with a synthetic one whose
// PersistentPreRunE injects the fake RC, so we don't depend on the real OS.
type fakeRC struct {
	bd fakes.BufferDeps
	rc *runcontext.RunContext
}

func newFakeRC() *fakeRC {
	bd := fakes.NewBufferDeps()
	bd.Git.MainVal = "/main"
	bd.Git.CurrentVal = "/worker"
	bd.Git.BranchVal = "feature/cmd-test"
	_ = bd.FS.WriteFile("/main/.wwtr.yml", []byte("version: 1\nvars:\n  p:\n    default: 1\n"), 0o644)
	rc := &runcontext.RunContext{
		Deps: di.Deps{
			FS:       bd.FS,
			Shell:    bd.Shell,
			Git:      bd.Git,
			Env:      bd.Env,
			Prompter: bd.Prompter,
			TTY:      bd.TTY,
			Clock:    bd.Clock,
			Stdout:   bd.Stdout,
			Stderr:   bd.Stderr,
		},
		TrustRegistryPath: "/trust/trust.yaml",
	}
	// Silence slog during cmd tests.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 100})))
	return &fakeRC{bd: bd, rc: rc}
}

// runWithFakeRC builds a root cmd whose PersistentPreRunE injects f.rc, sets
// args, and executes. Returns captured stdout and the execution error.
func runWithFakeRC(t *testing.T, f *fakeRC, args []string) (*bytes.Buffer, *bytes.Buffer, error) {
	t.Helper()
	root := NewRootCmd()
	root.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		c.SetContext(context.WithValue(c.Context(), rcKey{}, f.rc))
		return nil
	}
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SetArgs(args)
	execErr := root.Execute()
	return f.bd.Stdout, errBuf, execErr
}

func TestInitCmd_DelegatesToAppInit(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	f.rc.Flags.Yes = true

	_, _, err := runWithFakeRC(t, f, []string{"init"})
	if err != nil {
		t.Errorf("init: %v", err)
	}
}

func TestSetupCmd_DelegatesToAppSetup(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	f.rc.Flags.Yes = true
	f.bd.Shell.Program(fakes.ShellResult{}, fakes.ShellResult{}) // empty pre/post hooks → no calls

	_, _, err := runWithFakeRC(t, f, []string{"setup"})
	if err != nil {
		t.Errorf("setup: %v", err)
	}
}

func TestStartCmd_DelegatesToAppStart(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	f.rc.Flags.Yes = true
	_, _, err := runWithFakeRC(t, f, []string{"start"})
	if err != nil {
		t.Errorf("start: %v", err)
	}
}

func TestStopCmd_DelegatesToAppStop(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	f.rc.Flags.Yes = true
	_, _, err := runWithFakeRC(t, f, []string{"stop"})
	if err != nil {
		t.Errorf("stop: %v", err)
	}
}

func TestCleanCmd_DelegatesToAppClean(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	f.rc.Flags.Yes = true
	_, _, err := runWithFakeRC(t, f, []string{"clean"})
	if err != nil {
		t.Errorf("clean: %v", err)
	}
}

func TestInfoCmd_DefaultOutput(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	out, _, err := runWithFakeRC(t, f, []string{"info"})
	if err != nil {
		t.Errorf("info: %v", err)
	}
	if !strings.Contains(out.String(), "Builtins:") {
		t.Errorf("info output missing Builtins: section; got:\n%s", out.String())
	}
}

func TestInfoCmd_JSONFlag(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	out, _, err := runWithFakeRC(t, f, []string{"info", "--json"})
	if err != nil {
		t.Errorf("info --json: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("info --json output not JSON; got:\n%s", out.String())
	}
}

func TestInfoCmd_EnvFlag(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	out, _, err := runWithFakeRC(t, f, []string{"info", "--env"})
	if err != nil {
		t.Errorf("info --env: %v", err)
	}
	if !strings.Contains(out.String(), "export WWTR_BRANCH=") {
		t.Errorf("info --env output missing export; got:\n%s", out.String())
	}
}

func TestTrustCmd_ExplicitPath(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	_ = f.bd.FS.WriteFile("/custom/.wwtr.yml", []byte("version: 1\n"), 0o644)
	_, _, err := runWithFakeRC(t, f, []string{"trust", "/custom/.wwtr.yml"})
	if err != nil {
		t.Errorf("trust <path>: %v", err)
	}
	trustBytes, _ := f.bd.FS.ReadFile("/trust/trust.yaml")
	if !strings.Contains(string(trustBytes), "/custom/.wwtr.yml") {
		t.Errorf("trust entry missing; got: %q", string(trustBytes))
	}
}

func TestTrustCmd_NoPath_Discovered(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	_, _, err := runWithFakeRC(t, f, []string{"trust"})
	if err != nil {
		t.Errorf("trust: %v", err)
	}
	trustBytes, _ := f.bd.FS.ReadFile("/trust/trust.yaml")
	if !strings.Contains(string(trustBytes), "/main/.wwtr.yml") {
		t.Errorf("trust entry missing: %q", string(trustBytes))
	}
}

func TestUntrustCmd_Delegates(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	// First trust, then untrust.
	if _, _, err := runWithFakeRC(t, f, []string{"trust"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runWithFakeRC(t, f, []string{"untrust"}); err != nil {
		t.Errorf("untrust: %v", err)
	}
	trustBytes, _ := f.bd.FS.ReadFile("/trust/trust.yaml")
	if strings.Contains(string(trustBytes), "/main/.wwtr.yml") {
		t.Errorf("untrust did not remove entry: %q", string(trustBytes))
	}
}

func TestTrustCmd_TooManyArgs(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	_, _, err := runWithFakeRC(t, f, []string{"trust", "a", "b"})
	if err == nil {
		t.Error("nil err, want MaximumNArgs rejection")
	}
}

func TestInitCmd_PropagatesError(t *testing.T) {
	t.Parallel()
	f := newFakeRC()
	// Break git so Discover fails.
	f.bd.Git.MainErr = errors.New("no git")
	_, _, err := runWithFakeRC(t, f, []string{"init"})
	if err == nil {
		t.Error("nil err, want discover failure")
	}
}

func TestRootCmd_HelpListsAllSubcommands(t *testing.T) {
	t.Parallel()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetArgs([]string{"--help"})
	// --help returns nil/pflag.ErrHelp without running anything.
	_ = root.Execute()
	body := out.String()
	for _, want := range []string{"init", "setup", "start", "stop", "clean", "info", "trust", "untrust"} {
		if !strings.Contains(body, want) {
			t.Errorf("help text missing %q; got:\n%s", want, body)
		}
	}
}
