package worktree_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/worktree"
)

func TestDiscover_Success(t *testing.T) {
	t.Parallel()
	g := &fakes.FakeGit{
		MainVal:    "/repo",
		CurrentVal: "/repo-feature-x",
		BranchVal:  "feature/x",
	}
	info, err := worktree.Discover(context.Background(), g)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if info.MainPath != "/repo" {
		t.Errorf("MainPath=%q", info.MainPath)
	}
	if info.CurrentPath != "/repo-feature-x" {
		t.Errorf("CurrentPath=%q", info.CurrentPath)
	}
	if info.Branch != "feature/x" {
		t.Errorf("Branch=%q", info.Branch)
	}
	if info.IsMain() {
		t.Error("IsMain=true want false")
	}
	// All three calls fire exactly once.
	if g.MainCalls != 1 || g.CurrentCalls != 1 || g.BranchCalls != 1 {
		t.Errorf("call counts: main=%d current=%d branch=%d", g.MainCalls, g.CurrentCalls, g.BranchCalls)
	}
}

func TestDiscover_MainWorktreeFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("git rev-parse failed")
	g := &fakes.FakeGit{MainErr: boom}
	_, err := worktree.Discover(context.Background(), g)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "main worktree") {
		t.Errorf("error should mention main worktree: %v", err)
	}
	if !strings.Contains(err.Error(), "git rev-parse failed") {
		t.Errorf("error should include wrapped cause: %v", err)
	}
	// Failure short-circuits: only MainWorktree was called.
	if g.CurrentCalls != 0 || g.BranchCalls != 0 {
		t.Errorf("later calls should not fire: current=%d branch=%d", g.CurrentCalls, g.BranchCalls)
	}
}

func TestDiscover_CurrentWorktreeFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("--show-toplevel failed")
	g := &fakes.FakeGit{
		MainVal:    "/repo",
		CurrentErr: boom,
	}
	_, err := worktree.Discover(context.Background(), g)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "current worktree") {
		t.Errorf("error should mention current worktree: %v", err)
	}
	if !strings.Contains(err.Error(), "--show-toplevel failed") {
		t.Errorf("error should include cause: %v", err)
	}
	if g.BranchCalls != 0 {
		t.Errorf("Branch should not fire on Current failure: %d", g.BranchCalls)
	}
}

func TestDiscover_BranchFails(t *testing.T) {
	t.Parallel()
	boom := errors.New("--abbrev-ref failed")
	g := &fakes.FakeGit{
		MainVal:    "/repo",
		CurrentVal: "/repo",
		BranchErr:  boom,
	}
	_, err := worktree.Discover(context.Background(), g)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "branch") {
		t.Errorf("error should mention branch: %v", err)
	}
	if !strings.Contains(err.Error(), "--abbrev-ref failed") {
		t.Errorf("error should include cause: %v", err)
	}
}

func TestDiscover_ContextPropagated(t *testing.T) {
	t.Parallel()
	// The context is passed through to every Git call. Use a cancelled context
	// and a FakeGit that honours it via a custom wrapper to verify Discover
	// threads it through.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	g := &ctxAwareGit{FakeGit: &fakes.FakeGit{MainVal: "/repo"}}
	_, err := worktree.Discover(ctx, g)
	if err == nil {
		t.Fatal("want context error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
}

// ctxAwareGit wraps fakes.FakeGit and returns ctx.Err() from every method when
// the context is done. Demonstrates that Discover threads ctx through.
type ctxAwareGit struct {
	*fakes.FakeGit
}

func (g *ctxAwareGit) MainWorktree(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return g.FakeGit.MainWorktree(ctx)
}

func TestInfo_IsMain_True(t *testing.T) {
	t.Parallel()
	info := worktree.Info{MainPath: "/repo", CurrentPath: "/repo", Branch: "main"}
	if !info.IsMain() {
		t.Error("IsMain=false want true")
	}
}

func TestInfo_IsMain_False_DifferentPaths(t *testing.T) {
	t.Parallel()
	info := worktree.Info{MainPath: "/repo", CurrentPath: "/repo-x", Branch: "x"}
	if info.IsMain() {
		t.Error("IsMain=true want false")
	}
}

func TestInfo_IsMain_False_EmptyPaths(t *testing.T) {
	t.Parallel()
	cases := []worktree.Info{
		{},
		{MainPath: "/repo"},
		{CurrentPath: "/repo"},
	}
	for i, info := range cases {
		if info.IsMain() {
			t.Errorf("case %d: IsMain=true want false for empty path(s)", i)
		}
	}
}

func TestInfo_FieldsAccessible(t *testing.T) {
	t.Parallel()
	// Smoke-test that the struct fields are exported and readable.
	info := worktree.Info{MainPath: "m", CurrentPath: "c", Branch: "b"}
	if info.MainPath != "m" || info.CurrentPath != "c" || info.Branch != "b" {
		t.Errorf("fields wrong: %+v", info)
	}
}
