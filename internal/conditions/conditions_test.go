package conditions_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/wailorman/wwtr/internal/conditions"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/vars"
)

func newEval(fs *fakes.FakeFS, env fakes.MapEnv, shell *fakes.RecordShell) *conditions.Evaluator {
	return newEvalWithVars(fs, env, shell, map[string]string{"base_port": "3000"})
}

func newEvalWithVars(fs *fakes.FakeFS, env fakes.MapEnv, shell *fakes.RecordShell, uv map[string]string) *conditions.Evaluator {
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

func mustEval(t *testing.T, ev *conditions.Evaluator, exprStr string) bool {
	t.Helper()
	b, err := ev.Eval(context.Background(), exprStr)
	if err != nil {
		t.Fatalf("Eval(%q) unexpected error: %v", exprStr, err)
	}
	return b
}

func TestFileExistsAndDirExists(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile("/current/file.txt", []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := fs.MkdirAll("/current/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fs.WriteFile("/main/root.txt", []byte("y"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := fs.MkdirAll("/main/.codegraph", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	cases := []struct {
		expr string
		want bool
	}{
		{`fileExists('file.txt')`, true},
		{`fileExists('missing.txt')`, false},
		{`fileExists('sub')`, false},
		{`dirExists('sub')`, true},
		{`dirExists('file.txt')`, false},
		{`dirExists('missing')`, false},
		{`fileExistsInRoot('root.txt')`, true},
		{`fileExistsInRoot('missing.txt')`, false},
		{`dirExistsInRoot('.codegraph')`, true},
		{`dirExistsInRoot('root.txt')`, false},
		{`fileExistsInRoot('.codegraph')`, false},
	}
	for _, tc := range cases {
		if got := mustEval(t, ev, tc.expr); got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestPathResolution(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile("/current/sub/file", []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := fs.WriteFile("/main/cfg/env.tt", []byte("y"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	if !mustEval(t, ev, `fileExists('sub/file')`) {
		t.Errorf("relative path under current worktree not resolved")
	}
	if !mustEval(t, ev, `fileExistsInRoot('cfg/env.tt')`) {
		t.Errorf("relative path under main worktree not resolved")
	}
	if !mustEval(t, ev, `fileExists('/current/sub/file')`) {
		t.Errorf("absolute path not resolved as-is")
	}
	if !mustEval(t, ev, `fileExistsInRoot('/main/cfg/env.tt')`) {
		t.Errorf("absolute path under main not resolved as-is")
	}
}

func TestCommandExists(t *testing.T) {
	t.Parallel()

	t.Run("positive and negative", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		shell := &fakes.RecordShell{}
		shell.Program(
			fakes.ShellResult{ExitCode: 0},
			fakes.ShellResult{ExitCode: 1},
		)
		ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, shell)

		if !mustEval(t, ev, `commandExists('docker')`) {
			t.Errorf("commandExists('docker') = false, want true")
		}
		if mustEval(t, ev, `commandExists('nope')`) {
			t.Errorf("commandExists('nope') = true, want false")
		}
		if len(shell.Calls) != 2 {
			t.Errorf("shell.Calls = %d, want 2", len(shell.Calls))
		}
		wantCmd := "command -v docker"
		if shell.Calls[0] != wantCmd {
			t.Errorf("call[0] = %q, want %q", shell.Calls[0], wantCmd)
		}
	})

	t.Run("caches per name within evaluator", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		shell := &fakes.RecordShell{}
		shell.Program(
			fakes.ShellResult{ExitCode: 0},
			fakes.ShellResult{ExitCode: 1},
		)
		ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, shell)

		if !mustEval(t, ev, `commandExists('docker')`) {
			t.Fatalf("first call: want true")
		}
		if !mustEval(t, ev, `commandExists('docker')`) {
			t.Fatalf("second call: want true (cached)")
		}
		if !mustEval(t, ev, `commandExists('docker')`) {
			t.Fatalf("third call: want true (cached)")
		}
		if len(shell.Calls) != 1 {
			t.Errorf("shell.Calls = %d, want 1 (cache hit)", len(shell.Calls))
		}
		if shell.Results[0].ExitCode != 1 {
			t.Errorf("second programmed result should be unconsumed; got Results=%v", shell.Results)
		}
	})

	t.Run("shell error propagates", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		shell := &fakes.RecordShell{}
		boom := errors.New("shell dead")
		shell.Program(fakes.ShellResult{Err: boom})
		ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, shell)

		_, err := ev.Eval(context.Background(), `commandExists('docker')`)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !errors.Is(err, conditions.ErrEval) {
			t.Errorf("error not wrapped with ErrEval: %v", err)
		}
		if !strings.Contains(err.Error(), "docker") {
			t.Errorf("error doesn't mention command name: %v", err)
		}
	})
}

func TestEnvFunctions(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	env := fakes.MapEnv{Vars: map[string]string{
		"SET_EMPTY": "",
		"FOO":       "bar",
		"NUM":       "3000",
	}}
	ev := newEval(fs, env, &fakes.RecordShell{})

	cases := []struct {
		expr string
		want bool
	}{
		{`envSet('FOO')`, true},
		{`envSet('SET_EMPTY')`, true},
		{`envSet('MISSING')`, false},
		{`envEq('FOO', 'bar')`, true},
		{`envEq('FOO', 'baz')`, false},
		{`envEq('NUM', 3000)`, true},
		{`envEq('NUM', 3001)`, false},
		{`envEq('MISSING', '')`, false},
		{`envEq('SET_EMPTY', '')`, true},
		{`envEq('FOO', nil)`, false},
		{`envEq('MISSING', nil)`, false},
	}
	for _, tc := range cases {
		if got := mustEval(t, ev, tc.expr); got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestPlatformIs(t *testing.T) {
	t.Parallel()
	ev := newEval(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	if got := mustEval(t, ev, fmt.Sprintf(`platformIs('%s')`, runtime.GOOS)); !got {
		t.Errorf("platformIs(current) = false, want true")
	}
	if got := mustEval(t, ev, `platformIs('definitely-not-an-os')`); got {
		t.Errorf("platformIs('definitely-not-an-os') = true, want false")
	}
}

func TestVarEq(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	uv := map[string]string{
		"base_port": "3000",
		"name":      "wwtr",
	}
	ev := newEvalWithVars(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{}, uv)

	cases := []struct {
		expr string
		want bool
	}{
		{`varEq('base_port', 3000)`, true},
		{`varEq('base_port', '3000')`, true},
		{`varEq('base_port', 3001)`, false},
		{`varEq('name', 'wwtr')`, true},
		{`varEq('name', 'other')`, false},
		{`varEq('missing', '')`, false},
		{`varEq('name', nil)`, false},
	}
	for _, tc := range cases {
		if got := mustEval(t, ev, tc.expr); got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestUserVarsAccessibleViaVars(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	uv := map[string]string{"base_port": "3000", "name": "wwtr"}
	ev := newEvalWithVars(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{}, uv)

	cases := []struct {
		expr string
		want bool
	}{
		{`Vars.base_port == "3000"`, true},
		{`Vars['name'] == "wwtr"`, true},
		{`Vars.missing == ""`, true},
		{`Vars.base_port == "4000"`, false},
	}
	for _, tc := range cases {
		if got := mustEval(t, ev, tc.expr); got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestBuiltinVarsDirect(t *testing.T) {
	t.Parallel()
	ev := newEval(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	cases := []struct {
		expr string
		want bool
	}{
		{`Branch == "main"`, true},
		{`Slug == "main"`, true},
		{`Hash == "a1b2c3d4"`, true},
		{`ShortHash == "a1b2c3"`, true},
		{`SafeName == "main-a1b2c3d4"`, true},
		{`WorktreePath == "/current"`, true},
		{`WorktreeName == "current"`, true},
		{`MainWorktreePath == "/main"`, true},
		{`MainWorktreeName == "main"`, true},
		{`Branch == "develop"`, false},
		{`Hash == Branch[:8]`, false},
		{`ShortHash == Hash[:6]`, true},
	}
	for _, tc := range cases {
		if got := mustEval(t, ev, tc.expr); got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestBooleanOperators(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile("/current/.env", []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := fs.MkdirAll("/main/.codegraph", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	shell := &fakes.RecordShell{}
	shell.Program(
		fakes.ShellResult{ExitCode: 0},
		fakes.ShellResult{ExitCode: 0},
		fakes.ShellResult{ExitCode: 1},
	)
	uv := map[string]string{"base_port": "3000"}
	ev := newEvalWithVars(fs, fakes.MapEnv{Vars: map[string]string{}}, shell, uv)

	cases := []struct {
		expr string
		want bool
	}{
		{`dirExistsInRoot('.codegraph') && !dirExists('.codegraph') && commandExists('codegraph')`, true},
		{`commandExists('docker') || commandExists('podman')`, true},
		{`commandExists('docker') || commandExists('nope')`, true},
		{`!commandExists('nope')`, true},
		{`varEq("base_port", 3000) && fileExists(".env")`, true},
		{`fileExists(".env") && varEq("base_port", 3000)`, true},
		{`fileExists("missing") || fileExists(".env")`, true},
		{`fileExists("missing") && fileExists(".env")`, false},
		{`!fileExists(".env")`, false},
		{`!fileExists("missing")`, true},
		{`(fileExists(".env") && varEq("base_port", 3000)) || commandExists('nope')`, true},
	}
	for _, tc := range cases {
		if got := mustEval(t, ev, tc.expr); got != tc.want {
			t.Errorf("Eval(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestMissingVariableFailsCompile(t *testing.T) {
	t.Parallel()
	ev := newEval(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	_, err := ev.Eval(context.Background(), `UnknownVar == "x"`)
	if err == nil {
		t.Fatalf("expected compile error for unknown top-level var, got nil")
	}
	if !errors.Is(err, conditions.ErrEval) {
		t.Errorf("error not wrapped with ErrEval: %v", err)
	}
	if !strings.Contains(err.Error(), "compile") {
		t.Errorf("error should mention compile stage: %v", err)
	}
}

func TestUnknownFunctionFailsCompile(t *testing.T) {
	t.Parallel()
	ev := newEval(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	_, err := ev.Eval(context.Background(), `unknownFunc('x')`)
	if err == nil {
		t.Fatalf("expected compile error for unknown function, got nil")
	}
	if !errors.Is(err, conditions.ErrEval) {
		t.Errorf("error not wrapped with ErrEval: %v", err)
	}
}

func TestSyntaxErrorFailsCompile(t *testing.T) {
	t.Parallel()
	ev := newEval(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	_, err := ev.Eval(context.Background(), `((`)
	if err == nil {
		t.Fatalf("expected compile error for bad syntax, got nil")
	}
	if !errors.Is(err, conditions.ErrEval) {
		t.Errorf("error not wrapped with ErrEval: %v", err)
	}
}

func TestNonBoolExpressionFailsCompile(t *testing.T) {
	t.Parallel()
	ev := newEval(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	_, err := ev.Eval(context.Background(), `"just a string"`)
	if err == nil {
		t.Fatalf("expected compile error for non-bool expression, got nil")
	}
	if !errors.Is(err, conditions.ErrEval) {
		t.Errorf("error not wrapped with ErrEval: %v", err)
	}
}

func TestRuntimeErrorWrapped(t *testing.T) {
	t.Parallel()
	t.Run("panic in function is recovered as run error", func(t *testing.T) {
		t.Parallel()
		bv := vars.BuiltinVars{Branch: "abc"}
		ev := conditions.New(
			fakes.NewFakeFS(),
			fakes.MapEnv{Vars: map[string]string{}},
			&fakes.RecordShell{},
			"/c", "/m", bv, nil,
		)
		// expr-lang panics on out-of-bounds slice index at runtime; the run
		// branch wraps that as an ErrEval.
		_, err := ev.Eval(context.Background(), `Branch[99] == "x"`)
		if err == nil {
			t.Fatalf("expected run error, got nil")
		}
		if !errors.Is(err, conditions.ErrEval) {
			t.Errorf("error not wrapped with ErrEval: %v", err)
		}
	})
}

func TestFSErrorPropagates(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	fs.InjectError("/current/x", errors.New("permission denied"))
	ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	_, err := ev.Eval(context.Background(), `fileExists('x')`)
	if err == nil {
		t.Fatalf("expected error from FS predicate, got nil")
	}
	if !errors.Is(err, conditions.ErrEval) {
		t.Errorf("error not wrapped with ErrEval: %v", err)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error should contain underlying message: %v", err)
	}
}

func TestFSErrorPropagatesFromEachPredicate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		setup    func(*fakes.FakeFS)
		expr     string
		contains string
	}{
		{
			name:     "fileExists",
			setup:    func(fs *fakes.FakeFS) { fs.InjectError("/current/p", errors.New("io")) },
			expr:     `fileExists('p')`,
			contains: "fileExists",
		},
		{
			name:     "dirExists",
			setup:    func(fs *fakes.FakeFS) { fs.InjectError("/current/p", errors.New("io")) },
			expr:     `dirExists('p')`,
			contains: "dirExists",
		},
		{
			name:     "fileExistsInRoot",
			setup:    func(fs *fakes.FakeFS) { fs.InjectError("/main/p", errors.New("io")) },
			expr:     `fileExistsInRoot('p')`,
			contains: "fileExistsInRoot",
		},
		{
			name:     "dirExistsInRoot",
			setup:    func(fs *fakes.FakeFS) { fs.InjectError("/main/p", errors.New("io")) },
			expr:     `dirExistsInRoot('p')`,
			contains: "dirExistsInRoot",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fs := fakes.NewFakeFS()
			tc.setup(fs)
			ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

			_, err := ev.Eval(context.Background(), tc.expr)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("error %q should contain %q", err.Error(), tc.contains)
			}
		})
	}
}

func TestErrNotExistDoesNotPropagate(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	fs.InjectError("/current/x", os.ErrNotExist)
	ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	b, err := ev.Eval(context.Background(), `fileExists('x')`)
	if err != nil {
		t.Fatalf("ErrNotExist should not propagate, got: %v", err)
	}
	if b {
		t.Errorf("fileExists with ErrNotExist should be false")
	}
}

func TestConcurrentEval(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	shell := &fakes.RecordShell{}
	results := make([]fakes.ShellResult, 32)
	for i := range results {
		results[i] = fakes.ShellResult{ExitCode: 0}
	}
	shell.Program(results...)
	ev := newEval(fs, fakes.MapEnv{Vars: map[string]string{}}, shell)

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			b, err := ev.Eval(context.Background(), `commandExists('docker') && Branch == "main"`)
			if err != nil {
				t.Errorf("Eval error: %v", err)
				return
			}
			if !b {
				t.Errorf("expected true")
			}
		}()
	}
	wg.Wait()
}

func TestEmptyStringExprFailsCompile(t *testing.T) {
	t.Parallel()
	ev := newEval(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{})

	_, err := ev.Eval(context.Background(), ``)
	if err == nil {
		t.Fatalf("expected error for empty expression, got nil")
	}
	if !errors.Is(err, conditions.ErrEval) {
		t.Errorf("error not wrapped with ErrEval: %v", err)
	}
}

func TestNewCopiesUserVars(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	uv := map[string]string{"k": "v"}
	bv := vars.BuiltinVars{}
	ev := conditions.New(fs, fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{}, "/c", "/m", bv, uv)

	uv["k"] = "mutated"
	uv["injected"] = "x"

	if got := mustEval(t, ev, `varEq('k', 'v')`); !got {
		t.Errorf("user vars were not copied: varEq('k','v') = false")
	}
	if got := mustEval(t, ev, `varEq('injected', 'x')`); got {
		t.Errorf("post-construction mutation should not leak in")
	}
}

func TestNewAcceptsNilUserVars(t *testing.T) {
	t.Parallel()
	bv := vars.BuiltinVars{}
	ev := conditions.New(fakes.NewFakeFS(), fakes.MapEnv{Vars: map[string]string{}}, &fakes.RecordShell{}, "/c", "/m", bv, nil)
	if got := mustEval(t, ev, `varEq('anything', 'anything')`); got {
		t.Errorf("nil userVars: varEq should be false")
	}
	if got := mustEval(t, ev, `Vars.foo == ""`); !got {
		t.Errorf("nil userVars: Vars.foo should be empty")
	}
}
