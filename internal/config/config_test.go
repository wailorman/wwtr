package config_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/config"
	"github.com/wailorman/wwtr/internal/di/fakes"
	"github.com/wailorman/wwtr/internal/vars"
)

// writeConfig seeds a FakeFS with content at path and returns the FS.
func writeConfig(t *testing.T, path, content string) *fakes.FakeFS {
	t.Helper()
	fs := fakes.NewFakeFS()
	if err := fs.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return fs
}

// mustLoad parses content and fails the test on any error.
func mustLoad(t *testing.T, content string) *config.Config {
	t.Helper()
	fs := writeConfig(t, "/repo/.wwtr.yml", content)
	cfg, err := config.Load(fs, "/repo/.wwtr.yml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

const cfgPath = "/repo/.wwtr.yml"

// --- Load: success cases ---------------------------------------------------

func TestLoad_Minimal(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, "version: 1\n")
	if cfg.Version != 1 {
		t.Errorf("Version=%d want 1", cfg.Version)
	}
	if len(cfg.Vars()) != 0 {
		t.Errorf("Vars=%v want empty", cfg.Vars())
	}
	if len(cfg.Template) != 0 || len(cfg.Copy) != 0 || len(cfg.Symlink) != 0 {
		t.Errorf("path slices should be empty: t=%v c=%v s=%v", cfg.Template, cfg.Copy, cfg.Symlink)
	}
	if cfg.Hooks != nil {
		t.Errorf("Hooks=%v want nil", cfg.Hooks)
	}
}

func TestLoad_FullExample(t *testing.T) {
	t.Parallel()
	const doc = `
version: 1

vars:
  base_port:
    sources:
      - cli: "--base-port"
      - env: BASE_PORT
      - prompt: "BASE_PORT (блок 10 портов)"
        validate: '^\d+$'
    default: 3000
  db_prefix:
    value: 'wk_{{ .SafeName | trunc 50 }}'
  mail_container:
    value: 'wk_mail_{{ .Vars.db_prefix }}'

template:
  - { from: .worktree.env.tt, to: .worktree.env }

copy:
  - { from: .rvmrc }
  - { from: .tool-versions }

symlink:
  - { from: AGENTS.md }
  - from: CLAUDE.md
    to:   .claude/CLAUDE.md

hooks:
  init:
    post: []
  setup:
    pre:
      - load_env: .worktree.env
    post:
      - bundle install
      - npm install
      - run: codegraph init
        when: commandExists('codegraph')
  stop:
    post:
      - run: docker stop {{ .Vars.mail_container }}
        when: commandExists('docker')
`
	cfg := mustLoad(t, doc)

	if cfg.Version != 1 {
		t.Fatalf("Version=%d", cfg.Version)
	}

	// Vars preserve declaration order and carry the right shape.
	vs := cfg.Vars()
	if len(vs) != 3 {
		t.Fatalf("Vars len=%d want 3", len(vs))
	}
	if vs[0].Name() != "base_port" || vs[1].Name() != "db_prefix" || vs[2].Name() != "mail_container" {
		t.Errorf("declaration order lost: %s %s %s", vs[0].Name(), vs[1].Name(), vs[2].Name())
	}

	bp := vs[0]
	if !bp.HasSources() {
		t.Error("base_port.HasSources=false want true")
	}
	if bp.ValueExpr() != "" {
		t.Errorf("base_port.ValueExpr=%q want empty", bp.ValueExpr())
	}
	if d, ok := bp.Default(); !ok || d != 3000 {
		t.Errorf("base_port.Default=%v,%v want 3000,true", d, ok)
	}
	srcs := bp.Sources()
	if len(srcs) != 3 {
		t.Fatalf("base_port sources len=%d want 3", len(srcs))
	}
	if srcs[0].Kind != vars.SourceCLI || srcs[0].CLI != "--base-port" {
		t.Errorf("src[0]=%+v want cli --base-port", srcs[0])
	}
	if srcs[1].Kind != vars.SourceEnv || srcs[1].Env != "BASE_PORT" {
		t.Errorf("src[1]=%+v want env BASE_PORT", srcs[1])
	}
	if srcs[2].Kind != vars.SourcePrompt || srcs[2].Prompt == "" {
		t.Errorf("src[2]=%+v want prompt", srcs[2])
	}
	if srcs[2].Validate != `^\d+$` {
		t.Errorf("src[2].Validate=%q want ^\\d+$", srcs[2].Validate)
	}

	dp := vs[1]
	if dp.HasSources() {
		t.Error("db_prefix.HasSources=true want false")
	}
	if dp.ValueExpr() != "wk_{{ .SafeName | trunc 50 }}" {
		t.Errorf("db_prefix.ValueExpr=%q", dp.ValueExpr())
	}
	if _, ok := dp.Default(); ok {
		t.Error("db_prefix.Default present, want absent")
	}

	// Paths: explicit `to:` preserved, omitted `to:` defaults to `from:`.
	if len(cfg.Template) != 1 || cfg.Template[0] != (config.PathSpec{From: ".worktree.env.tt", To: ".worktree.env"}) {
		t.Errorf("Template=%+v", cfg.Template)
	}
	if len(cfg.Copy) != 2 {
		t.Fatalf("Copy len=%d", len(cfg.Copy))
	}
	if cfg.Copy[0] != (config.PathSpec{From: ".rvmrc", To: ".rvmrc"}) {
		t.Errorf("Copy[0]=%+v want To defaulting to From", cfg.Copy[0])
	}
	if cfg.Copy[1] != (config.PathSpec{From: ".tool-versions", To: ".tool-versions"}) {
		t.Errorf("Copy[1]=%+v", cfg.Copy[1])
	}
	if len(cfg.Symlink) != 2 {
		t.Fatalf("Symlink len=%d", len(cfg.Symlink))
	}
	if cfg.Symlink[0] != (config.PathSpec{From: "AGENTS.md", To: "AGENTS.md"}) {
		t.Errorf("Symlink[0]=%+v", cfg.Symlink[0])
	}
	if cfg.Symlink[1] != (config.PathSpec{From: "CLAUDE.md", To: ".claude/CLAUDE.md"}) {
		t.Errorf("Symlink[1]=%+v", cfg.Symlink[1])
	}

	// Hooks: every variant parses.
	if cfg.Hooks == nil {
		t.Fatal("Hooks nil")
	}
	if got := cfg.HookStage("init", "post"); len(got) != 0 {
		t.Errorf("init.post=%v want empty", got)
	}
	pre := cfg.HookStage("setup", "pre")
	if len(pre) != 1 || !pre[0].IsLoadEnv() || pre[0].LoadEnv != ".worktree.env" {
		t.Errorf("setup.pre=%+v", pre)
	}
	post := cfg.HookStage("setup", "post")
	if len(post) != 3 {
		t.Fatalf("setup.post len=%d", len(post))
	}
	if post[0].Run != "bundle install" || post[0].When != "" || post[0].IsLoadEnv() {
		t.Errorf("post[0]=%+v want bare string", post[0])
	}
	if post[2].Run != "codegraph init" || post[2].When != "commandExists('codegraph')" {
		t.Errorf("post[2]=%+v want run+when", post[2])
	}
	stop := cfg.HookStage("stop", "post")
	if len(stop) != 1 || stop[0].When != "commandExists('docker')" {
		t.Errorf("stop.post=%+v", stop)
	}
}

func TestLoad_Vars_PreserveDeclarationOrder(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
vars:
  zebra:
    value: "1"
  alpha:
    value: "2"
  mike:
    value: "3"
`)
	got := cfg.Vars()
	if len(got) != 3 {
		t.Fatalf("len=%d", len(got))
	}
	want := []string{"zebra", "alpha", "mike"}
	for i, w := range want {
		if got[i].Name() != w {
			t.Errorf("Vars[%d].Name=%q want %q", i, got[i].Name(), w)
		}
	}
}

func TestLoad_ToDefaultsToFrom_AllKinds(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
template:
  - from: a
copy:
  - from: b
symlink:
  - from: c
`)
	if cfg.Template[0].To != "a" {
		t.Errorf("Template To=%q want a", cfg.Template[0].To)
	}
	if cfg.Copy[0].To != "b" {
		t.Errorf("Copy To=%q want b", cfg.Copy[0].To)
	}
	if cfg.Symlink[0].To != "c" {
		t.Errorf("Symlink To=%q want c", cfg.Symlink[0].To)
	}
}

