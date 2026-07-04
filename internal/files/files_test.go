package files_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/files"
	"github.com/wailorman/wwtr/internal/vars"
)

func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func baseOpts(fs di.FS, p di.Prompter) files.Options {
	return files.Options{
		FS:          fs,
		Prompter:    p,
		Log:         silentLog(),
		MainPath:    "/main",
		CurrentPath: "/current",
		Builtin:     vars.BuiltinVars{Branch: "feat", Slug: "feat"},
		UserVars:    map[string]string{"name": "world"},
	}
}

func seed(t *testing.T, fs *fakes.FakeFS, path, content string) {
	t.Helper()
	if err := fs.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed %s: %v", path, err)
	}
}

func readOrFail(t *testing.T, fs *fakes.FakeFS, path string) []byte {
	t.Helper()
	b, err := fs.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// diffPrompter wraps FakePrompter and invokes diffFn when DecisionDiff is
// returned, so tests can verify the diff callback fired. The real huh-based
// prompter handles diff display internally; this wrapper lets the unit tests
// exercise our outer re-prompt loop without depending on huh.
type diffPrompter struct {
	inner *fakes.FakePrompter
	diffs int
}

func (d *diffPrompter) Confirm(msg string, dy bool) (bool, error) {
	return d.inner.Confirm(msg, dy)
}

func (d *diffPrompter) Input(msg, dv, rx string) (string, error) {
	return d.inner.Input(msg, dv, rx)
}

func (d *diffPrompter) Conflict(path string, diffFn func() (string, error)) (di.Decision, error) {
	dec, err := d.inner.Conflict(path, diffFn)
	if err != nil {
		return dec, err
	}
	if dec == di.DecisionDiff {
		_, _ = diffFn()
		d.diffs++
	}
	return dec, nil
}

// --- Fresh writes ----------------------------------------------------------

func TestApply_FreshCopy(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}
	seed(t, fs, "/main/src.txt", "hello")

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src.txt", To: "dst.txt"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res) != 1 || res[0].Action != files.ActionWrote {
		t.Fatalf("got %+v, want one Wrote", res)
	}
	if got := string(readOrFail(t, fs, "/current/dst.txt")); got != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
	if len(p.Calls) != 0 {
		t.Fatalf("expected no prompts, got %v", p.Calls)
	}
}

func TestApply_FreshTemplate(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}
	seed(t, fs, "/main/tpl.tt", "hello {{ .Vars.name }}")

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, From: "tpl.tt", To: "out.txt"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res) != 1 || res[0].Action != files.ActionWrote {
		t.Fatalf("got %+v, want one Wrote", res)
	}
	if got := string(readOrFail(t, fs, "/current/out.txt")); got != "hello world" {
		t.Fatalf("rendered = %q, want %q", got, "hello world")
	}
}

func TestApply_InlineTemplateContent_RendersWithoutFile(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, To: "config/database.yml", Content: "adapter: pg\ndb: {{ .Vars.name }}\n"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res) != 1 || res[0].Action != files.ActionWrote {
		t.Fatalf("got %+v, want one Wrote", res)
	}
	got := string(readOrFail(t, fs, "/current/config/database.yml"))
	want := "adapter: pg\ndb: world\n"
	if got != want {
		t.Fatalf("rendered = %q, want %q", got, want)
	}
	// Sanity: no main-side file was ever consulted.
	if fs.Exists("/main/config/database.yml") {
		t.Fatalf("inline template should not touch MainPath")
	}
}

func TestApply_InlineTemplateContent_StaticNoDirectives(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, To: "static.txt", Content: "plain content, no directives"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v, want Wrote", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/static.txt")); got != "plain content, no directives" {
		t.Fatalf("content = %q", got)
	}
}

