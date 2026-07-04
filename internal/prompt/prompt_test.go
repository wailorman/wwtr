package prompt

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/wailorman/wwtr/internal/di"
)

type fakeTTY struct{ interactive bool }

func (f fakeTTY) IsInteractive() bool { return f.interactive }

type errWriter struct{ err error }

func (w *errWriter) Write(p []byte) (int, error) { return 0, w.err }

// accessibleRun drives huh forms in accessible mode. Each entry of inputs is
// fed to one form.Run() invocation, so a conflict loop that re-prompts after
// Diff gets one entry per iteration. Within an entry, multiple lines let
// huh's internal validation loop retry.
func accessibleRun(inputs []string, out *bytes.Buffer) func(*huh.Form) error {
	var idx int
	return func(f *huh.Form) error {
		var in string
		if idx < len(inputs) {
			in = inputs[idx]
			idx++
		}
		return f.WithAccessible(true).WithOutput(out).WithInput(strings.NewReader(in)).Run()
	}
}

func TestNew_SetsDefaults(t *testing.T) {
	t.Parallel()
	p := New(fakeTTY{interactive: false}, io.Discard)
	if p.run == nil {
		t.Fatal("New left run nil")
	}
	if p.tty == nil {
		t.Fatal("New left tty nil")
	}
}

func TestDefaultRun_EmptyForm(t *testing.T) {
	t.Parallel()
	if err := defaultRun(huh.NewForm()); err != nil {
		t.Errorf("defaultRun(empty) = %v, want nil", err)
	}
}

