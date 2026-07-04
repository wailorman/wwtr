package hooks_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/conditions"
	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/hooks"
	"github.com/wailorman/wwtr/internal/vars"
)

// newOpts builds a ready Options with the most common test fixtures. Callers
// mutate fields before passing to hooks.Run.
func newOpts(t *testing.T) (hooks.Options, *fakes.RecordShell, *fakes.FakeFS, fakes.MapEnv, *bytes.Buffer, *bytes.Buffer, *slog.Logger) {
	t.Helper()
	shell := &fakes.RecordShell{}
	fs := fakes.NewFakeFS()
	env := fakes.MapEnv{Vars: map[string]string{}}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bv := vars.BuiltinVars{
		Branch:           "main",
		Slug:             "main",
		Hash:             "a1b2c3d4",
		ShortHash:        "a1b2c3",
		SafeName:         "main-a1b2c3d4",
		WorktreePath:     "/current",
		WorktreeName:     "current",
		MainWorktreePath: "/main",
		MainWorktreeName: "main",
	}
	return hooks.Options{
		Shell:    shell,
		FS:       fs,
		Env:      env,
		Log:      log,
		Stdout:   stdout,
		Stderr:   stderr,
		Builtin:  bv,
		UserVars: map[string]string{},
	}, shell, fs, env, stdout, stderr, log
}

// newCond returns a real conditions.Evaluator wired to the fakes. Use it to
// exercise the `when:` path end-to-end.
func newCond(fs *fakes.FakeFS, env fakes.MapEnv, shell *fakes.RecordShell, uv map[string]string) *conditions.Evaluator {
	bv := vars.BuiltinVars{
		Branch:           "main",
		Slug:             "main",
		Hash:             "a1b2c3d4",
		ShortHash:        "a1b2c3",
		SafeName:         "main-a1b2c3d4",
		WorktreePath:     "/current",
		WorktreeName:     "current",
		MainWorktreePath: "/main",
		MainWorktreeName: "main",
	}
	return conditions.New(fs, env, shell, "/current", "/main", bv, uv)
}

func TestEmptyHookList(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, nil)
	if err != nil {
		t.Fatalf("empty list should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 results, got %d", len(res))
	}
	if len(shell.Calls) != 0 {
		t.Errorf("expected 0 shell calls, got %d", len(shell.Calls))
	}
}

func TestNoHooksFlag(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	opts.NoHooks = true
	hooksList := []config.Hook{
		{Run: "echo hello"},
		{Run: "echo world"},
	}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err != nil {
		t.Fatalf("--no-hooks should not error: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("expected 0 results with --no-hooks, got %d", len(res))
	}
	if len(shell.Calls) != 0 {
		t.Errorf("expected 0 shell calls with --no-hooks, got %d", len(shell.Calls))
	}
}

func TestSimpleStringHook(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{Stdout: []byte("ok\n")})

	hooksList := []config.Hook{{Run: "echo hello"}}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	if len(shell.Calls) != 1 {
		t.Fatalf("expected 1 shell call, got %d", len(shell.Calls))
	}
	if shell.Calls[0] != "echo hello" {
		t.Errorf("call = %q, want %q", shell.Calls[0], "echo hello")
	}
	if res[0].Output != "ok" {
		t.Errorf("output = %q, want %q", res[0].Output, "ok")
	}
}

func TestRunWithTemplateInterpolation(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	opts.UserVars = map[string]string{"mail_container": "wk_spectator_mail_xyz"}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{Run: "docker stop {{ .Vars.mail_container }}"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "docker stop wk_spectator_mail_xyz"
	if shell.Calls[0] != want {
		t.Errorf("call = %q, want %q", shell.Calls[0], want)
	}
}

func TestRunWithBuiltinVar(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{Run: "echo {{ .Slug }}-{{ .Hash }}"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "echo main-a1b2c3d4"
	if shell.Calls[0] != want {
		t.Errorf("call = %q, want %q", shell.Calls[0], want)
	}
}

func TestWhenTrueRuns(t *testing.T) {
	t.Parallel()
	opts, shell, fs, env, _, _, _ := newOpts(t)
	opts.Cond = newCond(fs, env, shell, opts.UserVars)
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{Run: "echo yes", When: "true"}}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shell.Calls) != 1 {
		t.Errorf("expected 1 call, got %d", len(shell.Calls))
	}
	if res[0].Skipped {
		t.Errorf("hook should not be skipped")
	}
}

