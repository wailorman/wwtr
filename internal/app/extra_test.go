package app

import (
	"context"
	"errors"
	"testing"

	"github.com/wailorman/wwtr/internal/di/fakes"
)

func TestRunInit_StateWriteFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedMainConfig(t, bd.FS, `version: 1
vars:
  base_port:
    sources:
      - prompt: "BASE_PORT"
        validate: '^[0-9]+$'
    default: 3000
template:
  - from: t.tt
    to:   out
`)
	if err := bd.FS.WriteFile("/main/t.tt", []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	bd.Prompter.Inputs = []string{"3010"}
	// Inject error on the .wwtr dir MkdirAll (called only by state.Write at
	// end of init; earlier FS ops hit the file path, not the dir).
	bd.FS.InjectError("/worker/.wwtr", errors.New("disk full"))

	err := RunInit(context.Background(), rc)
	if err == nil {
		t.Fatal("nil err, want state write failure")
	}
}

func TestRunClean_StateRemoveFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedMainConfig(t, bd.FS, minimalConfig)
	_ = bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("x: \"1\"\n"), 0o644)
	bd.FS.InjectError("/worker/.wwtr/state.yaml", errors.New("fs error"))

	if err := RunClean(context.Background(), rc); err == nil {
		t.Error("nil err, want state remove failure")
	}
}

func TestRunClean_TrustFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	rc.Flags.Yes = false
	bd.Prompter.Confirms = []bool{false}

	err := RunClean(context.Background(), rc)
	if err == nil || ExitCode(err) != ExitTrust {
		t.Errorf("err=%v ExitCode=%d, want ExitTrust", err, ExitCode(err))
	}
}

func TestRunStart_TrustFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	rc.Flags.Yes = false
	bd.Prompter.Confirms = []bool{false}
	err := RunStart(context.Background(), rc)
	if err == nil || ExitCode(err) != ExitTrust {
		t.Errorf("err=%v ExitCode=%d", err, ExitCode(err))
	}
}

func TestRunStop_DiscoverFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	bd.Git.MainErr = errors.New("nope")
	if err := RunStop(context.Background(), rc); err == nil {
		t.Error("nil err")
	}
}

func TestRunClean_DiscoverFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	bd.Git.MainErr = errors.New("nope")
	if err := RunClean(context.Background(), rc); err == nil {
		t.Error("nil err")
	}
}

func TestRunStart_DiscoverFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	bd.Git.MainErr = errors.New("nope")
	if err := RunStart(context.Background(), rc); err == nil {
		t.Error("nil err")
	}
}

func TestRunInfo_DiscoverFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	bd.Git.MainErr = errors.New("nope")
	if err := RunInfo(context.Background(), rc); err == nil {
		t.Error("nil err")
	}
}

func TestRunTrust_StoreAddFailsWithWrap(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	if err := bd.FS.WriteFile("/custom/.wwtr.yml", []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Inject error on the trust registry write.
	bd.FS.InjectError("/trust/trust.yaml", errors.New("io error"))

	err := RunTrust(context.Background(), rc, "/custom/.wwtr.yml")
	if err == nil {
		t.Fatal("nil err, want store Add failure")
	}
}

func TestRunUntrust_StoreRemoveFailsWithWrap(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	// Seed trust so Remove proceeds.
	_ = RunTrust(context.Background(), rc, "")
	bd.FS.InjectError("/trust/trust.yaml", errors.New("io error"))

	err := RunUntrust(context.Background(), rc, "")
	if err == nil {
		t.Fatal("nil err, want store Remove failure")
	}
}

func TestRunInit_PostHookErr_LogsWarn(t *testing.T) {
	// Cover the post-hook error log branch in RunInit. We can't easily make
	// hooks.Run return a non-nil error (post is non-blocking), but a context
	// cancellation that propagates through ShellRunner.Run surfaces as an
	// error in Result.Err which post-stage warns about.
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = true
	seedMainConfig(t, bd.FS, `version: 1
hooks:
  init:
    post:
      - echo post
`)
	bd.Shell.Program(fakes.ShellResult{Err: errors.New("cancelled")})

	// hooks.Run post-stage swallows the error; RunInit should still succeed.
	if err := RunInit(context.Background(), rc); err != nil {
		t.Errorf("RunInit returned err: %v", err)
	}
}

func TestRunInfo_NilStdout(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	rc.Deps.Stdout = nil

	if err := RunInfo(context.Background(), rc); err != nil {
		t.Errorf("RunInfo with nil stdout: %v", err)
	}
}

func TestRunInit_EnsureTrustFails(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	rc.Flags.Yes = false
	bd.Prompter.Confirms = []bool{false}

	if err := RunInit(context.Background(), rc); err == nil ||
		ExitCode(err) != ExitTrust {
		t.Errorf("err=%v ExitCode=%d, want ExitTrust", err, ExitCode(err))
	}
}

func TestRunInit_PrompterErrorOnStateOverwrite(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Yes = false

	// Bootstrap trust so it does not consume our scripted ConfirmErr.
	rcB, bdB := newTestRC(t)
	workerGit(bdB.Git)
	seedMainConfig(t, bdB.FS, minimalConfig)
	rcB.Flags.Yes = true
	if err := RunInit(context.Background(), rcB); err != nil {
		t.Fatal(err)
	}
	trustBytes, _ := bdB.FS.ReadFile("/trust/trust.yaml")
	_ = bd.FS.WriteFile("/trust/trust.yaml", trustBytes, 0o600)

	seedMainConfig(t, bd.FS, minimalConfig)
	_ = bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("x: \"1\"\n"), 0o644)
	bd.Prompter.Confirms = []bool{true}
	bd.Prompter.ConfirmErr = []error{errors.New("io error")}

	err := RunInit(context.Background(), rc)
	if err == nil {
		t.Error("nil err, want prompter failure wrap")
	}
}
