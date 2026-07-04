package vars

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/di/fakes"
)

// --- test VarSpec implementation -------------------------------------------

type fakeSpec struct {
	name  string
	srcs  []SourceSpec
	value string
	hasS  bool
	def   any
	hasD  bool
}

func (f fakeSpec) Name() string          { return f.name }
func (f fakeSpec) HasSources() bool      { return f.hasS }
func (f fakeSpec) Sources() []SourceSpec { return f.srcs }
func (f fakeSpec) ValueExpr() string     { return f.value }
func (f fakeSpec) Default() (any, bool)  { return f.def, f.hasD }

// shortcuts for building sources
func cliSrc(flag string) SourceSpec   { return SourceSpec{Kind: SourceCLI, CLI: flag} }
func envSrc(name string) SourceSpec   { return SourceSpec{Kind: SourceEnv, Env: name} }
func promptSrc(msg string) SourceSpec { return SourceSpec{Kind: SourcePrompt, Prompt: msg} }
func promptValidSrc(msg, re string) SourceSpec {
	return SourceSpec{Kind: SourcePrompt, Prompt: msg, Validate: re}
}

func srcVar(name string, srcs ...SourceSpec) fakeSpec {
	return fakeSpec{name: name, srcs: srcs, hasS: true}
}

func srcVarDef(name string, def any, srcs ...SourceSpec) fakeSpec {
	return fakeSpec{name: name, srcs: srcs, hasS: true, def: def, hasD: true}
}
func valVar(name, expr string) fakeSpec { return fakeSpec{name: name, value: expr} }
func defOnlyVar(name string, def any) fakeSpec {
	return fakeSpec{name: name, hasS: false, def: def, hasD: true}
}

// --- ComputeBuiltins --------------------------------------------------------

func TestComputeBuiltins(t *testing.T) {
	t.Parallel()
	branch := "feature/JIRA-123_add-login"
	full := sha1Hex(branch)
	got := ComputeBuiltins(branch, "/work/repo-wt", "/work/repo")

	if got.Branch != branch {
		t.Errorf("Branch=%q want %q", got.Branch, branch)
	}
	wantSlug := "feature-jira-123-add-login"
	if got.Slug != wantSlug {
		t.Errorf("Slug=%q want %q", got.Slug, wantSlug)
	}
	if got.Hash != full[:8] {
		t.Errorf("Hash=%q want %q", got.Hash, full[:8])
	}
	if got.ShortHash != full[:6] {
		t.Errorf("ShortHash=%q want %q", got.ShortHash, full[:6])
	}
	wantSafe := wantSlug + "-" + full[:8]
	if got.SafeName != wantSafe {
		t.Errorf("SafeName=%q want %q", got.SafeName, wantSafe)
	}
	if got.WorktreePath != "/work/repo-wt" {
		t.Errorf("WorktreePath=%q", got.WorktreePath)
	}
	if got.WorktreeName != "repo-wt" {
		t.Errorf("WorktreeName=%q want repo-wt", got.WorktreeName)
	}
	if got.MainWorktreePath != "/work/repo" {
		t.Errorf("MainWorktreePath=%q", got.MainWorktreePath)
	}
	if got.MainWorktreeName != "repo" {
		t.Errorf("MainWorktreeName=%q want repo", got.MainWorktreeName)
	}
}

