package runcontext

import (
	"testing"

	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/di/fakes"
)

func TestRunContext_IsMain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		main       string
		current    string
		wantIsMain bool
	}{
		{"equal paths", "/repo", "/repo", true},
		{"different paths", "/repo", "/repo-wt-feature", false},
		{"empty main", "", "/repo", false},
		{"empty current", "/repo", "", false},
		{"both empty", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rc := &RunContext{MainPath: tc.main, CurrentPath: tc.current}
			if got := rc.IsMain(); got != tc.wantIsMain {
				t.Fatalf("IsMain=%v want %v", got, tc.wantIsMain)
			}
		})
	}
}

func TestRunContext_FlagsDefaults(t *testing.T) {
	t.Parallel()
	rc := &RunContext{Deps: di.Deps{}}
	if rc.Flags.Force || rc.Flags.Skip || rc.Flags.DryRun {
		t.Fatal("default Flags should all be false")
	}
}

// TestRunContext_DepsInjection is a smoke test that the di.Deps surface (real
// interfaces) accepts the fakes we will use throughout the test suite. If a
// fake signature drifts from the interface, this fails to compile.
func TestRunContext_DepsInjection(t *testing.T) {
	t.Parallel()
	b := fakes.NewBufferDeps()
	rc := &RunContext{
		Deps: di.Deps{
			FS:       b.FS,
			Shell:    b.Shell,
			Git:      b.Git,
			Env:      b.Env,
			Prompter: b.Prompter,
			TTY:      b.TTY,
			Clock:    b.Clock,
			Stdout:   b.Stdout,
			Stderr:   b.Stderr,
		},
	}
	if err := rc.Deps.FS.WriteFile("/probe", []byte("ok"), 0o644); err != nil {
		t.Fatalf("Deps.FS.WriteFile: %v", err)
	}
}
