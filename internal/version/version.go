// Package version exposes build-time version metadata injected via ldflags by
// goreleaser (-X main.version=... etc.). The variables live in main and are
// copied here at startup via Set.
package version

import "fmt"

// Build-time variables, set via -ldflags "-X main.*" in Makefile/goreleaser.
// Default to "dev" so a `go build` from a checkout still prints something sane.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Set is called once from main on startup to populate the package vars.
func Set(v, c, d string) {
	version = v
	commit = c
	date = d
}

// Version returns the human-readable version string ("0.1.0").
func Version() string { return version }

// Commit returns the short commit hash.
func Commit() string { return commit }

// Date returns the build date (RFC3339).
func Date() string { return date }

// Long returns a multi-line string suitable for `wwtr --version` output.
func Long() string {
	return fmt.Sprintf("wwtr version %s\ncommit: %s\nbuilt:  %s", version, commit, date)
}
