package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/files"
	"github.com/wailorman/wwtr/internal/hooks"
	"github.com/wailorman/wwtr/internal/runcontext"
	"github.com/wailorman/wwtr/internal/state"
)

// ErrStateOverwriteDeclined is returned by RunInit when the user declines the
// "state.yaml exists; overwrite?" prompt. It is not a typed exit code (the
// user explicitly chose to abort); cmd/ collapses it to ExitGeneral.
var ErrStateOverwriteDeclined = errors.New("init: declined to overwrite existing state")

// RunInit implements the 10-step init flow from PLAN §9.
func RunInit(ctx context.Context, rc *runcontext.RunContext) error {
	log := slog.Default()

	actx, err := Discover(ctx, rc, "init")
	if err != nil {
		return err
	}

	if err := EnsureTrust(ctx, actx, "init"); err != nil {
		return err
	}

	if !rc.Flags.NoState && rc.Deps.FS.Exists(actx.StatePath) {
		if !rc.Flags.Yes {
			overwrite, perr := rc.Deps.Prompter.Confirm(
				"state.yaml exists; overwrite?", false,
			)
			if perr != nil {
				return fmt.Errorf("init: state overwrite prompt: %w", perr)
			}
			if !overwrite {
				return ErrStateOverwriteDeclined
			}
		}
	}

	hookOpts := buildHookOpts(actx, log)
	if _, err := hooks.Run(ctx, hookOpts, hooks.StagePre, actx.Cfg.HookStage("init", "pre")); err != nil {
		return err
	}

	if _, err := files.Apply(ctx, buildFileOpts(actx, log), buildOps(actx.Cfg)); err != nil {
		return err
	}

	if _, err := hooks.Run(ctx, hookOpts, hooks.StagePost, actx.Cfg.HookStage("init", "post")); err != nil {
		log.Warn("init: post-hook stage error", "err", err)
	}

	if !rc.Flags.NoState && !rc.Flags.DryRun && len(actx.PromptVars) > 0 {
		if err := state.Write(rc.Deps.FS, actx.WT.CurrentPath, actx.PromptVars); err != nil {
			return err
		}
	}

	log.Info(
		"init: complete",
		"vars", len(actx.Vars),
		"prompt_vars", len(actx.PromptVars),
		"main", actx.WT.IsMain(),
	)
	return nil
}

// buildOps converts the config's template/copy/symlink sections into a single
// flat []files.Op slice in declaration order (template first, then copy, then
// symlink). This matches the order Apply executes them in PLAN §9 step 7.
func buildOps(cfg *config.Config) []files.Op {
	var ops []files.Op
	for _, p := range cfg.Template {
		ops = append(ops, files.Op{Kind: files.OpTemplate, From: p.From, To: p.To, Content: p.Content})
	}
	for _, p := range cfg.Copy {
		ops = append(ops, files.Op{Kind: files.OpCopy, From: p.From, To: p.To})
	}
	for _, p := range cfg.Symlink {
		ops = append(ops, files.Op{Kind: files.OpSymlink, From: p.From, To: p.To})
	}
	return ops
}
