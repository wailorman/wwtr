package template

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/wailorman/wwtr/internal/vars"
)

func sampleBuiltins() vars.BuiltinVars {
	return vars.BuiltinVars{
		Branch:           "feature/JIRA-123_add-login",
		Slug:             "feature-jira-123-add-login",
		Hash:             "a1b2c3d4",
		ShortHash:        "a1b2c3",
		SafeName:         "feature-jira-123-add-login-a1b2c3d4",
		WorktreePath:     "/work/repo-wt",
		WorktreeName:     "repo-wt",
		MainWorktreePath: "/work/repo",
		MainWorktreeName: "repo",
	}
}

func TestData_Embedding(t *testing.T) {
	t.Parallel()
	d := Data{
		BuiltinVars: sampleBuiltins(),
		Vars:        map[string]string{"base_port": "3000"},
	}
	if d.Branch != "feature/JIRA-123_add-login" {
		t.Errorf("embedded Branch not promoted: %q", d.Branch)
	}
	if d.Vars["base_port"] != "3000" {
		t.Errorf("Vars not exposed: %v", d.Vars)
	}
}

func TestRender_Builtins(t *testing.T) {
	t.Parallel()
	b := sampleBuiltins()
	cases := []struct {
		field string
		tmpl  string
		want  string
	}{
		{"Branch", `{{ .Branch }}`, b.Branch},
		{"Slug", `{{ .Slug }}`, b.Slug},
		{"Hash", `{{ .Hash }}`, b.Hash},
		{"ShortHash", `{{ .ShortHash }}`, b.ShortHash},
		{"SafeName", `{{ .SafeName }}`, b.SafeName},
		{"WorktreePath", `{{ .WorktreePath }}`, b.WorktreePath},
		{"WorktreeName", `{{ .WorktreeName }}`, b.WorktreeName},
		{"MainWorktreePath", `{{ .MainWorktreePath }}`, b.MainWorktreePath},
		{"MainWorktreeName", `{{ .MainWorktreeName }}`, b.MainWorktreeName},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			got, err := Render("test", tc.tmpl, Data{BuiltinVars: b})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRender_Vars(t *testing.T) {
	t.Parallel()
	data := Data{
		BuiltinVars: sampleBuiltins(),
		Vars: map[string]string{
			"base_port":      "3000",
			"db_prefix":      "wk_spectator_x",
			"mail_container": "wk_mail",
		},
	}
	got, err := Render("test", `{{ .Vars.base_port }}/{{ .Vars.db_prefix }}/{{ .Vars.mail_container }}`, data)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := "3000/wk_spectator_x/wk_mail"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_MissingVar_Errors(t *testing.T) {
	t.Parallel()
	_, err := Render("test", `{{ .Vars.missing }}`, Data{Vars: map[string]string{}})
	if err == nil {
		t.Fatal("expected execute error, got nil")
	}
	if !errors.Is(err, ErrExecute) {
		t.Errorf("want ErrExecute, got %v", err)
	}
	if errors.Is(err, ErrParse) {
		t.Errorf("execute error must not also be ErrParse: %v", err)
	}
	if !strings.Contains(err.Error(), "test") {
		t.Errorf("error must carry template name: %v", err)
	}
}

func TestRender_MissingField_Errors(t *testing.T) {
	t.Parallel()
	_, err := Render("test", `{{ .NoSuchField }}`, Data{BuiltinVars: sampleBuiltins()})
	if err == nil {
		t.Fatal("expected execute error, got nil")
	}
	if !errors.Is(err, ErrExecute) {
		t.Errorf("want ErrExecute, got %v", err)
	}
}

func TestRender_ParseError(t *testing.T) {
	t.Parallel()
	_, err := Render("broken.tmpl", `{{ .Branch `, Data{})
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !errors.Is(err, ErrParse) {
		t.Errorf("want ErrParse, got %v", err)
	}
	if errors.Is(err, ErrExecute) {
		t.Errorf("parse error must not also be ErrExecute: %v", err)
	}
	if !strings.Contains(err.Error(), "broken.tmpl") {
		t.Errorf("error must carry template name: %v", err)
	}
}

func TestRenderTo_WritesToWriter(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := RenderTo("test", `{{ .Branch | upper }}`, Data{BuiltinVars: sampleBuiltins()}, &buf)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := strings.ToUpper(sampleBuiltins().Branch)
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestRenderTo_ExecuteError(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := RenderTo("test", `{{ .Vars.missing }}`, Data{Vars: map[string]string{}}, &buf)
	if err == nil {
		t.Fatal("expected execute error")
	}
	if !errors.Is(err, ErrExecute) {
		t.Errorf("want ErrExecute, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("buffer should stay empty on execute error, got %q", buf.String())
	}
}

func TestRender_SprigFunctions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tmpl string
		want string
	}{
		{"lower", `{{ "HELLO" | lower }}`, "hello"},
		{"upper", `{{ "hello" | upper }}`, "HELLO"},
		{"replace", `{{ "a-b-c" | replace "-" "_" }}`, "a_b_c"},
		{"regexReplaceAll", `{{ regexReplaceAll "[0-9]+" "abc123def" "X" }}`, "abcXdef"},
		{"trim", `{{ "  hi  " | trim }}`, "hi"},
		{"trunc", `{{ "abcdef" | trunc 3 }}`, "abc"},
		{"sha1sum", `{{ "abc" | sha1sum }}`, "a9993e364706816aba3e25717850c26c9cd0d89d"},
		{"sha256sum", `{{ "abc" | sha256sum }}`, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"},
		{"add", `{{ add 2 3 }}`, "5"},
		{"sub", `{{ sub 10 4 }}`, "6"},
		{"mul", `{{ mul 3 4 }}`, "12"},
		{"default", `{{ "" | default "fallback" }}`, "fallback"},
		{"coalesce", `{{ coalesce "" "" "third" }}`, "third"},
		{"toJson", `{{ list "a" "b" | toJson }}`, `["a","b"]`},
		{"fromJson", `{{ "{\"a\":1}" | fromJson | toJson }}`, `{"a":1}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Render("test", tc.tmpl, Data{})
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRender_EmptyTemplate(t *testing.T) {
	t.Parallel()
	got, err := Render("test", "", Data{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty output, got %q", got)
	}
}

func TestRender_WhitespaceTrim(t *testing.T) {
	t.Parallel()
	b := sampleBuiltins()
	got, err := Render("test", "before\n{{- .Branch -}}\nafter", Data{BuiltinVars: b})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := "before" + b.Branch + "after"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_NestedTemplates(t *testing.T) {
	t.Parallel()
	b := sampleBuiltins()
	tmpl := `{{ define "greeting" }}Hello {{ .Branch }}{{ end }}{{ template "greeting" . }}`
	got, err := Render("test", tmpl, Data{BuiltinVars: b})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	want := "Hello " + b.Branch
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRender_WorktreeEnvTT(t *testing.T) {
	t.Parallel()
	tmpl := `# Per-worktree env (gitignored). Generated by wwtr init.
BASE_PORT={{ .Vars.base_port }}
DATABASE_PREFIX={{ .Vars.db_prefix }}
MAIL_CONTAINER_NAME={{ .Vars.mail_container }}
BRANCH={{ .Branch }}
SAFE_NAME_TRUNC={{ .SafeName | trunc 20 }}`
	b := sampleBuiltins()
	data := Data{
		BuiltinVars: b,
		Vars: map[string]string{
			"base_port":      "3010",
			"db_prefix":      "wk_db",
			"mail_container": "wk_mail",
		},
	}
	got, err := Render(".worktree.env.tt", tmpl, data)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	out := string(got)
	for _, sub := range []string{
		"BASE_PORT=3010",
		"DATABASE_PREFIX=wk_db",
		"MAIL_CONTAINER_NAME=wk_mail",
		"BRANCH=" + b.Branch,
		"SAFE_NAME_TRUNC=feature-jira-123-add",
	} {
		if !strings.Contains(out, sub) {
			t.Errorf("output missing %q\n--- got ---\n%s", sub, out)
		}
	}
}

func TestNewEngine_SprigAndStrictOption(t *testing.T) {
	t.Parallel()
	e := NewEngine("verify")
	if _, err := e.Parse(`{{ "X" | lower }}`); err != nil {
		t.Fatalf("sprig function not available on engine: %v", err)
	}

	e2 := NewEngine("verify2")
	if _, err := e2.Parse(`{{ .Vars.x }}`); err != nil {
		t.Fatalf("parse err: %v", err)
	}
	var buf bytes.Buffer
	if err := e2.Execute(&buf, Data{Vars: map[string]string{}}); err == nil {
		t.Fatal("expected execute error from missingkey=error")
	}
}