func TestComputeBuiltins_TableOfBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		branch   string
		wantSlug string
	}{
		{"main", "main"},
		{"feature/x", "feature-x"},
		{"feature/JIRA-123_add-login", "feature-jira-123-add-login"},
		{"UPPER", "upper"},
		{"release/v1.2.3", "release-v1-2-3"},
		{"feat--double", "feat-double"}, // collapsed runs
		{"_leading", "leading"},         // trimmed leading
		{"trailing_", "trailing"},       // trimmed trailing
		{"café", "caf"},                 // non-ascii → dash, trimmed
		{"", ""},                        // empty
		{"a/b/c", "a-b-c"},
	}
	for _, tc := range cases {
		t.Run(tc.branch, func(t *testing.T) {
			t.Parallel()
			got := ComputeBuiltins(tc.branch, "/wt", "/main")
			if got.Slug != tc.wantSlug {
				t.Errorf("branch %q: Slug=%q want %q", tc.branch, got.Slug, tc.wantSlug)
			}
			full := sha1Hex(tc.branch)
			if got.Hash != full[:8] {
				t.Errorf("branch %q: Hash=%q want %q", tc.branch, got.Hash, full[:8])
			}
			if got.ShortHash != full[:6] {
				t.Errorf("branch %q: ShortHash=%q want %q", tc.branch, got.ShortHash, full[:6])
			}
			if len(got.Slug) > 63 {
				t.Errorf("branch %q: Slug len %d > 63", tc.branch, len(got.Slug))
			}
			if len(got.SafeName) > 63 {
				t.Errorf("branch %q: SafeName len %d > 63: %q", tc.branch, len(got.SafeName), got.SafeName)
			}
			// Hash and ShortHash must be hex prefixes of sha1(branch).
			if !strings.HasPrefix(full, got.Hash) {
				t.Errorf("branch %q: Hash not prefix of sha1", tc.branch)
			}
			if !strings.HasPrefix(got.Hash, got.ShortHash) {
				t.Errorf("branch %q: ShortHash not prefix of Hash", tc.branch)
			}
		})
	}
}

func TestComputeBuiltins_LongBranchTruncation(t *testing.T) {
	t.Parallel()
	// 200-char branch → slug and SafeName must both stay ≤ 63.
	branch := strings.Repeat("a", 200)
	got := ComputeBuiltins(branch, "/wt", "/main")
	if len(got.Slug) > 63 {
		t.Errorf("Slug len %d > 63: %q", len(got.Slug), got.Slug)
	}
	if len(got.SafeName) > 63 {
		t.Errorf("SafeName len %d > 63: %q", len(got.SafeName), got.SafeName)
	}
	if len(got.Slug) != 63 {
		t.Errorf("Slug should be exactly 63 for 200-char branch, got %d (%q)", len(got.Slug), got.Slug)
	}
	// SafeName = slug[:54] + "-" + hash8 = 63 exactly.
	if len(got.SafeName) != 63 {
		t.Errorf("SafeName should be exactly 63, got %d (%q)", len(got.SafeName), got.SafeName)
	}
	if strings.HasSuffix(got.Slug, "-") {
		t.Errorf("Slug must not end with dash: %q", got.Slug)
	}
	if strings.HasSuffix(got.SafeName[:len(got.SafeName)-9], "-") {
		t.Errorf("SafeName slug part must not end with dash: %q", got.SafeName)
	}
}

func TestComputeBuiltins_EmptyBranch(t *testing.T) {
	t.Parallel()
	got := ComputeBuiltins("", "/wt", "/main")
	full := sha1Hex("")
	if got.Slug != "" {
		t.Errorf("empty branch Slug=%q want empty", got.Slug)
	}
	if got.SafeName != full[:8] {
		t.Errorf("empty branch SafeName=%q want %q (hash only)", got.SafeName, full[:8])
	}
	if got.WorktreeName != "wt" {
		t.Errorf("WorktreeName=%q want wt", got.WorktreeName)
	}
}

func TestComputeBuiltins_PathsAreBasename(t *testing.T) {
	t.Parallel()
	got := ComputeBuiltins("main", "/a/b/c", "/x/y/z")
	if got.WorktreeName != "c" || got.MainWorktreeName != "z" {
		t.Errorf("names wrong: wt=%q main=%q", got.WorktreeName, got.MainWorktreeName)
	}
}

func TestSha1Hex_MatchesCryptoSha1(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"", "main", "feature/x"} {
		sum := sha1.Sum([]byte(s))
		want := hex.EncodeToString(sum[:])
		if got := sha1Hex(s); got != want {
			t.Errorf("sha1Hex(%q)=%q want %q", s, got, want)
		}
	}
}

func TestSafeName_OversizedHash_ReturnsHashOnly(t *testing.T) {
	t.Parallel()
	// Defensive guard: if a caller passed a hash longer than 62 chars, the slug
	// slice would underflow; the guard returns the hash untouched instead.
	long := strings.Repeat("a", 70)
	if got := safeName("slug", long); got != long {
		t.Errorf("oversized hash: got %q want %q", got, long)
	}
}

