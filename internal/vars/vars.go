// Package vars resolves the user-defined variables declared in `.wwtr.yml`
// (PLAN §4). Resolution is deterministic and dependency-ordered: variables are
// evaluated in declaration order, so a later `value:` expression may reference
// an earlier one through `{{ .Vars.<name> }}`.
//
// Source priority differs by command:
//
//   - init:        CLI > ENV > prompt > default > fail
//   - non-init:    CLI > state > ENV > default > fail   (prompt skipped)
//
// The `prompt` source is honoured only during `init`; in every other command it
// is skipped and the value comes from state, env, or default. This is what makes
// prompt-answered values persist across `setup`/`start`/`stop` via state.yaml.
//
// `value:` is computed at resolve time with Go text/template + Masterminds/sprig.
// It is an alternative to `sources:` (they are mutually exclusive in the schema);
// if both were present, `value:` wins.
package vars

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/wailorman/wwtr/internal/di"
)

// ErrUnresolved is returned by Resolve when no source produced a value for a
// variable and no default is configured. Callers map this to exit code 4
// (PLAN §20).
var ErrUnresolved = errors.New("var: no value resolved and no default")

// SourceKind discriminates the source entries within a variable's `sources:`
// list. Each SourceSpec carries one of these kinds.
type SourceKind int

const (
	// SourceCLI reads the value from a CLI flag (populated by cobra into
	// ResolverDeps.CLIArgs under the flag name, e.g. "--base-port").
	SourceCLI SourceKind = iota
	// SourceEnv reads the value from an environment variable.
	SourceEnv
	// SourcePrompt asks the user interactively. Init-only; silently skipped in
	// every other command. Optional Validate is an anchored regex.
	SourcePrompt
)

// SourceSpec is a single entry in a variable's `sources:` list. Only the field
// matching Kind is meaningful. The concrete config package constructs these
// directly, which is why this is a struct (not an interface): config depends on
// vars types, never the reverse.
type SourceSpec struct {
	Kind     SourceKind
	CLI      string // SourceCLI: flag name, e.g. "--base-port"
	Env      string // SourceEnv: environment variable name
	Prompt   string // SourcePrompt: prompt message
	Validate string // SourcePrompt: optional validation regex
}

// VarSpec is the subset of config.Var that this package needs. Defining it here
// keeps vars decoupled from the (Phase 1) config package, so Phase 2 lands
// independently. The config package will satisfy this interface; config imports
// vars types (SourceSpec), never the reverse.
type VarSpec interface {
	// Name is the variable's key in the `vars:` map — also the `.Vars.<name>`
	// under which other variables reference it.
	Name() string
	// HasSources reports whether the `sources:` key was present (possibly empty).
	HasSources() bool
	// Sources returns the ordered source list. May be empty even when
	// HasSources is true.
	Sources() []SourceSpec
	// ValueExpr returns the Sprig expression for `value:`, or "" when not given.
	ValueExpr() string
	// Default returns the fallback value and true, or (_, false) when absent.
	Default() (any, bool)
}

// BuiltinVars is the set of pre-computed builtin variables available in every
// `value:` expression and template as `.Branch`, `.Slug`, etc.
type BuiltinVars struct {
	Branch           string
	Slug             string
	Hash             string
	ShortHash        string
	SafeName         string
	WorktreePath     string
	WorktreeName     string
	MainWorktreePath string
	MainWorktreeName string
}

// ComputeBuiltins derives every builtin variable from the branch name and the
// two worktree paths. The slug/hash family guarantees DNS/label-safe, length-
// capped identifiers so derived resources (containers, DB prefixes) never
// overflow naming limits.
func ComputeBuiltins(branch, currentPath, mainPath string) BuiltinVars {
	full := sha1Hex(branch)
	slug := slugify(branch)
	return BuiltinVars{
		Branch:           branch,
		Slug:             slug,
		Hash:             full[:8],
		ShortHash:        full[:6],
		SafeName:         safeName(slug, full[:8]),
		WorktreePath:     currentPath,
		WorktreeName:     filepath.Base(currentPath),
		MainWorktreePath: mainPath,
		MainWorktreeName: filepath.Base(mainPath),
	}
}

// ResolverDeps bundles every input the resolver needs that is not the var spec
// list itself. Maps may be nil; NewResolver normalises them.
type ResolverDeps struct {
	Prompter di.Prompter
	Env      di.Env
	CLIArgs  map[string]string // flag name → value, populated by cobra
	State    map[string]string // from state.yaml (ignored when NoState)
	Builtin  BuiltinVars
	Command  string // one of: init, setup, start, stop, clean, info, trust, untrust
	NoState  bool   // --no-state: ignore State entirely
}

// Resolver evaluates a list of VarSpecs in order. It is constructed per command
// invocation; Resolve is called exactly once.
type Resolver struct {
	deps           ResolverDeps
	promptResolved map[string]string
}

// NewResolver returns a ready Resolver. Nil maps are replaced with empty maps so
// the resolution code can index them unconditionally.
func NewResolver(deps ResolverDeps) *Resolver {
	if deps.CLIArgs == nil {
		deps.CLIArgs = map[string]string{}
	}
	if deps.State == nil {
		deps.State = map[string]string{}
	}
	return &Resolver{deps: deps}
}