func TestWhenFalseSkips(t *testing.T) {
	t.Parallel()
	opts, shell, fs, env, _, _, _ := newOpts(t)
	opts.Cond = newCond(fs, env, shell, opts.UserVars)

	hooksList := []config.Hook{{Run: "echo no", When: "false"}}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shell.Calls) != 0 {
		t.Errorf("expected 0 calls when=false, got %d", len(shell.Calls))
	}
	if !res[0].Skipped {
		t.Errorf("hook should be skipped")
	}
}

func TestWhenErrorPreAborts(t *testing.T) {
	t.Parallel()
	opts, shell, fs, env, _, _, _ := newOpts(t)
	opts.Cond = newCond(fs, env, shell, opts.UserVars)

	hooksList := []config.Hook{{Run: "echo", When: "((syntax-bad"}}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected error from bad when expression")
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("expected ErrAborted, got %v", err)
	}
	if len(shell.Calls) != 0 {
		t.Errorf("shell should not be called on when error")
	}
}

func TestWhenErrorPostWarnsContinues(t *testing.T) {
	t.Parallel()
	opts, shell, fs, env, _, _, _ := newOpts(t)
	opts.Cond = newCond(fs, env, shell, opts.UserVars)
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{
		{Run: "echo first", When: "((bad"},
		{Run: "echo second"},
	}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePost, hooksList)
	if err != nil {
		t.Fatalf("post-stage should not return top-level error: %v", err)
	}
	if !res[0].Skipped {
		t.Errorf("first (when-failed) should be marked skipped")
	}
	if len(shell.Calls) != 1 {
		t.Errorf("second hook should still run, got %d calls", len(shell.Calls))
	}
	if shell.Calls[0] != "echo second" {
		t.Errorf("call = %q, want %q", shell.Calls[0], "echo second")
	}
}

func TestWhenReferencesVars(t *testing.T) {
	t.Parallel()
	opts, shell, fs, env, _, _, _ := newOpts(t)
	opts.UserVars = map[string]string{"base_port": "3000"}
	opts.Cond = newCond(fs, env, shell, opts.UserVars)
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{
		{Run: "echo match", When: `varEq("base_port", 3000)`},
		{Run: "echo nomatch", When: `varEq("base_port", 9999)`},
	}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shell.Calls) != 1 {
		t.Errorf("expected 1 call (varEq=true only), got %d", len(shell.Calls))
	}
	if shell.Calls[0] != "echo match" {
		t.Errorf("call = %q, want %q", shell.Calls[0], "echo match")
	}
	if !res[1].Skipped {
		t.Errorf("second hook should be skipped")
	}
}

func TestCondNilAlwaysRuns(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{}, fakes.ShellResult{})

	hooksList := []config.Hook{
		{Run: "echo a", When: "varEq('x', 1)"}, // Cond is nil → ignored
		{Run: "echo b", When: "false"},
	}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shell.Calls) != 2 {
		t.Errorf("Cond=nil should always run; got %d calls", len(shell.Calls))
	}
}

func TestLoadEnvAppliesToSubsequentCommands(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	envContent := []byte("FOO=bar\nBAZ=qux\n# comment\n\n")
	if err := fs.WriteFile("/current/.env", envContent, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{}, fakes.ShellResult{})

	hooksList := []config.Hook{
		{LoadEnv: ".env"},
		{Run: "echo $FOO"},
	}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shell.Calls) != 1 {
		t.Fatalf("expected 1 shell call (load_env does not shell out), got %d", len(shell.Calls))
	}
	cmd := shell.Calls[0]
	if !strings.Contains(cmd, "export FOO='bar'") {
		t.Errorf("missing FOO export in cmd: %q", cmd)
	}
	if !strings.Contains(cmd, "export BAZ='qux'") {
		t.Errorf("missing BAZ export in cmd: %q", cmd)
	}
	if !strings.HasSuffix(cmd, "echo $FOO") {
		t.Errorf("cmd should end with the run command: %q", cmd)
	}
}

