// Package worktree discovers the current git-worktree layout via the
// [github.com/wailorman/wwtr/internal/di.Git] interface. It never shells out
// to git directly — that side-effect lives in di — so the same code runs
// against fakes.FakeGit in tests and the real git binary in production.
//
// Discover returns an Info populated from exactly three git calls: main
// worktree path, current worktree path, and current branch. The common-dir
// call from the Git interface is used by config discovery (PLAN §2 step 2),
// not here; Info.MainPath comes straight from MainWorktree.
package worktree

import (
	"context"
	"fmt"

	"github.com/wailorman/wwtr/internal/di"
)

// Info is the snapshot of worktree state every command needs. Populate it
// once at the start of a command via Discover and thread it through.
type Info struct {
	MainPath    string
	CurrentPath string
	Branch      string
}

// IsMain reports whether the current worktree IS the main worktree — i.e.
// copy/symlink must become no-ops (PLAN §11). Empty paths return false so a
// partially-populated Info never silently behaves as main.
func (i Info) IsMain() bool {
	if i.MainPath == "" || i.CurrentPath == "" {
		return false
	}
	return i.MainPath == i.CurrentPath
}

// Discover queries git for the main path, current path, and branch in that
// order. Any failure is wrapped with which step failed so the user can tell
// whether `git rev-parse --git-common-dir` or `--abbrev-ref HEAD` was at fault.
func Discover(ctx context.Context, g di.Git) (Info, error) {
	main, err := g.MainWorktree(ctx)
	if err != nil {
		return Info{}, fmt.Errorf("worktree: detect main worktree: %w", err)
	}
	current, err := g.CurrentWorktree(ctx)
	if err != nil {
		return Info{}, fmt.Errorf("worktree: detect current worktree: %w", err)
	}
	branch, err := g.Branch(ctx)
	if err != nil {
		return Info{}, fmt.Errorf("worktree: detect branch: %w", err)
	}
	return Info{
		MainPath:    main,
		CurrentPath: current,
		Branch:      branch,
	}, nil
}