func TestApply_InlineTemplateContent_ErrorDoesNotMentionMainPath(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, To: "out", Content: "{{ .Vars.missing }}"},
	})
	if err == nil {
		t.Fatal("want render error for missing var")
	}
	// The message must point at the render stage, not pretend it tried to
	// ReadFile from MainPath (inline templates never touch the FS).
	if strings.Contains(err.Error(), "/main") {
		t.Fatalf("error should not reference MainPath: %v", err)
	}
	if !strings.Contains(err.Error(), "render template") {
		t.Fatalf("error should come from render stage: %v", err)
	}
}

func TestClean_InlineTemplateContent_RerendersForComparison(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/current/out", "v=world") // matches rendered inline output
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, To: "out", Content: "v={{ .Vars.name }}"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v, want Wrote (removed)", res[0].Action)
	}
	if fs.Exists("/current/out") {
		t.Fatalf("expected removed")
	}
}

func TestClean_InlineTemplateContent_ModifiedFallsBackToPrompt(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/current/out", "user-edited")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionNo}}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, To: "out", Content: "v={{ .Vars.name }}"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionSkipped {
		t.Fatalf("action = %v, want Skipped", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/out")); got != "user-edited" {
		t.Fatalf("content changed: %q", got)
	}
}

func TestApply_FreshSymlink(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}
	seed(t, fs, "/main/target.txt", "content")

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "target.txt", To: "link.lnk"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res) != 1 || res[0].Action != files.ActionWrote {
		t.Fatalf("got %+v, want one Wrote", res)
	}
	target, err := fs.Readlink("/current/link.lnk")
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if target != "/main/target.txt" {
		t.Fatalf("target = %q, want %q", target, "/main/target.txt")
	}
}

func TestApply_EmptyToDefaultsToFrom(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}
	seed(t, fs, "/main/.envrc", "content")

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: ".envrc"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !fs.Exists("/current/.envrc") {
		t.Fatalf("expected /current/.envrc to exist")
	}
}

// --- Overwrite decisions ---------------------------------------------------

func TestApply_Overwrite_DifferentContent_Decisions(t *testing.T) {
	tests := []struct {
		name        string
		decisions   []di.Decision
		wantAction  files.Action
		wantErr     bool
		wantContent string
	}{
		{"Yes", []di.Decision{di.DecisionYes}, files.ActionWrote, false, "new"},
		{"No", []di.Decision{di.DecisionNo}, files.ActionSkipped, false, "old"},
		{"Quit", []di.Decision{di.DecisionQuit}, files.ActionAborted, true, "old"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := fakes.NewFakeFS()
			seed(t, fs, "/main/src", "new")
			seed(t, fs, "/current/dst", "old")
			p := &fakes.FakePrompter{Decisions: tc.decisions}

			res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
				{Kind: files.OpCopy, From: "src", To: "dst"},
			})
			if tc.wantErr {
				if !errors.Is(err, files.ErrAborted) {
					t.Fatalf("err = %v, want ErrAborted", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if res[0].Action != tc.wantAction {
				t.Fatalf("action = %v, want %v", res[0].Action, tc.wantAction)
			}
			if got := string(readOrFail(t, fs, "/current/dst")); got != tc.wantContent {
				t.Fatalf("content = %q, want %q", got, tc.wantContent)
			}
		})
	}
}

func TestApply_Overwrite_DecisionAll_Propagates(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/a", "newA")
	seed(t, fs, "/main/b", "newB")
	seed(t, fs, "/current/a", "oldA")
	seed(t, fs, "/current/b", "oldB")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionAll}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "a", To: "a"},
		{Kind: files.OpCopy, From: "b", To: "b"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res) != 2 || res[0].Action != files.ActionWrote || res[1].Action != files.ActionWrote {
		t.Fatalf("got %+v, want two Wrote", res)
	}
	if len(p.Calls) != 1 {
		t.Fatalf("expected 1 prompt (All propagates), got %d", len(p.Calls))
	}
	if got := string(readOrFail(t, fs, "/current/a")); got != "newA" {
		t.Fatalf("a = %q", got)
	}
	if got := string(readOrFail(t, fs, "/current/b")); got != "newB" {
		t.Fatalf("b = %q", got)
	}
}