// --- Load: error cases -----------------------------------------------------

func TestLoad_MissingFile_ErrNotFound(t *testing.T) {
	t.Parallel()
	fs := fakes.NewFakeFS()
	_, err := config.Load(fs, cfgPath)
	if !errors.Is(err, config.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want chain to contain os.ErrNotExist, got %v", err)
	}
	if !strings.Contains(err.Error(), cfgPath) {
		t.Errorf("error should mention path %q: %v", cfgPath, err)
	}
}

func TestLoad_ReadError_Propagated(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "version: 1\n")
	boom := errors.New("permission denied")
	fs.InjectError(cfgPath, boom)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("want permission denied, got %v", err)
	}
	if !strings.Contains(err.Error(), cfgPath) {
		t.Errorf("error should mention path: %v", err)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "version: 1\nhooks:\n  init: [unclosed\n")
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want parse error, got nil")
	}
	if !strings.Contains(err.Error(), cfgPath) {
		t.Errorf("error should mention path: %v", err)
	}
}

func TestLoad_EmptyFile_ErrInvalidVersion(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "")
	_, err := config.Load(fs, cfgPath)
	if !errors.Is(err, config.ErrInvalidVersion) {
		t.Fatalf("want ErrInvalidVersion, got %v", err)
	}
}

func TestLoad_MissingVersion_ErrInvalidVersion(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "vars:\n  x:\n    value: \"1\"\n")
	_, err := config.Load(fs, cfgPath)
	if !errors.Is(err, config.ErrInvalidVersion) {
		t.Fatalf("want ErrInvalidVersion, got %v", err)
	}
}