func TestLoadEnvScopedToStage(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	if err := fs.WriteFile("/current/.env", []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{}, fakes.ShellResult{})

	hooksList := []config.Hook{{LoadEnv: ".env"}, {Run: "echo $FOO"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Second call to Run with a fresh hook list: env prefix is per-invocation,
	// so a new stage sees no load_env exports.
	hooksList2 := []config.Hook{{Run: "echo $FOO"}}
	shell.Program(fakes.ShellResult{})
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shell.Calls[1] != "echo $FOO" {
		t.Errorf("second Run should not carry load_env prefix; got %q", shell.Calls[1])
	}
}

func TestLoadEnvTemplateRendering(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	opts.UserVars = map[string]string{"name": "wwtr"}
	envContent := []byte("CONTAINER=wk_{{ .Vars.name }}_x\n")
	if err := fs.WriteFile("/current/.env.tt", envContent, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{
		{LoadEnv: ".env.tt"},
		{Run: "echo"},
	}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(shell.Calls[0], "export CONTAINER='wk_wwtr_x'") {
		t.Errorf("value should be rendered: %q", shell.Calls[0])
	}
}

func TestLoadEnvTemplatedPath(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	opts.UserVars = map[string]string{"name": "wwtr"}
	if err := fs.WriteFile("/current/wwtr.env", []byte("FOO=1\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{
		{LoadEnv: "{{ .Vars.name }}.env"},
		{Run: "echo"},
	}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(shell.Calls[0], "export FOO='1'") {
		t.Errorf("expected FOO export from templated path: %q", shell.Calls[0])
	}
}

func TestLoadEnvMissingFileFails(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)

	hooksList := []config.Hook{
		{LoadEnv: "missing.env"},
		{Run: "echo should-not-run"},
	}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected error for missing load_env file")
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("expected ErrAborted, got %v", err)
	}
	if len(shell.Calls) != 0 {
		t.Errorf("subsequent hook should not run; got %d calls", len(shell.Calls))
	}
}

func TestLoadEnvParseErrorFails(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	if err := fs.WriteFile("/current/.env", []byte("BAD-NO-EQUALS\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hooksList := []config.Hook{{LoadEnv: ".env"}}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("expected ErrAborted, got %v", err)
	}
	if len(shell.Calls) != 0 {
		t.Errorf("shell should not be called on parse error, got %d calls", len(shell.Calls))
	}
}

func TestLoadEnvPostStageContinues(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{
		{LoadEnv: "missing.env"},
		{Run: "echo ok"},
	}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePost, hooksList)
	if err != nil {
		t.Fatalf("post-stage should not return top-level error: %v", err)
	}
	if len(shell.Calls) != 1 {
		t.Errorf("second hook should still run, got %d calls", len(shell.Calls))
	}
	if shell.Calls[0] != "echo ok" {
		t.Errorf("call = %q, want %q", shell.Calls[0], "echo ok")
	}
}

func TestMultilineRunIsOneShellCall(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{})

	multiline := "echo line1\necho line2\necho line3"
	hooksList := []config.Hook{{Run: multiline}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shell.Calls) != 1 {
		t.Fatalf("expected 1 shell call for multiline, got %d", len(shell.Calls))
	}
	if shell.Calls[0] != multiline {
		t.Errorf("call = %q, want %q", shell.Calls[0], multiline)
	}
}

func TestPreStageAbortsOnError(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(
		fakes.ShellResult{ExitCode: 1, Stderr: []byte("permission denied")},
		fakes.ShellResult{}, // should NOT be consumed
	)

	hooksList := []config.Hook{
		{Run: "false-cmd"},
		{Run: "echo never"},
	}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("expected ErrAborted, got %v", err)
	}
	if len(shell.Calls) != 1 {
		t.Errorf("only first hook should run, got %d calls", len(shell.Calls))
	}
	if len(res) != 1 {
		t.Errorf("results should include only first hook, got %d", len(res))
	}
	if res[0].Err == nil {
		t.Errorf("first result should carry the error")
	}
	if !strings.Contains(res[0].Err.Error(), "permission denied") {
		t.Errorf("error should contain stderr: %v", res[0].Err)
	}
	if !strings.Contains(res[0].Err.Error(), "code 1") {
		t.Errorf("error should mention exit code: %v", res[0].Err)
	}
}

func TestPostStageContinuesOnError(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(
		fakes.ShellResult{ExitCode: 1, Stderr: []byte("oops")},
		fakes.ShellResult{},
	)

	hooksList := []config.Hook{
		{Run: "first-fails"},
		{Run: "second-runs"},
	}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePost, hooksList)
	if err != nil {
		t.Fatalf("post-stage should not return top-level error: %v", err)
	}
	if len(shell.Calls) != 2 {
		t.Errorf("both hooks should run, got %d calls", len(shell.Calls))
	}
	if res[0].Err == nil {
		t.Errorf("first result should carry the error")
	}
	if res[1].Err != nil {
		t.Errorf("second result should be clean")
	}
}

