// Command wwtr is the entry point of the worktree wrapper. It does nothing on
// its own: all work happens in [github.com/wailorman/wwtr/cmd] and the
// internal packages. main exists only to wire version vars (set via -ldflags)
// into the version package and to forward os.Args to cobra.
//
// Build with version metadata:
//
//	go build -ldflags "-X main.version=0.1.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
package main

import (
	"fmt"
	"os"

	"github.com/wailorman/wwtr/cmd"
)

// These are set by the linker (-ldflags "-X main.version=..."). They default
// to development sentinels for plain `go build`.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Exit codes (PLAN §20):
//
//	0 success
//	1 general error
//	2 usage error (cobra adds Usage on these)
//	3 trust denied
//	4 unresolved var
//	5 pre-hook failed
//	6 file conflict abort
//
// Refinement to typed exit codes will land with the corresponding packages;
// for the skeleton we collapse every non-nil error to exit 1.
func main() {
	if err := cmd.Execute(version, commit, date); err != nil {
		fmt.Fprintf(os.Stderr, "wwtr: %v\n", err)
		os.Exit(1)
	}
}
