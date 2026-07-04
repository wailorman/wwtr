package hooks

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/di/fakes"
	tmpl "github.com/wailorman/wwtr/internal/template"
	"github.com/wailorman/wwtr/internal/vars"
)

func TestValidDotenvKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"1ABC", false},
		{"_OK", true},
		{"OK_KEY", true},
		{"A1_B2", true},
		{"kebab-case", false},
		{"with space", false},
		{"with.dot", false},
	}
	for _, tc := range cases {
		if got := validDotenvKey(tc.in); got != tc.want {
			t.Errorf("validDotenvKey(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseDotenvEmptyKey(t *testing.T) {
	t.Parallel()
	_, err := parseDotenv([]byte("=val\n"))
	if err == nil {
		t.Fatalf("expected empty-key error")
	}
	if !strings.Contains(err.Error(), "empty key") {
		t.Errorf("error should mention empty key: %v", err)
	}
}

func TestParseDotenvMixed(t *testing.T) {
	t.Parallel()
	out, err := parseDotenv([]byte("A=1\nB=2\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out["A"] != "1" || out["B"] != "2" {
		t.Errorf("parsed map wrong: %v", out)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", "''"},
		{"plain", "'plain'"},
		{"with'quote", `'with'\''quote'`},
		{"a'b'c", `'a'\''b'\''c'`},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUnquote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"plain", "plain"},
		{"\"double\"", "double"},
		{"'single'", "single"},
		{"'mismatch\"", "'mismatch\""},
		{"'", "'"},
		{"\"only-start", "\"only-start"},
	}
	for _, tc := range cases {
		if got := unquote(tc.in); got != tc.want {
			t.Errorf("unquote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()
	if got := describe(config.Hook{Run: "echo"}); got != "echo" {
		t.Errorf("describe(Run) = %q, want echo", got)
	}
	if got := describe(config.Hook{LoadEnv: "x.env"}); got != "load_env:x.env" {
		t.Errorf("describe(LoadEnv) = %q", got)
	}
	if got := describe(config.Hook{}); got != "(empty)" {
		t.Errorf("describe(empty) = %q, want (empty)", got)
	}
}

func TestResolvePathAbsolute(t *testing.T) {
	t.Parallel()
	if got := resolvePath("/abs/path", "/base"); got != "/abs/path" {
		t.Errorf("absolute path not preserved: %q", got)
	}
}

func TestResolvePathEmptyBase(t *testing.T) {
	t.Parallel()
	if got := resolvePath("rel.txt", ""); got != "rel.txt" {
		t.Errorf("empty base should fall back to '.': %q", got)
	}
}

func TestOrderedKeysDeterministic(t *testing.T) {
	t.Parallel()
	m := map[string]string{"c": "1", "a": "2", "b": "3"}
	got := orderedKeys(m)
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("orderedKeys[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestRenderTemplateNoBracesShortcircuit(t *testing.T) {
	t.Parallel()
	opts := Options{Log: slog.Default()}
	got, err := renderTemplate(opts, "n", "no template here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "no template here" {
		t.Errorf("plain string should pass through; got %q", got)
	}
}

func TestRenderTemplateError(t *testing.T) {
	t.Parallel()
	opts := Options{Log: slog.Default()}
	// Missingkey=error will fail on .Vars.x when no Vars map is set.
	_, err := renderTemplate(opts, "n", "{{ .Vars.missing }}")
	if err == nil {
		t.Fatalf("expected render error")
	}
	if !errors.Is(err, tmpl.ErrExecute) {
		t.Errorf("expected ErrExecute, got %v", err)
	}
}

func TestWriteOutputEmpty(t *testing.T) {
	t.Parallel()
	// Should be a no-op on empty input.
	writeOutput(nil, nil)
	writeOutput(nil, []byte(""))
}

func TestApplyLoadEnvValueRenderError(t *testing.T) {
	t.Parallel()
	content := []byte("KEY={{ .Vars.missing }}\n")
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile("/current/.env", content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := Options{
		FS:      fs,
		Log:     slog.Default(),
		Builtin: vars.BuiltinVars{WorktreePath: "/current"},
	}
	_, err := applyLoadEnv(opts, opts.Log, ".env")
	if err == nil {
		t.Fatalf("expected value render error")
	}
	if !strings.Contains(err.Error(), "render load_env KEY") {
		t.Errorf("error should mention the failing key: %v", err)
	}
}

func TestApplyLoadEnvPathRenderError(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	opts := Options{FS: fs, Log: slog.Default(), Builtin: vars.BuiltinVars{WorktreePath: "/current"}}
	_, err := applyLoadEnv(opts, opts.Log, "{{ .Vars.missing }}.env")
	if err == nil {
		t.Fatalf("expected path render error")
	}
	if !strings.Contains(err.Error(), "render load_env path") {
		t.Errorf("error should mention path render: %v", err)
	}
}
