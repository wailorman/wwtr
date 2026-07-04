package app

import (
	"context"
	"log/slog"

	"github.com/wailorman/wwtr/internal/files"
	"github.com/wailorman/wwtr/internal/hooks"
	"github.com/wailorman/wwtr/internal/runcontext"
	"github.com/wailorman/wwtr/internal/state"
)

// RunClean implements the clean flow from PLAN §9: pre_clean hooks (blocking,
// db:drop/docker stop), files.Clean (conflict-prompted teardown), post_clean
// hooks (non-blocking), and finally state.yaml removal. clean = full reset.
func RunClean(ctx context.Context, rc *runcontext.RunContext) error {
	log := slog.Default()

	actx, err := Discover(ctx, rc, "clean")
	if err != nil {
		return err
	}

	if err := EnsureTrust(ctx, actx, "clean"); err != nil {
		return err
	}

	hookOpts := buildHookOpts(actx, log)
	if _, err := hooks.Run(ctx, hookOpts, hooks.StagePre, actx.Cfg.HookStage("clean", "pre")); err != nil {
		return err
	}

	if _, err := files.Clean(ctx, buildFileOpts(actx, log), buildOps(actx.Cfg)); err != nil {
		return err
	}

	if _, err := hooks.Run(ctx, hookOpts, hooks.StagePost, actx.Cfg.HookStage("clean", "post")); err != nil {
		log.Warn("clean: post-hook stage error", "err", err)
	}

	if !rc.Flags.NoState && !rc.Flags.DryRun {
		if err := state.Remove(rc.Deps.FS, actx.WT.CurrentPath); err != nil {
			return err
		}
	}

	log.Info("clean: complete", "main", actx.WT.IsMain())
	return nil
}