func TestLoad_WrongVersion_ErrInvalidVersion(t *testing.T) {
	t.Parallel()
	for _, v := range []int{0, 2, -1, 99} {
		fs := writeConfig(t, cfgPath, "version: "+itoa(v)+"\n")
		_, err := config.Load(fs, cfgPath)
		if !errors.Is(err, config.ErrInvalidVersion) {
			t.Errorf("version %d: want ErrInvalidVersion, got %v", v, err)
		}
	}
}

func TestLoad_VersionString_ErrInvalidVersion(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `version: "1"`+"\n")
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal(`want error for quoted "1", got nil`)
	}
}

// --- var validation --------------------------------------------------------

func TestLoad_InvalidVarNames(t *testing.T) {
	t.Parallel()
	bad := []string{
		"with-dash",
		"123abc",
		"with space",
		"with.dot",
		"with/slash",
		"",
	}
	for _, name := range bad {
		fs := writeConfig(t, cfgPath, "version: 1\nvars:\n  "+name+":\n    value: x\n")
		_, err := config.Load(fs, cfgPath)
		if err == nil {
			t.Errorf("name %q: want validation error, got nil", name)
		}
	}
}

func TestLoad_ValidVarNames(t *testing.T) {
	t.Parallel()
	good := []string{"a", "_x", "CamelCase", "snake_case", "abc123", "_"}
	for _, name := range good {
		fs := writeConfig(t, cfgPath, "version: 1\nvars:\n  "+name+":\n    value: x\n")
		_, err := config.Load(fs, cfgPath)
		if err != nil {
			t.Errorf("name %q: want OK, got %v", name, err)
		}
	}
}

func TestLoad_SourcesAndValue_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  bad:
    sources:
      - env: X
    value: "literal"
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("want mutually-exclusive error, got %v", err)
	}
}

// --- source validation -----------------------------------------------------

func TestLoad_SourceNoKind_Error(t *testing.T) {
	t.Parallel()
	// Only validate set — no cli/env/prompt.
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  bad:
    sources:
      - validate: '^\d+$'
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("want exactly-one error, got %v", err)
	}
}

func TestLoad_SourceMultipleKinds_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  bad:
    sources:
      - cli: "--x"
        env: X
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("want exactly-one error, got %v", err)
	}
}

// --- path validation -------------------------------------------------------

func TestLoad_PathMissingFrom_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
template:
  - { to: only-to }
