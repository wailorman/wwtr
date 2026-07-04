package cmd

import (
	"context"
	"testing"

	"github.com/spf13/cobra"

	"github.com/wailorman/wwtr/internal/di/fakes"
)

// TestFindConfigArg covers all three argv forms.
func TestFindConfigArg(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"--config <path>", []string{"wwtr", "--config", "/x/.wwtr.yml", "init"}, "/x/.wwtr.yml"},
		{"--config=<path>", []string{"wwtr", "--config=/y/.wwtr.yaml", "info"}, "/y/.wwtr.yaml"},
		{"missing", []string{"wwtr", "init"}, ""},
		{"--config at end without value", []string{"wwtr", "init", "--config"}, ""},
		{"--config= empty", []string{"wwtr", "--config=", "init"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := findConfigArg(tc.args); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestDiscoverConfigPathEarlyDeps exercises the three discovery paths:
// explicit --config that exists, auto-discovery via git, both missing.
func TestDiscoverConfigPathEarlyDeps(t *testing.T) {
	t.Parallel()

	t.Run("explicit --config that exists", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/explicit/.wwtr.yml", []byte("version: 1\n"), 0o644)
		git := &fakes.FakeGit{MainErr: context.DeadlineExceeded} // git must NOT be called
		got, ok := discoverConfigPathEarlyDeps(fs, git, []string{"wwtr", "--config", "/explicit/.wwtr.yml", "init"})
		if !ok || got != "/explicit/.wwtr.yml" {
			t.Fatalf("got (%q,%v), want (/explicit/.wwtr.yml,true)", got, ok)
		}
		if git.MainCalls != 0 {
			t.Errorf("git should not be consulted when --config is given")
		}
	})

	t.Run("explicit --config that does NOT exist → fall through to auto-discovery", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/main/.wwtr.yml", []byte("version: 1\n"), 0o644)
		git := &fakes.FakeGit{MainVal: "/main"}
		got, ok := discoverConfigPathEarlyDeps(fs, git, []string{"wwtr", "--config", "/missing/path", "init"})
		if !ok || got != "/main/.wwtr.yml" {
			t.Fatalf("got (%q,%v), want auto-discovered /main/.wwtr.yml", got, ok)
		}
	})

	t.Run("auto-discovery via git main + .wwtr.yml", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/repo/.wwtr.yml", []byte("version: 1\n"), 0o644)
		git := &fakes.FakeGit{MainVal: "/repo"}
		got, ok := discoverConfigPathEarlyDeps(fs, git, []string{"wwtr", "init"})
		if !ok || got != "/repo/.wwtr.yml" {
			t.Fatalf("got (%q,%v)", got, ok)
		}
	})

	t.Run("auto-discovery via .wwtr.yaml fallback", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/repo/.wwtr.yaml", []byte("version: 1\n"), 0o644)
		git := &fakes.FakeGit{MainVal: "/repo"}
		got, ok := discoverConfigPathEarlyDeps(fs, git, []string{"wwtr", "init"})
		if !ok || got != "/repo/.wwtr.yaml" {
			t.Fatalf("got (%q,%v)", got, ok)
		}
	})

	t.Run("git error → false", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		git := &fakes.FakeGit{MainErr: context.Canceled}
		_, ok := discoverConfigPathEarlyDeps(fs, git, []string{"wwtr", "init"})
		if ok {
			t.Fatal("want ok=false when git fails")
		}
	})

	t.Run("no config anywhere → false", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		git := &fakes.FakeGit{MainVal: "/repo"}
		_, ok := discoverConfigPathEarlyDeps(fs, git, []string{"wwtr", "init"})
		if ok {
			t.Fatal("want ok=false when no config exists")
		}
	})

	t.Run("empty main path → false", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		git := &fakes.FakeGit{MainVal: ""}
		_, ok := discoverConfigPathEarlyDeps(fs, git, []string{"wwtr", "init"})
		if ok {
			t.Fatal("want ok=false when git returns empty main")
		}
	})
}

