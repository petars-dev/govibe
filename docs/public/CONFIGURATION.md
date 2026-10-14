# GoVibe Configuration

## Where settings live
- The framework's own settings and their defaults are in `config/defaults.yml`, embedded in the `config` package.
- An application's settings are in `env.yml` in its project root, embedded by `main.go` (`//go:embed env.yml`) and handed over as `App.Config`.
- There is no override file and no second location: the file in the project root is embedded at build time, and the environment is the only
  deploy-time input.

## Layering and placeholders
- Layers, lowest first: the framework defaults, then the application's `env.yml`. The application's value wins; maps merge deeply, lists are
  replaced, unknown keys are allowed.
- A value is a literal or a `${ENV:default}` placeholder, resolved from the process environment: `port: ${PORT:8080}`. A quoted placeholder
  stays a string (`"${VERSION:1.10}"`); an unquoted one is typed as YAML types the result.
- `config.Load` returns a value (`config.Config`); nothing is global. The framework's typed keys are fields (`cfg.Server.Port`); an application's own
  keys are read with `rt.Config.Get("shop.currency")` and `rt.Config.Unmarshal("shop", &cfg)`.

## The framework's keys
| Key | Environment | Default | Meaning |
| --- | --- | --- | --- |
| `env` | `APP_ENV` | `local` | `local`, `dev`, `staging` or `prod`/`production`; production never mounts the dev tools and serves embedded assets |
| `root` | `GOVIBE_ROOT` | (empty) | the application's root directory; empty finds the nearest directory with `govibe.yml` or `go.mod` |
| `server.port` | `PORT` | `8080` | HTTP port |
| `server.log_level` | `LOG_LEVEL` | `info` | logrus level |
| `database.type` | `DATABASE_TYPE` | `mysql` | goose dialect; always passed explicitly |
| `database.mariadb.dsn` | `MARIADB_DSN` | (empty) | the one database pool; empty means no database |
| `dev.db.allow_mutations` | `DEV_DB_ALLOW_MUTATIONS` | `false` | enables the test-data mutation tools; never effective in production |
| `dev.framework_src` | `GOVIBE_FRAMEWORK_SRC` | (empty) | contributors only: a checkout of the framework, whose docs and specs the dev docs then serve |
| `dev.graph.calls` | `GRAPH_CALLS` | `true` | index which function calls which (type-checks the module) |
| `dev.graph.git` | `GRAPH_GIT` | `false` | index git history per file |

The graph knows these as the framework's settings: a read of `server.port` in your code is not "read but not declared", and you are not told
that you never read `dev.graph.calls`. They appear in scope **Framework** or **All**, labelled.

## The project profile
`govibe.yml` in the project root tells the tools where things are: the settings files, the migrations directory, the docs directories, the
directory-to-layer table, the files that are generated and absent from a clone, and per-rule switches. Every key is optional; the default is the
layout of a generated app. An unknown key is an error.
```yaml
config: [env.yml]                    # settings files, lowest first
migrations: storage/migrations/sql
docs:
  dirs: [docs/public]
  specs: docs/specs
  spec_prefix: SPEC_
  root: [README.md, SKILL.md]
graph:
  dir: docs/graph
  derived: [docs/graph/*.bolt]       # generated, git-ignored, absent from a clone
layout:                              # a layer replaces its default directories
  controllers: [internal/http]
  business: [internal/core]
rules:
  code.unreferenced: off             # or error, warning, info
```
Accepting a finding in the baseline (`docs/graph/baseline.json`) needs a reason: `govibe graph index --update-baseline --reason "..."`. Each entry
keeps its reason and date, and a baseline entry without a reason fails the gate.