copy:
  - to: only-to
symlink:
  - to: only-to
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	// copy/symlink still report "from is required"; template now reports a
	// different message because `content:` is also a valid source.
	if c := strings.Count(err.Error(), "from is required"); c != 2 {
		t.Errorf("want 2 'from is required' (copy+symlink), got %d in %v", c, err)
	}
	if !strings.Contains(err.Error(), "from or content is required") {
		t.Errorf("template error should mention content alternative: %v", err)
	}
}

// --- inline content (`content:` field) -----------------------------------

func TestLoad_TemplateInlineContent_OK(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
template:
  - to: config/database.yml
    content: |
      adapter: postgresql
      database: {{ .Vars.db }}
`)
	if len(cfg.Template) != 1 {
		t.Fatalf("Template len=%d", len(cfg.Template))
	}
	got := cfg.Template[0]
	if got.To != "config/database.yml" {
		t.Errorf("To=%q", got.To)
	}
	if got.From != "" {
		t.Errorf("From=%q want empty", got.From)
	}
	want := "adapter: postgresql\ndatabase: {{ .Vars.db }}\n"
	if got.Content != want {
		t.Errorf("Content=%q want %q", got.Content, want)
	}
}

func TestLoad_TemplateFromAndContent_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
template:
  - from: a.tt
    to:   a
    content: inline
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("want mutually-exclusive error, got %v", err)
	}
}

func TestLoad_TemplateContentMissingTo_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
template:
  - content: inline
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "to is required with content") {
		t.Fatalf("want to-required error, got %v", err)
	}
}

func TestLoad_CopyWithContent_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
copy:
  - from: a
    to:   b
    content: inline
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "content is not supported on copy") {
		t.Fatalf("want content-not-supported error, got %v", err)
	}
}

func TestLoad_SymlinkWithContent_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
symlink:
  - from: a
    to:   b
    content: inline
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "content is not supported on symlink") {
		t.Fatalf("want content-not-supported error, got %v", err)
	}
}

// --- hook parsing/validation ----------------------------------------------

func TestLoad_HookScalar(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
hooks:
  setup:
    post:
      - bundle install
      - npm install
`)
	post := cfg.HookStage("setup", "post")
	if len(post) != 2 {
		t.Fatalf("len=%d", len(post))
	}
	if post[0].Run != "bundle install" || post[0].When != "" || post[0].IsLoadEnv() {
		t.Errorf("post[0]=%+v", post[0])
	}
	if post[1].Run != "npm install" {
		t.Errorf("post[1].Run=%q", post[1].Run)
	}
}

func TestLoad_HookRunWithWhen(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
hooks:
  setup:
    post:
      - run: codegraph init
        when: commandExists('codegraph')
`)
	post := cfg.HookStage("setup", "post")
	if len(post) != 1 {
		t.Fatalf("len=%d", len(post))
	}
	if post[0].Run != "codegraph init" {
		t.Errorf("Run=%q", post[0].Run)
	}
	if post[0].When != "commandExists('codegraph')" {
		t.Errorf("When=%q", post[0].When)
	}
	if post[0].IsLoadEnv() {
		t.Error("IsLoadEnv=true want false")
	}
}

func TestLoad_HookLoadEnv(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
hooks:
  setup:
    pre:
      - load_env: .worktree.env
`)
	pre := cfg.HookStage("setup", "pre")
	if len(pre) != 1 {
		t.Fatalf("len=%d", len(pre))
	}
	if !pre[0].IsLoadEnv() {
		t.Error("IsLoadEnv=false want true")
	}
	if pre[0].LoadEnv != ".worktree.env" {
		t.Errorf("LoadEnv=%q", pre[0].LoadEnv)
	}
	if pre[0].Run != "" || pre[0].When != "" {
		t.Errorf("Run/When should be empty: %+v", pre[0])
	}
}

func TestLoad_HookLoadEnvAndRun_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  setup:
    pre:
      - run: echo hi
        load_env: .env
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "load_env cannot be combined") {
		t.Fatalf("want load_env-incompatible error, got %v", err)
	}
}