func TestConfirm_NonInteractive(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		defYes bool
		want   bool
	}{
		{"default yes returns true", true, true},
		{"default no returns false", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := New(fakeTTY{interactive: false}, io.Discard)
			got, err := p.Confirm("ok?", tc.defYes)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("Confirm = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfirm_Interactive(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		defYes bool
		input  string
		want   bool
	}{
		{"default yes + empty input keeps yes", true, "\n", true},
		{"default no + empty input keeps no", false, "\n", false},
		{"default no + yes flips to yes", false, "y\n", true},
		{"default yes + no flips to no", true, "n\n", false},
		{"yes word accepted", true, "yes\n", true},
		{"no word accepted", false, "no\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			p := New(fakeTTY{interactive: true}, io.Discard)
			p.run = accessibleRun([]string{tc.input}, &out)
			got, err := p.Confirm("ok?", tc.defYes)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("Confirm(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestConfirm_AbortIsDenied(t *testing.T) {
	t.Parallel()
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = func(*huh.Form) error { return huh.ErrUserAborted }
	got, err := p.Confirm("ok?", true)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got {
		t.Errorf("Confirm on abort = true, want false (user-denied)")
	}
}

func TestConfirm_OtherErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = func(*huh.Form) error { return sentinel }
	_, err := p.Confirm("ok?", true)
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want wrap of %v", err, sentinel)
	}
}

func TestInput_NonInteractive(t *testing.T) {
	t.Parallel()
	p := New(fakeTTY{interactive: false}, io.Discard)
	_, err := p.Input("name?", "", "")
	if !errors.Is(err, ErrNotInteractive) {
		t.Errorf("err = %v, want ErrNotInteractive", err)
	}
}

func TestInput_InteractiveValue(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = accessibleRun([]string{"hello\n"}, &out)
	got, err := p.Input("name?", "default", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "hello" {
		t.Errorf("Input = %q, want %q", got, "hello")
	}
}

func TestInput_EmptyFallsBackToDefault(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = accessibleRun([]string{"\n"}, &out)
	got, err := p.Input("name?", "default", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "default" {
		t.Errorf("Input = %q, want default %q", got, "default")
	}
}

func TestInput_ValidationAcceptsFirstTry(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = accessibleRun([]string{"3010\n"}, &out)
	got, err := p.Input("port?", "", `\d+`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "3010" {
		t.Errorf("Input = %q, want %q", got, "3010")
	}
}

func TestInput_ValidationRetriesUntilGood(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = accessibleRun([]string{"abc\nx\n3010\n"}, &out)
	got, err := p.Input("port?", "", `\d+`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "3010" {
		t.Errorf("Input = %q, want %q after retries", got, "3010")
	}
}

func TestInput_RunErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = func(*huh.Form) error { return sentinel }
	_, err := p.Input("name?", "", "")
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want wrap of %v", err, sentinel)
	}
}

func TestConflict_NonInteractive(t *testing.T) {
	t.Parallel()
	p := New(fakeTTY{interactive: false}, io.Discard)
	_, err := p.Conflict("/x", func() (string, error) {
		t.Fatal("diffFn must not run in non-interactive mode")
		return "", nil
	})
	if !errors.Is(err, ErrNotInteractive) {
		t.Errorf("err = %v, want ErrNotInteractive", err)
	}
}

func TestConflict_TerminalChoices(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		want  di.Decision
	}{
		{"yes is option 1", "1\n", di.DecisionYes},
		{"no is option 2", "2\n", di.DecisionNo},
		{"all is option 3", "3\n", di.DecisionAll},
		{"quit is option 4", "4\n", di.DecisionQuit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			p := New(fakeTTY{interactive: true}, io.Discard)
			p.run = accessibleRun([]string{tc.input}, &out)
			got, err := p.Conflict("/path/to/file", func() (string, error) {
				t.Fatal("diffFn must not run for terminal choices")
				return "", nil
			})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("Conflict(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestConflict_DiffThenYes(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	diffOut := &bytes.Buffer{}
	p := New(fakeTTY{interactive: true}, diffOut)
	p.run = accessibleRun([]string{"5\n", "1\n"}, &out)
	calls := 0
	diffFn := func() (string, error) {
		calls++
		return "@@ diff @@\n", nil
	}
	got, err := p.Conflict("/p", diffFn)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != di.DecisionYes {
		t.Errorf("Conflict = %v, want DecisionYes", got)
	}
	if calls != 1 {
		t.Errorf("diffFn calls = %d, want 1", calls)
	}
	if diffOut.String() != "@@ diff @@\n" {
		t.Errorf("diff output = %q, want %q", diffOut.String(), "@@ diff @@\n")
	}
}

func TestConflict_DiffWriteError(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	sentinel := errors.New("write boom")
	p := New(fakeTTY{interactive: true}, &errWriter{err: sentinel})
	p.run = accessibleRun([]string{"5\n"}, &out)
	_, err := p.Conflict("/p", func() (string, error) { return "diff", nil })
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want wrap of %v", err, sentinel)
	}
}

func TestConflict_DiffFnError(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	sentinel := errors.New("diff boom")
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = accessibleRun([]string{"5\n"}, &out)
	_, err := p.Conflict("/p", func() (string, error) { return "", sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want wrap of %v", err, sentinel)
	}
}

func TestConflict_RunErrorWrapped(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("boom")
	p := New(fakeTTY{interactive: true}, io.Discard)
	p.run = func(*huh.Form) error { return sentinel }
	_, err := p.Conflict("/p", func() (string, error) { return "", nil })
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want wrap of %v", err, sentinel)
	}
}

func TestMatchRegex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		value   string
		pattern string
		wantErr bool
	}{
		{"match digits", "3010", `\d+`, false},
		{"mismatch", "abc", `\d+`, true},
		{"empty value no match", "", `\d+`, true},
		{"empty pattern matches all", "anything", ``, false},
		{"invalid pattern", "x", `(`, true},
		{"anchored full match", "3010", `^\d+$`, false},
		{"anchored rejects partial", "3010abc", `^\d+$`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := matchRegex(tc.value, tc.pattern)
			if tc.wantErr && err == nil {
				t.Errorf("matchRegex(%q,%q): want error, got nil", tc.value, tc.pattern)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("matchRegex(%q,%q): want nil, got %v", tc.value, tc.pattern, err)
			}
		})
	}
}

func TestMatchRegex_ErrorMentionsPattern(t *testing.T) {
	t.Parallel()
	err := matchRegex("foo", `\d+`)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), `\d+`) {
		t.Errorf("err = %v, want it to mention the pattern %q", err, `\d+`)
	}
}

func TestMapChoice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want di.Decision
	}{
		{choiceYes, di.DecisionYes},
		{choiceNo, di.DecisionNo},
		{choiceAll, di.DecisionAll},
		{choiceQuit, di.DecisionQuit},
		{choiceDiff, di.DecisionDiff},
		{"garbage", di.DecisionUnknown},
		{"", di.DecisionUnknown},
	} {
		if got := mapChoice(tc.in); got != tc.want {
			t.Errorf("mapChoice(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestShouldReprompt(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		d    di.Decision
		want bool
	}{
		{di.DecisionDiff, true},
		{di.DecisionYes, false},
		{di.DecisionNo, false},
		{di.DecisionAll, false},
		{di.DecisionQuit, false},
		{di.DecisionUnknown, false},
	} {
		if got := shouldReprompt(tc.d); got != tc.want {
			t.Errorf("shouldReprompt(%v) = %v, want %v", tc.d, got, tc.want)
		}
	}
}