func TestSafeName_EmptySlug_ReturnsHashOnly(t *testing.T) {
	t.Parallel()
	if got := safeName("", "deadbeef"); got != "deadbeef" {
		t.Errorf("empty slug: got %q want deadbeef", got)
	}
}

func TestSafeName_SlugTrimsTrailingDashAfterTruncation(t *testing.T) {
	t.Parallel()
	// A long slug whose truncation point (54) lands inside a dash run must have
	// the dangling dash trimmed so the result never contains "--".
	slug := strings.Repeat("a", 53) + "--extra" // truncating [:54] ends with "-"
	got := safeName(slug, "h1h2h3h4")
	if strings.Contains(got, "--") {
		t.Errorf("result has double dash: %q", got)
	}
	if len(got) > 63 {
		t.Errorf("result too long: %q (%d)", got, len(got))
	}
	// The 53 a's are preserved, followed by a single separator and the hash.
	if !strings.HasPrefix(got, strings.Repeat("a", 53)+"-") {
		t.Errorf("unexpected truncation result: %q", got)
	}
}

func TestSlugify_EdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"", ""},
		{"---", ""},        // all separators → trimmed to empty
		{"a---b", "a-b"},   // collapsed
		{"AbC", "abc"},     // lowercased
		{"a.b.c", "a-b-c"}, // dots → dashes
		{"a/b/c", "a-b-c"}, // slashes → dashes
	}
	for _, tc := range cases {
		if got := slugify(tc.in); got != tc.want {
			t.Errorf("slugify(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestStringify_Nil(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	got, err := r.Resolve([]VarSpec{fakeSpec{name: "p", hasS: false, def: nil, hasD: true}})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "" {
		t.Errorf("nil default should stringify to empty, got %q", got["p"])
	}
}

func TestStringify_Int(t *testing.T) {
	t.Parallel()
	if got := stringify(42); got != "42" {
		t.Errorf("int: got %q", got)
	}
}

// --- Resolve: source combinations ------------------------------------------

func newR(t *testing.T, deps ResolverDeps) *Resolver {
	t.Helper()
	return NewResolver(deps)
}

func TestResolve_CLIOnly(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		CLIArgs: map[string]string{"--port": "4000"},
	})
	got, err := r.Resolve([]VarSpec{srcVar("p", cliSrc("--port"))})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "4000" {
		t.Errorf("got %q want 4000", got["p"])
	}
}

func TestResolve_EnvOnly(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		Env:     fakes.MapEnv{Vars: map[string]string{"PORT": "5000"}},
	})
	got, err := r.Resolve([]VarSpec{srcVar("p", envSrc("PORT"))})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "5000" {
		t.Errorf("got %q want 5000", got["p"])
	}
}

func TestResolve_PromptOnly_Init(t *testing.T) {
	t.Parallel()
	p := &fakes.FakePrompter{Inputs: []string{"6000"}}
	r := newR(t, ResolverDeps{Command: "init", Prompter: p})
	got, err := r.Resolve([]VarSpec{srcVar("p", promptSrc("PORT?"))})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "6000" {
		t.Errorf("got %q want 6000", got["p"])
	}
	if len(p.Calls) != 1 || !strings.Contains(p.Calls[0], "PORT?") {
		t.Errorf("prompt not called as expected: %v", p.Calls)
	}
	if pr := r.PromptResolved(); pr["p"] != "6000" {
		t.Errorf("PromptResolved=%v want p=6000", pr)
	}
}

func TestResolve_PromptValidate_PassedThrough(t *testing.T) {
	t.Parallel()
	p := &fakes.FakePrompter{Inputs: []string{"42"}}
	r := newR(t, ResolverDeps{Command: "init", Prompter: p})
	got, err := r.Resolve([]VarSpec{srcVar("p", promptValidSrc("N?", `^\d+$`))})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "42" {
		t.Errorf("got %q want 42", got["p"])
	}
}

func TestResolve_DefaultOnly(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	got, err := r.Resolve([]VarSpec{defOnlyVar("p", 3000)})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "3000" {
		t.Errorf("got %q want 3000", got["p"])
	}
}

func TestResolve_DefaultString(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	got, err := r.Resolve([]VarSpec{defOnlyVar("p", "abc")})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "abc" {
		t.Errorf("got %q want abc", got["p"])
	}
}

