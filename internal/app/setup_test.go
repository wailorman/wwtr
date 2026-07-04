package app

import (
	"context"
	"errors"
	"testing"

	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/hooks"
)

const hooksCfg = `version: 1
vars:
  v:
    default: x
hooks:
  %s:
    pre:
      - %s
    post:
      - %s
`

func seedHooksCfg(t *testing.T, fs *fakes.FakeFS, cmd, preCmd, postCmd string) {
	t.Helper()
	body := "version: 1\nhooks:\n  " + cmd + ":\n    pre:\n      - " + preCmd + "\n    post:\n      - " + postCmd + "\n"
	seedMainConfig(t, fs, body)
}

func TestRunSetup_PrePostHooksCalled(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedHooksCfg(t, bd.FS, "setup", "echo pre-setup", "echo post-setup")
	bd.Shell.Program(
		fakes.ShellResult{Stdout: []byte("pre\n")},
		fakes.ShellResult{Stdout: []byte("post\n")},
	)

	if err := RunSetup(context.Background(), rc); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(bd.Shell.Calls) != 2 {
		t.Fatalf("shell calls = %v, want 2", bd.Shell.Calls)
	}
	if bd.Shell.Calls[0] != "echo pre-setup" || bd.Shell.Calls[1] != "echo post-setup" {
		t.Errorf("calls = %v", bd.Shell.Calls)
	}
}

func TestRunSetup_PreFails_ExitPreHook(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedHooksCfg(t, bd.FS, "setup", "exit 1", "echo post-setup")
	bd.Shell.Program(fakes.ShellResult{ExitCode: 1, Stderr: []byte("fail\n")})

	err := RunSetup(context.Background(), rc)
	if err == nil {
		t.Fatal("nil err, want pre-hook failure")
	}
	if code := ExitCode(err); code != ExitPreHook {
		t.Errorf("ExitCode = %d, want %d", code, ExitPreHook)
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("err = %v, want wrap ErrAborted", err)
	}
}

func TestRunSetup_PostFails_CommandSucceeds(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedHooksCfg(t, bd.FS, "setup", "echo pre", "exit 1")
	bd.Shell.Program(
		fakes.ShellResult{Stdout: []byte("pre\n")},
		fakes.ShellResult{ExitCode: 1, Stderr: []byte("post-fail\n")},
	)

	if err := RunSetup(context.Background(), rc); err != nil {
		t.Errorf("RunSetup returned err on failing post-hook: %v", err)
	}
}

func TestRunSetup_NoHooks_SkipsShell(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	rc.Flags.NoHooks = true
	seedHooksCfg(t, bd.FS, "setup", "echo pre", "echo post")

	if err := RunSetup(context.Background(), rc); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(bd.Shell.Calls) != 0 {
		t.Errorf("shell called under --no-hooks: %v", bd.Shell.Calls)
	}
}

func TestRunStart_HooksRun(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedHooksCfg(t, bd.FS, "start", "echo pre-start", "echo post-start")
	bd.Shell.Program(
		fakes.ShellResult{},
		fakes.ShellResult{},
	)
	if err := RunStart(context.Background(), rc); err != nil {
		t.Fatalf("RunStart: %v", err)
	}
	if len(bd.Shell.Calls) != 2 {
		t.Errorf("calls = %v", bd.Shell.Calls)
	}
}

func TestRunStop_PreFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedHooksCfg(t, bd.FS, "stop", "exit 5", "echo post-stop")
	bd.Shell.Program(fakes.ShellResult{ExitCode: 5, Stderr: []byte("nope\n")})

	err := RunStop(context.Background(), rc)
	if err == nil || ExitCode(err) != ExitPreHook {
		t.Errorf("RunStop pre-fail: err=%v ExitCode=%d", err, ExitCode(err))
	}
}