// TestRegisterCLISourceFlagsDeps verifies that vars.<name>.sources[].cli flags
// are added to root as persistent string flags.
func TestRegisterCLISourceFlagsDeps(t *testing.T) {
	t.Parallel()

	t.Run("registers all cli-sources", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/main/.wwtr.yml", []byte(`version: 1
vars:
  base_port:
    sources:
      - cli: "--base-port"
      - env: BASE_PORT
    default: 3000
  mode:
    sources:
      - cli: "--mode"
  no_cli:
    sources:
      - env: NO_CLI
`), 0o644)
		git := &fakes.FakeGit{MainVal: "/main"}
		root := &cobra.Command{Use: "wwtr"}
		newRootFlags(root) // register built-ins
		registerCLISourceFlagsDeps(root, fs, git, []string{"wwtr", "init"})

		for _, name := range []string{"base-port", "mode"} {
			if root.PersistentFlags().Lookup(name) == nil {
				t.Errorf("flag --%s not registered", name)
			}
		}
		if root.PersistentFlags().Lookup("no_cli") != nil {
			t.Error("env-only var should not register a flag")
		}
	})

	t.Run("does not duplicate on second call", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/main/.wwtr.yml", []byte("version: 1\nvars:\n  x:\n    sources:\n      - cli: --x-flag\n"), 0o644)
		git := &fakes.FakeGit{MainVal: "/main"}
		root := &cobra.Command{Use: "wwtr"}
		newRootFlags(root)
		registerCLISourceFlagsDeps(root, fs, git, nil)
		registerCLISourceFlagsDeps(root, fs, git, nil) // idempotent
		// Lookup by name succeeds; the second call must not panic on duplicate.
		if root.PersistentFlags().Lookup("x-flag") == nil {
			t.Fatal("flag not registered")
		}
	})

	t.Run("no config discoverable → silent no-op", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		git := &fakes.FakeGit{MainErr: context.Canceled}
		root := &cobra.Command{Use: "wwtr"}
		newRootFlags(root)
		registerCLISourceFlagsDeps(root, fs, git, nil)
		// Nothing should be added beyond built-ins.
		if root.PersistentFlags().Lookup("anything") != nil {
			t.Fatal("unexpected flag registered")
		}
	})

	t.Run("config parse error → silent no-op", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/main/.wwtr.yml", []byte("this: : is:not: valid: yaml"), 0o644)
		git := &fakes.FakeGit{MainVal: "/main"}
		root := &cobra.Command{Use: "wwtr"}
		newRootFlags(root)
		// Must not panic; must not register anything.
		registerCLISourceFlagsDeps(root, fs, git, nil)
	})

	t.Run("skips cli sources that collide with builtin names", func(t *testing.T) {
		t.Parallel()
		fs := fakes.NewFakeFS()
		_ = fs.WriteFile("/main/.wwtr.yml", []byte(`version: 1
vars:
  cfg:
    sources:
      - cli: "--config"
`), 0o644)
		git := &fakes.FakeGit{MainVal: "/main"}
		root := &cobra.Command{Use: "wwtr"}
		newRootFlags(root)
		registerCLISourceFlagsDeps(root, fs, git, nil)
		// The built-in --config must remain a StringVar with the original default.
		f := root.PersistentFlags().Lookup("config")
		if f == nil {
			t.Fatal("--config not registered")
		}
		// Original newRootFlags sets usage "path to .wwtr.yml..."; the dynamic
		// registration must NOT have overwritten it.
		const wantUsage = "path to .wwtr.yml (bypass auto-discovery)"
		if f.Usage != wantUsage {
			t.Errorf("config flag usage = %q, want %q (dynamic reg should skip builtins)", f.Usage, wantUsage)
		}
	})
}

// TestCollectCLIVars covers Changed-vs-unset and builtin-skip behaviour.
func TestCollectCLIVars(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "wwtr"}
	newRootFlags(root)
	root.PersistentFlags().String("base-port", "", "dynamic")
	root.PersistentFlags().String("mode", "", "dynamic")

	// Set dynamic flags + one builtin.
	if err := root.PersistentFlags().Set("base-port", "4017"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("mode", "dev"); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("config", "/x/.wwtr.yml"); err != nil {
		t.Fatal(err)
	}
	// leave force unset

	got := collectCLIVars(root)
	if got["--base-port"] != "4017" {
		t.Errorf("base-port = %q", got["--base-port"])
	}
	if got["--mode"] != "dev" {
		t.Errorf("mode = %q", got["--mode"])
	}
	if _, ok := got["--config"]; ok {
		t.Error("builtin --config must be excluded")
	}
	if _, ok := got["--force"]; ok {
		t.Error("unset --force must be excluded")
	}
}