func TestResolve_DefaultBool(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	got, err := r.Resolve([]VarSpec{defOnlyVar("p", true)})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "true" {
		t.Errorf("got %q want true", got["p"])
	}
}

func TestResolve_MissingAll_ErrUnresolved(t *testing.T) {
	t.Parallel()
	p := &fakes.FakePrompter{} // no scripted inputs → prompt returns error → falls through
	r := newR(t, ResolverDeps{
		Command:  "init",
		Env:      fakes.MapEnv{Vars: map[string]string{}},
		Prompter: p,
	})
	_, err := r.Resolve([]VarSpec{srcVar("p", cliSrc("--x"), envSrc("X"), promptSrc("X?"))})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("want ErrUnresolved, got %v", err)
	}
	if !strings.Contains(err.Error(), `"p"`) {
		t.Errorf("error should name the var: %v", err)
	}
}

func TestResolve_NoSourcesNoValueNoDefault_ErrUnresolved(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	_, err := r.Resolve([]VarSpec{fakeSpec{name: "p"}})
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("want ErrUnresolved, got %v", err)
	}
}

func TestResolve_PriorityInit_CLI_Over_ENV_Over_Prompt_Over_Default(t *testing.T) {
	t.Parallel()
	// all four present → CLI wins.
	p := &fakes.FakePrompter{Inputs: []string{"prompt-v"}}
	r := newR(t, ResolverDeps{
		Command:  "init",
		CLIArgs:  map[string]string{"--port": "cli-v"},
		Env:      fakes.MapEnv{Vars: map[string]string{"PORT": "env-v"}},
		Prompter: p,
	})
	spec := srcVarDef("p", "def-v", cliSrc("--port"), envSrc("PORT"), promptSrc("P?"))
	got, err := r.Resolve([]VarSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "cli-v" {
		t.Errorf("CLI should win: got %q", got["p"])
	}
	if len(p.Calls) != 0 {
		t.Errorf("prompt should not be called when CLI present: %v", p.Calls)
	}

	// ENV over prompt over default (no CLI).
	p2 := &fakes.FakePrompter{Inputs: []string{"prompt-v"}}
	r2 := newR(t, ResolverDeps{
		Command:  "init",
		Env:      fakes.MapEnv{Vars: map[string]string{"PORT": "env-v"}},
		Prompter: p2,
	})
	got2, err := r2.Resolve([]VarSpec{srcVarDef("p", "def-v", envSrc("PORT"), promptSrc("P?"))})
	if err != nil {
		t.Fatal(err)
	}
	if got2["p"] != "env-v" {
		t.Errorf("ENV should beat prompt: got %q", got2["p"])
	}

	// Prompt over default (no CLI, no ENV).
	p3 := &fakes.FakePrompter{Inputs: []string{"prompt-v"}}
	r3 := newR(t, ResolverDeps{Command: "init", Prompter: p3})
	got3, err := r3.Resolve([]VarSpec{srcVarDef("p", "def-v", promptSrc("P?"))})
	if err != nil {
		t.Fatal(err)
	}
	if got3["p"] != "prompt-v" {
		t.Errorf("prompt should beat default: got %q", got3["p"])
	}
}

func TestResolve_PromptFails_FallsToDefault(t *testing.T) {
	t.Parallel()
	// Prompter returns error (non-tty) → fall to default, no ErrUnresolved.
	p := &fakes.FakePrompter{InputErr: []error{errors.New("not a tty")}}
	r := newR(t, ResolverDeps{Command: "init", Prompter: p})
	got, err := r.Resolve([]VarSpec{srcVarDef("p", "3000", promptSrc("P?"))})
	if err != nil {
		t.Fatalf("want default fallback, got err %v", err)
	}
	if got["p"] != "3000" {
		t.Errorf("got %q want 3000", got["p"])
	}
	if len(r.PromptResolved()) != 0 {
		t.Errorf("prompt failed → nothing recorded: %v", r.PromptResolved())
	}
}

func TestResolve_MultiplePromptSources_FirstWins(t *testing.T) {
	t.Parallel()
	p := &fakes.FakePrompter{Inputs: []string{"first", "second"}}
	r := newR(t, ResolverDeps{Command: "init", Prompter: p})
	got, err := r.Resolve([]VarSpec{srcVar("p", promptSrc("one?"), promptSrc("two?"))})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "first" {
		t.Errorf("got %q want first", got["p"])
	}
	if len(p.Calls) != 1 {
		t.Errorf("only first prompt should fire: %v", p.Calls)
	}
}

func TestResolve_NonInit_PromptSkipped_StateUsed(t *testing.T) {
	t.Parallel()
	p := &fakes.FakePrompter{} // no scripted inputs; would error if called
	r := newR(t, ResolverDeps{
		Command:  "setup",
		State:    map[string]string{"p": "from-state"},
		Prompter: p,
	})
	got, err := r.Resolve([]VarSpec{
		srcVarDef("p", "def", cliSrc("--port"), envSrc("PORT"), promptSrc("P?")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "from-state" {
		t.Errorf("non-init should use state: got %q want from-state", got["p"])
	}
	if len(p.Calls) != 0 {
		t.Errorf("prompt must be skipped in non-init: %v", p.Calls)
	}
	if len(r.PromptResolved()) != 0 {
		t.Errorf("non-init records no prompt vars: %v", r.PromptResolved())
	}
}

func TestResolve_NonInit_Priority_CLI_Over_State_Over_ENV_Over_Default(t *testing.T) {
	t.Parallel()
	// CLI wins over state.
	r := newR(t, ResolverDeps{
		Command: "setup",
		CLIArgs: map[string]string{"--port": "cli"},
		State:   map[string]string{"p": "state"},
		Env:     fakes.MapEnv{Vars: map[string]string{"PORT": "env"}},
	})
	got, _ := r.Resolve([]VarSpec{
		srcVarDef("p", "def", cliSrc("--port"), envSrc("PORT"), promptSrc("P?")),
	})
	if got["p"] != "cli" {
		t.Errorf("CLI should win: got %q", got["p"])
	}

	// State wins over ENV.
	r2 := newR(t, ResolverDeps{
		Command: "setup",
		State:   map[string]string{"p": "state"},
		Env:     fakes.MapEnv{Vars: map[string]string{"PORT": "env"}},
	})
	got2, _ := r2.Resolve([]VarSpec{
		srcVarDef("p", "def", cliSrc("--port"), envSrc("PORT"), promptSrc("P?")),
	})
	if got2["p"] != "state" {
		t.Errorf("state should beat ENV: got %q", got2["p"])
	}

	// ENV wins over default (no CLI, no state).
	r3 := newR(t, ResolverDeps{
		Command: "setup",
		Env:     fakes.MapEnv{Vars: map[string]string{"PORT": "env"}},
	})
	got3, _ := r3.Resolve([]VarSpec{
		srcVarDef("p", "def", cliSrc("--port"), envSrc("PORT"), promptSrc("P?")),
	})
	if got3["p"] != "env" {
		t.Errorf("ENV should beat default: got %q", got3["p"])
	}
}

func TestResolve_NoState_Flag_IgnoresState(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "setup",
		NoState: true,
		State:   map[string]string{"p": "from-state"},
		Env:     fakes.MapEnv{Vars: map[string]string{"PORT": "env"}},
	})
	got, err := r.Resolve([]VarSpec{
		srcVarDef("p", "def", cliSrc("--port"), envSrc("PORT"), promptSrc("P?")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "env" {
		t.Errorf("--no-state should skip state: got %q want env", got["p"])
	}
}

func TestResolve_NoState_PromptOnlyVar_FallsToDefault(t *testing.T) {
	t.Parallel()
	// A prompt-only var in non-init, no --no-state, but no entry in state → default.
	r := newR(t, ResolverDeps{Command: "setup", State: map[string]string{}})
	got, err := r.Resolve([]VarSpec{srcVarDef("p", "def-v", promptSrc("P?"))})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "def-v" {
		t.Errorf("got %q want def-v", got["p"])
	}
}

func TestResolve_NoState_Flag_PromptOnlyVar_FailsWithoutDefault(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "setup", NoState: true, State: map[string]string{"p": "ignored"}})
	_, err := r.Resolve([]VarSpec{srcVar("p", promptSrc("P?"))})
	if !errors.Is(err, ErrUnresolved) {
		t.Fatalf("want ErrUnresolved, got %v", err)
	}
}

func TestResolve_Init_IgnoresState(t *testing.T) {
	t.Parallel()
	// init never reads state even if present.
	r := newR(t, ResolverDeps{
		Command: "init",
		State:   map[string]string{"p": "from-state"},
		Env:     fakes.MapEnv{Vars: map[string]string{"PORT": "env"}},
	})
	got, err := r.Resolve([]VarSpec{
		srcVarDef("p", "def", cliSrc("--port"), envSrc("PORT"), promptSrc("P?")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "env" {
		t.Errorf("init should skip state → ENV: got %q want env", got["p"])
	}
}

// --- Resolve: value expressions --------------------------------------------

func TestResolve_Value_Basic(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		Builtin: ComputeBuiltins("feature/x", "/wt", "/main"),
	})
	got, err := r.Resolve([]VarSpec{valVar("p", "prefix-{{ .Slug }}")})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "prefix-feature-x" {
		t.Errorf("got %q", got["p"])
	}
}

func TestResolve_Value_SprigFunctions(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		Builtin: ComputeBuiltins("feature/x", "/wt", "/main"),
	})
	got, err := r.Resolve([]VarSpec{valVar("p", "{{ .Slug | upper }}")})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "FEATURE-X" {
		t.Errorf("got %q want FEATURE-X", got["p"])
	}
}

func TestResolve_Value_SafeNameTrunc(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		Builtin: ComputeBuiltins("feature/x", "/wt", "/main"),
	})
	got, err := r.Resolve([]VarSpec{valVar("p", "wk_{{ .SafeName | trunc 50 }}")})
	if err != nil {
		t.Fatal(err)
	}
	// SafeName for "feature/x" = "feature-x-<hash8>"; trunc 50 keeps it whole.
	if !strings.HasPrefix(got["p"], "wk_feature-x-") {
		t.Errorf("got %q", got["p"])
	}
}

