// Package hooks executes the `pre_<cmd>` / `post_<cmd>` hook lists defined in
// PLAN §9 and §12. Two execution models coexist (PLAN §20):
//
//   - shell commands (`run:` or a bare string): the rendered command string is
//     passed verbatim to di.ShellRunner, which on Unix is `sh -c` and on
//     Windows is `cmd /c`. Multi-line `run: |` blocks become one shell call.
//   - `load_env: <path>`: a dotenv file parsed by wwtr (not the shell) whose
//     keys are then exposed to subsequent commands in the SAME stage.
//
// `when:` expressions are evaluated by [github.com/wailorman/wwtr/internal/conditions]
// (expr-lang); `run:` and `load_env:` paths/values are rendered by
// [github.com/wailorman/wwtr/internal/template] (text/template + Sprig). The two
// engines are intentionally separate (PLAN §20).
//
// Blocking semantics (PLAN §9): pre-hooks abort the calling command on the
// first error (Run returns ErrAborted); post-hooks log a warning and continue,
// returning no top-level error.
package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wailorman/wwtr/internal/conditions"
	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/template"
	"github.com/wailorman/wwtr/internal/vars"
)

// ErrAborted is returned by Run when a pre-stage hook fails. Callers map this
// to exit code 5 (PLAN §20).
var ErrAborted = errors.New("hooks: pre-hook failed")

// Stage selects the pre or post half of a command's hook list. Only the
// blocking semantics differ between the two (PLAN §9).
type Stage int

const (
	StagePre Stage = iota
	StagePost
)

// Options bundles the per-invocation inputs. Construct one in the calling
// command (internal/app/*) and hand it to Run alongside the hook list.
type Options struct {
	Shell    di.ShellRunner
	FS       di.FS
	Env      di.Env
	Log      *slog.Logger
	Stdout   io.Writer
	Stderr   io.Writer
	Cond     *conditions.Evaluator
	Builtin  vars.BuiltinVars
	UserVars map[string]string
	DryRun   bool
	NoHooks  bool
}

// Result describes what happened for one hook. Skipped is true when the hook
// was filtered out by `when:` or skipped due to DryRun; Err carries any
// non-nil shell or parse error.
type Result struct {
	Hook    config.Hook
	Skipped bool
	Err     error
	Output  string
}

// Run executes hooks in declaration order for the given stage. The stage only
// affects error handling: pre-stage aborts on the first error and returns
// ErrAborted wrapped over the underlying failure; post-stage logs a warning and
// continues, returning a nil error.
//
// `when:`, `run:` template rendering, and `load_env:` application all happen
// here. Loaded env vars are scoped to the stage: they are emitted as `export`
// prefixes on each subsequent `run:` in the same call (PLAN §20), because
// di.ShellRunner exposes no env-injection surface and wwtr does not mutate the
// parent process environment.
func Run(ctx context.Context, opts Options, stage Stage, hooks []config.Hook) ([]Result, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	results := make([]Result, 0, len(hooks))

	if opts.NoHooks {
		log.Debug("hooks: --no-hooks set, skipping all hooks")
		return results, nil
	}

	envPrefix := ""
	for _, h := range hooks {
		res, newPrefix := executeHook(ctx, opts, log, stage, h, envPrefix)
		envPrefix = newPrefix
		results = append(results, res)

		if res.Err == nil {
			continue
		}

		if stage == StagePre {
			return results, fmt.Errorf("%w: %w", ErrAborted, res.Err)
		}
		log.Warn("post-hook failed (non-blocking)", "hook", describe(h), "err", res.Err)
	}
	return results, nil
}

// executeHook runs a single hook and returns its result plus the possibly-
// extended env prefix (load_env appends to it).
func executeHook(
	ctx context.Context,
	opts Options,
	log *slog.Logger,
	stage Stage,
	h config.Hook,
	envPrefix string,
) (Result, string) {
	if h.IsLoadEnv() {
		loaded, err := applyLoadEnv(opts, log, h.LoadEnv)
		if err != nil {
			return Result{Hook: h, Err: err}, envPrefix
		}
		return Result{Hook: h}, envPrefix + loaded
	}

	if opts.Cond != nil && h.When != "" {
		ok, err := opts.Cond.Eval(ctx, h.When)
		if err != nil {
			if stage == StagePost {
				log.Warn("post-hook `when:` evaluation failed (non-blocking)", "hook", h.Run, "err", err)
				return Result{Hook: h, Skipped: true}, envPrefix
			}
			return Result{Hook: h, Err: fmt.Errorf("when %q: %w", h.When, err)}, envPrefix
		}
		if !ok {
			log.Debug("hooks: skipping, when=false", "hook", h.Run)
			return Result{Hook: h, Skipped: true}, envPrefix
		}
	}

	rendered, err := renderTemplate(opts, "hook-run", h.Run)
	if err != nil {
		return Result{Hook: h, Err: fmt.Errorf("render run %q: %w", h.Run, err)}, envPrefix
	}

	full := rendered
	if envPrefix != "" {
		full = envPrefix + rendered
	}

	if opts.DryRun {
		log.Info("hooks: dry-run", "cmd", full)
		return Result{Hook: h, Skipped: true, Output: full}, envPrefix
	}

	log.Debug("hooks: run", "cmd", full)
	stdout, stderr, code, runErr := opts.Shell.Run(ctx, full)
	writeOutput(opts.Stdout, stdout)
	writeOutput(opts.Stderr, stderr)

	out := strings.TrimSpace(string(stdout))
	if runErr != nil {
		return Result{Hook: h, Err: fmt.Errorf("shell %q: %w", full, runErr), Output: out}, envPrefix
	}
	if code != 0 {
		msg := strings.TrimSpace(string(stderr))
		return Result{
			Hook:   h,
			Err:    fmt.Errorf("shell %q exited with code %d: %s", full, code, msg),
			Output: out,
		}, envPrefix
	}
	return Result{Hook: h, Output: out}, envPrefix
}

