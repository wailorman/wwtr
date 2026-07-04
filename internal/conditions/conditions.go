// Package conditions evaluates `when:` expressions on hooks (PLAN §8) against
// the resolved variable set and worktree state. It uses expr-lang/expr — a
// different engine from the Sprig-based vars.value (PLAN §20): the split is
// intentional, expr-lang gives us short-circuit && / || and a small typed
// function surface that reads naturally in YAML.
package conditions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/vars"
)

// ErrEval wraps any error raised while compiling or running an expression.
// Callers use errors.Is to distinguish a bad/failed condition from a
// hook-skip decision.
var ErrEval = errors.New("conditions: evaluation error")

// Evaluator answers `when:` questions for one command invocation. Construct it
// once with the resolved variables and worktree paths; Eval may be called many
// times, concurrently is safe (the command-exists cache is mutex-guarded).
type Evaluator struct {
	fs          di.FS
	env         di.Env
	shell       di.ShellRunner
	currentPath string
	mainPath    string
	userVars    map[string]string
	builtin     vars.BuiltinVars

	cmdCacheMu sync.Mutex
	cmdCache   map[string]bool
}

// New constructs an Evaluator. userVars may be nil; it is copied so later
// mutation by the caller does not leak into evaluated expressions.
func New(
	fs di.FS,
	env di.Env,
	shell di.ShellRunner,
	currentPath, mainPath string,
	v vars.BuiltinVars,
	userVars map[string]string,
) *Evaluator {
	copied := make(map[string]string, len(userVars))
	for k, val := range userVars {
		copied[k] = val
	}
	return &Evaluator{
		fs:          fs,
		env:         env,
		shell:       shell,
		currentPath: currentPath,
		mainPath:    mainPath,
		userVars:    copied,
		builtin:     v,
		cmdCache:    map[string]bool{},
	}
}

// callState carries the per-Eval context plus the first non-ErrNotExist error
// raised by an FS predicate. expr-lang functions can only return values, so
// errors are stashed here and surfaced by Eval after Run returns.
type callState struct {
	ctx context.Context
	mu  sync.Mutex
	err error
}

func (c *callState) setErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
	}
}

// Eval compiles and runs exprStr. Compile errors, runtime errors, and any FS
// error (other than ErrNotExist) raised by a builtin predicate are wrapped
// with ErrEval so callers can distinguish a failed condition from a false one.
func (e *Evaluator) Eval(ctx context.Context, exprStr string) (bool, error) {
	cs := &callState{ctx: ctx}
	env := e.buildEnv(cs)

	program, err := expr.Compile(exprStr, expr.Env(env), expr.AsBool())
	if err != nil {
		return false, fmt.Errorf("%w: compile %q: %v", ErrEval, exprStr, err)
	}
	out, err := expr.Run(program, env)
	if err != nil {
		return false, fmt.Errorf("%w: run %q: %v", ErrEval, exprStr, err)
	}
	if cs.err != nil {
		return false, fmt.Errorf("%w: %q: %v", ErrEval, exprStr, cs.err)
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("%w: %q returned %T, expected bool", ErrEval, exprStr, out)
	}
	return b, nil
}

func (e *Evaluator) buildEnv(cs *callState) map[string]any {
	return map[string]any{
		"Branch":           e.builtin.Branch,
		"Slug":             e.builtin.Slug,
		"Hash":             e.builtin.Hash,
		"ShortHash":        e.builtin.ShortHash,
		"SafeName":         e.builtin.SafeName,
		"WorktreePath":     e.builtin.WorktreePath,
		"WorktreeName":     e.builtin.WorktreeName,
		"MainWorktreePath": e.builtin.MainWorktreePath,
		"MainWorktreeName": e.builtin.MainWorktreeName,
		"Vars":             e.userVars,
		"fileExists":       func(path string) bool { return e.fileExists(cs, path) },
		"dirExists":        func(path string) bool { return e.dirExists(cs, path) },
		"fileExistsInRoot": func(path string) bool { return e.fileExistsInRoot(cs, path) },
		"dirExistsInRoot":  func(path string) bool { return e.dirExistsInRoot(cs, path) },
		"commandExists":    func(name string) bool { return e.commandExists(cs, name) },
		"envSet":           e.envSet,
		"envEq":            e.envEq,
		"platformIs":       e.platformIs,
		"varEq":            e.varEq,
	}
}

func (e *Evaluator) resolveCurrent(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(e.currentPath, path)
}

func (e *Evaluator) resolveMain(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(e.mainPath, path)
}

func (e *Evaluator) fileExists(cs *callState, path string) bool {
	info, err := e.fs.Stat(e.resolveCurrent(path))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			cs.setErr(fmt.Errorf("fileExists(%q): %w", path, err))
		}
		return false
	}
	return !info.IsDir()
}

func (e *Evaluator) dirExists(cs *callState, path string) bool {
	info, err := e.fs.Stat(e.resolveCurrent(path))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			cs.setErr(fmt.Errorf("dirExists(%q): %w", path, err))
		}
		return false
	}
	return info.IsDir()
}

func (e *Evaluator) fileExistsInRoot(cs *callState, path string) bool {
	info, err := e.fs.Stat(e.resolveMain(path))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			cs.setErr(fmt.Errorf("fileExistsInRoot(%q): %w", path, err))
		}
		return false
	}
	return !info.IsDir()
}

func (e *Evaluator) dirExistsInRoot(cs *callState, path string) bool {
	info, err := e.fs.Stat(e.resolveMain(path))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			cs.setErr(fmt.Errorf("dirExistsInRoot(%q): %w", path, err))
		}
		return false
	}
	return info.IsDir()
}

// commandExists shells out `command -v <name>`: exit 0 means present. Results
// are cached per name on the Evaluator so repeated references in one run make
// at most one subprocess each.
func (e *Evaluator) commandExists(cs *callState, name string) bool {
	e.cmdCacheMu.Lock()
	if v, ok := e.cmdCache[name]; ok {
		e.cmdCacheMu.Unlock()
		return v
	}
	e.cmdCacheMu.Unlock()

	_, _, code, err := e.shell.Run(cs.ctx, "command -v "+name)
	if err != nil {
		cs.setErr(fmt.Errorf("commandExists(%q): %w", name, err))
		return false
	}
	result := code == 0

	e.cmdCacheMu.Lock()
	e.cmdCache[name] = result
	e.cmdCacheMu.Unlock()
	return result
}

func (e *Evaluator) envSet(name string) bool {
	_, ok := e.env.Lookup(name)
	return ok
}

func (e *Evaluator) envEq(name string, val any) bool {
	v, ok := e.env.Lookup(name)
	if !ok {
		return false
	}
	return v == stringify(val)
}

func (e *Evaluator) platformIs(name string) bool {
	return runtime.GOOS == name
}

func (e *Evaluator) varEq(name string, val any) bool {
	v, ok := e.userVars[name]
	if !ok {
		return false
	}
	return v == stringify(val)
}

// stringify normalises a YAML-decoded scalar (string, int, bool, …) to the
// string form wwtr stores everywhere — vars resolve to strings.
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprintf("%v", v)
	}
}
