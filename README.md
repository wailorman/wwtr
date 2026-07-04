# wwtr

[![CI](https://github.com/wailorman/wwtr/actions/workflows/ci.yml/badge.svg)](https://github.com/wailorman/wwtr/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wailorman/wwtr.svg)](https://pkg.go.dev/github.com/wailorman/wwtr)
[![Go Report Card](https://goreportcard.com/badge/github.com/wailorman/wwtr)](https://goreportcard.com/report/github.com/wailorman/wwtr)

> **Vibecoded.** This project was built end-to-end with AI assistance. It has
> tests (95.7% coverage) and lints clean, but treat it as experimental — review
> the code before relying on it, and especially before running its hooks.

`wwtr` is a declarative wrapper around `git worktree`. You describe per-branch
environment setup in a single `.wwtr.yml` — templated config files, copies,
symlinks, and lifecycle hooks — and `wwtr` materialises them into each
worktree.

**What is `git worktree`?** It lets you check out multiple branches of the
same repository into separate directories at once, all sharing a single
`.git`. No stashing, no re-cloning, no editor re-indexing.

## Why

Switching branches inside a single checkout is friction: kill the dev server,
re-point `.env`, swap the database, tear down `docker compose`. `git worktree`
solves the checkout side (one directory per branch) but each new worktree
still needs its own env file, port assignment, and dependency bootstrap.
`wwtr` automates that second half — declare the setup once in `.wwtr.yml`,
run `wwtr init` in a fresh worktree, and the right files land in place.

## Install

**Homebrew:**

```sh
brew tap wailorman/tap
brew install wwtr
```

**Binary** — see [releases](https://github.com/wailorman/wwtr/releases) for
Linux / macOS / Windows on amd64 + arm64.

**From source:**

```sh
git clone https://github.com/wailorman/wwtr
cd wwtr && make build   # → bin/wwtr
```

## Quick start

```sh
git worktree add ../my-feature feature/new-auth
cd ../my-feature

wwtr init       # render templates, copy/symlink files, run init hooks
wwtr setup      # bundle install / go mod download / db:migrate
wwtr start      # docker compose up, background workers
# …hack on the branch…
wwtr stop       # tear down running services
wwtr clean      # remove generated files and state.yaml
```

The lifecycle is **init → setup → start → stop → clean**. Each is a separate
command so you can run only what you need; `setup` / `start` / `stop` run
hooks only, while `init` / `clean` also touch files and state.

## Features

- **Declarative `.wwtr.yml`** — one manifest in the main worktree, versioned
  with the repo.
- **Templated files** — Go `text/template` + [Sprig](https://masterminds.github.io/sprig/),
  rendered against per-branch builtins and user vars.
- **Copy & symlink** — share static files between worktrees without duplication.
- **Lifecycle hooks** — `pre`/`post` shell hooks for every command, executed
  via `sh -c` (Unix) / `cmd /c` (Windows).
- **Conditional hooks** — `when:` clauses in [expr-lang](https://expr-lang.org):
  short-circuit `&&` / `||`, file/dir/command/env predicates.
- **Vars from CLI, env, prompt, or expression** — with init-vs-runtime source
  priority; prompt answers persist in state.
- **Safe per-branch identifiers** — `Slug`, `Hash`, `SafeName` builtins for
  containers, DB names, port assignments.
- **Trust store** — first encounter with an unknown config prompts before
  running anything; scriptable via `wwtr trust`.
- **Composable output** — `eval "$(wwtr info --env)"` exports resolved vars
  into your shell.

<details>
<summary><b>Rails example</b></summary>

`.wwtr.yml` (lives in the main worktree):

```yaml
version: 1

vars:
  base_port:
    sources:
      - prompt: "Base port for this branch:"
        validate: '^[0-9]+$'
    default: 3000
  web_port:
    value: '{{ .Vars.base_port }}'
  webpack_port:
    value: '{{ add .Vars.base_port 1 }}'
  db_name:
    value: '{{ .Slug }}_dev'

template:
  - from: config/database.yml.tt
    to:   config/database.yml
  - from: .env.tt
    to:   .env

hooks:
  init:
    pre:
      - when: 'commandExists("rbenv")'
        run: rbenv install -s
    post:
      - run: bundle install
      - run: bin/rails db:create db:migrate
  setup:
    post:
      - run: bin/rails db:migrate
  start:
    post:
      - run: bin/rails server -p {{ .Vars.web_port }}
      - run: bin/webpack-dev-server --port {{ .Vars.webpack_port }}
  stop:
    pre:
      - run: pkill -f 'rails server -p {{ .Vars.web_port }}' || true
  clean:
    post:
      - run: bin/rails db:drop
```

`config/database.yml.tt`:

```yaml
development:
  database: {{ .Vars.db_name }}
```

`.env.tt`:

```
PORT={{ .Vars.web_port }}
WEBPACK_PORT={{ .Vars.webpack_port }}
```

</details>

<details>
<summary><b>Go example</b></summary>

`.wwtr.yml`:

```yaml
version: 1

vars:
  base_port:
    sources:
      - cli: "--base-port"
      - env: WWTR_BASE_PORT
      - prompt: "Base port:"
        validate: '^[0-9]+$'
    default: 8080
  http_port:
    value: '{{ .Vars.base_port }}'
  grpc_port:
    value: '{{ add .Vars.base_port 1 }}'
  instance_id:
    value: '{{ .SafeName }}'

template:
  - from: .env.tt
    to:   .env

hooks:
  init:
    post:
      - when: 'fileExistsInRoot("go.mod")'
        run: go mod download
  setup:
    post:
      - run: go generate ./...
      - run: docker run -d --name {{ .Vars.instance_id }}-redis -p {{ .Vars.grpc_port }}:6379 redis:7
  start:
    post:
      - run: HTTP_PORT={{ .Vars.http_port }} GRPC_PORT={{ .Vars.grpc_port }} go run ./cmd/server
  stop:
    pre:
      - run: docker stop {{ .Vars.instance_id }}-redis || true
```

`.env.tt`:

```
HTTP_PORT={{ .Vars.http_port }}
GRPC_PORT={{ .Vars.grpc_port }}
INSTANCE_ID={{ .Vars.instance_id }}
```

CLI usage — the `cli:` source becomes a real flag, registered dynamically from
the manifest:

```sh
wwtr init --base-port 9000     # → http_port=9000, grpc_port=9001
```

</details>

## `.wwtr.yml` reference

### Top-level keys

| key | description |
|---|---|
| `version: 1` | schema version (required) |
| `vars:` | named variables, resolved in declaration order |
| `template:` | list of `{from, to}` files rendered through Sprig |
| `copy:` | list of `{from, to}` files copied verbatim |
| `symlink:` | list of `{from, to}` files symlinked from the main worktree |
| `hooks:` | `pre` / `post` hook lists per command |

`to:` is optional — an entry with only `from:` lands at the same relative
path. All paths are relative to the worktree root.

### Vars

```yaml
vars:
  base_port:
    sources:
      - cli: "--base-port"          # auto-registered as a CLI flag
      - env: WWTR_BASE_PORT
      - prompt: "Base port:"
        validate: '^[0-9]+$'        # optional anchored regex
    default: 8080
  derived:
    value: '{{ add .Vars.base_port 1 }}'   # Sprig expression
```

`sources:` and `value:` are mutually exclusive. Source priority differs by
command — the `prompt` source only fires during `init`, and its answers are
persisted to state so later commands replay them:

| command | priority |
|---|---|
| `init` | CLI > ENV > prompt > default > fail |
| other  | CLI > state > ENV > default > fail |

### Builtins

Available in every `value:` expression and `template:` file as top-level
fields (`.Branch`, `.Slug`, …) and in `when:` clauses as bare names
(`Branch`, `Slug`, …):

| builtin | example | meaning |
|---|---|---|
| `Branch` | `feature/new-auth` | current git branch |
| `Slug` | `feature-new-auth` | DNS-safe `[a-z0-9-]` form |
| `Hash` | `a1b2c3d4` | first 8 hex of `sha1(branch)` |
| `ShortHash` | `a1b2c3` | first 6 hex |
| `SafeName` | `feature-new-auth-a1b2c3d4` | `≤63` chars, `slug-hash` |
| `WorktreePath` | `/repo/feature-new-auth` | absolute path to current worktree |
| `WorktreeName` | `feature-new-auth` | basename of current worktree |
| `MainWorktreePath` | `/repo` | absolute path to main worktree |
| `MainWorktreeName` | `repo` | basename of main worktree |

### `when:` predicate reference

The `when:` field uses [expr-lang](https://expr-lang.org) — short-circuit
`&&`, `||`, `!`, parentheses, plus these predicates:

| function | description |
|---|---|
| `fileExists("rel/path")` | file exists in current worktree |
| `dirExists("rel/path")` | directory exists in current worktree |
| `fileExistsInRoot("rel/path")` | file exists in main worktree |
| `dirExistsInRoot("rel/path")` | directory exists in main worktree |
| `commandExists("docker")` | `command -v <name>` exits 0 (cached per run) |
| `envSet("FOO")` | environment variable is set |
| `envEq("FOO", "bar")` | environment variable equals value |
| `platformIs("darwin")` | `runtime.GOOS` match |
| `varEq("base_port", "8080")` | resolved user var equals value |

Example: `when: 'commandExists("docker") && (envEq("CI", "1") || varEq("use_compose", "yes"))'`.

> **Two engines, intentionally.** `value:` / `template:` use Go `text/template`
> + Sprig; `when:` uses expr-lang. Don't try to unify them — see
> [AGENTS.md](AGENTS.md) §5.

### Hooks

Each of `init`, `setup`, `start`, `stop`, `clean` accepts `pre:` and `post:`
hook lists. Three entry shapes:

```yaml
hooks:
  init:
    pre:
      - echo bare command                 # scalar → plain shell string
      - run: echo explicit                # {run: ...}, optionally with when:
        when: 'fileExists("go.mod")'
      - load_env: .env.generated          # {load_env: path} — dotenv parsed by wwtr
```

Semantics:

- **pre** hooks abort the command on the first error.
- **post** hooks log a warning and continue.
- `run:` and `load_env:` values are rendered through Sprig; `when:` through
  expr-lang.
- `load_env` keys are scoped to the current stage — they are prepended as
  `export` prefixes on subsequent `run:` commands in the same stage.
- Multi-line `run: |` becomes a single shell invocation.
- `load_env` cannot be combined with `run` / `when` on the same hook.

## Commands

| command | description |
|---|---|
| `wwtr init` | render templates, copy/symlink files, run init hooks, write state |
| `wwtr setup` | run setup hooks (deps install, db migrate) |
| `wwtr start` | run start hooks (services up, background workers) |
| `wwtr stop` | run stop hooks (services down) |
| `wwtr clean` | run clean hooks, remove generated files, delete state |
| `wwtr info` | print resolved vars and builtins (no trust check, no hooks) |
| `wwtr trust [path]` | explicitly approve a config (CI/scripts) |
| `wwtr untrust [path]` | revoke an approval |
| `wwtr version` | print version, commit, build date |

Global flags: `--config`, `--force`, `--skip`, `--dry-run`, `--no-hooks`,
`--yes` (auto-approve trust and all y/n prompts), `--no-state`, `-v/--verbose`.

`info` has its own output flags:

```sh
wwtr info                  # human-readable
wwtr info --env            # export lines for eval
wwtr info --json           # JSON
eval "$(wwtr info --env)"  # exports WWTR_BRANCH, WWTR_SLUG, WWTR_VAR_*, ...
```

## How it works

### Lifecycle

```
┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐   ┌──────────┐
│   init   │ → │  setup   │ → │  start   │ → │   stop   │ → │  clean   │
│ files +  │   │  hooks   │   │  hooks   │   │  hooks   │   │ files +  │
│ hooks +  │   │          │   │          │   │          │   │ hooks +  │
│ state    │   │          │   │          │   │          │   │ state rm │
└──────────┘   └──────────┘   └──────────┘   └──────────┘   └──────────┘
```

`init` is the only command that writes files; `clean` is its reverse;
`setup` / `start` / `stop` run hooks only.

### Trust

Every command except `info` checks the config against a trust store at
`~/.config/wwtr/trust.yaml` before running. First encounter with an unknown
(or changed since last approval) config prompts y/n. For non-interactive use:

```sh
wwtr trust               # approve the discovered config up front
wwtr init --yes          # or auto-approve inline (also forces file conflicts)
```

### State

`.wwtr/state.yaml` (per worktree) stores only the values resolved through
`prompt` during `init`. Everything else is re-derived from CLI/env/default on
every run, so the file stays minimal and diff-friendly. `clean` removes it.

## Alternatives

Plain `git worktree` gives you the directories but no per-branch setup
scripting; most teams accumulate ad-hoc shell scripts that don't know which
branch they're on. `wwtr`'s pitch is to declare that setup once in a versioned
manifest and get branch-aware vars, hooks, and file rendering for free.

Other worktree wrappers (e.g. `git-worktree-wrapper`, `wt`) tend to focus on
adding/removing/listing worktrees rather than the environment-setup half.
`wwtr` deliberately does only the second half — you keep using
`git worktree add` directly.

## Development

```sh
make build         # → bin/wwtr
make test-race     # CI baseline
make test-cover    # enforces ≥95% coverage
make lint          # golangci-lint v2
make vuln          # go tool govulncheck ./...
make fmt           # gofumpt
```

See [AGENTS.md](AGENTS.md) for architecture rules and where things live.

## License

MIT
