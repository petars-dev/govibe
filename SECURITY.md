# Security policy

## Reporting a vulnerability
Please do not open a public issue for a vulnerability. Report it privately through
[GitHub's security advisories](https://github.com/petars-dev/govibe/security/advisories/new) for this repository, with the version, how to
reproduce it and what it exposes. You will get an acknowledgement within a week; fixes ship as a patch release with a note in
`docs/public/UPGRADING.md`, and the advisory is published once a fixed version exists.

## Supported versions
Before 1.0, the latest minor release is supported: a fix lands on `main` and is released as the next patch of the current minor.

## Scope, and what the design already promises
- **The dev tools are never in a production build.** `/dev`, the graph, the database explorer, the scheduler page and the MCP server exist only in
  a binary built with the `govibe_dev` tag, and even then only when `APP_ENV` is not production. A report that a default build serves anything
  under `/dev` or `/_govibe` beyond htmx is a vulnerability.
- **The dev tools are for a developer's own machine.** They have no authentication: do not expose a dev build to a network you do not trust.
  The database explorer is read-only unless `DEV_DB_ALLOW_MUTATIONS` is set, and the graph never stores a configuration value, a secret or a row.
- **Tooling runs the application.** `govibe check`, `govibe doctor` and `govibe graph index` run the project's own binary (`inspect --json`) under a
  scrubbed environment; index a project you do not trust with `--no-inspect`.
- **Dependencies** are pinned in `go.mod`; a vulnerability in one of them is handled with a patch release that bumps it.