// applyLoadEnv reads, parses and template-renders a dotenv file, returning a
// shell prefix that exports every key. The prefix is prepended to subsequent
// `run:` commands in the same stage (PLAN §20). Values are single-quote
// escaped to be safe against any shell metacharacter.
//
// The path is resolved against the current worktree (Builtin.WorktreePath)
// when relative, matching the conditions package's path handling.
func applyLoadEnv(opts Options, log *slog.Logger, path string) (string, error) {
	renderedPath, err := renderTemplate(opts, "load_env-path", path)
	if err != nil {
		return "", fmt.Errorf("render load_env path %q: %w", path, err)
	}
	resolved := resolvePath(string(renderedPath), opts.Builtin.WorktreePath)
	data, err := opts.FS.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("load_env %q: %w", renderedPath, err)
	}
	kv, err := parseDotenv(data)
	if err != nil {
		return "", fmt.Errorf("load_env %q: %w", renderedPath, err)
	}
	var b strings.Builder
	for _, k := range orderedKeys(kv) {
		renderedVal, err := renderTemplate(opts, "load_env-value-"+k, kv[k])
		if err != nil {
			return "", fmt.Errorf("render load_env %s: %w", k, err)
		}
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(shellQuote(renderedVal))
		b.WriteString("; ")
	}
	log.Debug("hooks: load_env applied", "path", renderedPath, "keys", len(kv))
	return b.String(), nil
}

// resolvePath returns abs joined with base when path is relative, or path
// cleaned in place when already absolute. Empty base falls back to ".".
func resolvePath(path, base string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	if base == "" {
		base = "."
	}
	return filepath.Join(base, path)
}

// renderTemplate is the single funnel through which `run:`, `load_env:` paths
// and dotenv values reach text/template. A render failure aborts the hook.
func renderTemplate(opts Options, name, tmpl string) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	out, err := template.Render(name, tmpl, template.Data{
		BuiltinVars: opts.Builtin,
		Vars:        opts.UserVars,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// writeOutput mirrors shell output to the corresponding wwtr stream. Errors are
// ignored: a broken pipe on stdout/stderr is not a hook failure.
func writeOutput(w io.Writer, data []byte) {
	if w == nil || len(data) == 0 {
		return
	}
	_, _ = w.Write(data)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		_, _ = w.Write([]byte("\n"))
	}
}

// describe returns a short human-readable hook identifier for log lines.
func describe(h config.Hook) string {
	if h.IsLoadEnv() {
		return "load_env:" + h.LoadEnv
	}
	if h.Run != "" {
		return h.Run
	}
	return "(empty)"
}

// ---------------------------------------------------------------------------
// dotenv parser (PLAN §20): KEY=VALUE, # comments, blank lines, quotes.
// ---------------------------------------------------------------------------

// parseDotenv parses dotenv bytes. It accepts:
//   - blank lines,
//   - lines whose first non-space char is `#`,
//   - `KEY=VALUE`, with optional double or single quotes around VALUE.
//
// Inline `#` comments after an unquoted value are preserved verbatim (no
// shell semantics): dotenv is a simple format, not a shell dialect.
func parseDotenv(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for lineNo, raw := range bytes.Split(data, []byte("\n")) {
		line := strings.TrimRight(string(raw), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			return nil, fmt.Errorf("line %d: missing '='", lineNo+1)
		}
		key := strings.TrimSpace(trimmed[:eq])
		val := strings.TrimSpace(trimmed[eq+1:])
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo+1)
		}
		if !validDotenvKey(key) {
			return nil, fmt.Errorf("line %d: invalid key %q", lineNo+1, key)
		}
		out[key] = unquote(val)
	}
	return out, nil
}

// unquote strips a single layer of surrounding single or double quotes if they
// match on both ends.
func unquote(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func validDotenvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// orderedKeys returns kv's keys in lexicographic order so tests can assert
// deterministic export prefixes. Go map iteration is randomised.
func orderedKeys(kv map[string]string) []string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// shellQuote wraps s in single quotes and escapes any embedded single quote
// via the POSIX `'\”` sequence. The result is safe to splice into an sh -c
// command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
