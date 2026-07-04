package fakes

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wailorman/wwtr/internal/di"
)

// ---------------------------------------------------------------------------
// FakeFS
// ---------------------------------------------------------------------------

func TestFakeFS_WriteReadRoundTrip(t *testing.T) {
	t.Parallel()
	fs := NewFakeFS()
	if err := fs.WriteFile("/a/b.txt", []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := fs.ReadFile("/a/b.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("got %q want %q", got, "hello")
	}
	if !fs.Exists("/a/b.txt") {
		t.Fatal("Exists false after write")
	}
	// Implicit parent dir creation.
	if _, err := fs.Stat("/a"); err != nil {
		t.Fatalf("parent /a should exist: %v", err)
	}
}

func TestFakeFS_ReadMissingIsErrNotExist(t *testing.T) {
	t.Parallel()
	fs := NewFakeFS()
	_, err := fs.ReadFile("/nope")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v want os.ErrNotExist", err)
	}
	if fs.Exists("/nope") {
		t.Fatal("Exists true for missing file")
	}
}

func TestFakeFS_SymlinkTargetAndLstat(t *testing.T) {
	t.Parallel()
	fs := NewFakeFS()
	if err := fs.WriteFile("/etc/host", []byte("127.0.0.1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fs.Symlink("/etc/host", "/host"); err != nil {
		t.Fatal(err)
	}
	got, err := fs.Readlink("/host")
	if err != nil || got != "/etc/host" {
		t.Fatalf("Readlink got %q err %v", got, err)
	}
	data, err := fs.ReadFile("/host") // follows link
	if err != nil || string(data) != "127.0.0.1" {
		t.Fatalf("ReadFile via symlink got %q err %v", data, err)
	}
	fi, err := fs.Lstat("/host")
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Lstat mode %v: want symlink bit", fi.Mode())
	}
}

func TestFakeFS_RemoveAndRemoveAll(t *testing.T) {
	t.Parallel()
	fs := NewFakeFS()
	_ = fs.WriteFile("/x/y/1", []byte("a"), 0o644)
	_ = fs.WriteFile("/x/y/2", []byte("b"), 0o644)
	if err := fs.RemoveAll("/x"); err != nil {
		t.Fatal(err)
	}
	if fs.Exists("/x/y/1") || fs.Exists("/x/y/2") || fs.Exists("/x") {
		t.Fatal("RemoveAll left files behind")
	}
}

func TestFakeFS_InjectError(t *testing.T) {
	t.Parallel()
	fs := NewFakeFS()
	boom := errors.New("boom")
	fs.InjectError("/x", boom)
	if err := fs.WriteFile("/x", nil, 0o644); err != boom {
		t.Fatalf("got %v want boom", err)
	}
	// Second op on the same path should succeed (consume-once semantics).
	if err := fs.WriteFile("/x", []byte("ok"), 0o644); err != nil {
		t.Fatalf("second op should succeed, got %v", err)
	}
}

func TestFakeFS_SnapshotSortedAndTagged(t *testing.T) {
	t.Parallel()
	fs := NewFakeFS()
	_ = fs.WriteFile("/b", nil, 0o644)
	_ = fs.WriteFile("/a", nil, 0o644)
	_ = fs.Symlink("/a", "/l")
	_ = fs.MkdirAll("/d", 0o755)
	snap := fs.Snapshot()
	// Tags prefix every line; ordering is deterministic (sorted).
	for _, l := range snap {
		switch {
		case strings.HasPrefix(l, "F:"), strings.HasPrefix(l, "L:"), strings.HasPrefix(l, "D:"):
		default:
			t.Fatalf("unexpected snapshot line %q", l)
		}
	}
	if !slices.Contains(snap, "F:/a") {
		t.Fatalf("snapshot should contain F:/a; got %v", snap)
	}
}

// ---------------------------------------------------------------------------
// RecordShell
// ---------------------------------------------------------------------------

func TestRecordShell_FIFOAndCapture(t *testing.T) {
	t.Parallel()
	r := &RecordShell{}
	r.Program(
		ShellResult{Stdout: []byte("first")},
		ShellResult{ExitCode: 2, Err: errors.New("second")},
	)
	out, _, code, err := r.Run(context.Background(), "echo first")
	if err != nil || code != 0 || string(out) != "first" {
		t.Fatalf("call 1: out=%q code=%d err=%v", out, code, err)
	}
	_, _, code2, err2 := r.Run(context.Background(), "false")
	if code2 != 2 || err2 == nil {
		t.Fatalf("call 2: code=%d err=%v", code2, err2)
	}
	if len(r.Calls) != 2 || r.Calls[0] != "echo first" || r.Calls[1] != "false" {
		t.Fatalf("Calls captured wrong: %v", r.Calls)
	}
}