func TestResolve_Value_ReferencesEarlierVars_OrderingPreserved(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		Builtin: ComputeBuiltins("feature/x", "/wt", "/main"),
	})
	specs := []VarSpec{
		valVar("base", "1000"),
		valVar("derived", "{{ .Vars.base }}-{{ .Slug }}"),
	}
	got, err := r.Resolve(specs)
	if err != nil {
		t.Fatal(err)
	}
	if got["base"] != "1000" {
		t.Errorf("base=%q", got["base"])
	}
	if got["derived"] != "1000-feature-x" {
		t.Errorf("derived=%q want 1000-feature-x", got["derived"])
	}
}

func TestResolve_Value_SourceVarReferencedByValue(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		CLIArgs: map[string]string{"--port": "4000"},
	})
	specs := []VarSpec{
		srcVar("port", cliSrc("--port")),
		valVar("url", "http://localhost:{{ .Vars.port }}"),
	}
	got, err := r.Resolve(specs)
	if err != nil {
		t.Fatal(err)
	}
	if got["url"] != "http://localhost:4000" {
		t.Errorf("url=%q", got["url"])
	}
}

func TestResolve_Value_ReferencesUnresolvedVar_Errors(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init", Builtin: ComputeBuiltins("main", "/w", "/m")})
	_, err := r.Resolve([]VarSpec{valVar("p", "{{ .Vars.never }}")})
	if err == nil {
		t.Fatal("referencing unresolved var should error (missingkey=error)")
	}
}

