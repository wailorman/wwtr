package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/wailorman/wwtr/internal/runcontext"
	"github.com/wailorman/wwtr/internal/trust"
)

// RunTrust explicitly approves the config at path (or the discovered one when
// path is ""). PLAN §6: this is the scriptable/CI counterpart to the inline
// y/n prompt performed by EnsureTrust. The approval is recorded in
// ~/.config/wwtr/trust.yaml.
func RunTrust(ctx context.Context, rc *runcontext.RunContext, path string) error {
	cfgPath, err := resolveConfigPath(ctx, rc, path)
	if err != nil {
		return err
	}
	store := trust.NewStore(rc.Deps.FS, rc.TrustRegistryPath)
	if err := store.Add(cfgPath); err != nil {
		return fmt.Errorf("trust: %w", err)
	}
	slog.Info("trust: approved", "config", cfgPath)
	return nil
}

// RunUntrust revokes a previously-recorded approval. No-op (and no error) when
// the path was never approved.
func RunUntrust(ctx context.Context, rc *runcontext.RunContext, path string) error {
	cfgPath, err := resolveConfigPath(ctx, rc, path)
	if err != nil {
		return err
	}
	store := trust.NewStore(rc.Deps.FS, rc.TrustRegistryPath)
	if err := store.Remove(cfgPath); err != nil {
		return fmt.Errorf("untrust: %w", err)
	}
	slog.Info("untrust: revoked", "config", cfgPath)
	return nil
}

// resolveConfigPath absolutises an explicit path or falls back to the
// worktree-discovered config. We deliberately do NOT run var resolution here:
// trust must succeed even when the config has unresolved vars (otherwise the
// user could not bootstrap a partially-broken config).
func resolveConfigPath(ctx context.Context, rc *runcontext.RunContext, path string) (string, error) {
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("trust: resolve path %s: %w", path, err)
		}
		return abs, nil
	}
	return DiscoverConfigPath(ctx, rc)
}
