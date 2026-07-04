package state

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/di/fakes"
)

// statYAML reports the raw contents of state.yaml inside the fake FS, or "" if
// absent. Keeps assertions terse and avoids reaching into FakeFS internals.
func statYAML(t *testing.T, fs *fakes.FakeFS, worktreeDir string) (string, bool) {
	t.Helper()
	data, err := fs.ReadFile(Path(worktreeDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		t.Fatalf("statYAML: %v", err)
	}
	return string(data), true
}

func TestPath_AndDirPath(t *testing.T) {
	cases := []struct {
		name        string
		worktreeDir string
		wantDir     string
		wantFile    string
	}{
		{"simple", "/repo", "/repo/.wwtr", "/repo/.wwtr/state.yaml"},
		{"trailing-slash-trimmed", "/repo/", "/repo/.wwtr", "/repo/.wwtr/state.yaml"},
		{"relative", ".", ".wwtr", ".wwtr/state.yaml"},
		{"deep", "/a/b/c", "/a/b/c/.wwtr", "/a/b/c/.wwtr/state.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DirPath(tc.worktreeDir); got != tc.wantDir {
				t.Errorf("DirPath(%q)=%q want %q", tc.worktreeDir, got, tc.wantDir)
			}
			if got := Path(tc.worktreeDir); got != tc.wantFile {
				t.Errorf("Path(%q)=%q want %q", tc.worktreeDir, got, tc.wantFile)
			}
		})
	}
}

func TestRead_MissingFile_ReturnsEmptyNoError(t *testing.T) {
	fs := fakes.NewFakeFS()
	got, err := Read(fs, "/repo")
	if err != nil {
		t.Fatalf("Read missing: unexpected err %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Read missing: want empty map, got %v", got)
	}
}

func TestWrite_ThenRead_RoundTrip(t *testing.T) {
	fs := fakes.NewFakeFS()
	in := map[string]string{
		"base_port":   "3010",
		"db_prefix":   "wk_app_feature_x",
		"empty_value": "",
	}
	if err := Write(fs, "/repo", in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := Read(fs, "/repo")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("round-trip size: got %d want %d (%v)", len(out), len(in), out)
	}
	for k, v := range in {
		if out[k] != v {
			t.Errorf("round-trip[%q]: got %q want %q", k, out[k], v)
		}
	}
}

func TestWrite_CreatesDirAndFile(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := Write(fs, "/repo", map[string]string{"x": "1"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !fs.Exists(DirPath("/repo")) {
		t.Errorf(".wwtr dir not created")
	}
	content, ok := statYAML(t, fs, "/repo")
	if !ok {
		t.Fatal("state.yaml not written")
	}
	if !strings.Contains(content, "x: \"1\"") && !strings.Contains(content, "x: 1") {
		t.Errorf("state.yaml content unexpected:\n%s", content)
	}
}

func TestWrite_EmptyMap_DeletesFile(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := Write(fs, "/repo", map[string]string{"x": "1"}); err != nil {
		t.Fatalf("Write initial: %v", err)
	}
	if _, ok := statYAML(t, fs, "/repo"); !ok {
		t.Fatal("setup: file should exist before empty write")
	}
	if err := Write(fs, "/repo", map[string]string{}); err != nil {
		t.Fatalf("Write empty: %v", err)
	}
	if _, ok := statYAML(t, fs, "/repo"); ok {
		t.Error("empty Write should have removed the file")
	}
}

func TestWrite_EmptyMap_NeverExisted_NoError(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := Write(fs, "/repo", map[string]string{}); err != nil {
		t.Fatalf("Write empty on absent file: %v", err)
	}
	if _, ok := statYAML(t, fs, "/repo"); ok {
		t.Error("no file should have been created for empty map")
	}
}

func TestRemove_ExistingFile(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := Write(fs, "/r", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Remove(fs, "/r"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := statYAML(t, fs, "/r"); ok {
		t.Error("file still present after Remove")
	}
}

func TestRemove_MissingFile_NoError(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := Remove(fs, "/r"); err != nil {
		t.Fatalf("Remove absent: %v", err)
	}
}

func TestRead_ParseError_Propagated(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile(Path("/repo"), []byte("foo: [unterminated"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := Read(fs, "/repo"); err == nil {
		t.Fatal("Read malformed YAML: want error, got nil")
	}
}

func TestRead_OsError_Propagated(t *testing.T) {
	fs := fakes.NewFakeFS()
	// Seed then inject a permission-style error on the read path.
	if err := fs.WriteFile(Path("/repo"), []byte("x: \"1\""), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs.InjectError(Path("/repo"), errors.New("permission denied"))
	if _, err := Read(fs, "/repo"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Read with injected error: want permission denied, got %v", err)
	}
}

func TestWrite_MkdirError_Propagated(t *testing.T) {
	fs := fakes.NewFakeFS()
	fs.InjectError(DirPath("/repo"), errors.New("disk full"))
	err := Write(fs, "/repo", map[string]string{"x": "1"})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Write MkdirAll error: want disk full, got %v", err)
	}
}

func TestWrite_WriteFileError_Propagated(t *testing.T) {
	fs := fakes.NewFakeFS()
	fs.InjectError(Path("/repo"), errors.New("read-only fs"))
	err := Write(fs, "/repo", map[string]string{"x": "1"})
	if err == nil || !strings.Contains(err.Error(), "read-only fs") {
		t.Fatalf("Write WriteFile error: want read-only fs, got %v", err)
	}
}

func TestWrite_EncodeError_UnreachableButCovered(t *testing.T) {
	// yaml.Marshal of map[string]string cannot fail, so this is a guard: a nil
	// map triggers the empty-map removal branch instead. Ensures the encode
	// path is the only non-exercised line.
	fs := fakes.NewFakeFS()
	if err := Write(fs, "/repo", nil); err != nil {
		t.Fatalf("Write(nil): %v", err)
	}
	if _, ok := statYAML(t, fs, "/repo"); ok {
		t.Error("Write(nil) should not create a file")
	}
}

func TestRemove_RemoveError_Propagated(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile(Path("/repo"), []byte("x: \"1\""), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs.InjectError(Path("/repo"), errors.New("fs busy"))
	if err := Remove(fs, "/repo"); err == nil || !strings.Contains(err.Error(), "fs busy") {
		t.Fatalf("Remove error: want fs busy, got %v", err)
	}
}

func TestRoundTrip_DeterministicKeyOrder(t *testing.T) {
	fs := fakes.NewFakeFS()
	if err := Write(fs, "/r", map[string]string{"zebra": "1", "alpha": "2", "mike": "3"}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	content, _ := statYAML(t, fs, "/r")
	// yaml.v3 sorts string keys alphabetically — stable output matters for git.
	if !(strings.Index(content, "alpha") < strings.Index(content, "mike") &&
		strings.Index(content, "mike") < strings.Index(content, "zebra")) {
		t.Errorf("state.yaml keys not sorted alphabetically:\n%s", content)
	}
}