func TestResolve_Value_ParseError(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	_, err := r.Resolve([]VarSpec{valVar("p", "{{ .Slug ")})
	if err == nil || !strings.Contains(err.Error(), "parse value") {
		t.Fatalf("want parse error, got %v", err)
	}
}

func TestResolve_Value_BuiltinFieldsAccessible(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		Builtin: ComputeBuiltins("feature/x", "/wt", "/main"),
	})
	got, err := r.Resolve([]VarSpec{valVar("p", "{{ .Branch }}|{{ .Hash }}|{{ .ShortHash }}|{{ .WorktreeName }}|{{ .MainWorktreeName }}")})
	if err != nil {
		t.Fatal(err)
	}
	b := ComputeBuiltins("feature/x", "/wt", "/main")
	want := b.Branch + "|" + b.Hash + "|" + b.ShortHash + "|" + b.WorktreeName + "|" + b.MainWorktreeName
	if got["p"] != want {
		t.Errorf("got %q want %q", got["p"], want)
	}
}

func TestResolve_Value_Over_Sources(t *testing.T) {
	t.Parallel()
	// Both set (shouldn't happen via schema) — value wins per package contract.
	r := newR(t, ResolverDeps{
		Command: "init",
		CLIArgs: map[string]string{"--port": "4000"},
		Builtin: ComputeBuiltins("x", "/w", "/m"),
	})
	spec := fakeSpec{name: "p", value: "{{ .Slug }}", srcs: []SourceSpec{cliSrc("--port")}, hasS: true}
	got, err := r.Resolve([]VarSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	if got["p"] != "x" {
		t.Errorf("value should win: got %q", got["p"])
	}
}

func TestResolve_FirstFailureStops(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		CLIArgs: map[string]string{"--present": "v"},
	})
	_, err := r.Resolve([]VarSpec{
		srcVar("ok", cliSrc("--present")),
		srcVar("bad", cliSrc("--missing")),
	})
	if err == nil || !strings.Contains(err.Error(), `"bad"`) {
		t.Fatalf("want error naming bad, got %v", err)
	}
}

