// Package app orchestrates the multi-step command flows defined in PLAN §9.
// Each Run<X> function takes a context and a [runcontext.RunContext] produced
// by cmd/, runs Discover for the shared preamble, then drives the per-command
// pipeline (hooks/files/state) against internal/* packages.
//
// Errors propagate up to cmd/ which maps them to exit codes via ExitCode.
package app

import (
	"errors"

	"github.com/wailorman/wwtr/internal/files"
	"github.com/wailorman/wwtr/internal/hooks"
	"github.com/wailorman/wwtr/internal/trust"
	"github.com/wailorman/wwtr/internal/vars"
)

// Exit codes per PLAN §20.
const (
	ExitOK       = 0
	ExitGeneral  = 1
	ExitUsage    = 2
	ExitTrust    = 3
	ExitVar      = 4
	ExitPreHook  = 5
	ExitConflict = 6
)

// ExitCode maps an error returned by Run<X> to a process exit code. The
// sentinel-error checks are ordered so the most specific match wins; an unknown
// error collapses to ExitGeneral.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, trust.ErrDenied) || errors.Is(err, trust.ErrNeedsApproval) {
		return ExitTrust
	}
	if errors.Is(err, vars.ErrUnresolved) {
		return ExitVar
	}
	if errors.Is(err, hooks.ErrAborted) {
		return ExitPreHook
	}
	if errors.Is(err, files.ErrAborted) {
		return ExitConflict
	}
	return ExitGeneral
}
