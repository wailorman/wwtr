package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/runcontext"
	"github.com/wailorman/wwtr/internal/trust"
	"github.com/wailorman/wwtr/internal/vars"
)

// newTestRC builds a RunContext wired to a BufferDeps, suitable for table-driven
// app tests. The caller customises the deps via the returned fields.
func newTestRC(t *testing.T) (*runcontext.RunContext, *fakes.BufferDeps) {
	t.Helper()
	bd := fakes.NewBufferDeps()
	rc := &runcontext.RunContext{
		Deps: di.Deps{
			FS:       bd.FS,
			Shell:    bd.Shell,
			Git:      bd.Git,
			Env:      bd.Env,
			Prompter: bd.Prompter,
			TTY:      bd.TTY,
			Clock:    bd.Clock,
			Stdout:   bd.Stdout,
			Stderr:   bd.Stderr,
		},
		TrustRegistryPath: "/trust/trust.yaml",
	}
	// Disable slog noise during tests; packages that default to slog.Default
	// pick this up.
	slog.SetDefault(slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError + 100})))
	return rc, &bd
}

// seedMainConfig writes the given config bytes at /main/.wwtr.yml.
func seedMainConfig(t *testing.T, fs *fakes.FakeFS, body string) {
	t.Helper()
	if err := fs.WriteFile("/main/.wwtr.yml", []byte(body), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

// preApproveTrust writes a trust registry entry matching the current SHA-256
// of /main/.wwtr.yml, so EnsureTrust returns Trusted without prompting. Used
// by tests that exercise conflict-prompt flows where --yes would also auto-
// force file operations (PLAN §3: --yes auto-approves all y/n).
func preApproveTrust(t *testing.T, fs *fakes.FakeFS, configPath string) {
	t.Helper()
	body, err := fs.ReadFile(configPath)
	if err != nil {
		t.Fatalf("preApproveTrust read: %v", err)
	}
	sum := sha256.Sum256(body)
	store := trust.NewStore(fs, "/trust/trust.yaml")
	if err := store.Add(configPath); err != nil {
		// Fall back to writing the registry directly if Add mishandles the
		// directory. (trust.Store.Add MkdirAlls the parent, so this should
		// not happen; the branch exists for defensive clarity.)
		_ = sum
		t.Fatalf("preApproveTrust Add: %v", err)
	}
}

// workerGit configures a FakeGit to report a worker worktree at /worker with
// main at /main.
func workerGit(g *fakes.FakeGit) {
	g.MainVal = "/main"
	g.CurrentVal = "/worker"
	g.BranchVal = "feature/test"
}

// mainGit configures a FakeGit to report the main worktree as current.
func mainGit(g *fakes.FakeGit) {
	g.MainVal = "/main"
	g.CurrentVal = "/main"
	g.BranchVal = "main"
}

const minimalConfig = `version: 1
vars:
  greeting:
    value: 'hello {{ .SafeName }}'
  port:
    sources:
      - env: PORT
    default: 3000
`

func TestDiscover_Success(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	bd.Env.Vars["PORT"] = "4000"
	seedMainConfig(t, bd.FS, minimalConfig)

	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if actx.ConfigPath != "/main/.wwtr.yml" {
		t.Errorf("ConfigPath = %q, want /main/.wwtr.yml", actx.ConfigPath)
	}
	if actx.WT.MainPath != "/main" || actx.WT.CurrentPath != "/worker" {
		t.Errorf("WT paths main=%q current=%q", actx.WT.MainPath, actx.WT.CurrentPath)
	}
	if got := actx.Vars["port"]; got != "4000" {
		t.Errorf("port = %q, want 4000", got)
	}
	if got := actx.Vars["greeting"]; !strings.Contains(got, "hello") {
		t.Errorf("greeting = %q, want hello-prefix", got)
	}
	if actx.Builtin.Branch != "feature/test" {
		t.Errorf("Builtin.Branch = %q", actx.Builtin.Branch)
	}
	if actx.StatePath == "" {
		t.Error("StatePath empty")
	}
	if actx.PromptVars == nil {
		t.Error("PromptVars nil")
	}
}

func TestDiscover_GitError(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	gitErr := errors.New("git rev-parse: not in a worktree")
	bd.Git.MainErr = gitErr

	if _, err := Discover(context.Background(), rc, "setup"); err == nil ||
		!strings.Contains(err.Error(), "detect main worktree") {
		t.Errorf("Discover err = %v, want wrap of 'detect main worktree'", err)
	}
}

func TestDiscover_ConfigMissing(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	// No .wwtr.yml written.
	_, err := Discover(context.Background(), rc, "setup")
	if !errors.Is(err, config.ErrNotFound) {
		t.Errorf("Discover err = %v, want ErrNotFound", err)
	}
}

func TestDiscover_ConfigExplicitPathNotFound(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	rc.Flags.Config = "/main/missing.yml"
	_, err := Discover(context.Background(), rc, "setup")
	if !errors.Is(err, config.ErrNotFound) {
		t.Errorf("Discover err = %v, want ErrNotFound", err)
	}
}

func TestDiscover_ExplicitConfigFlagRespected(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	customCfg := "/custom/.wwtr.yml"
	if err := bd.FS.WriteFile(customCfg, []byte(minimalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.Flags.Config = customCfg

	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if actx.ConfigPath != customCfg {
		t.Errorf("ConfigPath = %q, want %q", actx.ConfigPath, customCfg)
	}
}

func TestDiscover_YamlExtension(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	if err := bd.FS.WriteFile("/main/.wwtr.yaml", []byte(minimalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if actx.ConfigPath != "/main/.wwtr.yaml" {
		t.Errorf("ConfigPath = %q", actx.ConfigPath)
	}
}

func TestDiscover_VarUnresolved(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, `version: 1
vars:
  must_have:
    sources:
      - env: MUST_HAVE
`) // no default, MUST_HAS unset
	_, err := Discover(context.Background(), rc, "setup")
	if !errors.Is(err, vars.ErrUnresolved) {
		t.Errorf("Discover err = %v, want ErrUnresolved", err)
	}
}

func TestDiscover_NoStateReadForInit(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	if err := bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("port: \"9999\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	actx, err := Discover(context.Background(), rc, "init")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// Init must NOT consult state; with PORT unset it falls back to default 3000.
	if got := actx.Vars["port"]; got != "3000" {
		t.Errorf("port = %q, want 3000 (init should not read state)", got)
	}
}

func TestDiscover_NoStateFlagSkipsState(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	if err := bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("port: \"9999\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.Flags.NoState = true
	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got := actx.Vars["port"]; got != "3000" {
		t.Errorf("port = %q, want 3000 (NoState should skip state)", got)
	}
}

func TestDiscover_StateReadForNonInit(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	if err := bd.FS.WriteFile("/worker/.wwtr/state.yaml", []byte("port: \"9999\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if got := actx.Vars["port"]; got != "9999" {
		t.Errorf("port = %q, want 9999 from state", got)
	}
}

func TestDiscoverConfigPath_NoVarResolution(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, `version: 1
vars:
  broken:
    sources:
      - env: BROKEN
`) // would fail var resolution
	path, err := DiscoverConfigPath(context.Background(), rc)
	if err != nil {
		t.Fatalf("DiscoverConfigPath: %v", err)
	}
	if path != "/main/.wwtr.yml" {
		t.Errorf("path = %q", path)
	}
}

func TestEnsureTrust_InfoSkips(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	actx, err := Discover(context.Background(), rc, "info")
	if err != nil {
		t.Fatal(err)
	}
	// No trust store seeded; EnsureTrust for info must not even attempt a check.
	if err := EnsureTrust(context.Background(), actx, "info"); err != nil {
		t.Errorf("EnsureTrust(info) = %v, want nil", err)
	}
}

func TestEnsureTrust_YesAutoApproves(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	rc.Flags.Yes = true

	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureTrust(context.Background(), actx, "setup"); err != nil {
		t.Errorf("EnsureTrust with --yes: %v", err)
	}
	// The trust store should now record the config hash.
	data, rerr := bd.FS.ReadFile("/trust/trust.yaml")
	if rerr != nil {
		t.Fatalf("read trust registry: %v", rerr)
	}
	if !strings.Contains(string(data), "/main/.wwtr.yml") {
		t.Errorf("trust registry = %q, want path present", string(data))
	}
}

func TestEnsureTrust_DeniedByUser(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	bd.Prompter.Confirms = []bool{false}

	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatal(err)
	}
	err = EnsureTrust(context.Background(), actx, "setup")
	if err == nil {
		t.Fatal("EnsureTrust nil err, want denial")
	}
	if code := ExitCode(err); code != ExitTrust {
		t.Errorf("ExitCode = %d, want %d (ExitTrust)", code, ExitTrust)
	}
}

func TestEnsureTrust_AlreadyTrusted(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	rc.Flags.Yes = true // bootstrap trust

	actx, err := Discover(context.Background(), rc, "setup")
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureTrust(context.Background(), actx, "setup"); err != nil {
		t.Fatal(err)
	}
	// Reset --yes; second call must succeed without prompting.
	rc.Flags.Yes = false
	bd.Prompter.Confirms = nil
	if err := EnsureTrust(context.Background(), actx, "setup"); err != nil {
		t.Errorf("EnsureTrust already-trusted: %v", err)
	}
}

func TestExitCode_Mapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"general", errors.New("boom"), ExitGeneral},
	}
	for _, tc := range cases {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("%s: got %d want %d", tc.name, got, tc.want)
		}
	}
}

// Suppress unused import if the test file occasionally drops a helper.
var _ = os.Args
