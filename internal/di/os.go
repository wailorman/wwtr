package di

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OsFS is the FS implementation backed by the real operating-system filesystem.
type OsFS struct{}

func (OsFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (OsFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
func (OsFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (OsFS) Lstat(path string) (os.FileInfo, error)       { return os.Lstat(path) }
func (OsFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (OsFS) Remove(path string) error                     { return os.Remove(path) }
func (OsFS) RemoveAll(path string) error                  { return os.RemoveAll(path) }
func (OsFS) Symlink(target, link string) error            { return os.Symlink(target, link) }
func (OsFS) Readlink(link string) (string, error)         { return os.Readlink(link) }
func (OsFS) Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// OsShell runs commands via the platform shell (sh -c on Unix, cmd /c on
// Windows). The current working directory and environment are inherited from
// the parent process; wwtr sets no special env here (load_env hook effects
// live in the hooks executor, not in OsShell).
type OsShell struct{}

func (OsShell) Run(ctx context.Context, cmd string) (stdout, stderr []byte, exitCode int, err error) {
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/c", cmd)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}
	var out, errBuf bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errBuf
	runErr := c.Run()
	stdout = out.Bytes()
	stderr = errBuf.Bytes()
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			return stdout, stderr, ee.ExitCode(), ee
		}
		return stdout, stderr, -1, runErr
	}
	return stdout, stderr, 0, nil
}

// OsGit shells out to `git` in the current working directory.
type OsGit struct{}

func (OsGit) CommonDir(ctx context.Context) (string, error) {
	return gitOutput(ctx, "rev-parse", "--git-common-dir")
}

func (OsGit) CurrentWorktree(ctx context.Context) (string, error) {
	return gitOutput(ctx, "rev-parse", "--show-toplevel")
}

func (OsGit) MainWorktree(ctx context.Context) (string, error) {
	common, err := gitOutput(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(common)
	if err != nil {
		return "", err
	}
	return filepath.Dir(abs), nil
}

func (OsGit) Branch(ctx context.Context) (string, error) {
	return gitOutput(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

func gitOutput(ctx context.Context, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", args...)
	var out, errBuf bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errBuf
	if err := c.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(errBuf.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// OsEnv wraps os.Getenv / os.LookupEnv.
type OsEnv struct{}

func (OsEnv) Get(name string) string            { return os.Getenv(name) }
func (OsEnv) Lookup(name string) (string, bool) { return os.LookupEnv(name) }

// OsClock returns the real wall-clock time.
type OsClock struct{}

func (OsClock) Now() time.Time { return time.Now() }

// OsTTY reports whether both stdin and stdout are character devices and we
// were not asked to be non-interactive.
type OsTTY struct {
	ForceNonInteractive bool // set when --yes or CI is detected
}

func (t OsTTY) IsInteractive() bool {
	if t.ForceNonInteractive {
		return false
	}
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

// isTerminal returns true if f is a terminal. We use a minimal implementation
// to avoid pulling in a separate dependency; on Unix it's an ioctl, on Windows
// a console API. The golang.org/x/sys package we already (transitively) depend
// on is sufficient.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// DefaultDeps returns a Deps wired to real OS implementations, writing to
// os.Stdout / os.Stderr. cmd/root.go customises it based on flags (--yes flips
// OsTTY.ForceNonInteractive; --dry-run swaps FS for a no-op recorder, etc.).
func DefaultDeps() Deps {
	return Deps{
		FS:     OsFS{},
		Shell:  OsShell{},
		Git:    OsGit{},
		Env:    OsEnv{},
		Clock:  OsClock{},
		TTY:    OsTTY{},
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

// to avoid unused-import warnings while Prompter's OS impl is not yet wired
// (Phase 8). Replace with a real huh-backed implementation when prompt/ lands.
var _ io.Writer = (*bytes.Buffer)(nil)
