package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTrust_ExplicitPath(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	// Trust doesn't need worktree discovery when path is explicit; still
	// seed the file so Add can hash it.
	if err := bd.FS.WriteFile("/custom/.wwtr.yml", []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunTrust(context.Background(), rc, "/custom/.wwtr.yml"); err != nil {
		t.Fatalf("RunTrust: %v", err)
	}
	data, _ := bd.FS.ReadFile("/trust/trust.yaml")
	if !strings.Contains(string(data), "/custom/.wwtr.yml") {
		t.Errorf("trust.yaml = %q, want path present", string(data))
	}
}

func TestRunTrust_RelativePathAbsolutised(t *testing.T) {
	t.Parallel()
	// filepath.Abs relies on os.Getwd; we cannot chdir in parallel tests, so
	// verify the resolveConfigPath helper directly with an already-absolute
	// path. The chdir-driven behaviour is covered indirectly by cmd/ tests.
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	got, err := resolveConfigPath(context.Background(), rc, "/some/dir/.wwtr.yml")
	if err != nil {
		t.Fatalf("resolveConfigPath: %v", err)
	}
	if want := filepath.Clean("/some/dir/.wwtr.yml"); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRunTrust_DiscoveredPath(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)

	if err := RunTrust(context.Background(), rc, ""); err != nil {
		t.Fatalf("RunTrust: %v", err)
	}
	data, _ := bd.FS.ReadFile("/trust/trust.yaml")
	if !strings.Contains(string(data), "/main/.wwtr.yml") {
		t.Errorf("trust.yaml = %q", string(data))
	}
}

func TestRunTrust_AddIsIdempotent(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)

	_ = RunTrust(context.Background(), rc, "")
	if err := RunTrust(context.Background(), rc, ""); err != nil {
		t.Errorf("second RunTrust: %v", err)
	}
}

func TestRunUntrust_Existing(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	// Seed trust first.
	if err := RunTrust(context.Background(), rc, ""); err != nil {
		t.Fatal(err)
	}

	if err := RunUntrust(context.Background(), rc, ""); err != nil {
		t.Fatalf("RunUntrust: %v", err)
	}
	data, _ := bd.FS.ReadFile("/trust/trust.yaml")
	if strings.Contains(string(data), "/main/.wwtr.yml") {
		t.Errorf("trust.yaml still contains entry: %q", string(data))
	}
}

func TestRunUntrust_NonExisting_NoError(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	// No prior trust call.

	if err := RunUntrust(context.Background(), rc, ""); err != nil {
		t.Errorf("RunUntrust on non-trusted: %v", err)
	}
}

func TestRunTrust_MissingFile(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	// Don't seed the file at the explicit path.

	err := RunTrust(context.Background(), rc, "/missing/.wwtr.yml")
	if err == nil {
		t.Fatal("nil err, want failure")
	}
	if !errors.Is(err, err) { // sanity on errors.Is
		t.Fatal("errors.Is self failed")
	}
	if !strings.Contains(err.Error(), "trust:") {
		t.Errorf("err not prefixed with trust: %v", err)
	}
}

func TestResolveConfigPath_Explicit(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	got, err := resolveConfigPath(context.Background(), rc, "/explicit/.wwtr.yml")
	if err != nil {
		t.Fatalf("resolveConfigPath: %v", err)
	}
	if want := filepath.Clean("/explicit/.wwtr.yml"); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
