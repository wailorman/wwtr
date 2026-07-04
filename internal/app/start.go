package app

import (
	"context"

	"github.com/wailorman/wwtr/internal/runcontext"
)

// RunStart runs the pre_start/post_start hooks (PLAN §9): docker compose up,
// mock servers, background workers.
func RunStart(ctx context.Context, rc *runcontext.RunContext) error {
	return runHooksCmd(ctx, rc, "start")
}
