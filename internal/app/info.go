package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/wailorman/wwtr/internal/runcontext"
)

// RunInfo prints resolved variables and builtins in one of three formats
// (PLAN §3, §9). It performs no trust check and runs no hooks; output goes to
// stdout so the `--env` form composes with `eval "$(wwtr info --env)"`.
func RunInfo(ctx context.Context, rc *runcontext.RunContext) error {
	actx, err := Discover(ctx, rc, "info")
	if err != nil {
		return err
	}

	out := rc.Deps.Stdout
	if out == nil {
		out = io.Discard
	}

	switch {
	case rc.Flags.JSON:
		return writeInfoJSON(out, actx)
	case rc.Flags.Env:
		return writeInfoEnv(out, actx)
	default:
		return writeInfoHuman(out, actx)
	}
}

func writeInfoHuman(w io.Writer, actx *Ctx) error {
	var b strings.Builder
	fmt.Fprintln(&b, "Builtins:")
	fmt.Fprintf(&b, "  Branch:           %s\n", actx.Builtin.Branch)
	fmt.Fprintf(&b, "  Slug:             %s\n", actx.Builtin.Slug)
	fmt.Fprintf(&b, "  Hash:             %s\n", actx.Builtin.Hash)
	fmt.Fprintf(&b, "  ShortHash:        %s\n", actx.Builtin.ShortHash)
	fmt.Fprintf(&b, "  SafeName:         %s\n", actx.Builtin.SafeName)
	fmt.Fprintf(&b, "  WorktreePath:     %s\n", actx.Builtin.WorktreePath)
	fmt.Fprintf(&b, "  WorktreeName:     %s\n", actx.Builtin.WorktreeName)
	fmt.Fprintf(&b, "  MainWorktreePath: %s\n", actx.Builtin.MainWorktreePath)
	fmt.Fprintf(&b, "  MainWorktreeName: %s\n", actx.Builtin.MainWorktreeName)

	if len(actx.Vars) > 0 {
		fmt.Fprintln(&b, "Vars:")
		for _, k := range sortedKeys(actx.Vars) {
			fmt.Fprintf(&b, "  %s: %s\n", k, actx.Vars[k])
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeInfoEnv(w io.Writer, actx *Ctx) error {
	var b strings.Builder
	emitEnv(&b, "WWTR_BRANCH", actx.Builtin.Branch)
	emitEnv(&b, "WWTR_SLUG", actx.Builtin.Slug)
	emitEnv(&b, "WWTR_HASH", actx.Builtin.Hash)
	emitEnv(&b, "WWTR_SHORT_HASH", actx.Builtin.ShortHash)
	emitEnv(&b, "WWTR_SAFE_NAME", actx.Builtin.SafeName)
	emitEnv(&b, "WWTR_WORKTREE_PATH", actx.Builtin.WorktreePath)
	emitEnv(&b, "WWTR_WORKTREE_NAME", actx.Builtin.WorktreeName)
	emitEnv(&b, "WWTR_MAIN_WORKTREE_PATH", actx.Builtin.MainWorktreePath)
	emitEnv(&b, "WWTR_MAIN_WORKTREE_NAME", actx.Builtin.MainWorktreeName)
	for _, k := range sortedKeys(actx.Vars) {
		emitEnv(&b, "WWTR_VAR_"+strings.ToUpper(k), actx.Vars[k])
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func emitEnv(b *strings.Builder, key, val string) {
	fmt.Fprintf(b, "export %s=%s\n", key, shellSingleQuote(val))
}

// shellSingleQuote wraps s in POSIX single quotes, escaping any embedded
// single quote via the '\” sequence. Safe to splice into sh -c.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeInfoJSON(w io.Writer, actx *Ctx) error {
	payload := struct {
		Builtins any               `json:"builtins"`
		Vars     map[string]string `json:"vars"`
	}{
		Builtins: actx.Builtin,
		Vars:     actx.Vars,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
