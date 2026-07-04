// Package runcontext carries everything a command's business logic needs: the
// loaded config, resolved variables, worktree paths, side-effect dependencies
// and the parsed global flags. cmd/root.go builds one of these from cobra args
// and hands it to internal/app/*.
//
// The struct is deliberately plain data: no methods, no embedded behaviour.
// Each app/*.Run function takes a *RunContext as its first argument.
package runcontext

import (
	"github.com/wailorman/wwtr/internal/di"
)

// Flags holds the parsed global flags. Kept separate from di.Deps because
// these aren't side-effectful services — they're inputs to business decisions.
type Flags struct {
	Config  string // --config <path>; "" = auto-discover
	Force   bool   // --force
	Skip    bool   // --skip
	DryRun  bool   // --dry-run
	NoHooks bool   // --no-hooks
	Yes     bool   // --yes (auto-approve trust + y/n)
	NoState bool   // --no-state
	Verbose bool   // -v / --verbose
	JSON    bool   // info --json
	Env     bool   // info --env

	// CLIVars holds the values of dynamic flags registered from .wwtr.yml's
	// `vars.<name>.sources[].cli` entries (e.g. --base-port from §13). Keys
	// are the full flag form including leading dashes ("--base-port") as
	// expected by vars.cliValue. Populated by cmd/ from cobra's parsed flags.
	CLIVars map[string]string
}

type RunContext struct {
	Flags Flags
	Deps  di.Deps
	// The following fields are populated progressively as the command moves
	// through its lifecycle (config load → worktree discovery → vars
	// resolution). They are pointers so nil cleanly signals "not yet known".

	// Worktree paths. Set by worktree.Discover in Phase 1.
	MainPath    string
	CurrentPath string

	// Current branch name. Set by worktree.Discover.
	Branch string

	// TrustRegistryPath is the absolute path to ~/.config/wwtr/trust.yaml.
	// Populated by cmd/ via os.UserConfigDir; consumed by internal/app/trust.
	TrustRegistryPath string
}

// IsMain reports whether the current worktree is the main one. worktree.Discover
// populates both paths and this is a pure comparison.
func (rc *RunContext) IsMain() bool {
	if rc.MainPath == "" || rc.CurrentPath == "" {
		return false
	}
	return rc.MainPath == rc.CurrentPath
}