func TestApply_Overwrite_QuitStopsSubsequentOps(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/a", "newA")
	seed(t, fs, "/main/b", "newB")
	seed(t, fs, "/current/a", "oldA")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionQuit}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "a", To: "a"},
		{Kind: files.OpCopy, From: "b", To: "b"},
	})
	if !errors.Is(err, files.ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result (Quit truncates), got %d", len(res))
	}
	if res[0].Action != files.ActionAborted {
		t.Fatalf("action = %v", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/a")); got != "oldA" {
		t.Fatalf("a changed: %q", got)
	}
	if fs.Exists("/current/b") {
		t.Fatalf("subsequent op ran despite Quit")
	}
}

func TestApply_Overwrite_DiffThenYes(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "new")
	seed(t, fs, "/current/dst", "old")
	p := &diffPrompter{inner: &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionDiff, di.DecisionYes}}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v, want Wrote", res[0].Action)
	}
	if p.diffs != 1 {
		t.Fatalf("diffFn invoked %d times, want 1", p.diffs)
	}
	if got := string(readOrFail(t, fs, "/current/dst")); got != "new" {
		t.Fatalf("content = %q, want %q", got, "new")
	}
}

func TestApply_PrompterError_Propagates(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "new")
	seed(t, fs, "/current/dst", "old")
	p := &fakes.FakePrompter{} // no decisions scripted

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err == nil {
		t.Fatalf("expected prompter error to propagate")
	}
}

// --- Identical content -----------------------------------------------------

func TestApply_IdenticalContent_SilentSkip(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "same")
	seed(t, fs, "/current/dst", "same")
	p := &fakes.FakePrompter{}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "identical content" {
		t.Fatalf("got %+v, want Skipped/identical content", res[0])
	}
	if len(p.Calls) != 0 {
		t.Fatalf("expected no prompt, got %v", p.Calls)
	}
}

func TestApply_IdenticalContent_Template(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t.tt", "hi {{ .Vars.name }}")
	seed(t, fs, "/current/out", "hi world") // matches rendered output
	p := &fakes.FakePrompter{}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, From: "t.tt", To: "out"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "identical content" {
		t.Fatalf("got %+v", res[0])
	}
}

// --- Symlink scenarios -----------------------------------------------------

func TestApply_Symlink_OurTarget_NoOp(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	if err := fs.Symlink("/main/t", "/current/link"); err != nil {
		t.Fatal(err)
	}
	p := &fakes.FakePrompter{}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "symlink already points to target" {
		t.Fatalf("got %+v", res[0])
	}
}

func TestApply_Symlink_ForeignTarget_SkipsWithWarn(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	if err := fs.Symlink("/elsewhere", "/current/link"); err != nil {
		t.Fatal(err)
	}
	p := &fakes.FakePrompter{}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "foreign symlink" {
		t.Fatalf("got %+v", res[0])
	}
	target, _ := fs.Readlink("/current/link")
	if target != "/elsewhere" {
		t.Fatalf("foreign symlink overwritten: %q", target)
	}
}

func TestApply_Symlink_OverRegularFile_PromptsThenReplaces(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/current/link", "regfile")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionYes}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v, want Wrote", res[0].Action)
	}
	target, _ := fs.Readlink("/current/link")
	if target != "/main/t" {
		t.Fatalf("target = %q, want /main/t", target)
	}
}

func TestApply_Symlink_OverRegularFile_DiffThenYes(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/current/link", "regfile")
	p := &diffPrompter{inner: &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionDiff, di.DecisionYes}}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v", res[0].Action)
	}
	if p.diffs != 1 {
		t.Fatalf("diff invoked %d times, want 1", p.diffs)
	}
}

func TestApply_Symlink_OverRegularFile_NoDecision_Skips(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/current/link", "regfile")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionNo}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped {
		t.Fatalf("action = %v", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/link")); got != "regfile" {
		t.Fatalf("content changed: %q", got)
	}
}

