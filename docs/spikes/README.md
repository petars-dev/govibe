# Phase 0 spikes: saved material

Reference material from Phase 0 of `docs/specs/SPEC_FRAMEWORK_EXTRACTION.md` (spikes S1 to S9, run 2026-10-11 in scratch
copies of this repository at commit `a353f8a`). **None of this is built, tested or shipped.** The results table and the
amendments they require are in the spec, under "Spike results (Phase 0)"; this directory keeps the evidence behind them and the
few pieces a later phase can start from. Delete it once the phases that reuse it have landed.

## Layout
| Path | What it is | Used by |
| --- | --- | --- |
| `notes/S1.md` to `S9.md` | One note per spike: method, exact commands and numbers, surprises, "Consequence for the design" | the spec's results table |
| `scripts/s7-rename.sh` | The module rename (`petars-dev/govibe` to `github.com/petars-dev/govibe`); idempotent, skips the spec and this directory on purpose | T2.1, used 2026-10-12 after two fixes (it now sees untracked files and leaves `docs/spikes` alone) |
| `scripts/s2-refresh_mod.sh`, `s2-show_routes.py` | Rebuild the module-cache variant of the scratch app; print the route table | reproducing S2 |
| `scripts/s9-collect.sh`, `s9-analyze.py` | Collect views, reports and tool output per scope; compare them | reproducing S9 |
| `patches/s1-tag-exclusion.patch` | The `govibe_dev` split: a tagged dev mount, an untagged twin, nine tagged controllers | T2.5 and AM5 |
| `patches/s9-scope-filter.patch` | `origin` and `Scope` in the `Model`, findings on `all`, `cmd/s9dump` and `cmd/s9probe` | T3.2, T3.3, AM9 |
| `patches/s2-s3-s5-s6-framework.patch` | The prototype framework: `govibe.go`, `config/layered.go` and `defaults.yml`, `govibetest`, `web`, `internal/{kernel,migrate,routeinfo}`, the route-origin changes in the indexer, `cmd/spikecalls`, `cmd/spikeindex` | T1.1, T1.2, T3.2, T3.6, AM7, AM8 |
| `_prototypes/s8-scheduler-runner/` | The own scheduler runner over the robfig parser (183 code lines), its fake clock and 9 tests; `compare/` holds the gocron comparison harness and the recorded outputs | T4.1, AM12 |
| `_prototypes/shop-app/` | The scratch app from S2, S3, S5 and S6: root `env.yml`, embedded hand-over, `main_test.go`, a tag-only dev route | the shape of `examples/hello` (T2.3) |
| `_prototypes/s4-tool-directive/` | `go.mod` of an app with `tool` directives, once with latest tools (`go 1.26.0`) and once pinned (`go 1.25.0`) | AM1 |

## Using it
- **Patches** were made with `diff -ruN` against the repository at `a353f8a` and apply with `patch -p1` from the repository root of
  that commit. They are references to read, not changes to apply: the design moved on in the amendments.
- **`_prototypes/`** starts with an underscore on purpose: the Go tool and the graph indexer both skip such directories, so
  nothing here is compiled, indexed or listed by the dev panel. `docs/spikes/` is outside `docs/public` and `docs/specs`, so the
  docs layer does not read it either. The `.go.txt` files are single-file programs (each a `package main`) kept as text so they do
  not read as one Go package.
- **Paths in the notes** point into the scratch directory they were written in. `base` is the repository at `a353f8a`; `s1`, `s7`
  and `s9` are copies of it; `fw` is the scratch framework; `app` is `_prototypes/shop-app`; `s8` is `_prototypes/s8-scheduler-runner`.
  `go.mod` files here that mention `replace ... => ../fw` or `../../s4fw` point at directories that no longer exist.
- **Running anything** from these notes: use a scratch copy and start with `env -i` (see Guardrails in the spec). The shell that
  ran Phase 0 exported database and WorkOS variables, and four early scratch boots connected to the local database.

## Not kept
Raw outputs (`s9out/`, about 55 MB), built binaries, and the Go build and module caches the spikes used (several GB).
