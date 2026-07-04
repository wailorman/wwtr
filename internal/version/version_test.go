package version

import (
	"strings"
	"testing"
)

// Package version mutates module-level vars. These tests must NOT run in
// parallel with each other (they would race on the globals), so none of them
// call t.Parallel(). Each test restores the previous values via t.Cleanup.

func TestVersion_SetAndGet(t *testing.T) {
	prevV, prevC, prevD := version, commit, date
	t.Cleanup(func() { version, commit, date = prevV, prevC, prevD })

	Set("0.1.0", "abc1234", "2026-07-04T12:00:00Z")
	if got := Version(); got != "0.1.0" {
		t.Fatalf("Version=%q want 0.1.0", got)
	}
	if got := Commit(); got != "abc1234" {
		t.Fatalf("Commit=%q want abc1234", got)
	}
	if got := Date(); got != "2026-07-04T12:00:00Z" {
		t.Fatalf("Date=%q want 2026-07-04T12:00:00Z", got)
	}
}

func TestVersion_Defaults(t *testing.T) {
	prevV, prevC, prevD := version, commit, date
	t.Cleanup(func() { version, commit, date = prevV, prevC, prevD })
	version, commit, date = "dev", "none", "unknown"
	if Version() != "dev" || Commit() != "none" || Date() != "unknown" {
		t.Fatal("defaults wrong")
	}
}

func TestLong_IncludesAllFields(t *testing.T) {
	prevV, prevC, prevD := version, commit, date
	t.Cleanup(func() { version, commit, date = prevV, prevC, prevD })
	Set("9.9.9", "deadbeef", "2026-01-01T00:00:00Z")
	long := Long()
	for _, want := range []string{"9.9.9", "deadbeef", "2026-01-01T00:00:00Z", "wwtr version", "commit:", "built:"} {
		if !strings.Contains(long, want) {
			t.Errorf("Long() missing %q; got:\n%s", want, long)
		}
	}
}