// --- Flags -----------------------------------------------------------------

func TestApply_Force(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "new")
	seed(t, fs, "/current/dst", "old")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.Force = true

	res, err := files.Apply(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v, want Wrote", res[0].Action)
	}
	if len(p.Calls) != 0 {
		t.Fatalf("expected no prompt with --force, got %v", p.Calls)
	}
	if got := string(readOrFail(t, fs, "/current/dst")); got != "new" {
		t.Fatalf("content = %q", got)
	}
}

func TestApply_Skip(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "new")
	seed(t, fs, "/current/dst", "old")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.Skip = true

	res, err := files.Apply(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped {
		t.Fatalf("action = %v, want Skipped", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/dst")); got != "old" {
		t.Fatalf("content changed: %q", got)
	}
}

func TestApply_DryRun_NoWrites(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "new")
	seed(t, fs, "/current/dst", "old")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.DryRun = true

	res, err := files.Apply(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "dry-run" {
		t.Fatalf("got %+v", res[0])
	}
	if got := string(readOrFail(t, fs, "/current/dst")); got != "old" {
		t.Fatalf("content changed in dry-run: %q", got)
	}
	if len(p.Calls) != 0 {
		t.Fatalf("expected no prompt in dry-run, got %v", p.Calls)
	}
}

func TestApply_DryRun_Symlink(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.DryRun = true

	res, err := files.Apply(context.Background(), o, []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "dry-run" {
		t.Fatalf("got %+v", res[0])
	}
	if fs.Exists("/current/link") {
		t.Fatalf("symlink created in dry-run")
	}
}

// --- Main worktree ---------------------------------------------------------

func TestApply_MainWorktree_CopySymlinkNoOp_TemplateRuns(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src.tt", "hello {{ .Vars.name }}")
	seed(t, fs, "/main/src", "x")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.IsMain = true
	o.CurrentPath = "/main"

	res, err := files.Apply(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
		{Kind: files.OpSymlink, From: "src", To: "link"},
		{Kind: files.OpTemplate, From: "src.tt", To: "out.txt"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "main worktree" {
		t.Fatalf("copy: %+v", res[0])
	}
	if res[1].Action != files.ActionSkipped || res[1].Reason != "main worktree" {
		t.Fatalf("symlink: %+v", res[1])
	}
	if res[2].Action != files.ActionWrote {
		t.Fatalf("template: %+v", res[2])
	}
	if got := string(readOrFail(t, fs, "/main/out.txt")); got != "hello world" {
		t.Fatalf("template content = %q", got)
	}
	if fs.Exists("/main/dst") || fs.Exists("/main/link") {
		t.Fatalf("copy/symlink wrote in main worktree")
	}
}

// --- Nested paths ----------------------------------------------------------

func TestApply_NestedPath_CreatesParents(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "a/b/c/dst.txt"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !fs.Exists("/current/a/b/c/dst.txt") {
		t.Fatalf("expected nested file")
	}
}

// --- Multiple ops preserve order -------------------------------------------

func TestApply_MultipleOps_OrderPreserved(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/a", "A")
	seed(t, fs, "/main/b", "B")
	seed(t, fs, "/main/c", "C")
	p := &fakes.FakePrompter{}

	ops := []files.Op{
		{Kind: files.OpCopy, From: "a", To: "a"},
		{Kind: files.OpCopy, From: "b", To: "b"},
		{Kind: files.OpCopy, From: "c", To: "c"},
	}
	res, err := files.Apply(context.Background(), baseOpts(fs, p), ops)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res) != 3 {
		t.Fatalf("got %d results", len(res))
	}
	for i, want := range ops {
		if res[i].Op != want {
			t.Fatalf("res[%d].Op = %+v, want %+v", i, res[i].Op, want)
		}
	}
}

// --- Context cancellation --------------------------------------------------

func TestApply_ContextCancelled_Stops(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/a", "A")
	seed(t, fs, "/main/b", "B")
	p := &fakes.FakePrompter{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := files.Apply(ctx, baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "a", To: "a"},
		{Kind: files.OpCopy, From: "b", To: "b"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected 0 results, got %d", len(res))
	}
}

// --- Error cases -----------------------------------------------------------

func TestApply_MainFromMissing_ReturnsError(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "missing", To: "dst"},
	})
	if err == nil {
		t.Fatalf("expected error for missing main/from")
	}
}

func TestApply_TemplateRenderError(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/bad.tt", "{{ .Vars.missing_key }}")
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, From: "bad.tt", To: "out"},
	})
	if err == nil {
		t.Fatalf("expected render error")
	}
}

