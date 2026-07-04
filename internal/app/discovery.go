package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"

	"github.com/wailorman/wwtr/internal/conditions"
	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/files"
	"github.com/wailorman/wwtr/internal/hooks"
	"github.com/wailorman/wwtr/internal/runcontext"
	"github.com/wailorman/wwtr/internal/state"
	"github.com/wailorman/wwtr/internal/trust"
	"github.com/wailorman/wwtr/internal/vars"
	"github.com/wailorman/wwtr/internal/worktree"
)

// Ctx is the per-command application context: config + worktree + resolved
// vars. Built by Discover and threaded through every command's pipeline.
type Ctx struct {
	RC *runcontext.RunContext

	Cfg        *config.Config
	WT         worktree.Info
	Builtin    vars.BuiltinVars
	Vars       map[string]string
	PromptVars map[string]string

	StatePath  string
	ConfigPath string
}

// Discover performs the shared preamble every command needs (PLAN §9):
//  1. Find main + current worktree + branch via [worktree.Discover].
//  2. Locate .wwtr.yml: --config flag, else <main>/.wwtr.yml, else <main>/.wwtr.yaml.
//  3. Load config via [config.Load].
//  4. Populate RunContext.MainPath/CurrentPath/Branch.
//  5. Read state.yaml (unless --no-state or cmd == "init").
//  6. Resolve vars via [vars.Resolver]; cmd controls the source priority.
//  7. Compute BuiltinVars via [vars.ComputeBuiltins].
//
// cmd is one of: init, setup, start, stop, clean, info, trust, untrust.
// "init" uses CLI>ENV>prompt>default; every other command skips prompt and
// inserts state between CLI and ENV.
func Discover(ctx context.Context, rc *runcontext.RunContext, cmd string) (*Ctx, error) {
	wt, err := worktree.Discover(ctx, rc.Deps.Git)
	if err != nil {
		return nil, err
	}
	rc.MainPath = wt.MainPath
	rc.CurrentPath = wt.CurrentPath
	rc.Branch = wt.Branch

	cfgPath, err := locateConfig(rc, wt.MainPath)
	if err != nil {
		return nil, err
	}

	cfg, err := config.Load(rc.Deps.FS, cfgPath)
	if err != nil {
		return nil, err
	}

	stateMap := map[string]string{}
	if cmd != "init" && !rc.Flags.NoState {
		stateMap, err = state.Read(rc.Deps.FS, wt.CurrentPath)
		if err != nil {
			return nil, err
		}
	}

	builtin := vars.ComputeBuiltins(wt.Branch, wt.CurrentPath, wt.MainPath)

	resolver := vars.NewResolver(vars.ResolverDeps{
		Prompter: rc.Deps.Prompter,
		Env:      rc.Deps.Env,
		CLIArgs:  rc.Flags.CLIVars,
		State:    stateMap,
		Builtin:  builtin,
		Command:  cmd,
		NoState:  rc.Flags.NoState,
	})
	resolved, err := resolver.Resolve(toVarSpecs(cfg.Vars()))
	if err != nil {
		return nil, err
	}

	return &Ctx{
		RC:         rc,
		Cfg:        cfg,
		WT:         wt,
		Builtin:    builtin,
		Vars:       resolved,
		PromptVars: resolver.PromptResolved(),
		StatePath:  state.Path(wt.CurrentPath),
		ConfigPath: cfgPath,
	}, nil
}

// DiscoverConfigPath is a lighter helper for trust/untrust: it finds the
// worktree + config path WITHOUT running var resolution or state reads. The
// trust commands must work even when vars would fail (a half-broken config
// still needs to be approvable).
func DiscoverConfigPath(ctx context.Context, rc *runcontext.RunContext) (string, error) {
	wt, err := worktree.Discover(ctx, rc.Deps.Git)
	if err != nil {
		return "", err
	}
	rc.MainPath = wt.MainPath
	rc.CurrentPath = wt.CurrentPath
	rc.Branch = wt.Branch
	return locateConfig(rc, wt.MainPath)
}

func locateConfig(rc *runcontext.RunContext, mainPath string) (string, error) {
	if rc.Flags.Config != "" {
		return rc.Flags.Config, nil
	}
	if yml := filepath.Join(mainPath, ".wwtr.yml"); rc.Deps.FS.Exists(yml) {
		return yml, nil
	}
	if yaml := filepath.Join(mainPath, ".wwtr.yaml"); rc.Deps.FS.Exists(yaml) {
		return yaml, nil
	}
	return "", fmt.Errorf("config: %w (.wwtr.yml not found in %s)", config.ErrNotFound, mainPath)
}

func toVarSpecs(vs []config.Var) []vars.VarSpec {
	out := make([]vars.VarSpec, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}

// EnsureTrust wraps [trust.Ensure] for the command pipeline. cmd=="info" is
// the documented exception (PLAN §9: "trust не требуется") and short-circuits
// to nil. --yes (rc.Flags.Yes) auto-approves Unknown/Changed configs.
func EnsureTrust(_ context.Context, actx *Ctx, cmd string) error {
	if cmd == "info" {
		return nil
	}
	store := trust.NewStore(actx.RC.Deps.FS, actx.RC.TrustRegistryPath)
	return trust.Ensure(store, actx.RC.Deps.Prompter, actx.RC.Deps.TTY, actx.ConfigPath, actx.RC.Flags.Yes)
}

// buildHookOpts assembles the hooks.Options shared by every command that runs
// hooks. The conditions.Evaluator captures builtin+vars+worktree paths once so
// every `when:` clause in the run sees the same facts.
func buildHookOpts(actx *Ctx, log *slog.Logger) hooks.Options {
	return hooks.Options{
		Shell:    actx.RC.Deps.Shell,
		FS:       actx.RC.Deps.FS,
		Env:      actx.RC.Deps.Env,
		Log:      log,
		Stdout:   actx.RC.Deps.Stdout,
		Stderr:   actx.RC.Deps.Stderr,
		Cond:     buildConditions(actx),
		Builtin:  actx.Builtin,
		UserVars: actx.Vars,
		DryRun:   actx.RC.Flags.DryRun,
		NoHooks:  actx.RC.Flags.NoHooks,
	}
}

// buildFileOpts assembles the files.Options shared by init/clean. Force is true
// when --force OR --yes is set: --yes auto-approves every y/n including the
// conflict-prompt (PLAN §3 "auto-approve trust and all y/n prompts").
func buildFileOpts(actx *Ctx, log *slog.Logger) files.Options {
	rc := actx.RC
	return files.Options{
		FS:          rc.Deps.FS,
		Prompter:    rc.Deps.Prompter,
		Log:         log,
		MainPath:    actx.WT.MainPath,
		CurrentPath: actx.WT.CurrentPath,
		IsMain:      actx.WT.IsMain(),
		Force:       rc.Flags.Force || rc.Flags.Yes,
		Skip:        rc.Flags.Skip,
		DryRun:      rc.Flags.DryRun,
		Builtin:     actx.Builtin,
		UserVars:    actx.Vars,
	}
}

func buildConditions(actx *Ctx) *conditions.Evaluator {
	return conditions.New(
		actx.RC.Deps.FS,
		actx.RC.Deps.Env,
		actx.RC.Deps.Shell,
		actx.WT.CurrentPath,
		actx.WT.MainPath,
		actx.Builtin,
		actx.Vars,
	)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
