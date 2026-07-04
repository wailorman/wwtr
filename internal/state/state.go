// Package state reads and writes the per-worktree state file at
// `.wwtr/state.yaml`. State stores ONLY the values of variables that were
// resolved through an interactive prompt during `init` (see PLAN §5); every
// other variable is deterministic and re-derived on each command, so it never
// enters this file.
//
// The package is intentionally unaware of that policy: it just (de)serialises a
// flat `key: value` map. The init orchestrator decides what to put in the map;
// the non-init var resolver reads it back as one source among several.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailorman/wwtr/internal/di"
	"gopkg.in/yaml.v3"
)

const (
	dirName  = ".wwtr"
	fileName = "state.yaml"
)

// DirPath returns the absolute path of the `.wwtr` directory inside worktreeDir.
// It is a pure string operation; the directory need not exist.
func DirPath(worktreeDir string) string {
	return filepath.Join(worktreeDir, dirName)
}

// Path returns the absolute path of `state.yaml` inside worktreeDir's `.wwtr`
// directory. Pure string operation; the file need not exist.
func Path(worktreeDir string) string {
	return filepath.Join(DirPath(worktreeDir), fileName)
}

// Read loads state.yaml. A missing file is not an error: it yields an empty,
// non-nil map. Any other read or parse failure is wrapped and returned.
func Read(fs di.FS, worktreeDir string) (map[string]string, error) {
	data, err := fs.ReadFile(Path(worktreeDir))
	if err != nil {
		// The common case — first run, or after `clean`. Treat as empty state.
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("state: read %s: %w", Path(worktreeDir), err)
	}
	out := map[string]string{}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("state: parse %s: %w", Path(worktreeDir), err)
	}
	return out, nil
}

// Write persists vars to state.yaml. An empty map deletes the file instead
// (PLAN §5: the file exists only while there is something to remember), and is
// a no-op when the file is already absent. The `.wwtr` directory is created if
// needed.
func Write(fs di.FS, worktreeDir string, vars map[string]string) error {
	if len(vars) == 0 {
		return Remove(fs, worktreeDir)
	}
	if err := fs.MkdirAll(DirPath(worktreeDir), 0o755); err != nil {
		return fmt.Errorf("state: create %s: %w", DirPath(worktreeDir), err)
	}
	data, err := yaml.Marshal(vars)
	if err != nil {
		return fmt.Errorf("state: encode: %w", err)
	}
	if err := fs.WriteFile(Path(worktreeDir), data, 0o644); err != nil {
		return fmt.Errorf("state: write %s: %w", Path(worktreeDir), err)
	}
	return nil
}

// Remove deletes state.yaml. It is a no-op (no error) when the file is absent —
// matching the `clean` lifecycle where the file may already be gone.
func Remove(fs di.FS, worktreeDir string) error {
	if err := fs.Remove(Path(worktreeDir)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("state: remove %s: %w", Path(worktreeDir), err)
	}
	return nil
}
