package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/files"
	"github.com/wailorman/wwtr/internal/hooks"
	"github.com/wailorman/wwtr/internal/vars"
)

// configWithTemplate produces a config with one template, one copy, one symlink
// and a single prompt-source var so we can observe state.yaml writes.
const configWithTemplate = `version: 1
vars:
  base_port:
    sources:
      - prompt: "BASE_PORT"
        validate: '^[0-9]+$'
    default: 3000
template:
  - from: .worktree.env.tt
    to:   .worktree.env
copy:
  - from: copy-source.txt
    to:   copied.txt
symlink:
  - from: link-target.txt
    to:   link.txt
`

// seedMainRepo writes a config + the template/copy/symlink source files into
// /main via fs. Returns the bytes that the template is expected to render to.
func seedMainRepo(t *testing.T, fs *fakes.FakeFS) {
	t.Helper()
	seedMainConfig(t, fs, configWithTemplate)
	if err := fs.WriteFile("/main/.worktree.env.tt", []byte("PORT={{ .Vars.base_port }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile("/main/copy-source.txt", []byte("copied-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.WriteFile("/main/link-target.txt", []byte("link-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func preInitHookConfig(hooksYAML string) string {
	return `version: 1
hooks:
  init:
    pre:
` + hooksYAML
}

func TestRunInit_Worker_AllFilesApplied_StateWritten(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	bd.Prompter.Inputs = []string{"3010"}
	seedMainRepo(t, bd.FS)

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}

	// Template rendered.
	rendered, err := bd.FS.ReadFile("/worker/.worktree.env")
	if err != nil {
		t.Errorf("template output missing: %v", err)
	}
	if string(rendered) != "PORT=3010\n" {
		t.Errorf("template render = %q", string(rendered))
	}

	// File copied.
	copied, err := bd.FS.ReadFile("/worker/copied.txt")
	if err != nil {
		t.Errorf("copy output missing: %v", err)
	}
	if string(copied) != "copied-content\n" {
		t.Errorf("copy = %q", string(copied))
	}

	// Symlink created.
	target, err := bd.FS.Readlink("/worker/link.txt")
	if err != nil {
		t.Errorf("symlink missing: %v", err)
	} else if target != "/main/link-target.txt" {
		t.Errorf("symlink target = %q", target)
	}

	// State written (prompt resolved a var).
	stateBytes, err := bd.FS.ReadFile("/worker/.wwtr/state.yaml")
	if err != nil {
		t.Errorf("state.yaml missing: %v", err)
	}
	if !strings.Contains(string(stateBytes), "base_port") || !strings.Contains(string(stateBytes), "3010") {
		t.Errorf("state.yaml = %q", string(stateBytes))
	}
}

func TestRunInit_MainWorktree_CopySymlinkSkipped(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	mainGit(bd.Git)
	rc.Flags.Yes = true
	seedMainRepo(t, bd.FS)

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}

	// Template still rendered in main.
	if !bd.FS.Exists("/main/.worktree.env") {
		t.Error("template not rendered in main worktree")
	}
	// Copy/symlink must NOT be created in main (no-op per §11).
	if bd.FS.Exists("/main/copied.txt") {
		t.Error("copy unexpectedly applied in main")
	}
	if _, err := bd.FS.Readlink("/main/link.txt"); err == nil {
		t.Error("symlink unexpectedly created in main")
	}
}

func TestRunInit_NoPromptedVars_NoStateWritten(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	// Config with no prompt-source: only an env-default var.
	seedMainConfig(t, bd.FS, `version: 1
vars:
  port:
    sources:
      - env: PORT
    default: 3000
`)
	bd.Env.Vars["PORT"] = "5000"

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	if bd.FS.Exists("/worker/.wwtr/state.yaml") {
		t.Error("state.yaml unexpectedly written for env-resolved var")
	}
}

func TestRunInit_NoStateFlag_SkipsStateWrite(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	rc.Flags.NoState = true
	bd.Prompter.Inputs = []string{"3010"}
	seedMainRepo(t, bd.FS)

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	if bd.FS.Exists("/worker/.wwtr/state.yaml") {
		t.Error("state.yaml unexpectedly written under --no-state")
	}
}

func TestRunInit_DryRun_NoWrites(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	rc.Flags.DryRun = true
	bd.Prompter.Inputs = []string{"3010"}
	seedMainRepo(t, bd.FS)

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	for _, p := range []string{"/worker/.worktree.env", "/worker/copied.txt", "/worker/.wwtr/state.yaml"} {
		if bd.FS.Exists(p) {
			t.Errorf("dry-run wrote %s", p)
		}
	}
	if _, err := bd.FS.Readlink("/worker/link.txt"); err == nil {
		t.Error("dry-run created symlink")
	}
}

func TestRunInit_NoHooks_NoShellCalls(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	rc.Flags.NoHooks = true
	bd.Prompter.Inputs = []string{"3010"}
	seedMainRepo(t, bd.FS)
	// Add a pre-init hook to verify it gets skipped.
	seedMainConfig(t, bd.FS, preInitHookConfig("      - echo should-not-run\n"))

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	if len(bd.Shell.Calls) != 0 {
		t.Errorf("shell called under --no-hooks: %v", bd.Shell.Calls)
	}
}

func TestRunInit_PreHookFails_ExitPreHook(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	bd.Prompter.Inputs = []string{"3010"}
	seedMainConfig(t, bd.FS, preInitHookConfig("      - exit 7\n"))

	err := RunInit(context.Background(), rc)
	if err == nil {
		t.Fatal("RunInit nil err, want pre-hook failure")
	}
	if code := ExitCode(err); code != ExitPreHook {
		t.Errorf("ExitCode = %d, want %d", code, ExitPreHook)
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("err does not wrap hooks.ErrAborted: %v", err)
	}
}

func TestRunInit_PostHookFails_CommandStillSucceeds(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	bd.Prompter.Inputs = []string{"3010"}
	seedMainConfig(t, bd.FS, `version: 1
hooks:
  init:
    post:
      - exit 1
`)

	if err := RunInit(context.Background(), rc); err != nil {
		t.Errorf("RunInit returned err on failing post-hook: %v", err)
	}
}

func TestRunInit_FileConflictAbort_ExitConflict(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainRepo(t, bd.FS)
	// Pre-approve trust so --yes is not needed (which would also auto-force
	// file conflicts, defeating the test's purpose).
	preApproveTrust(t, bd.FS, "/main/.wwtr.yml")
	bd.Prompter.Inputs = []string{"3010"}
	// Pre-create the copy target with divergent content; user picks Quit.
	if err := bd.FS.WriteFile("/worker/copied.txt", []byte("different\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bd.Prompter.Decisions = []di.Decision{di.DecisionQuit}

	err := RunInit(context.Background(), rc)
	if err == nil {
		t.Fatal("RunInit nil err, want conflict abort")
	}
	if code := ExitCode(err); code != ExitConflict {
		t.Errorf("ExitCode = %d, want %d", code, ExitConflict)
	}
	if !errors.Is(err, files.ErrAborted) {
		t.Errorf("err does not wrap files.ErrAborted: %v", err)
	}
}

func TestRunInit_UnresolvedVar_ExitVar(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	// No --yes auto-approval of prompts that fail validation: an env-only var
	// with no default produces ErrUnresolved.
	seedMainConfig(t, bd.FS, `version: 1
vars:
  required:
    sources:
      - env: REQUIRED
`)

	err := RunInit(context.Background(), rc)
	if err == nil {
		t.Fatal("RunInit nil err, want unresolved")
	}
	if code := ExitCode(err); code != ExitVar {
		t.Errorf("ExitCode = %d, want %d", code, ExitVar)
	}
	if !errors.Is(err, vars.ErrUnresolved) {
		t.Errorf("err does not wrap vars.ErrUnresolved: %v", err)
	}
}

func TestRunInit_StateOverwrite_Yes(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = false
	// Bootstrap trust by seeding the registry; otherwise the trust prompt
	// would consume the Confirm call.
	rc2, bd2 := newTestRC(t)
	_ = rc2
	workerGit(bd2.Git)
	seedMainRepo(t, bd2.FS)
	bd2.Prompter.Inputs = []string{"3010"}
	bd2.Prompter.Confirms = nil
	rc2.Flags.Yes = true
	if err := RunInit(context.Background(), rc2); err != nil {
		t.Fatal(err)
	}
	// Copy the trust registry from bd2 into bd so the second init doesn't
	// trigger the trust prompt.
	trustBytes, _ := bd2.FS.ReadFile("/trust/trust.yaml")
	_ = bd.FS.WriteFile("/trust/trust.yaml", trustBytes, 0o600)

	seedMainRepo(t, bd.FS)
	if err := bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("base_port: \"9999\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bd.Prompter.Inputs = []string{"4242"}
	bd.Prompter.Confirms = []bool{true} // overwrite prompt

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	stateBytes, _ := bd.FS.ReadFile("/worker/.wwtr/state.yaml")
	if !strings.Contains(string(stateBytes), "4242") {
		t.Errorf("state.yaml not overwritten: %q", string(stateBytes))
	}
}

func TestRunInit_StateOverwrite_No_Aborts(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	// Bootstrap trust: pre-seed the registry with the config hash by using
	// a Yes-approved first init.
	rcBootstrap, bdBootstrap := newTestRC(t)
	workerGit(bdBootstrap.Git)
	seedMainRepo(t, bdBootstrap.FS)
	bdBootstrap.Prompter.Inputs = []string{"3010"}
	rcBootstrap.Flags.Yes = true
	if err := RunInit(context.Background(), rcBootstrap); err != nil {
		t.Fatal(err)
	}
	trustBytes, _ := bdBootstrap.FS.ReadFile("/trust/trust.yaml")
	_ = bd.FS.WriteFile("/trust/trust.yaml", trustBytes, 0o600)

	seedMainRepo(t, bd.FS)
	_ = bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("base_port: \"9999\"\n"), 0o644)
	bd.Prompter.Inputs = []string{"4242"}
	bd.Prompter.Confirms = []bool{false} // decline overwrite

	err := RunInit(context.Background(), rc)
	if err == nil {
		t.Fatal("RunInit nil err, want decline")
	}
	if !errors.Is(err, ErrStateOverwriteDeclined) {
		t.Errorf("err = %v, want ErrStateOverwriteDeclined", err)
	}
}

func TestRunInit_StateOverwrite_YesFlagSkipsPrompt(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedMainRepo(t, bd.FS)
	bd.Prompter.Inputs = []string{"4242"}
	_ = bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("base_port: \"9999\"\n"), 0o644)

	if err := RunInit(context.Background(), rc); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	// --yes must not consume a Confirm slot; Inputs is the only scripted answer.
	if len(bd.Prompter.Calls) != 1 { // only the prompt.Input for base_port
		t.Errorf("prompter calls = %v, want just Input", bd.Prompter.Calls)
	}
}

func TestBuildOps_OrderAndKinds(t *testing.T) {
	t.Parallel()
	// Direct unit test on buildOps to lock the template<copy<symlink order.
	cfg := mustParseConfig(t, `version: 1
template:
  - from: a.tt
copy:
  - from: b.txt
symlink:
  - from: c.txt
`)
	ops := buildOps(cfg)
	if len(ops) != 3 {
		t.Fatalf("ops = %d items, want 3", len(ops))
	}
	if ops[0].Kind != files.OpTemplate || ops[1].Kind != files.OpCopy || ops[2].Kind != files.OpSymlink {
		t.Errorf("ops kinds = %v %v %v", ops[0].Kind, ops[1].Kind, ops[2].Kind)
	}
}

// mustParseConfig parses body via the real config.Load against a fresh FakeFS,
// useful for tests that need a *config.Config without running discovery.
func mustParseConfig(t *testing.T, body string) *config.Config {
	t.Helper()
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile("/cfg.yml", []byte(body), 0o644); err != nil {
		t.Fatalf("seed cfg: %v", err)
	}
	cfg, err := config.Load(fs, "/cfg.yml")
	if err != nil {
		t.Fatalf("parse cfg: %v", err)
	}
	return cfg
}
