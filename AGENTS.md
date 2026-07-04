# AGENTS.md — guidance for AI agents (Claude, Codex, opencode, …) working on wwtr.

## What this is

`wwtr` is a Go CLI that wraps `git worktree` with declarative YAML config
(`.wwtr.yml`). See `PLAN.md` for the full spec, `internal/di/interfaces.go`
for the dependency-injection surface, and the package layout in `PLAN.md` §15.

## Essential commands

```sh
make build         # → bin/wwtr (with version metadata)
make test          # plain
make test-race     # with -race (CI baseline)
make test-cover    # enforces ≥95% total coverage (fails the build otherwise)
make lint          # golangci-lint v2 (config in .golangci.yml)
make fmt           # gofumpt
make vuln          # govulncheck
make tidy          # go mod tidy
```

Run ALL of `make test-race lint` before considering a task done. `make test-cover`
is the gate for coverage.

## Architecture rules (do not violate)

1. **Side-effects only through `internal/di` interfaces.** Never import `os`,
   `os/exec`, `time.Now`, or read ENV directly from internal/* business code.
   Inject `di.Deps` (or a narrower interface) instead.
2. **`cmd/` is thin.** Parse cobra flags → build `runcontext.RunContext` →
   delegate to `internal/app/<name>.Run(ctx, *RunContext)`. No business logic
   in `cmd/`.
3. **Pure core.** `vars`, `template`, `conditions`, the decision tree in
   `files` — keep them pure functions of their inputs.
4. **`context.Context` everywhere** — pass it from cobra through every
   side-effectful call (ShellRunner.Run, FS ops when long-running).
5. **Two expression engines** (intentional):
   - `vars.value` and `template:` use Go `text/template` + `Masterminds/sprig/v3`.
   - `when:` uses `expr-lang/expr`.
   Don't try to unify them; document the split in user-facing docs.
6. **Tests are first-class.** Every phase ends green on `make test-race` with
   ≥95% coverage on new code. Fakes live in `internal/di/fakes`; integration
   tests for `app/*` use real `git init` in `t.TempDir()`.

## Where things live

| Area | Path |
|---|---|
| Entry point | `main.go` |
| Cobra commands | `cmd/` |
| DI interfaces + OS impl | `internal/di/` |
| DI fakes (tests) | `internal/di/fakes/` |
| Per-command flow (10-step init, etc.) | `internal/app/` |
| `.wwtr.yml` parsing | `internal/config/` |
| worktree discovery | `internal/worktree/` |
| vars resolution | `internal/vars/` |
| `.wwtr/state.yaml` | `internal/state/` |
| trust store | `internal/trust/` |
| Sprig template engine | `internal/template/` |
| expr-lang `when:` engine | `internal/conditions/` |
| hook executor (sh -c / cmd) | `internal/hooks/` |
| copy/symlink/render + conflict | `internal/files/` |
| huh v2 prompts | `internal/prompt/` |
| Build version | `internal/version/` |

## Commit style

Conventional Commits (`feat:`, `fix:`, `test:`, `refactor:`, `docs:`, `chore:`).
No Co-authored-by lines, no AI signatures.

## Don'ts

- Don't introduce `survey`/`promptui` — both archived/dead (PLAN §14).
- Don't read config via Viper/koanf — single YAML file, parse with yaml.v3.
- Don't add a `pkg/` directory. wwtr is a binary, not a library.
- Don't run `git commit/push` unless explicitly asked.
- Don't write comments that explain *what* the code does; only explain *why*.
