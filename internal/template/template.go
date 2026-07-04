// Package template renders the `template:` files and `vars.value:` expressions
// defined in PLAN §7. It wraps Go's text/template with Masterminds/sprig/v3
// and the strict `missingkey=error` option, so referencing an unknown field
// or variable is a hard error rather than silently empty output.
package template

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/wailorman/wwtr/internal/vars"
)

// ErrParse is the sentinel wrapping text/template parse failures. Use
// errors.Is to distinguish a broken template from a data-driven execute error.
var ErrParse = errors.New("template: parse error")

// ErrExecute is the sentinel wrapping text/template execution failures
// (missing field, missing var, wrong type). errors.Is(err, ErrExecute)
// distinguishes them from parse errors so callers can surface the right exit
// code (PLAN §20: exit 4 for unresolved var).
var ErrExecute = errors.New("template: execute error")

// Data is the context passed to every template. The embedded
// [vars.BuiltinVars] exposes Branch/Slug/Hash/... at the top level
// (`.Branch`, `.Slug`, ...). User-resolved variables are looked up under
// `.Vars.<name>`.
type Data struct {
	vars.BuiltinVars
	Vars map[string]string
}

// NewEngine returns a text/template instance preloaded with all Masterminds/
// sprig/v3 functions and the strict missingkey=error option. The name is used
// in error messages; pass the file path (or "" when irrelevant).
//
// Callers compose it via Parse + Execute against a [Data] value. The
// [Render] / [RenderTo] helpers do exactly that for the common one-shot case.
func NewEngine(name string) *template.Template {
	return template.New(name).Funcs(sprig.FuncMap()).Option("missingkey=error")
}

// Render parses tmpl and executes it against data, returning the rendered
// bytes. Parse failures are wrapped with [ErrParse]; execution failures are
// wrapped with [ErrExecute]. Both carry the template name; the underlying
// text/template error already carries the offending line/column.
func Render(name, tmpl string, data Data) ([]byte, error) {
	var buf bytes.Buffer
	if err := RenderTo(name, tmpl, data, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// RenderTo is [Render] streaming to w. Same error contract.
func RenderTo(name, tmpl string, data Data, w io.Writer) error {
	t, err := NewEngine(name).Parse(tmpl)
	if err != nil {
		return fmt.Errorf("%w: %q: %w", ErrParse, name, err)
	}
	if err := t.Execute(w, data); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrExecute, name, err)
	}
	return nil
}