func TestDryRunNoShellCalls(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	opts.DryRun = true

	hooksList := []config.Hook{
		{Run: "echo a"},
		{Run: "echo b"},
	}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err != nil {
		t.Fatalf("dry-run should not error: %v", err)
	}
	if len(shell.Calls) != 0 {
		t.Errorf("dry-run should make 0 shell calls, got %d", len(shell.Calls))
	}
	if !res[0].Skipped {
		t.Errorf("dry-run result should be marked skipped")
	}
	if res[0].Output != "echo a" {
		t.Errorf("dry-run output should be the would-be cmd: %q", res[0].Output)
	}
}

func TestDryRunStillAppliesLoadEnvPrefix(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	opts.DryRun = true
	if err := fs.WriteFile("/current/.env", []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hooksList := []config.Hook{
		{LoadEnv: ".env"},
		{Run: "echo $FOO"},
	}
	res, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err != nil {
		t.Fatalf("dry-run should not error: %v", err)
	}
	if len(shell.Calls) != 0 {
		t.Errorf("dry-run should make 0 shell calls, got %d", len(shell.Calls))
	}
	// load_env still executes (it's not a shell call); it extends the prefix
	// that gets logged for the dry-run hook.
	if !strings.Contains(res[1].Output, "export FOO='bar'") {
		t.Errorf("dry-run output should include load_env prefix: %q", res[1].Output)
	}
	if !strings.HasSuffix(res[1].Output, "echo $FOO") {
		t.Errorf("dry-run output should end with the run cmd: %q", res[1].Output)
	}
}

func TestShellError(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	boom := errors.New("exec failed")
	shell.Program(fakes.ShellResult{Err: boom})

	hooksList := []config.Hook{{Run: "anything"}}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error chain should contain original err: %v", err)
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("error should wrap ErrAborted: %v", err)
	}
}

func TestTemplateRenderErrorPre(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)

	hooksList := []config.Hook{{Run: "echo {{ .Vars.missing_var }}"}}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected template render error")
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("expected ErrAborted, got %v", err)
	}
	if len(shell.Calls) != 0 {
		t.Errorf("shell should not be called on render error")
	}
}

func TestTemplateRenderErrorPost(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{
		{Run: "echo {{ .Vars.missing }}"},
		{Run: "echo ok"},
	}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePost, hooksList)
	if err != nil {
		t.Fatalf("post-stage should not return top-level error: %v", err)
	}
	if len(shell.Calls) != 1 {
		t.Errorf("second hook should still run, got %d calls", len(shell.Calls))
	}
	if shell.Calls[0] != "echo ok" {
		t.Errorf("call = %q, want %q", shell.Calls[0], "echo ok")
	}
}

func TestStdoutStderrPassthrough(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, stdout, stderr, _ := newOpts(t)
	shell.Program(fakes.ShellResult{
		Stdout: []byte("out-line\n"),
		Stderr: []byte("err-line\n"),
	})

	hooksList := []config.Hook{{Run: "echo"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "out-line\n" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "out-line\n")
	}
	if stderr.String() != "err-line\n" {
		t.Errorf("stderr = %q, want %q", stderr.String(), "err-line\n")
	}
}

