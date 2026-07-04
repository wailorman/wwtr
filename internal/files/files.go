// Package files applies the template/copy/symlink operations declared in
// `.wwtr.yml` (PLAN §9 step 7, §10). Every filesystem mutation goes through
// [di.FS], every interactive decision through [di.Prompter], so the conflict
// decision tree is fully exercisable in unit tests via the fakes.
//
// Conflict resolution follows PLAN §10: identical content is silently skipped,
// divergent content prompts the user with Y/n/a/q/d, --force/--skip short-
// circuit the prompt, --dry-run logs but does not write. copy and symlink are
// no-ops in the main worktree (PLAN §11) because both `cp x x` and `ln x x`
// are pointless or cyclic; template still runs in main.
package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/template"
	"github.com/wailorman/wwtr/internal/vars"
)

// ErrAborted is returned by Apply (or Clean) when the user picks Quit at any
// conflict prompt. Callers map this to exit code 6 (PLAN §20).
var ErrAborted = errors.New("files: user aborted")

// OpKind discriminates the three operations declared in `.wwtr.yml`.
type OpKind int

const (
	OpTemplate OpKind = iota
	OpCopy
	OpSymlink
)

// String returns the YAML section name for the kind, used in logs.
func (k OpKind) String() string {
	switch k {
	case OpTemplate:
		return "template"
	case OpCopy:
		return "copy"
	case OpSymlink:
		return "symlink"
	}
	return "unknown"
}

// Op is a single normalised template/copy/symlink entry. From is relative to
// the main worktree; To is relative to the current worktree. An empty To
// defaults to From at apply time (matches config.PathSpec normalisation).
//
// Content carries inline template text from the `content:` YAML field. When
// set, From is empty and applyTemplate renders Content directly instead of
// reading From from the filesystem.
type Op struct {
	Kind    OpKind
	From    string
	To      string
	Content string
}

// Action is the per-op outcome reported back to the caller.
type Action int

const (
	ActionWrote Action = iota
	ActionSkipped
	ActionAborted
)

// Result describes what happened for one Op. Reason is a short, human-readable
// string used in logs and tests; it is empty for plain successes.
type Result struct {
	Op     Op
	Action Action
	Reason string
}

// Options bundles the per-invocation inputs. The same struct is accepted by
// Apply and Clean; Builtin and UserVars are only consulted by template ops.
type Options struct {
	FS          di.FS
	Prompter    di.Prompter
	Log         *slog.Logger
	MainPath    string
	CurrentPath string
	IsMain      bool
	Force       bool
	Skip        bool
	DryRun      bool
	Builtin     vars.BuiltinVars
	UserVars    map[string]string
}

// Apply runs ops in declaration order. The returned results slice mirrors the
// input order unless an early abort or context cancellation truncates it.
// ErrAborted is returned when the user picks Quit; the underlying context
// error is returned when the context is cancelled mid-run.
func Apply(ctx context.Context, opts Options, ops []Op) ([]Result, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	st := &runner{opts: opts, log: log}
	results := make([]Result, 0, len(ops))
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		res, err := st.applyOne(op)
		if err != nil {
			return results, err
		}
		results = append(results, res)
		if res.Action == ActionAborted {
			return results, ErrAborted
		}
	}
	return results, nil
}

// Clean removes the targets previously written by Apply. For symlink it
// removes only the link, never the main-side target. For copy/template it
// removes the file when its content still matches what Apply would produce,
// otherwise prompts before destroying user edits. Foreign symlinks and
// non-symlink files at symlink targets are left untouched with a warning.
// Main-worktree copy/symlink ops are no-ops (Apply never wrote them).
func Clean(ctx context.Context, opts Options, ops []Op) ([]Result, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	st := &runner{opts: opts, log: log}
	results := make([]Result, 0, len(ops))
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		res, err := st.cleanOne(op)
		if err != nil {
			return results, err
		}
		results = append(results, res)
		if res.Action == ActionAborted {
			return results, ErrAborted
		}
	}
	return results, nil
}

// runner carries the per-Apply/Clean mutable state, currently just the "all
// yes" latch flipped by DecisionAll so subsequent conflicts in the same call
// skip the prompt.
type runner struct {
	opts   Options
	log    *slog.Logger
	allYes bool
}