func TestApply_UnknownKind_ReturnsError(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpKind(99), From: "x", To: "y"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

func TestApply_Symlink_MkdirAllError(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	fs.InjectError("/current", os.ErrPermission) // parent dir MkdirAll fails
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err == nil {
		t.Fatalf("expected mkdir error to propagate")
	}
}

func TestApply_NilLogger_Defaults(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.Log = nil

	_, err := files.Apply(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Apply with nil Log: %v", err)
	}
}

// === Clean tests ===========================================================

func TestClean_Copy_RemovesUnchanged(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	seed(t, fs, "/current/dst", "x")
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v, want Wrote", res[0].Action)
	}
	if fs.Exists("/current/dst") {
		t.Fatalf("file still exists")
	}
}

func TestClean_Copy_Diverged_UserConfirms(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "orig")
	seed(t, fs, "/current/dst", "edited")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionYes}}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v", res[0].Action)
	}
	if fs.Exists("/current/dst") {
		t.Fatalf("file still exists")
	}
}

func TestClean_Copy_Diverged_UserKeeps(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "orig")
	seed(t, fs, "/current/dst", "edited")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionNo}}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "user kept" {
		t.Fatalf("got %+v", res[0])
	}
	if got := string(readOrFail(t, fs, "/current/dst")); got != "edited" {
		t.Fatalf("content changed: %q", got)
	}
}

func TestClean_Copy_NotExists(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "nothing to clean" {
		t.Fatalf("got %+v", res[0])
	}
}

func TestClean_Copy_MainMissing_FallsBackToPrompt(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/current/dst", "content") // main/from missing
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionYes}}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v", res[0].Action)
	}
	if fs.Exists("/current/dst") {
		t.Fatalf("expected removed")
	}
}

func TestClean_Template_RerendersForComparison(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t.tt", "v={{ .Vars.name }}")
	seed(t, fs, "/current/out", "v=world") // matches rendered output
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, From: "t.tt", To: "out"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v, want Wrote", res[0].Action)
	}
	if fs.Exists("/current/out") {
		t.Fatalf("expected removed")
	}
}

func TestClean_Template_BadTemplate_FallsBackToPrompt(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/bad.tt", "{{ .Vars.missing }}")
	seed(t, fs, "/current/out", "x")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionNo}}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, From: "bad.tt", To: "out"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionSkipped {
		t.Fatalf("action = %v", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/out")); got != "x" {
		t.Fatalf("content changed: %q", got)
	}
}

func TestClean_Symlink_OurTarget_Removed(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	if err := fs.Symlink("/main/t", "/current/link"); err != nil {
		t.Fatal(err)
	}
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v", res[0].Action)
	}
	if fs.Exists("/current/link") {
		t.Fatalf("symlink still exists")
	}
}

func TestClean_Symlink_ForeignTarget_Skipped(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	if err := fs.Symlink("/elsewhere", "/current/link"); err != nil {
		t.Fatal(err)
	}
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Reason != "foreign symlink" {
		t.Fatalf("got %+v", res[0])
	}
	// Use Readlink (not Exists) because FakeFS.Exists follows the broken
	// symlink to /elsewhere and reports it as missing even though the link
	// entry itself is still in the symlinks map.
	if _, err := fs.Readlink("/current/link"); err != nil {
		t.Fatalf("foreign symlink was removed: %v", err)
	}
}

