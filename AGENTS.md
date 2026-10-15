# agentimport

Converters from other coding agents' session logs into the Agent
Session Format: Claude Code, pi and Codex today. Each source is a
format package that reads the native log as it is, and a `convert`
package that turns it into `agentsession` sessions and reports every
difference. The design is `docs/plans/agentimport.md`; read it before
writing code. To add a source, read `docs/plans/new-source.md` too: the
build-and-review loop, the defects the first three sources had, the
decisions they settled, and what is known of the candidates.

## Module

- Module path: `github.com/ChristopherDavenport/agentimport`.
- Go 1.25 is the floor.
- Packages: `agentimport` (the report, `Builder`, `CallIDs`) at the
  root; per source, `<source>` (the native format) and
  `<source>/convert`; `cmd/agentimport`.
- The module imports `agentsession`, `openresponses` and the standard
  library alone; `make deps` enforces it. A format package imports the
  standard library alone, or a package of this module's `internal`
  tree that does, at any depth (`internal/jsonline`, the codec that
  keeps undeclared members); `TestImportBoundary` enforces it, and it
  lists the format packages, so a new source adds itself there.

## Rules every converter keeps

- Every native line is accounted for, as mapped, kept, dropped or
  malformed, and the tally adds up to the lines read. A test asserts it
  per fixture.
- Every difference is a `Finding` with a stable code. Each code is
  documented in `docs/formats/<source>.md`, and a test fails on a code
  the document does not list. A `gap` code, something the session
  format has no place for, is also listed in
  `docs/incompatibilities.md`.
- A format package keeps the members it does not type and writes them
  back: a fixture line decodes and re-encodes to the same JSON value.
- A malformed native line is reported and skipped, never fatal.
- Converted output passes `agentimport.Verify` before it is returned.
- Payload mappings follow `openresponses/providers` (thinking
  signature in `encrypted_content`, `anthropic.<block>` items, usage
  with cache reads folded into input) so a converted session replays
  through the same adapter.
- Fixtures are synthetic. Never copy a real session from `~/.claude`,
  `~/.pi` or `~/.codex` into the repository, not even a redacted one;
  write a fixture with the shape. Real corpora are read in place by
  `make corpus`.

## Siblings

Peer repositories, each independently versioned:

- `../agentsession`: the format written. Its RFC is normative; where
  this module needs something the RFC lacks, that is a `gap` and a
  question for the RFC, not a new core member here.
- `../open-responses`: the payload items. Its `providers/anthropic`
  mapping is the one the Claude Code converter follows.
- `../dax`: a product that reads sessions; the first consumer of
  imported ones.

## Conventions

Mirror `../agentskill`: a `Makefile` with `build`, `deps`, `test`,
`vet`, `fmt`, `tidy`, `tidy-check`, `lint`, `vuln`, `check`,
`corpus`, `release-guard` and `release` targets, the same CI workflow
shape, a `CHANGELOG.md` in Keep a Changelog form, and annotated `v*`
tags whose message becomes the release notes. `make check` must pass
before any commit; in this workspace run it as
`GOWORK=off GOFLAGS=-mod=readonly make check`.

Tests are table-driven and offline. Golden outputs (converted session
and report) live under each package's `testdata/` and are regenerated
with `go test ./... -update`; review the diff.
