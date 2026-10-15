# Contributing

Issues and pull requests are welcome.

## Before you start

This module reads other agents' session logs and converts them into
the Agent Session Format. It does not write native formats back, and
it adds nothing to the session format itself: where a native log holds
something the format has no place for, the converter keeps it in a
namespaced extension or drops it, reports it, and the question goes to
`agentsession`'s RFC. `docs/plans/agentimport.md` is the design; read
it first.

For anything larger than a bug fix, or a new source, open an issue
first so the shape of the change can be discussed before you spend
time on it.

## Development

Go 1.25 or later is required. The full local check is:

```sh
make check        # gofmt, tidy, vet, deps, staticcheck, govulncheck, race tests
```

The module depends on `agentsession`, `openresponses` and the standard
library; the format packages on the standard library alone. `make deps`
and `TestImportBoundary` fail if anything else creeps in.

Fixtures are synthetic: write the shape you need, never a copy of a
real session. Golden outputs are regenerated with
`go test ./... -update`; review the diff. To check the converters
against real logs where they are, without copying them:

```sh
make corpus CLAUDECODE=~/.claude/projects PI=~/.pi/agent/sessions CODEX=~/.codex/sessions
```

## Pull requests

- Keep each pull request focused on one change.
- Add or update table-driven, offline tests.
- A new finding code goes in `docs/formats/<source>.md` in the same
  change, and a new `gap` in `docs/incompatibilities.md` too.
- Run `make check` before pushing.
- Note user-visible changes under *Unreleased* in `CHANGELOG.md`.

## Releases

This repository is a single module, tagged `vX.Y.Z`. With the
changelog's *Unreleased* section written:

```sh
make release VERSION=v0.1.0
```