func TestRecordShell_ExhaustedErrors(t *testing.T) {
	t.Parallel()
	r := &RecordShell{}
	_, _, _, err := r.Run(context.Background(), "anything")
	if err == nil {
		t.Fatal("want error when no results programmed")
	}
}

// ---------------------------------------------------------------------------
// FakeGit
// ---------------------------------------------------------------------------

func TestFakeGit_StubbedReturns(t *testing.T) {
	t.Parallel()
	g := &FakeGit{
		CommonDirVal: "/repo/.git",
		CurrentVal:   "/repo",
		MainVal:      "/repo",
		BranchVal:    "main",
	}
	ctx := context.Background()
	if v, _ := g.CommonDir(ctx); v != "/repo/.git" {
		t.Fail()
	}
	if v, _ := g.CurrentWorktree(ctx); v != "/repo" {
		t.Fail()
	}
	if v, _ := g.MainWorktree(ctx); v != "/repo" {
		t.Fail()
	}
	if v, _ := g.Branch(ctx); v != "main" {
		t.Fail()
	}
	if g.CommonDirCalls != 1 || g.CurrentCalls != 1 || g.MainCalls != 1 || g.BranchCalls != 1 {
		t.Fatalf("call counters wrong: %+v", g)
	}
}

// ---------------------------------------------------------------------------
// MapEnv
// ---------------------------------------------------------------------------

func TestMapEnv(t *testing.T) {
	t.Parallel()
	e := MapEnv{Vars: map[string]string{"A": "1", "EMPTY": ""}}
	if e.Get("A") != "1" {
		t.Fail()
	}
	if v, ok := e.Lookup("MISSING"); ok || v != "" {
		t.Fatalf("missing should be ok=false")
	}
	if v, ok := e.Lookup("EMPTY"); !ok || v != "" {
		t.Fatalf("EMPTY should be ok=true, value=''")
	}
}

// ---------------------------------------------------------------------------
// FakePrompter
// ---------------------------------------------------------------------------

func TestFakePrompter_AllThreeKinds(t *testing.T) {
	t.Parallel()
	p := &FakePrompter{
		Confirms:  []bool{true},
		Inputs:    []string{"3010"},
		Decisions: []di.Decision{di.DecisionAll},
	}
	if v, _ := p.Confirm("ok?", false); !v {
		t.Fail()
	}
	if v, _ := p.Input("port?", "", `^\d+$`); v != "3010" {
		t.Fail()
	}
	if d, _ := p.Conflict("/x", nil); d != di.DecisionAll {
		t.Fail()
	}
	if len(p.Calls) != 3 {
		t.Fatalf("calls captured wrong: %v", p.Calls)
	}
}

func TestFakePrompter_ExhaustedErrors(t *testing.T) {
	t.Parallel()
	p := &FakePrompter{}
	if _, err := p.Confirm("?", false); err == nil {
		t.Fail()
	}
	if _, err := p.Input("?", "", ""); err == nil {
		t.Fail()
	}
	if _, err := p.Conflict("?", nil); err == nil {
		t.Fail()
	}
}

// ---------------------------------------------------------------------------
// FakeClock / FakeTTY
// ---------------------------------------------------------------------------

func TestFakeClock(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := FakeClock{T: t0}
	if !c.Now().Equal(t0) {
		t.Fail()
	}
}

func TestFakeTTY(t *testing.T) {
	t.Parallel()
	if !(FakeTTY{Interactive: true}).IsInteractive() {
		t.Fail()
	}
	if (FakeTTY{}).IsInteractive() {
		t.Fail()
	}
}

// ---------------------------------------------------------------------------
// BufferDeps bundle
// ---------------------------------------------------------------------------

func TestNewBufferDeps_AllReady(t *testing.T) {
	t.Parallel()
	b := NewBufferDeps()
	if b.FS == nil || b.Shell == nil || b.Git == nil || b.Stdout == nil {
		t.Fatal("BufferDeps field not initialised")
	}
	// Smoke: write+read round trip via FS.
	if err := b.FS.WriteFile("/a", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.FS.ReadFile("/a"); err != nil {
		t.Fatal(err)
	}
}