func TestClean_Symlink_NotASymlink(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/current/link", "regular")
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Reason != "not a symlink" {
		t.Fatalf("got %+v", res[0])
	}
}

func TestClean_Symlink_NotExists(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Reason != "nothing to clean" {
		t.Fatalf("got %+v", res[0])
	}
}

func TestClean_MainWorktree_CopySymlinkNoOp(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/main/tt", "y")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.IsMain = true

	res, err := files.Clean(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "t", To: "t"},
		{Kind: files.OpSymlink, From: "tt", To: "l"},
		{Kind: files.OpTemplate, From: "t", To: "t"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Reason != "main worktree" || res[1].Reason != "main worktree" {
		t.Fatalf("copy/symlink not skipped in main: %+v", res)
	}
	// Template still cleans in main.
	if !fs.Exists("/main/t") {
		t.Fatalf("template clean ran in main despite no current-side file")
	}
}

func TestClean_DryRun_NoMutation(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	seed(t, fs, "/current/dst", "x")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.DryRun = true

	res, err := files.Clean(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "dry-run" {
		t.Fatalf("got %+v", res[0])
	}
	if !fs.Exists("/current/dst") {
		t.Fatalf("file removed in dry-run")
	}
}

func TestClean_Force(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "orig")
	seed(t, fs, "/current/dst", "edited")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.Force = true

	_, err := files.Clean(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if fs.Exists("/current/dst") {
		t.Fatalf("expected file removed")
	}
}

func TestClean_Skip(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "orig")
	seed(t, fs, "/current/dst", "edited")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.Skip = true

	res, err := files.Clean(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionSkipped {
		t.Fatalf("action = %v", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/dst")); got != "edited" {
		t.Fatalf("content changed: %q", got)
	}
}

func TestClean_Quit(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "orig")
	seed(t, fs, "/current/dst", "edited")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionQuit}}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if !errors.Is(err, files.ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if res[0].Action != files.ActionAborted {
		t.Fatalf("action = %v", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/dst")); got != "edited" {
		t.Fatalf("content changed: %q", got)
	}
}

func TestClean_UnknownKind(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}

	_, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpKind(99), From: "x", To: "y"},
	})
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

