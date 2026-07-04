// Package cmd wires cobra commands to internal/app/* implementations. Each
// command file in this package is intentionally thin: parse flags, build a
// runcontext.RunContext, delegate to the corresponding app.Run function.
//
// The package name is "cmd" (not "commands" or "wwtrcmd") because cobra's own
// generator uses "cmd" and the convention is well-established.
package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/prompt"
	"github.com/wailorman/wwtr/internal/runcontext"
	"github.com/wailorman/wwtr/internal/version"
)

// rootFlags is the receiver for cobra's PersistentFlags parsing. We use a
// struct rather than package-level globals so tests can construct independent
// root commands when needed.
type rootFlags struct {
	config  string
	force   bool
	skip    bool
	dryRun  bool
	noHooks bool
	yes     bool
	noState bool
	verbose bool
}

// newRootFlags returns a zero-value rootFlags bound to the persistent flags of
// the given cobra command.
func newRootFlags(c *cobra.Command) *rootFlags {
	f := &rootFlags{}
	c.PersistentFlags().StringVar(&f.config, "config", "", "path to .wwtr.yml (bypass auto-discovery)")
	c.PersistentFlags().BoolVar(&f.force, "force", false, "overwrite files without prompting (init)")
	c.PersistentFlags().BoolVar(&f.skip, "skip", false, "skip conflicts without prompting (init)")
	c.PersistentFlags().BoolVar(&f.dryRun, "dry-run", false, "show what would happen, do not execute")
	c.PersistentFlags().BoolVar(&f.noHooks, "no-hooks", false, "skip all hooks (file operations only)")
	c.PersistentFlags().BoolVar(&f.yes, "yes", false, "auto-approve trust and all y/n prompts (for CI)")
	c.PersistentFlags().BoolVar(&f.noState, "no-state", false, "ignore .wwtr/state.yaml on read AND write")
	c.PersistentFlags().BoolVarP(&f.verbose, "verbose", "v", false, "verbose (debug) logging")
	return f
}

// toRunContext assembles a runcontext.RunContext from parsed flags. The
// side-effect dependencies (Deps) default to real OS implementations; callers
// can override RunContext.Deps in tests. The worktree paths and Branch are
// filled in later by internal/worktree.Discover.
func (f *rootFlags) toRunContext() *runcontext.RunContext {
	deps := di.DefaultDeps()
	deps.TTY = di.OsTTY{ForceNonInteractive: f.yes}
	deps.Prompter = prompt.New(deps.TTY, deps.Stdout)

	trustPath := ""
	if cfgDir, err := os.UserConfigDir(); err == nil {
		trustPath = filepath.Join(cfgDir, "wwtr", "trust.yaml")
	}

	return &runcontext.RunContext{
		Flags: runcontext.Flags{
			Config:  f.config,
			Force:   f.force,
			Skip:    f.skip,
			DryRun:  f.dryRun,
			NoHooks: f.noHooks,
			Yes:     f.yes,
			NoState: f.noState,
			Verbose: f.verbose,
		},
		Deps:              deps,
		TrustRegistryPath: trustPath,
	}
}

// configureLogger installs a slog text-handler on the program's stderr. Verbose
// flips to debug level. stdout is left untouched so commands like `info --env`
// can be safely piped through eval.
func configureLogger(w io.Writer, verbose bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
	return slog.Default()
}

// NewRootCmd builds the root cobra command with all global flags and subcommand
// stubs registered. Subcommands are added here as they're implemented.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "wwtr",
		Short:         "worktree wrapper — declarative .wwtr.yml helper for `git worktree`",
		Long:          "wwtr complements `git worktree` with templated files, symlinks, copies and lifecycle hooks, all declared in .wwtr.yml.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version(),
	}
	root.SetVersionTemplate(version.Long() + "\n")

	rf := newRootFlags(root)

	// Register dynamic `vars.<name>.sources[].cli` flags from .wwtr.yml BEFORE
	// cobra parses argv, so that `wwtr init --base-port 4017` works as expected.
	registerCLISourceFlags(root)

	// PersistentPreRunE: build RunContext, set up logger. The actual RunContext
	// is stashed on the command via SetContext so subcommands can read it.
	root.PersistentPreRunE = func(c *cobra.Command, _ []string) error {
		configureLogger(os.Stderr, rf.verbose)
		rc := rf.toRunContext()
		rc.Flags.CLIVars = collectCLIVars(root)
		c.SetContext(contextWithRC(c.Context(), rc))
		return nil
	}

	// Subcommands are added by their own files via add<Name>Cmd(root).
	addVersionCmd(root)
	addInitCmd(root)
	addSetupCmd(root)
	addStartCmd(root)
	addStopCmd(root)
	addCleanCmd(root)
	addInfoCmd(root)
	addTrustCmd(root)
	addUntrustCmd(root)
	return root
}

// Execute is the entry point called from main.go. It resolves the root command
// and runs it against os.Args. The version is injected via ldflags.
func Execute(v, commit, date string) error {
	version.Set(v, commit, date)
	if err := NewRootCmd().Execute(); err != nil {
		return err
	}
	return nil
}

// contextWithRC is a tiny helper for stashing a RunContext in a context.Context.
// It is intentionally not exported — only the cmd package should fish it out.
type rcKey struct{}

func contextWithRC(parent context.Context, rc *runcontext.RunContext) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithValue(parent, rcKey{}, rc)
}

// RunContextFromCmd extracts the RunContext previously stashed by
// PersistentPreRunE. Returns an error if called outside a command tree (e.g.
// directly in unit tests) — those callers should build a RunContext manually.
func RunContextFromCmd(c *cobra.Command) (*runcontext.RunContext, error) {
	ctx := c.Context()
	if ctx == nil {
		return nil, fmt.Errorf("cmd: no context on command (PersistentPreRunE did not run)")
	}
	v := ctx.Value(rcKey{})
	if v == nil {
		return nil, fmt.Errorf("cmd: no RunContext in context (PersistentPreRunE did not run)")
	}
	return v.(*runcontext.RunContext), nil
}

// Ensure context import is used.
var _ context.Context
