// Package config parses and validates the project's `.wwtr.yml` manifest
// (PLAN §2, §12). The on-disk schema maps closely to the Config struct with one
// wrinkle — the `vars:` map must preserve declaration order so the vars
// resolver can evaluate later variables in terms of earlier ones. yaml.v3
// alone loses map order, so vars are decoded from a yaml.Node directly.
package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/wailorman/wwtr/internal/di"
	"github.com/wailorman/wwtr/internal/vars"
	"gopkg.in/yaml.v3"
)

// currentVersion is the only schema version wwtr understands (PLAN §12).
// Anything else in `.wwtr.yml` is rejected as ErrInvalidVersion.
const currentVersion = 1

// Sentinel errors. Load wraps these with the offending path so callers can
// discriminate via errors.Is while still seeing where the failure originated.
var (
	ErrNotFound       = errors.New("config: file not found")
	ErrInvalidVersion = errors.New("config: missing or unsupported version")
)

// varNameRe constrains var names to the same shape as a Go identifier — this
// is what makes them safely addressable as `.Vars.<name>` in Sprig templates.
var varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Config is the parsed and validated `.wwtr.yml`. Vars are kept in declaration
// order; that slice is the canonical input for vars.Resolver.
type Config struct {
	Version  int
	vars     []Var
	Template []PathSpec
	Copy     []PathSpec
	Symlink  []PathSpec
	Hooks    map[string]HookStage
}

// Vars returns the user-defined variables in their declaration order so the
// resolver can evaluate `value:` expressions that reference earlier ones.
func (c *Config) Vars() []Var { return c.vars }

// HookStage returns the hooks for one stage ("pre" or "post") of one command
// ("init", "setup", ...). Returns nil when the command or stage is absent —
// callers iterate the result without further guards.
func (c *Config) HookStage(cmd, stage string) []Hook {
	if c.Hooks == nil {
		return nil
	}
	hs, ok := c.Hooks[cmd]
	if !ok {
		return nil
	}
	switch stage {
	case "pre":
		return hs.Pre
	case "post":
		return hs.Post
	}
	return nil
}

// PathSpec is a single template/copy/symlink entry. After normalisation To is
// always populated: a YAML entry with only `from:` gets To = From.
type PathSpec struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// HookStage groups the pre and post hook lists of a single command.
type HookStage struct {
	Pre  []Hook `yaml:"pre"`
	Post []Hook `yaml:"post"`
}

// Hook is a single hook entry. The YAML element takes one of three shapes
// (PLAN §12): a bare command string, a `run:`/`when:` map, or a `load_env:`
// map. UnmarshalYAML collapses all three into this struct.
type Hook struct {
	Run     string
	When    string
	LoadEnv string
}

// IsLoadEnv reports whether this hook is a `load_env:` action rather than a
// shell command. The hooks executor branches on this (PLAN §20).
func (h Hook) IsLoadEnv() bool { return h.LoadEnv != "" }

// UnmarshalYAML accepts the three hook shapes from PLAN §12. A scalar becomes
// a plain `run`; a mapping picks up run/when/load_env by tag.
func (h *Hook) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		h.Run = value.Value
		return nil
	case yaml.MappingNode:
		var fields struct {
			Run     string `yaml:"run"`
			When    string `yaml:"when"`
			LoadEnv string `yaml:"load_env"`
		}
		if err := value.Decode(&fields); err != nil {
			return err
		}
		h.Run = fields.Run
		h.When = fields.When
		h.LoadEnv = fields.LoadEnv
		return nil
	}
	return fmt.Errorf("hook: expected scalar or mapping, got %s", kindName(value.Kind))
}

// Var is a single user-defined variable. It implements [vars.VarSpec] so the
// resolver can consume []Var via a trivial []vars.VarSpec conversion.
type Var struct {
	name       string
	hasSources bool
	sources    []vars.SourceSpec
	value      string
	hasDefault bool
	defaultVal any
}

// Compile-time check that Var satisfies vars.VarSpec.
var _ vars.VarSpec = Var{}

// Name returns the variable's key under `vars:`.
func (v Var) Name() string { return v.name }

// HasSources reports whether the `sources:` key was present.
func (v Var) HasSources() bool { return v.hasSources }

// Sources returns the ordered source list (may be empty even when HasSources).
func (v Var) Sources() []vars.SourceSpec { return v.sources }

// ValueExpr returns the Sprig `value:` expression, or "" when not given.
func (v Var) ValueExpr() string { return v.value }