func (r *runner) applyOne(op Op) (Result, error) {
	to := op.To
	if to == "" {
		to = op.From
	}
	if r.opts.IsMain && (op.Kind == OpCopy || op.Kind == OpSymlink) {
		r.log.Info("files: skipped (main worktree)", "kind", op.Kind.String(), "from", op.From)
		return Result{Op: op, Action: ActionSkipped, Reason: "main worktree"}, nil
	}
	switch op.Kind {
	case OpTemplate:
		return r.applyTemplate(op, to)
	case OpCopy:
		return r.applyCopy(op, to)
	case OpSymlink:
		return r.applySymlink(op, to)
	}
	return Result{}, fmt.Errorf("files: unknown op kind %d", op.Kind)
}

func (r *runner) applyTemplate(op Op, to string) (Result, error) {
	raw, name, err := r.templateSource(op)
	if err != nil {
		return Result{}, err
	}
	rendered, err := template.Render(name, raw, template.Data{
		BuiltinVars: r.opts.Builtin,
		Vars:        r.opts.UserVars,
	})
	if err != nil {
		return Result{}, fmt.Errorf("files: render template %s: %w", name, err)
	}
	toPath := filepath.Join(r.opts.CurrentPath, to)
	return r.writeContent(op, toPath, rendered)
}

// templateSource returns the raw template body and the name to use in error
// messages. Inline Content (when set) bypasses the filesystem; the file-based
// form reads MainPath/From as before. The name tracks From for file-based
// templates (the source path is what the user needs to debug) and To for
// inline ones (From is empty there).
func (r *runner) templateSource(op Op) (string, string, error) {
	if op.Content != "" {
		return op.Content, op.To, nil
	}
	fromPath := filepath.Join(r.opts.MainPath, op.From)
	data, err := r.opts.FS.ReadFile(fromPath)
	if err != nil {
		return "", "", fmt.Errorf("files: read template %s: %w", fromPath, err)
	}
	return string(data), op.From, nil
}

func (r *runner) applyCopy(op Op, to string) (Result, error) {
	fromPath := filepath.Join(r.opts.MainPath, op.From)
	content, err := r.opts.FS.ReadFile(fromPath)
	if err != nil {
		return Result{}, fmt.Errorf("files: read copy %s: %w", fromPath, err)
	}
	toPath := filepath.Join(r.opts.CurrentPath, to)
	return r.writeContent(op, toPath, content)
}