func TestClean_ContextCancelled(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	seed(t, fs, "/current/dst", "x")
	p := &fakes.FakePrompter{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := files.Clean(ctx, baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestClean_NilLogger(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	seed(t, fs, "/current/dst", "x")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.Log = nil

	_, err := files.Clean(context.Background(), o, []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean with nil Log: %v", err)
	}
}

// removeFailFS wraps an FS whose Remove always fails. Used to exercise the
// Remove error branch in cleanSymlink — FakeFS.InjectError would be consumed
// by the earlier Readlink call, so we wrap instead.
type removeFailFS struct {
	di.FS
	err error
}

func (f *removeFailFS) Remove(path string) error { return f.err }

func TestClean_Symlink_RemoveError(t *testing.T) {
	inner := fakes.NewFakeFS()
	seed(t, inner, "/main/t", "x")
	if err := inner.Symlink("/main/t", "/current/link"); err != nil {
		t.Fatal(err)
	}
	fs := &removeFailFS{FS: inner, err: os.ErrPermission}
	p := &fakes.FakePrompter{}

	_, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err == nil {
		t.Fatalf("expected remove error")
	}
}

// === OpKind.String =========================================================

func TestOpKind_String(t *testing.T) {
	tests := map[files.OpKind]string{
		files.OpTemplate: "template",
		files.OpCopy:     "copy",
		files.OpSymlink:  "symlink",
		files.OpKind(99): "unknown",
	}
	for k, want := range tests {
		if got := k.String(); got != want {
			t.Errorf("OpKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

// === failFS — wraps di.FS to force specific methods to return an error. ===
//
// FakeFS.InjectError is consumed by the first op on a given path, so it can
// not reach later operations (WriteFile/Remove/Symlink) that run after
// earlier probes. failFS lets a test fail one specific method unconditionally.

type failFS struct {
	di.FS
	writeFileErr error
	mkdirAllErr  error
	removeErr    error
	removeAllErr error
	symlinkErr   error
	readFileErr  error // unconditional ReadFile failure
}

func (f *failFS) ReadFile(path string) ([]byte, error) {
	if f.readFileErr != nil {
		return nil, f.readFileErr
	}
	return f.FS.ReadFile(path)
}

func (f *failFS) WriteFile(p string, d []byte, m os.FileMode) error {
	if f.writeFileErr != nil {
		return f.writeFileErr
	}
	return f.FS.WriteFile(p, d, m)
}

func (f *failFS) MkdirAll(p string, m os.FileMode) error {
	if f.mkdirAllErr != nil {
		return f.mkdirAllErr
	}
	return f.FS.MkdirAll(p, m)
}

func (f *failFS) Remove(p string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	return f.FS.Remove(p)
}

func (f *failFS) RemoveAll(p string) error {
	if f.removeAllErr != nil {
		return f.removeAllErr
	}
	return f.FS.RemoveAll(p)
}

func (f *failFS) Symlink(target, link string) error {
	if f.symlinkErr != nil {
		return f.symlinkErr
	}
	return f.FS.Symlink(target, link)
}

// === Error-path coverage ===================================================

func TestApply_TemplateMainMissing(t *testing.T) {
	fs := fakes.NewFakeFS()
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpTemplate, From: "missing.tt", To: "out"},
	})
	if err == nil {
		t.Fatalf("expected error for missing template main/from")
	}
}

func TestApply_WriteContent_MkdirAllError(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	fs.InjectError("/current", os.ErrPermission) // parent of /current/dst
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err == nil {
		t.Fatalf("expected mkdir error")
	}
}

func TestApply_WriteContent_WriteFileError(t *testing.T) {
	inner := fakes.NewFakeFS()
	seed(t, inner, "/main/src", "x")
	fs := &failFS{FS: inner, writeFileErr: os.ErrPermission}
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err == nil {
		t.Fatalf("expected write error")
	}
}

func TestApply_Symlink_OverFile_PrompterError(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/current/link", "regfile")
	p := &fakes.FakePrompter{} // no decisions

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err == nil {
		t.Fatalf("expected prompter error to propagate")
	}
}

func TestApply_Symlink_OverFile_Quit(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/current/link", "regfile")
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionQuit}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if !errors.Is(err, files.ErrAborted) {
		t.Fatalf("err = %v, want ErrAborted", err)
	}
	if res[0].Action != files.ActionAborted {
		t.Fatalf("action = %v", res[0].Action)
	}
	if got := string(readOrFail(t, fs, "/current/link")); got != "regfile" {
		t.Fatalf("file changed: %q", got)
	}
}

func TestApply_Symlink_OverFile_RemoveAllError(t *testing.T) {
	inner := fakes.NewFakeFS()
	seed(t, inner, "/main/t", "x")
	seed(t, inner, "/current/link", "regfile")
	fs := &failFS{FS: inner, removeAllErr: os.ErrPermission}
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionYes}}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err == nil {
		t.Fatalf("expected removeAll error")
	}
}

func TestApply_Symlink_CreateError(t *testing.T) {
	inner := fakes.NewFakeFS()
	seed(t, inner, "/main/t", "x")
	fs := &failFS{FS: inner, symlinkErr: os.ErrPermission}
	p := &fakes.FakePrompter{}

	_, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err == nil {
		t.Fatalf("expected symlink error")
	}
}

func TestApply_Symlink_OverFile_DryRun(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	seed(t, fs, "/current/link", "regfile")
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.DryRun = true

	res, err := files.Apply(context.Background(), o, []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "dry-run" {
		t.Fatalf("got %+v", res[0])
	}
	if got := string(readOrFail(t, fs, "/current/link")); got != "regfile" {
		t.Fatalf("file changed in dry-run: %q", got)
	}
}

func TestClean_EmptyTo_DefaultsToFrom(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "x")
	seed(t, fs, "/current/src", "x") // matches main
	p := &fakes.FakePrompter{}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v", res[0].Action)
	}
	if fs.Exists("/current/src") {
		t.Fatalf("expected /current/src removed")
	}
}

