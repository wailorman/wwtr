// Package trust maintains the registry of approved .wwtr.yml configs.
// Each entry maps the absolute path of a config file to the SHA-256 of its
// bytes; any byte change (comments, key order, var renames) flips the hash and
// forces a re-prompt. See PLAN §6.
package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/wailorman/wwtr/internal/di"
	"gopkg.in/yaml.v3"
)

// Status is the outcome of comparing a config file against the registry.
type Status int

const (
	StatusTrusted Status = iota // hash matches the registry entry
	StatusChanged               // path is registered but hash differs
	StatusUnknown               // path is not in the registry at all
)

// Decision is what the user (or --yes) decided when prompted for an Unknown or
// Changed config. It is part of the package API so app/* can report it back to
// the user; Ensure below returns sentinel errors instead, which fits Go's
// standard error-propagation pattern.
type Decision int

const (
	DecisionApproved Decision = iota
	DecisionDenied
)

// ErrDenied is returned when the user refuses the trust prompt.
var ErrDenied = errors.New("trust: denied")

// ErrNeedsApproval is returned when a config is Unknown/Changed but no prompt
// can be asked (non-interactive session, no --yes). The caller should surface
// a hint about `wwtr trust` or `--yes`.
var ErrNeedsApproval = errors.New("trust: config requires explicit approval (use `wwtr trust` or --yes)")

// Store is the trust registry API. Every operation goes through di.FS so the
// same code path runs in production (OsFS) and in tests (fakes.FakeFS).
type Store interface {
	// Check returns the current trust status of the config at configPath.
	// Reads the file's SHA-256 and compares against the registry.
	Check(configPath string) (Status, error)
	// Add writes (or overwrites) the registry entry for configPath with the
	// current SHA-256 of the file.
	Add(configPath string) error
	// Remove deletes the registry entry for configPath. No-op if absent.
	Remove(configPath string) error
	// All returns the full registry contents (path → sha256). Used by `info`.
	All() (map[string]string, error)
}

// NewStore returns a Store backed by a YAML file at registryPath. In production
// registryPath is "$CONFIG/wwtr/trust.yaml" (resolved from os.UserConfigDir in
// cmd/root.go); tests pass any path inside a fake FS.
func NewStore(fs di.FS, registryPath string) Store {
	return &store{fs: fs, path: registryPath}
}

type store struct {
	fs   di.FS
	path string
}

// Check returns the trust status of the config at configPath. A missing
// registry file is treated as an empty registry (StatusUnknown for any path);
// a missing or unreadable config file is a hard error.
func (s *store) Check(configPath string) (Status, error) {
	hash, err := s.hashOf(configPath)
	if err != nil {
		return StatusUnknown, fmt.Errorf("read config: %w", err)
	}
	reg, err := s.loadRegistry()
	if err != nil {
		return StatusUnknown, fmt.Errorf("read registry: %w", err)
	}
	stored, ok := reg[configPath]
	if !ok {
		return StatusUnknown, nil
	}
	if stored == hash {
		return StatusTrusted, nil
	}
	return StatusChanged, nil
}

// Add writes (or overwrites) the registry entry for configPath with the current
// SHA-256 of the file's bytes. The registry's parent directory is created on
// demand so the first run in a fresh user-config dir works without extra setup.
func (s *store) Add(configPath string) error {
	hash, err := s.hashOf(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	reg, err := s.loadRegistry()
	if err != nil {
		return fmt.Errorf("read registry: %w", err)
	}
	reg[configPath] = hash
	return s.saveRegistry(reg)
}

// Remove deletes the registry entry for configPath. Missing entries and a
// missing registry file are both silent no-ops (no write happens).
func (s *store) Remove(configPath string) error {
	reg, err := s.loadRegistry()
	if err != nil {
		return fmt.Errorf("read registry: %w", err)
	}
	if _, ok := reg[configPath]; !ok {
		return nil
	}
	delete(reg, configPath)
	return s.saveRegistry(reg)
}

// All returns the full registry contents (absolute path → lowercase hex
// SHA-256). Used by `wwtr info` to list approvals.
func (s *store) All() (map[string]string, error) {
	return s.loadRegistry()
}

// loadRegistry reads and unmarshals the registry YAML. A missing file is not an
// error: it yields an empty (non-nil) map, which is how a fresh install looks.
func (s *store) loadRegistry() (map[string]string, error) {
	data, err := s.fs.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	reg := map[string]string{}
	if err := yaml.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	return reg, nil
}

// saveRegistry marshals the registry and writes it in place. MVP: not atomic
// (no tmp+rename); the registry is small and written from a single goroutine
// per command invocation, so the partial-write risk is acceptable. Revisit if
// the registry grows or becomes shared.
func (s *store) saveRegistry(reg map[string]string) error {
	if err := s.fs.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create registry dir: %w", err)
	}
	// yaml.Marshal on map[string]string is infallible in practice.
	data, _ := yaml.Marshal(reg)
	if err := s.fs.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write registry: %w", err)
	}
	return nil
}

// hashOf returns the lowercase hex SHA-256 of the file at configPath.
func (s *store) hashOf(configPath string) (string, error) {
	data, err := s.fs.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Ensure is the high-level gate called by every command except `info`. It
// never silently approves an Unknown/Changed config: either the user says yes
// (interactively or via autoYes) or it returns an error.
func Ensure(store Store, prompter di.Prompter, tty di.TTYChecker, configPath string, autoYes bool) error {
	status, err := store.Check(configPath)
	if err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	if status == StatusTrusted {
		return nil
	}
	if autoYes {
		if err := store.Add(configPath); err != nil {
			return fmt.Errorf("trust: %w", err)
		}
		return nil
	}
	if !tty.IsInteractive() {
		return ErrNeedsApproval
	}
	confirmed, err := prompter.Confirm(
		fmt.Sprintf("Config %q is new or has changed. Trust it?", configPath),
		false,
	)
	if err != nil {
		return fmt.Errorf("trust prompt: %w", err)
	}
	if !confirmed {
		return ErrDenied
	}
	if err := store.Add(configPath); err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	return nil
}