// writeContent handles the copy/template shared write path: identical-content
// short-circuit, conflict prompt, dry-run gate, MkdirAll + WriteFile.
func (r *runner) writeContent(op Op, toPath string, newContent []byte) (Result, error) {
	if existing, ok := r.readIfExists(toPath); ok && bytes.Equal(existing, newContent) {
		r.log.Debug("files: identical content, skipping", "to", toPath)
		return Result{Op: op, Action: ActionSkipped, Reason: "identical content"}, nil
	}
	exists := r.opts.FS.Exists(toPath)
	diffFn := func() (string, error) { return r.diffContent(toPath, newContent), nil }
	decision, err := r.resolveConflict(toPath, exists, diffFn)
	if err != nil {
		return Result{}, err
	}
	switch decision {
	case di.DecisionNo:
		return Result{Op: op, Action: ActionSkipped, Reason: "user skipped"}, nil
	case di.DecisionQuit:
		return Result{Op: op, Action: ActionAborted, Reason: "user aborted"}, nil
	}
	if r.opts.DryRun {
		r.log.Info("files: dry-run, would write", "to", toPath, "exists", exists)
		return Result{Op: op, Action: ActionSkipped, Reason: "dry-run"}, nil
	}
	if err := r.opts.FS.MkdirAll(filepath.Dir(toPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("files: mkdir parent of %s: %w", toPath, err)
	}
	if err := r.opts.FS.WriteFile(toPath, newContent, 0o644); err != nil {
		return Result{}, fmt.Errorf("files: write %s: %w", toPath, err)
	}
	r.log.Debug("files: wrote", "to", toPath, "bytes", len(newContent))
	return Result{Op: op, Action: ActionWrote}, nil
}

// applySymlink resolves current/to against main/from. Symlinks over regular
// files/dirs go through the same conflict prompt as copy/template would; the
// §10 table explicitly lists only foreign-target symlinks as "skip with warn",
// so a regular file at the target is treated as a regular conflict.
//
// Detection uses Readlink+Exists rather than Lstat: Readlink's nil-error
// unambiguously identifies a symlink (regular files and dirs return EINVAL on
// real OSes, ErrNotExist on the fake), and Exists distinguishes "regular
// entry present" from "no entry at all".
func (r *runner) applySymlink(op Op, to string) (Result, error) {
	fromPath := filepath.Join(r.opts.MainPath, op.From)
	toPath := filepath.Join(r.opts.CurrentPath, to)

	if target, lerr := r.opts.FS.Readlink(toPath); lerr == nil {
		if target == fromPath {
			r.log.Debug("files: symlink already points to target", "to", toPath)
			return Result{Op: op, Action: ActionSkipped, Reason: "symlink already points to target"}, nil
		}
		r.log.Warn("files: foreign symlink, skipping", "to", toPath, "target", target)
		return Result{Op: op, Action: ActionSkipped, Reason: "foreign symlink"}, nil
	}

	exists := r.opts.FS.Exists(toPath)
	diffFn := func() (string, error) {
		return fmt.Sprintf("existing entry at %s would be replaced by symlink -> %s", toPath, fromPath), nil
	}
	decision, err := r.resolveConflict(toPath, exists, diffFn)
	if err != nil {
		return Result{}, err
	}
	switch decision {
	case di.DecisionNo:
		return Result{Op: op, Action: ActionSkipped, Reason: "user skipped"}, nil
	case di.DecisionQuit:
		return Result{Op: op, Action: ActionAborted, Reason: "user aborted"}, nil
	}
	if r.opts.DryRun {
		r.log.Info("files: dry-run, would create symlink", "to", toPath, "target", fromPath)
		return Result{Op: op, Action: ActionSkipped, Reason: "dry-run"}, nil
	}
	if exists {
		if err := r.opts.FS.RemoveAll(toPath); err != nil {
			return Result{}, fmt.Errorf("files: remove existing %s: %w", toPath, err)
		}
	}
	if err := r.opts.FS.MkdirAll(filepath.Dir(toPath), 0o755); err != nil {
		return Result{}, fmt.Errorf("files: mkdir parent of %s: %w", toPath, err)
	}
	if err := r.opts.FS.Symlink(fromPath, toPath); err != nil {
		return Result{}, fmt.Errorf("files: symlink %s -> %s: %w", toPath, fromPath, err)
	}
	r.log.Debug("files: created symlink", "to", toPath, "target", fromPath)
	return Result{Op: op, Action: ActionWrote}, nil
}

// --- Clean ------------------------------------------------------------------

func (r *runner) cleanOne(op Op) (Result, error) {
	to := op.To
	if to == "" {
		to = op.From
	}
	if r.opts.IsMain && (op.Kind == OpCopy || op.Kind == OpSymlink) {
		r.log.Info("files: clean skipped (main worktree)", "kind", op.Kind.String(), "from", op.From)
		return Result{Op: op, Action: ActionSkipped, Reason: "main worktree"}, nil
	}
	toPath := filepath.Join(r.opts.CurrentPath, to)
	switch op.Kind {
	case OpTemplate, OpCopy:
		return r.cleanContent(op, toPath)
	case OpSymlink:
		return r.cleanSymlink(op, toPath)
	}
	return Result{}, fmt.Errorf("files: unknown op kind %d", op.Kind)
}

// cleanContent removes the file at toPath. If the content still matches what
// Apply would produce (best-effort), the removal is silent; otherwise the
// user's edits trigger a conflict prompt before destruction.
func (r *runner) cleanContent(op Op, toPath string) (Result, error) {
	if !r.opts.FS.Exists(toPath) {
		return Result{Op: op, Action: ActionSkipped, Reason: "nothing to clean"}, nil
	}
	if expected, ok := r.expectedContent(op); ok {
		if existing, err := r.opts.FS.ReadFile(toPath); err == nil && bytes.Equal(existing, expected) {
			return r.remove(op, toPath, "removed (unchanged)")
		}
	}
	diffFn := func() (string, error) {
		return fmt.Sprintf("modified file at %s would be deleted", toPath), nil
	}
	decision, err := r.resolveConflict(toPath, true, diffFn)
	if err != nil {
		return Result{}, err
	}
	switch decision {
	case di.DecisionNo:
		return Result{Op: op, Action: ActionSkipped, Reason: "user kept"}, nil
	case di.DecisionQuit:
		return Result{Op: op, Action: ActionAborted, Reason: "user aborted"}, nil
	}
	return r.remove(op, toPath, "removed (modified)")
}

func (r *runner) cleanSymlink(op Op, toPath string) (Result, error) {
	fromPath := filepath.Join(r.opts.MainPath, op.From)
	target, lerr := r.opts.FS.Readlink(toPath)
	if lerr != nil {
		if !r.opts.FS.Exists(toPath) {
			return Result{Op: op, Action: ActionSkipped, Reason: "nothing to clean"}, nil
		}
		r.log.Warn("files: clean skipping, not a symlink", "to", toPath)
		return Result{Op: op, Action: ActionSkipped, Reason: "not a symlink"}, nil
	}
	if target != fromPath {
		r.log.Warn("files: clean skipping foreign symlink", "to", toPath, "target", target)
		return Result{Op: op, Action: ActionSkipped, Reason: "foreign symlink"}, nil
	}
	if r.opts.DryRun {
		r.log.Info("files: dry-run, would remove symlink", "to", toPath)
		return Result{Op: op, Action: ActionSkipped, Reason: "dry-run"}, nil
	}
	if err := r.opts.FS.Remove(toPath); err != nil {
		return Result{}, fmt.Errorf("files: remove symlink %s: %w", toPath, err)
	}
	return Result{Op: op, Action: ActionWrote, Reason: "removed symlink"}, nil
}

func (r *runner) remove(op Op, toPath, reason string) (Result, error) {
	if r.opts.DryRun {
		r.log.Info("files: dry-run, would remove", "to", toPath)
		return Result{Op: op, Action: ActionSkipped, Reason: "dry-run"}, nil
	}
	if err := r.opts.FS.Remove(toPath); err != nil {
		return Result{}, fmt.Errorf("files: remove %s: %w", toPath, err)
	}
	return Result{Op: op, Action: ActionWrote, Reason: reason}, nil
}

// expectedContent returns what Apply would write for a copy/template op, or
// (nil, false) if it cannot be determined (main/from missing or render error).
func (r *runner) expectedContent(op Op) ([]byte, bool) {
	raw, name, err := r.templateSource(op)
	if err != nil {
		return nil, false
	}
	if op.Kind != OpTemplate {
		return []byte(raw), true
	}
	rendered, err := template.Render(name, raw, template.Data{
		BuiltinVars: r.opts.Builtin,
		Vars:        r.opts.UserVars,
	})
	if err != nil {
		return nil, false
	}
	return rendered, true
}

// --- Conflict resolution ----------------------------------------------------

// resolveConflict decides whether to overwrite an existing entry. It never
// prompts when there is nothing to overwrite, when a flag pre-decides
// (--force/--skip/--dry-run), or when the user already picked "all" in this
// run. DecisionAll flips r.allYes and is returned as Yes; DecisionDiff loops
// back to the prompter so the user can pick a second time after viewing the
// diff (handles both prompter contracts: prompters that loop internally and
// never return Diff, and those that return Diff and expect the caller to loop).
func (r *runner) resolveConflict(
	toPath string,
	exists bool,
	diffFn func() (string, error),
) (di.Decision, error) {
	if !exists {
		return di.DecisionYes, nil
	}
	if r.opts.DryRun {
		return di.DecisionYes, nil
	}
	if r.opts.Force {
		return di.DecisionYes, nil
	}
	if r.opts.Skip {
		return di.DecisionNo, nil
	}
	if r.allYes {
		return di.DecisionYes, nil
	}
	for {
		dec, err := r.opts.Prompter.Conflict(toPath, diffFn)
		if err != nil {
			return di.DecisionUnknown, err
		}
		switch dec {
		case di.DecisionAll:
			r.allYes = true
			return di.DecisionYes, nil
		case di.DecisionDiff:
			continue
		default:
			return dec, nil
		}
	}
}

func (r *runner) readIfExists(path string) ([]byte, bool) {
	existing, err := r.opts.FS.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return existing, true
}

// diffContent is the MVP diff shown to the user via Prompter.Conflict's
// diffFn callback. It is intentionally a labelled byte-count summary rather
// than a unified diff — full diff rendering is out of MVP scope.
func (r *runner) diffContent(toPath string, newContent []byte) string {
	existing, err := r.opts.FS.ReadFile(toPath)
	if err != nil {
		return fmt.Sprintf("--- %s (cannot read existing)\n+++ new (%d bytes)\n", toPath, len(newContent))
	}
	return fmt.Sprintf("--- %s (%d bytes existing)\n+++ new (%d bytes)\n", toPath, len(existing), len(newContent))
}
