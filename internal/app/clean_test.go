package app

import (
	"context"
	"errors"
	"testing"

	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/files"
)

func TestRunClean_PreClean_FilesClean_StateRemove(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true

	// Single config carrying vars+files+hooks so files.Clean sees the same
	// var set as the resolver.
	seedMainConfig(t, bd.FS, `version: 1
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
hooks:
  clean:
    pre:
      - echo pre-clean
    post:
      - echo post-clean
`)
	if err := bd.FS.WriteFile("/main/.worktree.env.tt", []byte("PORT={{ .Vars.base_port }}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bd.FS.WriteFile("/main/copy-source.txt", []byte("copied-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bd.FS.WriteFile("/main/link-target.txt", []byte("link-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pretend init has already happened: outputs exist in /worker.
	if err := bd.FS.WriteFile("/worker/.worktree.env", []byte("PORT=3000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bd.FS.WriteFile("/worker/copied.txt", []byte("copied-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bd.FS.Symlink("/main/link-target.txt", "/worker/link.txt"); err != nil {
		t.Fatal(err)
	}
	if err := bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("base_port: \"3000\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bd.Shell.Program(fakes.ShellResult{}, fakes.ShellResult{})

	if err := RunClean(context.Background(), rc); err != nil {
		t.Fatalf("RunClean: %v", err)
	}

	if bd.FS.Exists("/worker/.worktree.env") {
		t.Error("template output not removed")
	}
	if bd.FS.Exists("/worker/copied.txt") {
		t.Error("copy not removed")
	}
	if _, err := bd.FS.Readlink("/worker/link.txt"); err == nil {
		t.Error("symlink not removed")
	}
	if bd.FS.Exists("/worker/.wwtr/state.yaml") {
		t.Error("state.yaml not removed")
	}
	if len(bd.Shell.Calls) != 2 {
		t.Errorf("shell calls = %v, want pre+post", bd.Shell.Calls)
	}
}

func TestRunClean_PreFails_ExitPreHook(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedMainConfig(t, bd.FS, `version: 1
hooks:
  clean:
    pre:
      - exit 1
`)
	bd.Shell.Program(fakes.ShellResult{ExitCode: 1, Stderr: []byte("nope\n")})

	err := RunClean(context.Background(), rc)
	if err == nil {
		t.Fatal("nil err")
	}
	if ExitCode(err) != ExitPreHook {
		t.Errorf("ExitCode = %d, want %d", ExitCode(err), ExitPreHook)
	}
}

func TestRunClean_DryRun_PreservesFilesAndState(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	rc.Flags.DryRun = true
	seedMainRepo(t, bd.FS)
	if err := bd.FS.WriteFile("/worker/copied.txt", []byte("copied-content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("base_port: \"3010\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunClean(context.Background(), rc); err != nil {
		t.Fatalf("RunClean dry-run: %v", err)
	}
	if !bd.FS.Exists("/worker/copied.txt") {
		t.Error("dry-run removed copied.txt")
	}
	if !bd.FS.Exists("/worker/.wwtr/state.yaml") {
		t.Error("dry-run removed state.yaml")
	}
}

func TestRunClean_ConflictAbort_ExitConflict(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainRepo(t, bd.FS)
	// Pre-approve trust so --yes is not needed (which would auto-force files).
	preApproveTrust(t, bd.FS, "/main/.wwtr.yml")
	// Modified copy target: files.Clean prompts, user picks Quit.
	if err := bd.FS.WriteFile("/worker/copied.txt", []byte("user-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bd.Prompter.Decisions = []di.Decision{di.DecisionQuit}

	err := RunClean(context.Background(), rc)
	if err == nil {
		t.Fatal("nil err, want abort")
	}
	if !errors.Is(err, files.ErrAborted) {
		t.Errorf("err = %v, want files.ErrAborted wrap", err)
	}
	if ExitCode(err) != ExitConflict {
		t.Errorf("ExitCode = %d, want %d", ExitCode(err), ExitConflict)
	}
}

func TestRunClean_NoStateFlag_SkipsStateRemove(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	rc.Flags.NoState = true
	seedMainRepo(t, bd.FS)
	if err := bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("base_port: \"3010\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunClean(context.Background(), rc); err != nil {
		t.Fatalf("RunClean: %v", err)
	}
	if !bd.FS.Exists("/worker/.wwtr/state.yaml") {
		t.Error("--no-state removed state.yaml (should be invisible)")
	}
}
