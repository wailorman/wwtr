package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/di"
)

// builtinFlagNames are the flags cobra/wwtr register statically. The dynamic
// flag collector skips them so we don't accidentally feed --config into vars.
var builtinFlagNames = map[string]struct{}{
	"config": {}, "force": {}, "skip": {}, "dry-run": {}, "no-hooks": {},
	"yes": {}, "no-state": {}, "verbose": {}, "v": {},
	"help": {}, "version": {},
	"json": {}, "env": {}, // info-only local flags
}

// registerCLISourceFlags pre-discovers .wwtr.yml and registers each
// `vars.<name>.sources[].cli: "--<flag>"` entry as a real persistent string
// flag on root. Without this, `wwtr init --base-port 4017` would fail with
// "unknown flag: --base-port" — cobra requires explicit registration before
// argv parsing.
//
// Uses OS-only deps (the RunContext does not exist yet at NewRootCmd time).
func registerCLISourceFlags(root *cobra.Command) {
	registerCLISourceFlagsDeps(root, di.OsFS{}, di.OsGit{}, os.Args)
}

// registerCLISourceFlagsDeps is the testable form: deps are injected so we can
// drive discovery from fakes. argv is the raw os.Args slice.
func registerCLISourceFlagsDeps(root *cobra.Command, fs di.FS, git di.Git, argv []string) {
	cfgPath, ok := discoverConfigPathEarlyDeps(fs, git, argv)
	if !ok {
		return
	}
	cfg, err := config.Load(fs, cfgPath)
	if err != nil {
		return
	}
	seen := map[string]struct{}{}
	for _, v := range cfg.Vars() {
		for _, src := range v.Sources() {
			if src.CLI == "" {
				continue
			}
			// Normalize "--name" / "-n" → cobra flag name without dashes.
			name := strings.TrimLeft(src.CLI, "-")
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			if _, builtin := builtinFlagNames[name]; builtin {
				continue
			}
			seen[name] = struct{}{}
			if root.PersistentFlags().Lookup(name) == nil {
				root.PersistentFlags().String(name, "", "(from .wwtr.yml: var "+v.Name()+")")
			}
		}
	}
}

// collectCLIVars reads the values of all dynamic flags actually set on argv
// into a map keyed by the full "--<name>" form (as vars.cliValue expects).
// Unset flags are skipped so env/prompt/default can resolve the var instead.
func collectCLIVars(root *cobra.Command) map[string]string {
	out := map[string]string{}
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Changed {
			return
		}
		if _, builtin := builtinFlagNames[f.Name]; builtin {
			return
		}
		out["--"+f.Name] = f.Value.String()
	})
	return out
}

// discoverConfigPathEarlyDeps mirrors internal/app/locateConfig but runs
// before cobra parsing. Returns ("", false) when no config can be found
// without surfacing an error — the actual command flow will report the
// proper diagnostic. Production callers pass di.OsFS{}, di.OsGit{}, os.Args.
func discoverConfigPathEarlyDeps(fs di.FS, git di.Git, argv []string) (string, bool) {
	if cfg := findConfigArg(argv); cfg != "" && fs.Exists(cfg) {
		return cfg, true
	}
	ctx := context.Background()
	main, err := git.MainWorktree(ctx)
	if err != nil || main == "" {
		return "", false
	}
	for _, name := range []string{".wwtr.yml", ".wwtr.yaml"} {
		p := filepath.Join(main, name)
		if fs.Exists(p) {
			return p, true
		}
	}
	return "", false
}

// findConfigArg scans args for an explicit --config <path> or --config=<path>
// without invoking pflag (we don't want to consume the whole argv here).
func findConfigArg(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--config" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, "--config=") {
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return ""
}