func TestLoad_HookLoadEnvAndWhen_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  setup:
    pre:
      - when: "true"
        load_env: .env
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestLoad_HookEmptyMap_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  setup:
    pre:
      - {}
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "must have either run or load_env") {
		t.Fatalf("want empty-hook error, got %v", err)
	}
}

func TestLoad_HookWhenOnly_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  setup:
    pre:
      - when: "true"
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want error for when-only hook")
	}
}

func TestLoad_HookInvalidShape_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  setup:
    pre:
      - [array, not, valid]
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want error for sequence hook")
	}
}

// --- structural errors ----------------------------------------------------

func TestLoad_VarsNotMapping_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars: not-a-map
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "vars") {
		t.Fatalf("want vars error, got %v", err)
	}
}

func TestLoad_VarsSequence_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  - alpha
  - beta
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "expected mapping") {
		t.Fatalf("want expected-mapping error, got %v", err)
	}
}

func TestLoad_VarsNull_TreatedAsAbsent(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, "version: 1\nvars: null\n")
	if len(cfg.Vars()) != 0 {
		t.Errorf("vars: null should yield empty slice, got %v", cfg.Vars())
	}
}

func TestLoad_DefaultNull_TreatedAsAbsent(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
vars:
  x:
    value: "v"
    default: null
`)
	v := cfg.Vars()[0]
	if _, ok := v.Default(); ok {
		t.Error("default: null should be treated as absent")
	}
}

// --- Config.HookStage / Vars accessors -----------------------------------

func TestConfig_HookStage_Unknown(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, "version: 1\n")
	if got := cfg.HookStage("init", "pre"); got != nil {
		t.Errorf("unknown cmd should return nil, got %v", got)
	}
	if got := cfg.HookStage("setup", "during"); got != nil {
		t.Errorf("unknown stage should return nil, got %v", got)
	}
}

func TestConfig_HookStage_UnknownStageWithHooks(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
hooks:
  init:
    pre: []
`)
	if got := cfg.HookStage("init", "during"); got != nil {
		t.Errorf("unknown stage should return nil, got %v", got)
	}
	if got := cfg.HookStage("setup", "pre"); got != nil {
		t.Errorf("unknown cmd should return nil, got %v", got)
	}
	// Sanity: pre on init returns the empty (non-nil) list.
	if got := cfg.HookStage("init", "pre"); len(got) != 0 {
		t.Errorf("init.pre should be empty slice, got %v", got)
	}
}

func TestConfig_Vars_ReturnsEmptySlice_NotNil(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, "version: 1\n")
	// Cover the nil-vs-empty contract — len() must be 0 either way.
	if len(cfg.Vars()) != 0 {
		t.Errorf("want 0 vars, got %d", len(cfg.Vars()))
	}
}

// --- Var interop with vars.VarSpec ---------------------------------------

func TestVar_ImplementsVarsVarSpec(t *testing.T) {
	t.Parallel()
	// Compile-time check enforced in config.go via `var _ vars.VarSpec = Var{}`.
	// Runtime check: pass a []config.Var as []vars.VarSpec to verify shape.
	cfg := mustLoad(t, `
version: 1
vars:
  port:
    sources:
      - env: PORT
    default: 3000
  name:
    value: "computed-{{ .Vars.port }}"
`)
	specs := make([]vars.VarSpec, len(cfg.Vars()))
	for i, v := range cfg.Vars() {
		specs[i] = v
	}
	if len(specs) != 2 {
		t.Fatalf("len=%d", len(specs))
	}
	if specs[0].Name() != "port" || !specs[0].HasSources() {
		t.Errorf("specs[0]=%+v", specs[0])
	}
	if specs[1].ValueExpr() == "" {
		t.Errorf("specs[1].ValueExpr empty")
	}
}

// --- Validate called directly --------------------------------------------

func TestValidate_ZeroConfig_ErrInvalidVersion(t *testing.T) {
	t.Parallel()
	err := config.Validate(&config.Config{})
	if !errors.Is(err, config.ErrInvalidVersion) {
		t.Fatalf("want ErrInvalidVersion, got %v", err)
	}
}