func TestStdoutAddsTrailingNewline(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, stdout, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{Stdout: []byte("no-newline")})

	hooksList := []config.Hook{{Run: "x"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout.String() != "no-newline\n" {
		t.Errorf("expected trailing newline added; got %q", stdout.String())
	}
}

func TestStdoutNilWriter(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	opts.Stdout = nil
	opts.Stderr = nil
	shell.Program(fakes.ShellResult{Stdout: []byte("x"), Stderr: []byte("y")})

	hooksList := []config.Hook{{Run: "x"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("nil writers should not cause errors: %v", err)
	}
}

func TestDefaultLoggerWhenNil(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	opts.Log = nil
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{Run: "echo"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("nil logger should not cause errors: %v", err)
	}
}

func TestContextCancellationPropagates(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before run

	shell.Program(fakes.ShellResult{Err: context.Canceled})

	hooksList := []config.Hook{{Run: "long-running"}}
	_, err := hooks.Run(ctx, opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected error from canceled ctx shell")
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("pre-stage error should wrap ErrAborted: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error chain should preserve context.Canceled: %v", err)
	}
}

func TestShellEscapeInLoadEnv(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	// value containing a single quote — must be escaped via `'\''`.
	if err := fs.WriteFile("/current/.env", []byte("SECRET=it's a 'test'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{LoadEnv: ".env"}, {Run: "run"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(shell.Calls[0], "export SECRET='it'\\''s a '\\''test'\\'''") {
		t.Errorf("single quotes not properly escaped: %q", shell.Calls[0])
	}
}

func TestOrderedKeys(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	// reverse-alphabetical in the file → prefix must still come out alpha.
	if err := fs.WriteFile("/current/.env", []byte("ZEBRA=1\nALPHA=2\nMIKE=3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{LoadEnv: ".env"}, {Run: "run"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmd := shell.Calls[0]
	alphaIdx := strings.Index(cmd, "ALPHA")
	mikeIdx := strings.Index(cmd, "MIKE")
	zebraIdx := strings.Index(cmd, "ZEBRA")
	if !(alphaIdx < mikeIdx && mikeIdx < zebraIdx) {
		t.Errorf("keys not in lexicographic order in cmd: %q", cmd)
	}
}

func TestMultipleHooksSequential(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{}, fakes.ShellResult{}, fakes.ShellResult{})

	hooksList := []config.Hook{
		{Run: "first"},
		{Run: "second"},
		{Run: "third"},
	}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePost, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(shell.Calls) != 3 {
		t.Fatalf("expected 3 calls in order, got %d", len(shell.Calls))
	}
	if shell.Calls[0] != "first" || shell.Calls[1] != "second" || shell.Calls[2] != "third" {
		t.Errorf("calls not in order: %v", shell.Calls)
	}
}

func TestLoadEnvWithQuotedValues(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	content := []byte(`DOUBLE="quoted value"` + "\n" + `SINGLE='another'` + "\n")
	if err := fs.WriteFile("/current/.env", content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{LoadEnv: ".env"}, {Run: "run"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cmd := shell.Calls[0]
	if !strings.Contains(cmd, `export DOUBLE='quoted value'`) {
		t.Errorf("double-quoted value not unquoted: %q", cmd)
	}
	if !strings.Contains(cmd, `export SINGLE='another'`) {
		t.Errorf("single-quoted value not unquoted: %q", cmd)
	}
}

func TestLoadEnvWithBlankAndComments(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	content := []byte("# header comment\n\n   \nKEY=val\n# trailing comment\n")
	if err := fs.WriteFile("/current/.env", content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{LoadEnv: ".env"}, {Run: "run"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(shell.Calls[0], "export KEY='val'") {
		t.Errorf("only KEY should be present: %q", shell.Calls[0])
	}
}

func TestLoadEnvInvalidKeyFails(t *testing.T) {
	t.Parallel()
	opts, _, fs, _, _, _, _ := newOpts(t)
	if err := fs.WriteFile("/current/.env", []byte("1ABC=val\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	hooksList := []config.Hook{{LoadEnv: ".env"}}
	_, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList)
	if err == nil {
		t.Fatalf("expected invalid-key error")
	}
	if !errors.Is(err, hooks.ErrAborted) {
		t.Errorf("expected ErrAborted, got %v", err)
	}
}

func TestLoadEnvEmptyFile(t *testing.T) {
	t.Parallel()
	opts, shell, fs, _, _, _, _ := newOpts(t)
	if err := fs.WriteFile("/current/.env", []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	shell.Program(fakes.ShellResult{})

	hooksList := []config.Hook{{LoadEnv: ".env"}, {Run: "echo"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shell.Calls[0] != "echo" {
		t.Errorf("empty load_env should yield no prefix; got %q", shell.Calls[0])
	}
}

func TestNoTemplateShortcircuit(t *testing.T) {
	t.Parallel()
	opts, shell, _, _, _, _, _ := newOpts(t)
	shell.Program(fakes.ShellResult{})

	// Plain string with no `{{` should pass through verbatim, no template work.
	hooksList := []config.Hook{{Run: "git status --short"}}
	if _, err := hooks.Run(context.Background(), opts, hooks.StagePre, hooksList); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shell.Calls[0] != "git status --short" {
		t.Errorf("call = %q, want verbatim", shell.Calls[0])
	}
}
