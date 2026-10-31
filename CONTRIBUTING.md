# Contributing to GoVibe

Thank you for working on GoVibe. This file is the short version; the contributor guide with the layout, the constraints and the
conventions is [SKILL.md](SKILL.md), and how the framework is built is [docs/public/ARCHITECTURE.md](docs/public/ARCHITECTURE.md).

## Before you start
- **An issue first** for anything that changes the public surface (the packages `govibe`, `config`, `web`, `govibetest`, the files a
  generated app gets, the CLI) or adds a dependency. Fixes and docs can go straight to a pull request.
- **Not planned** (see the spec's non-goals): a plugin API, a runtime DI container, replacing chi, templ, htmx, sqlx or goose, mail, auth,
  queues. An issue is still welcome if you think one of these should change.

## Setting up
Go 1.25 or newer. Clone, then:
```sh
make dev          # the reference app (examples/hello) with the dev panel at http://localhost:8080/dev
make test-short   # the inner loop: the suite minus the tests that build and run a whole app (a few seconds per package)
make test         # the framework's tests and the example's, default build
make test-dev     # the same with the dev tools compiled in (some tests exist only under the govibe_dev tag)
make check        # both, plus the two graph gates
```
`make keystone` scaffolds a real app from your working tree with `govibe new`, adds a scheduled command and a dev tool, builds it both ways,
tests it, gates it and boots it twice; it takes minutes and downloads modules. CI runs all of this (`.github/workflows/ci.yml`, `graph.yml`).

## Working on it
- **Verify in scratch copies, under a clean environment.** A running dev server holds the graph store's lock, and a shell that exports
  `MARIADB_DSN` makes a boot open the real database. Start scratch apps with `env -i HOME="$HOME" PATH="$PATH" ...`.
- **The dev tools stay out of production by construction.** Only `internal/kernel/devmount_dev.go` names them, behind the `govibe_dev` tag;
  never import a dev package from an untagged file, and never tag a dev package itself. `TestDefaultBuildLinksNoDevTools` and the `production`
  CI job enforce it.
- **Moves change no logic in the same commit**; the golden files under `internal/codegraph/testdata/golden` are the proof. Regenerate them only on an
  intended change (`go test ./internal/codegraph -update`) and review the diff.
- **Anything an application sees is public API**: a note in `docs/public/ARCHITECTURE.md` and, before 1.0, in `docs/public/UPGRADING.md` under
  "Unreleased". A change to what `govibe new` writes changes the managed files' hashes: say so there too.
- **Docs are indexed.** The framework's own gate reads `docs/public`: a backticked path that does not exist in this repository is a finding.
  What an app reads (the embedded docs, `SKILL.md`'s managed block, tool descriptions) uses neutral names, never the test fixtures' domain.
- **Generated copies.** After editing a doc of the allowlist, `make docs-embed`; after editing `examples/hello` or the app SKILL template,
  `make scaffold-sync`. A test fails when a copy is stale.
- **Spec-driven.** Larger work has a spec in `docs/specs` (`SPEC_<NAME>.md`, with the sections `govibe make spec` writes); update its status
  table and deviations when you land a phase.

## Pull requests
- One concern per pull request; the template asks what changed, why, how it was verified and what an app sees.
- CI must be green: tests both ways, the two gates with their empty baselines, the production check, the keystone.
- Commits are signed off in the message (`git commit -s`), under the [Developer Certificate of Origin](https://developercertificate.org).
  Contributions are licensed under the repository's [Apache-2.0](LICENSE) license.

## Releasing (maintainers)
1. On `main`, move the "Unreleased" notes in `docs/public/UPGRADING.md` under a `## vX.Y.Z` heading (keep an empty "Unreleased" above it), run
   `make docs-embed`, and make sure `make check` and `make keystone` are green.
2. `go run golang.org/x/exp/cmd/gorelease@latest -version=vX.Y.Z` reports what changed against the last tag; before 1.0 a minor may break the
   public surface, and the notes must say so.
3. Tag and push: `git tag -a vX.Y.Z -m "vX.Y.Z" && git push origin vX.Y.Z`. The `release` workflow runs the tests, checks that the notes exist and
   publishes the GitHub release with them. The module proxy picks the tag up on the first `go get`.
4. `go install github.com/petars-dev/govibe/cmd/govibe@vX.Y.Z && govibe new github.com/acme/smoke` from an empty directory proves the release.