// Default returns the fallback value and whether `default:` was present.
func (v Var) Default() (any, bool) { return v.defaultVal, v.hasDefault }

// Load reads, parses, normalises and validates the manifest at path. A missing
// file yields an error wrapping ErrNotFound; an unsupported or absent version
// yields one wrapping ErrInvalidVersion. Every error includes path for context.
func Load(fs di.FS, path string) (*Config, error) {
	data, err := fs.ReadFile(path)
	if err != nil {
		// Preserve both ErrNotFound (callers ask for it) and the underlying
		// os.ErrNotExist (so errors.Is(err, os.ErrNotExist) still works).
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config load %s: %w", path, errors.Join(ErrNotFound, err))
		}
		return nil, fmt.Errorf("config load %s: %w", path, err)
	}
	cfg, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("config load %s: %w", path, err)
	}
	normalise(cfg)
	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("config load %s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks the schema-level invariants that don't depend on file I/O.
// Returns a joined error covering every violation so users see all problems
// from a single run rather than fixing them one at a time.
func Validate(c *Config) error {
	var errs []error
	if c.Version != currentVersion {
		errs = append(errs, fmt.Errorf("%w (got %d, want %d)", ErrInvalidVersion, c.Version, currentVersion))
	}
	errs = append(errs, validateVars(c.vars)...)
	errs = append(errs, validatePaths("template", c.Template)...)
	errs = append(errs, validatePaths("copy", c.Copy)...)
	errs = append(errs, validatePaths("symlink", c.Symlink)...)
	errs = append(errs, validateHooks(c.Hooks)...)
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func validateVars(vs []Var) []error {
	var errs []error
	for _, v := range vs {
		if !varNameRe.MatchString(v.name) {
			errs = append(errs, fmt.Errorf("var %q: name must match %s", v.name, varNameRe))
		}
		// Mutually exclusive by PLAN §4.2 — `value` is the alternative to sources.
		if v.value != "" && v.hasSources {
			errs = append(errs, fmt.Errorf("var %q: sources and value are mutually exclusive", v.name))
		}
	}
	return errs
}

func validatePaths(kind string, ps []PathSpec) []error {
	var errs []error
	for i, p := range ps {
		if p.From == "" {
			errs = append(errs, fmt.Errorf("%s[%d]: from is required", kind, i))
		}
	}
	return errs
}

func validateHooks(hooks map[string]HookStage) []error {
	var errs []error
	for cmd, hs := range hooks {
		for i, h := range hs.Pre {
			if err := validateHook(h); err != nil {
				errs = append(errs, fmt.Errorf("hooks.%s.pre[%d]: %w", cmd, i, err))
			}
		}
		for i, h := range hs.Post {
			if err := validateHook(h); err != nil {
				errs = append(errs, fmt.Errorf("hooks.%s.post[%d]: %w", cmd, i, err))
			}
		}
	}
	return errs
}

func validateHook(h Hook) error {
	if h.LoadEnv != "" && (h.Run != "" || h.When != "") {
		return errors.New("load_env cannot be combined with run or when")
	}
	if h.LoadEnv == "" && h.Run == "" {
		return errors.New("hook must have either run or load_env")
	}
	return nil
}

// --- parsing ---------------------------------------------------------------

type rawSource struct {
	CLI      string `yaml:"cli"`
	Env      string `yaml:"env"`
	Prompt   string `yaml:"prompt"`
	Validate string `yaml:"validate"`
}

func parse(data []byte) (*Config, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		// Empty document — leaves every field at its zero value; Validate will
		// then surface ErrInvalidVersion.
		return &Config{}, nil
	}
	if root.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("expected top-level document, got %s", kindName(root.Kind))
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected top-level mapping, got %s", kindName(top.Kind))
	}

	cfg := &Config{}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if err := applyKey(cfg, top.Content[i].Value, top.Content[i+1]); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// applyKey dispatches one top-level key to its handler. Unknown keys are
// silently ignored — strict mode would reject `# comment with typo` style
// configs that round-tripped through editors.
func applyKey(cfg *Config, key string, valNode *yaml.Node) error {
	switch key {
	case "version":
		if err := valNode.Decode(&cfg.Version); err != nil {
			return fmt.Errorf("version: %w", err)
		}
	case "vars":
		return parseVars(cfg, valNode)
	case "template":
		if err := valNode.Decode(&cfg.Template); err != nil {
			return fmt.Errorf("template: %w", err)
		}
	case "copy":
		if err := valNode.Decode(&cfg.Copy); err != nil {
			return fmt.Errorf("copy: %w", err)
		}
	case "symlink":
		if err := valNode.Decode(&cfg.Symlink); err != nil {
			return fmt.Errorf("symlink: %w", err)
		}
	case "hooks":
		if valNode.Tag == "!!null" {
			return nil
		}
		if err := valNode.Decode(&cfg.Hooks); err != nil {
			return fmt.Errorf("hooks: %w", err)
		}
	}
	return nil
}

func parseVars(cfg *Config, varsNode *yaml.Node) error {
	if varsNode.Tag == "!!null" {
		return nil
	}
	if varsNode.Kind != yaml.MappingNode {
		return fmt.Errorf("vars: expected mapping, got %s", kindName(varsNode.Kind))
	}
	for i := 0; i+1 < len(varsNode.Content); i += 2 {
		keyNode := varsNode.Content[i]
		valNode := varsNode.Content[i+1]
		v, err := parseVar(keyNode.Value, valNode)
		if err != nil {
			return fmt.Errorf("var %q: %w", keyNode.Value, err)
		}
		cfg.vars = append(cfg.vars, v)
	}
	return nil
}

func parseVar(name string, valNode *yaml.Node) (Var, error) {
	v := Var{name: name}
	if valNode.Tag == "!!null" {
		return v, nil
	}
	if valNode.Kind != yaml.MappingNode {
		return Var{}, fmt.Errorf("expected mapping, got %s", kindName(valNode.Kind))
	}
	for i := 0; i+1 < len(valNode.Content); i += 2 {
		if err := applyVarField(&v, valNode.Content[i].Value, valNode.Content[i+1]); err != nil {
			return Var{}, err
		}
	}
	return v, nil
}

func applyVarField(v *Var, key string, fieldNode *yaml.Node) error {
	switch key {
	case "sources":
		return parseVarSources(v, fieldNode)
	case "value":
		v.value = fieldNode.Value
	case "default":
		return parseVarDefault(v, fieldNode)
	}
	return nil
}

func parseVarSources(v *Var, fieldNode *yaml.Node) error {
	if fieldNode.Tag == "!!null" {
		return nil
	}
	if fieldNode.Kind != yaml.SequenceNode {
		return fmt.Errorf("sources: expected sequence, got %s", kindName(fieldNode.Kind))
	}
	v.hasSources = true
	for _, sn := range fieldNode.Content {
		var rs rawSource
		if err := sn.Decode(&rs); err != nil {
			return fmt.Errorf("source: %w", err)
		}
		ss, err := buildSource(rs)
		if err != nil {
			return err
		}
		v.sources = append(v.sources, ss)
	}
	return nil
}

func parseVarDefault(v *Var, fieldNode *yaml.Node) error {
	if fieldNode.Tag == "!!null" {
		return nil
	}
	v.hasDefault = true
	var d any
	if err := fieldNode.Decode(&d); err != nil {
		return fmt.Errorf("default: %w", err)
	}
	v.defaultVal = d
	return nil
}

func buildSource(rs rawSource) (vars.SourceSpec, error) {
	set := 0
	if rs.CLI != "" {
		set++
	}
	if rs.Env != "" {
		set++
	}
	if rs.Prompt != "" {
		set++
	}
	if set != 1 {
		return vars.SourceSpec{}, errors.New("source: exactly one of cli, env, or prompt must be set")
	}
	switch {
	case rs.CLI != "":
		return vars.SourceSpec{Kind: vars.SourceCLI, CLI: rs.CLI}, nil
	case rs.Env != "":
		return vars.SourceSpec{Kind: vars.SourceEnv, Env: rs.Env}, nil
	default:
		return vars.SourceSpec{Kind: vars.SourcePrompt, Prompt: rs.Prompt, Validate: rs.Validate}, nil
	}
}

// normalise fills in the implicit defaults: every `to:` that the user omitted
// becomes a copy of `from:` (PLAN §12).
func normalise(c *Config) {
	normalisePaths(c.Template)
	normalisePaths(c.Copy)
	normalisePaths(c.Symlink)
}

func normalisePaths(ps []PathSpec) {
	for i := range ps {
		if ps[i].To == "" {
			ps[i].To = ps[i].From
		}
	}
}

func kindName(k yaml.Kind) string {
	switch k {
	case yaml.DocumentNode:
		return "document"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.MappingNode:
		return "mapping"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	}
	return "unknown"
}