// Resolve evaluates every spec in declaration order so later `value:` vars may
// reference earlier ones. It returns the full resolved map, or (nil, error)
// naming the first unresolved variable. Variables resolved via prompt during
// init are recorded for PromptResolved.
func (r *Resolver) Resolve(specs []VarSpec) (map[string]string, error) {
	r.promptResolved = map[string]string{}
	out := make(map[string]string, len(specs))
	for _, spec := range specs {
		val, fromPrompt, err := r.resolveOne(spec, out)
		if err != nil {
			return nil, fmt.Errorf("var %q: %w", spec.Name(), err)
		}
		out[spec.Name()] = val
		if fromPrompt {
			r.promptResolved[spec.Name()] = val
		}
	}
	return out, nil
}

// PromptResolved returns the variables that were resolved through an interactive
// prompt during the most recent successful Resolve (init only). The init
// orchestrator writes exactly this subset to state.yaml (PLAN §5: only
// prompt-answered values are persisted). Returns an empty map otherwise.
func (r *Resolver) PromptResolved() map[string]string {
	out := make(map[string]string, len(r.promptResolved))
	for k, v := range r.promptResolved {
		out[k] = v
	}
	return out
}

// resolveOne resolves a single variable against the already-resolved set (so
// `value:` may reference earlier vars). The fromPrompt flag tells Resolve to
// record the value for state persistence.
func (r *Resolver) resolveOne(spec VarSpec, resolved map[string]string) (string, bool, error) {
	if expr := spec.ValueExpr(); expr != "" {
		v, err := r.evalValue(expr, resolved)
		if err != nil {
			return "", false, err
		}
		return v, false, nil
	}

	isInit := r.deps.Command == "init"
	srcs := spec.Sources()

	if v, ok := cliValue(r.deps.CLIArgs, srcs); ok {
		return v, false, nil
	}
	// State sits between CLI and ENV in non-init (PLAN §4.3). Prompt-answered
	// values thus resurface here without re-asking.
	if !isInit && !r.deps.NoState {
		if v, ok := r.deps.State[spec.Name()]; ok {
			return v, false, nil
		}
	}
	if v, ok := envValue(r.deps.Env, srcs); ok {
		return v, false, nil
	}
	if isInit {
		if v, ok := r.promptValue(srcs); ok {
			return v, true, nil
		}
	}
	if d, ok := spec.Default(); ok {
		return stringify(d), false, nil
	}
	return "", false, ErrUnresolved
}

func cliValue(cli map[string]string, srcs []SourceSpec) (string, bool) {
	for _, s := range srcs {
		if s.Kind != SourceCLI {
			continue
		}
		if v, ok := cli[s.CLI]; ok {
			return v, true
		}
	}
	return "", false
}

func envValue(env di.Env, srcs []SourceSpec) (string, bool) {
	for _, s := range srcs {
		if s.Kind != SourceEnv {
			continue
		}
		if v, ok := env.Lookup(s.Env); ok {
			return v, true
		}
	}
	return "", false
}

// promptValue asks each prompt source in turn. A prompt that returns an error
// (non-tty, --yes without scripted input, validation exhausted) is treated as
// "no answer" and the search continues to the next prompt source or default —
// this is how non-interactive `init` falls back to defaults gracefully.
func (r *Resolver) promptValue(srcs []SourceSpec) (string, bool) {
	for _, s := range srcs {
		if s.Kind != SourcePrompt {
			continue
		}
		v, err := r.deps.Prompter.Input(s.Prompt, "", s.Validate)
		if err != nil {
			continue
		}
		return v, true
	}
	return "", false
}

// evalValue renders a Sprig expression. Builtins are exposed at the top level
// (`.Branch`, `.Slug`, ...) and already-resolved user vars under `.Vars`. A
// reference to a not-yet-resolved variable is a hard error (missingkey=error):
// variables must be declared in dependency order.
func (r *Resolver) evalValue(expr string, resolved map[string]string) (string, error) {
	tmpl, err := template.New("var").Funcs(sprig.FuncMap()).Option("missingkey=error").Parse(expr)
	if err != nil {
		return "", fmt.Errorf("parse value %q: %w", expr, err)
	}
	data := struct {
		BuiltinVars
		Vars map[string]string
	}{
		BuiltinVars: r.deps.Builtin,
		Vars:        resolved,
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("eval value %q: %w", expr, err)
	}
	return buf.String(), nil
}

// stringify converts a YAML-decoded default (string, int, bool, …) to the string
// form templates and hooks expect. `%v` matches how Go renders scalars.
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

// safeName combines the (possibly truncated) slug with an 8-hex hash so two
// branches that slugify identically still produce distinct identifiers, while
// never exceeding 63 characters (PLAN §4.1). Uses `.Hash` (8 chars).
func safeName(slug, hash8 string) string {
	const max = 63
	maxSlug := max - 1 - len(hash8)
	if maxSlug < 0 {
		return hash8
	}
	if len(slug) > maxSlug {
		slug = strings.TrimRight(slug[:maxSlug], "-")
	}
	if slug == "" {
		return hash8
	}
	return slug + "-" + hash8
}

// slugify produces a DNS-label-safe lower-case `[a-z0-9-]` form of branch,
// collapsing runs of non-conforming characters into single hyphens, trimming
// leading/trailing hyphens, and capping at 63 characters.
func slugify(branch string) string {
	const max = 63
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(branch) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > max {
		out = strings.TrimRight(out[:max], "-")
	}
	return out
}

func sha1Hex(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