func TestResolve_EmptySpecs(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	got, err := r.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}

func TestResolve_AllVariablesInOrder(t *testing.T) {
	t.Parallel()
	// Mirrors PLAN §13: a cli/env/prompt var, then two computed vars referencing SafeName.
	p := &fakes.FakePrompter{Inputs: []string{"3010"}}
	r := newR(t, ResolverDeps{
		Command:  "init",
		Env:      fakes.MapEnv{Vars: map[string]string{}},
		Prompter: p,
		Builtin:  ComputeBuiltins("feature/JIRA-123_add-login", "/wt", "/main"),
	})
	specs := []VarSpec{
		srcVarDef("base_port", 3000, cliSrc("--base-port"), envSrc("BASE_PORT"), promptValidSrc("PORT?", `^\d+$`)),
		valVar("db_prefix", "wk_{{ .SafeName | trunc 50 }}"),
		valVar("mail_container", "wk_mail_{{ .Vars.db_prefix }}"),
	}
	got, err := r.Resolve(specs)
	if err != nil {
		t.Fatal(err)
	}
	if got["base_port"] != "3010" {
		t.Errorf("base_port=%q want 3010", got["base_port"])
	}
	if !strings.HasPrefix(got["db_prefix"], "wk_") {
		t.Errorf("db_prefix=%q", got["db_prefix"])
	}
	if !strings.HasPrefix(got["mail_container"], "wk_mail_wk_") {
		t.Errorf("mail_container=%q (should reference db_prefix)", got["mail_container"])
	}
	if pr := r.PromptResolved(); pr["base_port"] != "3010" || len(pr) != 1 {
		t.Errorf("PromptResolved=%v want only base_port=3010", pr)
	}
}

func TestNewResolver_NilMapsNormalized(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{
		Command: "init",
		Env:     fakes.MapEnv{Vars: map[string]string{}},
	})
	// These would panic if CLIArgs/State were not normalised to empty maps.
	if _, err := r.Resolve([]VarSpec{srcVar("p", cliSrc("--x"), envSrc("X"))}); !errors.Is(err, ErrUnresolved) {
		t.Fatalf("want ErrUnresolved, got %v", err)
	}
}

func TestPromptResolved_EmptyBeforeResolve(t *testing.T) {
	t.Parallel()
	r := newR(t, ResolverDeps{Command: "init"})
	if got := r.PromptResolved(); len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestPromptResolved_CopyNotAlias(t *testing.T) {
	t.Parallel()
	p := &fakes.FakePrompter{Inputs: []string{"v"}}
	r := newR(t, ResolverDeps{Command: "init", Prompter: p})
	_, _ = r.Resolve([]VarSpec{srcVar("p", promptSrc("?"))})
	pr := r.PromptResolved()
	pr["p"] = "mutated"
	again := r.PromptResolved()
	if again["p"] != "v" {
		t.Errorf("PromptResolved should return a copy: got %v after external mutation", again)
	}
}
