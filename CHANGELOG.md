# Changelog

All user-visible changes to this library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/); before v1.0.0 minor
versions may break the API.

## Unreleased

- Added: `claudecode`, a reader for Claude Code session transcripts
  (written from 2.1.271 to 2.1.300 and the Agent SDK's typings): line
  types that keep every member they do not declare and round-trip;
  content blocks decoded one by one, so a block of a shape not typed,
  or a null, is kept raw; a line that does not decode keeps its
  envelope and its place in the tree; a reader that skips torn and
  NUL-filled lines anywhere in a file; `Open` for a session and its
  sub-agents; and `BuildTree`, which groups a response's lines, puts
  parallel tool results and the lines written between blocks back in
  their response, and hangs a compaction from its logical parent.
- Added: `claudecode/convert`, Claude Code to agentsession: a session
  per transcript; an agent the call waited for as a subsession whose
  output names its leaf, one the call only started or a team member as
  a peer; refused calls as `reject` decisions; a prompt's origin as the
  item's source; compactions as compaction entries; prompt snapshots
  and request shapes as config, server tools as `anthropic.<type>`
  tools; refusals as the provider writes them. Every line is accounted
  for and every difference reported under a code
  `docs/formats/claudecode.md` documents. `Options.Slim` leaves out
  the sidecars the model never saw.
- Added: `CleanJSON`, which makes native JSON valid Unicode, member
  names included, before it goes into a member or a custom entry; the
  builder applies it to every member.
- Changed: `Builder.Leaf` writes a leaf label unless the file's last
  entry is the leaf, so a reader resumes on the native leaf exactly;
  `Builder.Last` and `Builder.Resume` return the last entry and the
  leaf.
- Added: the `agentimport` package: `Report` (a tally of where every
  native line went and findings by stable code), `Builder` (appends a
  native tree to a session, keeps native IDs in namespaced members,
  records provenance in the `agentimport:source` header member) and
  `CallIDs` (call IDs unique across a session, as the format requires).