func TestValidate_MultipleErrors_Joined(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  with-dash:
    sources:
      - env: X
    value: "x"
template:
  - to: no-from
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want joined error")
	}
	msg := err.Error()
	// Both the name error and the mutual-exclusion error should fire for the
	// same var; plus the missing `from`.
	if !strings.Contains(msg, "with-dash") {
		t.Errorf("error should name var: %v", err)
	}
	if !strings.Contains(msg, "mutually exclusive") {
		t.Errorf("error should mention mutual exclusion: %v", err)
	}
	if !strings.Contains(msg, "from or content is required") {
		t.Errorf("error should mention template from/content: %v", err)
	}
}

func TestValidate_ValidConfig_NilError(t *testing.T) {
	t.Parallel()
	// mustLoad asserts no error from Load — and Load runs Validate, so a
	// successful return here means Validate returned nil.
	cfg := mustLoad(t, `
version: 1
vars:
  ok:
    value: v
`)
	if got := cfg.Vars()[0].Name(); got != "ok" {
		t.Errorf("name=%q want ok", got)
	}
}

func TestValidate_HookErrors(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  setup:
    pre:
      - when: "true"
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "hooks.setup.pre[0]") {
		t.Fatalf("want hooks.setup.pre[0] error, got %v", err)
	}
}

func TestValidate_HookErrors_Post(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  setup:
    post:
      - {}
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "hooks.setup.post[0]") {
		t.Fatalf("want hooks.setup.post[0] error, got %v", err)
	}
}

// --- structural edge cases (cover parse / parseVar branches) ---------------

func TestLoad_TopLevelScalar_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "hello\n")
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "top-level mapping") {
		t.Fatalf("want top-level mapping error, got %v", err)
	}
}

func TestLoad_TopLevelSequence_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "[1, 2, 3]\n")
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "top-level mapping") {
		t.Fatalf("want top-level mapping error, got %v", err)
	}
}

func TestLoad_VarValueNull_Tolerated(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
vars:
  empty: null
`)
	vs := cfg.Vars()
	if len(vs) != 1 {
		t.Fatalf("len=%d", len(vs))
	}
	if vs[0].Name() != "empty" {
		t.Errorf("name=%q want empty", vs[0].Name())
	}
	if vs[0].HasSources() || vs[0].ValueExpr() != "" {
		t.Errorf("null var should be empty shape: %+v", vs[0])
	}
}

func TestLoad_VarValueScalar_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  x: hello
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "expected mapping") {
		t.Fatalf("want expected-mapping error, got %v", err)
	}
}

func TestLoad_VarSourcesNotSequence_Error(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  x:
    sources:
      cli: "--x"
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "expected sequence") {
		t.Fatalf("want expected-sequence error, got %v", err)
	}
}

func TestLoad_HookMappingDecodeError(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
hooks:
  init:
    post:
      - run: [not, a, string]
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want decode error, got nil")
	}
}

func TestLoad_VarSourceDecodeError(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, `
version: 1
vars:
  x:
    sources:
      - cli: [not, a, string]
`)
	_, err := config.Load(fs, cfgPath)
	if err == nil {
		t.Fatal("want source decode error, got nil")
	}
}

func TestLoad_HooksNull_Tolerated(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, "version: 1\nhooks: null\n")
	if cfg.Hooks != nil {
		t.Errorf("Hooks=%v want nil", cfg.Hooks)
	}
}

func TestLoad_VarSourcesNull_Tolerated(t *testing.T) {
	t.Parallel()
	cfg := mustLoad(t, `
version: 1
vars:
  x:
    value: v
    sources: null
`)
	v := cfg.Vars()[0]
	if v.HasSources() {
		t.Error("sources: null should yield HasSources=false")
	}
}

func TestLoad_TemplateDecodeError(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "version: 1\ntemplate: hello\n")
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("want template error, got %v", err)
	}
}

func TestLoad_CopyDecodeError(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "version: 1\ncopy: hello\n")
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "copy") {
		t.Fatalf("want copy error, got %v", err)
	}
}

func TestLoad_SymlinkDecodeError(t *testing.T) {
	t.Parallel()
	fs := writeConfig(t, cfgPath, "version: 1\nsymlink: hello\n")
	_, err := config.Load(fs, cfgPath)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("want symlink error, got %v", err)
	}
}

// itoa avoids importing strconv for a tiny test helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
