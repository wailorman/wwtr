// Package di defines the dependency-injection interfaces that isolate every
// side-effect in wwtr. Business packages depend on these interfaces, never on
// concrete OS implementations. Tests inject fakes from [github.com/wailorman/wwtr/internal/di/fakes].
//
// The split is intentional: pure business logic (vars resolution, conflict
// decision tree, hook ordering, template rendering) lives in the internal/*
// packages and is fully testable without touching the real filesystem,
// network, or subprocesses.
package di

import (
	"context"
	"io"
	"os"
	"time"
)

// FS is the filesystem abstraction used by config/state/trust/files/conditions.
// It is narrower than afero.Fs on purpose — only the operations wwtr needs.
type FS interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	Lstat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	Remove(path string) error
	RemoveAll(path string) error
	Symlink(target, link string) error
	Readlink(link string) (string, error)
	// Exists is a convenience for the common pattern in conditions/files.
	// Returns false on any Stat error (including permission denied); callers
	// needing to distinguish errors should call Stat directly.
	Exists(path string) bool
}

// ShellRunner executes a single shell command string. The command is passed
// verbatim to the platform shell (sh -c on Unix, cmd /c on Windows); argv
// splitting is the shell's responsibility. Stdout and stderr are captured
// fully — wwtr does not stream hook output live (MVP decision; see PLAN §7).
type ShellRunner interface {
	Run(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error)
}

// Git wraps the small subset of `git` commands wwtr needs to discover the
// worktree layout. Implementations shell out to `git` in the current working
// directory.
type Git interface {
	// CommonDir returns the absolute path to the common git directory
	// (`git rev-parse --git-common-dir`).
	CommonDir(ctx context.Context) (string, error)
	// CurrentWorktree returns the absolute path of the current worktree
	// (`git rev-parse --show-toplevel`).
	CurrentWorktree(ctx context.Context) (string, error)
	// MainWorktree returns the absolute path of the main worktree, i.e. the
	// parent of the common git directory.
	MainWorktree(ctx context.Context) (string, error)
	// Branch returns the current branch name (`git rev-parse --abbrev-ref HEAD`).
	Branch(ctx context.Context) (string, error)
}

// Env exposes process environment variables. Use the [os.LookupEnv]-style
// Lookup form whenever the distinction between "unset" and "empty" matters.
type Env interface {
	Get(name string) string
	Lookup(name string) (string, bool)
}

// Decision is the result of a conflict prompt (see PLAN §10: Y/n/a/q/d).
type Decision int

const (
	DecisionUnknown Decision = iota
	DecisionYes
	DecisionNo
	DecisionAll
	DecisionQuit
	DecisionDiff
)

// Prompter abstracts interactive prompts. All methods must respect
// TTYChecker.IsInteractive: in non-interactive mode Confirm returns its
// defaultYes and Input returns an error (an unresolved var). The --yes flag
// is enforced by the caller, not here.
type Prompter interface {
	// Confirm asks a yes/no question. defaultYes controls what [Enter] does.
	Confirm(message string, defaultYes bool) (bool, error)
	// Input asks for a free-form string. validateRegex is optional ("" = none);
	// when provided the input is retried until it matches.
	Input(message, defaultVal, validateRegex string) (string, error)
	// Conflict asks how to proceed when a file operation would overwrite an
	// existing file. diffFn is called only if the user picks "diff".
	Conflict(path string, diffFn func() (string, error)) (Decision, error)
}

// TTYChecker reports whether the current session can do interactive I/O.
type TTYChecker interface {
	IsInteractive() bool
}

// Clock returns the current time. Injected so time-dependent logic is
// deterministic in tests.
type Clock interface {
	Now() time.Time
}

// Deps is the bundle of side-effectful services passed to business logic.
// Construct one of these per command invocation (cmd/root.go assembles it from
// global flags) and thread it through via [github.com/wailorman/wwtr/internal/runcontext.RunContext].
type Deps struct {
	FS       FS
	Shell    ShellRunner
	Git      Git
	Env      Env
	Prompter Prompter
	TTY      TTYChecker
	Clock    Clock
	Stdout   io.Writer
	Stderr   io.Writer
}
