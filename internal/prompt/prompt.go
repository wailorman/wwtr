// Package prompt implements [github.com/wailorman/wwtr/internal/di.Prompter]
// using charm.land/huh/v2. Interactive prompts short-circuit to a default
// when the supplied [di.TTYChecker] reports a non-interactive session, so the
// same code path serves a real terminal and CI.
package prompt

import (
	"errors"
	"fmt"
	"io"
	"regexp"

	"charm.land/huh/v2"

	"github.com/wailorman/wwtr/internal/di"
)

// ErrNotInteractive is returned by Input and Conflict when the session is not
// a TTY. The vars resolver treats it as "skip prompt, try next source"; the
// files package treats it as an abort.
var ErrNotInteractive = errors.New("prompt: not interactive")

// Internal option keys for the conflict menu. mapChoice translates a key into
// a di.Decision; the huh option labels are what the user actually sees.
const (
	choiceYes  = "yes"
	choiceNo   = "no"
	choiceAll  = "all"
	choiceQuit = "quit"
	choiceDiff = "diff"
)

// HuhPrompter is a [di.Prompter] backed by charm.land/huh/v2. The run field
// executes a constructed form; production callers use New (which wires the
// default runner), tests override it to drive huh's accessible mode.
type HuhPrompter struct {
	tty di.TTYChecker
	out io.Writer
	run func(*huh.Form) error
}

// New constructs a HuhPrompter that writes conflict-diff output to out.
func New(tty di.TTYChecker, out io.Writer) *HuhPrompter {
	return &HuhPrompter{tty: tty, out: out, run: defaultRun}
}

// Compile-time assertion that HuhPrompter satisfies di.Prompter.
var _ di.Prompter = (*HuhPrompter)(nil)

// Confirm asks a yes/no question. defaultYes controls what [Enter] picks.
// In non-interactive mode it returns defaultYes without prompting.
// Esc/Ctrl+C (huh.ErrUserAborted) is treated as user-denied: returns false, nil.
func (p *HuhPrompter) Confirm(message string, defaultYes bool) (bool, error) {
	if !p.tty.IsInteractive() {
		return defaultYes, nil
	}
	v := defaultYes
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(message).Value(&v),
	))
	if err := p.run(form); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, fmt.Errorf("prompt: confirm: %w", err)
	}
	return v, nil
}

// Input asks for a free-form string. validateRegex is optional ("" disables
// validation); when set the input is retried (by huh) until it matches.
// In non-interactive mode it returns ErrNotInteractive.
func (p *HuhPrompter) Input(message, defaultVal, validateRegex string) (string, error) {
	if !p.tty.IsInteractive() {
		return "", ErrNotInteractive
	}
	v := defaultVal
	field := huh.NewInput().Title(message).Value(&v)
	if validateRegex != "" {
		field = field.Validate(func(s string) error { return matchRegex(s, validateRegex) })
	}
	form := huh.NewForm(huh.NewGroup(field))
	if err := p.run(form); err != nil {
		return "", fmt.Errorf("prompt: input: %w", err)
	}
	return v, nil
}

// Conflict asks how to proceed when a file operation would overwrite an
// existing file. The Thor-style menu offers Yes / No / All / Quit / Diff
// (PLAN §10). When the user picks Diff, diffFn's output is written to the
// constructor-supplied writer and the prompt repeats. In non-interactive mode
// it returns ErrNotInteractive (callers treat it as an abort).
func (p *HuhPrompter) Conflict(path string, diffFn func() (string, error)) (di.Decision, error) {
	if !p.tty.IsInteractive() {
		return di.DecisionUnknown, ErrNotInteractive
	}
	for {
		choice, err := p.askConflict(path)
		if err != nil {
			return di.DecisionUnknown, err
		}
		decision := mapChoice(choice)
		if !shouldReprompt(decision) {
			return decision, nil
		}
		diff, derr := diffFn()
		if derr != nil {
			return di.DecisionUnknown, fmt.Errorf("prompt: conflict diff %s: %w", path, derr)
		}
		if _, werr := io.WriteString(p.out, diff); werr != nil {
			return di.DecisionUnknown, fmt.Errorf("prompt: write conflict diff: %w", werr)
		}
	}
}

func (p *HuhPrompter) askConflict(path string) (string, error) {
	var choice string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("Overwrite %s?", path)).
			Value(&choice).
			Options(
				huh.NewOption("Yes — overwrite this file", choiceYes),
				huh.NewOption("No — skip this file", choiceNo),
				huh.NewOption("All — overwrite this and the rest", choiceAll),
				huh.NewOption("Quit — abort the command", choiceQuit),
				huh.NewOption("Diff — show the difference and ask again", choiceDiff),
			),
	))
	if err := p.run(form); err != nil {
		return "", fmt.Errorf("prompt: conflict: %w", err)
	}
	return choice, nil
}

// defaultRun is the production form-runner. Tests inject a substitute via the
// run field to drive huh's accessible mode (real TTY not required).
func defaultRun(f *huh.Form) error { return f.Run() }

// matchRegex compiles pattern and reports whether value matches it. A pattern
// that fails to compile is itself an error so the user sees a fixable message
// rather than a silent skip.
func matchRegex(value, pattern string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("prompt: invalid regex %q: %w", pattern, err)
	}
	if !re.MatchString(value) {
		return fmt.Errorf("prompt: %q does not match %q", value, pattern)
	}
	return nil
}

// mapChoice converts the option key the user picked in the conflict menu into
// a di.Decision. Unknown keys map to DecisionUnknown rather than a destructive
// default.
func mapChoice(choice string) di.Decision {
	switch choice {
	case choiceYes:
		return di.DecisionYes
	case choiceNo:
		return di.DecisionNo
	case choiceAll:
		return di.DecisionAll
	case choiceQuit:
		return di.DecisionQuit
	case choiceDiff:
		return di.DecisionDiff
	default:
		return di.DecisionUnknown
	}
}

// shouldReprompt reports whether the conflict loop should ask again. Only
// DecisionDiff (the user just saw the diff) continues; terminal choices end
// the loop.
func shouldReprompt(d di.Decision) bool {
	return d == di.DecisionDiff
}
