package app

import (
	"context"

	"github.com/wailorman/wwtr/internal/runcontext"
)

// RunStop runs the pre_stop/post_stop hooks (PLAN §9): docker stop, kill
// processes, release resources.
func RunStop(ctx context.Context, rc *runcontext.RunContext) error {
	return runHooksCmd(ctx, rc, "stop")
}
