## What changed, and why


## What an application sees
<!-- A change to the public packages, the files `govibe new` writes, the CLI, the runtime commands or the settings is public API:
     add a note to docs/public/UPGRADING.md under "Unreleased" and to docs/public/ARCHITECTURE.md. Write "nothing" otherwise. -->


## How it was verified
- [ ] `make test` and `make test-dev` (the suite both ways)
- [ ] `make graph-check` and `make graph-check-app` (the gates, empty baselines)
- [ ] `make keystone`, when the change touches the scaffold, the generators, the kernel or the dev tools
- [ ] Generated copies are current (`make docs-embed`, `make scaffold-sync`)
- [ ] Verified in a scratch copy under a clean environment, not against the working tree's dev server

## Checklist
- [ ] Goldens regenerated only on an intended change, and the diff reviewed
- [ ] No dev package imported from an untagged file
- [ ] The spec's status table updated, if a spec covers this work
- [ ] Commits signed off (`git commit -s`)