func TestClean_Diverged_PrompterError(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "orig")
	seed(t, fs, "/current/dst", "edited")
	p := &fakes.FakePrompter{} // no decisions

	_, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err == nil {
		t.Fatalf("expected prompter error to propagate")
	}
}

func TestClean_Diverged_DiffThenYes(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/src", "orig")
	seed(t, fs, "/current/dst", "edited")
	p := &diffPrompter{inner: &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionDiff, di.DecisionYes}}}

	res, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v", res[0].Action)
	}
	if p.diffs != 1 {
		t.Fatalf("diff invoked %d times, want 1", p.diffs)
	}
}

func TestClean_Symlink_DryRun(t *testing.T) {
	fs := fakes.NewFakeFS()
	seed(t, fs, "/main/t", "x")
	if err := fs.Symlink("/main/t", "/current/link"); err != nil {
		t.Fatal(err)
	}
	p := &fakes.FakePrompter{}
	o := baseOpts(fs, p)
	o.DryRun = true

	res, err := files.Clean(context.Background(), o, []files.Op{
		{Kind: files.OpSymlink, From: "t", To: "link"},
	})
	if err != nil {
		t.Fatalf("Clean: %v", err)
	}
	if res[0].Action != files.ActionSkipped || res[0].Reason != "dry-run" {
		t.Fatalf("got %+v", res[0])
	}
	if _, err := fs.Readlink("/current/link"); err != nil {
		t.Fatalf("symlink removed in dry-run: %v", err)
	}
}

func TestClean_Diverged_RemoveError(t *testing.T) {
	inner := fakes.NewFakeFS()
	seed(t, inner, "/main/src", "orig")
	seed(t, inner, "/current/dst", "edited")
	fs := &failFS{FS: inner, removeErr: os.ErrPermission}
	p := &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionYes}}

	_, err := files.Clean(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err == nil {
		t.Fatalf("expected remove error")
	}
}

// diffFailFS fails the Nth ReadFile call to exercise the err branch of
// diffContent. Read order in writeContent with a Diff: (1) applyCopy reads
// main/from, (2) readIfExists reads toPath, (3) diffContent reads toPath
// from inside the prompter's diffFn — so failAt=3 lands on diffContent.
type diffFailFS struct {
	di.FS
	failAt  int
	reads   int
	readErr error
}

func (f *diffFailFS) ReadFile(path string) ([]byte, error) {
	f.reads++
	if f.reads == f.failAt {
		return nil, f.readErr
	}
	return f.FS.ReadFile(path)
}

func TestApply_DiffContent_ReadError(t *testing.T) {
	inner := fakes.NewFakeFS()
	seed(t, inner, "/main/src", "new")
	seed(t, inner, "/current/dst", "old")
	fs := &diffFailFS{FS: inner, failAt: 3, readErr: os.ErrPermission}
	p := &diffPrompter{inner: &fakes.FakePrompter{Decisions: []di.Decision{di.DecisionDiff, di.DecisionYes}}}

	res, err := files.Apply(context.Background(), baseOpts(fs, p), []files.Op{
		{Kind: files.OpCopy, From: "src", To: "dst"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res[0].Action != files.ActionWrote {
		t.Fatalf("action = %v", res[0].Action)
	}
	if p.diffs != 1 {
		t.Fatalf("diff invoked %d times, want 1", p.diffs)
	}
	if fs.reads < 3 {
		t.Fatalf("expected at least 3 reads, got %d", fs.reads)
	}
}
