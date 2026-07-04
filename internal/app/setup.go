package app

import (
	"context"
	"log/slog"

	"github.com/wailorman/wwtr/internal/hooks"
	"github.com/wailorman/wwtr/internal/runcontext"
)

// RunSetup runs the pre_setup/post_setup hooks (PLAN §9). Setup never touches
// the filesystem directly — its job is dependency installation, migrations,
// etc., all of which happen via hook shell commands.
func RunSetup(ctx context.Context, rc *runcontext.RunContext) error {
	return runHooksCmd(ctx, rc, "setup")
}

// runHooksCmd is the shared body of setup/start/stop: Discover, EnsureTrust,
// blocking pre-hooks, non-blocking post-hooks. Each command differs only in
// which hook lists are addressed, controlled by cmd.
func runHooksCmd(ctx context.Context, rc *runcontext.RunContext, cmd string) error {
	log := slog.Default()

	actx, err := Discover(ctx, rc, cmd)
	if err != nil {
		return err
	}

	if err := EnsureTrust(ctx, actx, cmd); err != nil {
		return err
	}

	opts := buildHookOpts(actx, log)
	if _, err := hooks.Run(ctx, opts, hooks.StagePre, actx.Cfg.HookStage(cmd, "pre")); err != nil {
		return err
	}
	if _, err := hooks.Run(ctx, opts, hooks.StagePost, actx.Cfg.HookStage(cmd, "post")); err != nil {
		log.Warn(cmd+": post-hook stage error", "err", err)
	}
	log.Info(cmd + ": complete")
	return nil
}
