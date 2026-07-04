package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunInfo_HumanReadable(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	bd.Env.Vars["PORT"] = "4040"

	if err := RunInfo(context.Background(), rc); err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	out := bd.Stdout.String()
	for _, want := range []string{"Builtins:", "Branch:", "feature/test", "Vars:", "port:", "4040"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunInfo_Env(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	bd.Env.Vars["PORT"] = "4040"
	rc.Flags.Env = true

	if err := RunInfo(context.Background(), rc); err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	out := bd.Stdout.String()
	for _, want := range []string{
		"export WWTR_BRANCH='feature/test'",
		"export WWTR_WORKTREE_PATH='/worker'",
		"export WWTR_VAR_PORT='4040'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
}

func TestRunInfo_Env_QuotesSpecialChars(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, `version: 1
vars:
  msg:
    default: "it's a 'test'"
`)
	rc.Flags.Env = true

	if err := RunInfo(context.Background(), rc); err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	if !strings.Contains(bd.Stdout.String(), `export WWTR_VAR_MSG='it'\''s a '\''test'\'''`) {
		t.Errorf("env output does not single-quote-escape correctly:\n%s", bd.Stdout.String())
	}
}

func TestRunInfo_JSON(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	bd.Env.Vars["PORT"] = "4040"
	rc.Flags.JSON = true

	if err := RunInfo(context.Background(), rc); err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	var payload struct {
		Builtins map[string]string `json:"builtins"`
		Vars     map[string]string `json:"vars"`
	}
	if err := json.Unmarshal(bd.Stdout.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v; body=%q", err, bd.Stdout.String())
	}
	if payload.Builtins["Branch"] != "feature/test" {
		t.Errorf("builtins.Branch = %q", payload.Builtins["Branch"])
	}
	if payload.Vars["port"] != "4040" {
		t.Errorf("vars.port = %q", payload.Vars["port"])
	}
}

func TestRunInfo_NoVars(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, `version: 1`)

	if err := RunInfo(context.Background(), rc); err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	if !strings.Contains(bd.Stdout.String(), "Builtins:") {
		t.Errorf("output missing Builtins section; got:\n%s", bd.Stdout.String())
	}
	if strings.Contains(bd.Stdout.String(), "Vars:") {
		t.Errorf("Vars section present with no vars; got:\n%s", bd.Stdout.String())
	}
}

func TestRunInfo_JSONWinsOverEnv(t *testing.T) {
	t.Parallel()
	rc, bd := newTestRC(t)
	workerGit(bd.Git)
	seedMainConfig(t, bd.FS, minimalConfig)
	rc.Flags.JSON = true
	rc.Flags.Env = true

	if err := RunInfo(context.Background(), rc); err != nil {
		t.Fatalf("RunInfo: %v", err)
	}
	// JSON should win.
	if !strings.HasPrefix(strings.TrimSpace(bd.Stdout.String()), "{") {
		t.Errorf("expected JSON output, got:\n%s", bd.Stdout.String())
	}
}
