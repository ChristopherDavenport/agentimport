# Changelog

All user-visible changes to this library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/); before v1.0.0 minor
versions may break the API.

## Unreleased

- Added: `claudecode`, a reader for Claude Code session transcripts
  (written from 2.1.271 to 2.1.300 and the Agent SDK's typings): line
  types that keep every member they do not declare and round-trip, a
  reader that skips torn and NUL-filled lines anywhere in a file,
  `Open` for a session and its sub-agents, and `BuildTree`, which groups
  a response's lines, puts parallel tool results back in their
  response and hangs a compaction from its logical parent.
- Added: `claudecode/convert`, Claude Code to agentsession: a session
  per transcript, sub-agents as subsessions linked from their call,
  compactions as compaction entries, prompt snapshots as config, and
  every line accounted for with its findings documented in
  `docs/formats/claudecode.md`. `Options.Slim` leaves out the sidecars
  the model never saw.
- Changed: `Builder.Leaf` checks the leaf against the file's last
  entry, which is where a reader resumes, rather than the session's
  in-memory leaf; `Builder.Last` returns that entry.

- Added: the `agentimport` package: `Report` (a tally of where every
  native line went and findings by stable code), `Builder` (appends a
  native tree to a session, keeps native IDs in namespaced members,
  records provenance in the `agentimport:source` header member) and
  `CallIDs` (call IDs unique across a session, as the format requires).
