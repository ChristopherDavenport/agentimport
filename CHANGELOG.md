# Changelog

All user-visible changes to this library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/); before v1.0.0 minor
versions may break the API.

## Unreleased

- Dependencies: agentsession v0.0.26, whose jsonl store refuses a
  session ID or working directory that would put a file outside its
  root; the command's own check, which did the same, is gone.
- Added: the `agentimport` command. `convert -o <store>` writes every
  session a conversion makes into an agentsession jsonl store and
  prints the report; `report` converts without writing and adds the
  findings up per source, for surveying a corpus. Each file's source is
  detected from its name and first lines, or named with `-from`;
  `-strict` fails on dropped or gap findings, `-slim` drops what the
  model never saw, `-force` replaces a session the store holds. The
  store keeps every file under `-o` whatever the native log names: it
  refuses an ID that is not a file name, and files a working directory
  of `/..` under `default`.
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
- Added: `pi`, a reader for pi session files (session version 3, from
  pi's own specification and, where they disagree, its code): line
  types that keep every member they do not declare and round-trip, a
  member of another JSON type kept as written rather than failing the
  line, content blocks read one by one, lines pi skips (NUL bytes,
  null, not an object) skipped, lone surrogate escapes removed as pi
  removes them, `Migrate` for versions 1 and 2 as pi migrates them, and
  `BuildTree`, which links entries by id as pi does.
- Added: `pi/convert`, pi to agentsession, converting entries in file
  order: system messages as config deltas with named instruction parts
  and tools; assistant parts as items (a Responses reasoning item and
  message ids recovered from pi's signatures, `call_id|item_id` split,
  Gemini thought signatures as openresponses' Gemini provider carries
  them, Responses custom tool calls as function calls marked
  `pi:tool_kind`);
  errored and aborted attempts left out of context with `omit`, as pi
  leaves them out; compactions and branch summaries with pi's framing;
  context edits as `omit`; usage entries as paid-call records; forks
  linked to their parent. Every line is accounted for, with its
  findings documented in `docs/formats/pi.md`.
- Added: `CleanJSON` keeps the last of a repeated member name, as
  JSON.parse does, since I-JSON forbids repeats; `RepeatedMembers` and
  `InvalidText` tell what it changed, and the builder reports
  `agentimport/repeated-member`.
- Changed: the format packages share one codec, `internal/jsonline`;
  `TestImportBoundary` allows an internal package that imports the
  standard library alone.
- Added: `codex`, a reader for Codex CLI rollouts, written from
  openai/codex at 7ae83199 (2026-10-15): the envelope lines and the
  layout before it (summer 2025), with every payload kind the source
  persists typed and every member kept; torn and NUL-filled lines are
  skipped as Codex skips them; a zstd-compressed rollout is refused
  with `ErrCompressed`.
- Added: `codex/convert`, Codex to agentsession, following how Codex
  rebuilds the history it sends: a run of output items and its token
  count as a response; turn contexts as config deltas; a rollback as a
  branch from before the first dropped turn; a compaction as a
  compaction entry over the window Codex installed, rebuilt as Codex
  rebuilds it when the rollout predates replacement histories; a custom
  tool call (apply_patch) as the function call it is, marked
  `codex:tool_kind`; other items openresponses does not type as
  `openai.<type>` or `codex.<type>` extension items. Every line is accounted for and every difference
  reported under a code `docs/formats/codex.md` documents.
- Changed: `codex/convert` reads a response's records in the order the
  rollout's Codex wrote them (the token count before a batch of items
  up to 0.65, after the items from 0.66, after the tool outputs from
  0.131, with a usage record from 0.159), judging the order when the
  version is unknown; a rollback undoes a compaction written in the
  turns it drops, as Codex's reconstruction does; Codex's own messages
  are told from a person's by a port of Codex's classifier; a response
  copied into a fork or a context-inheriting sub-agent keeps its usage
  in `codex:usage` only; nothing in a compaction's window stops a
  conversion.
- Added: `Finish` writes a converted session, reads it back, checks it
  and returns the session as read back; every converter returns that
  session, so its leaf is the one a reader of the file resumes on.
- Added: the `agentimport` package: `Report` (a tally of where every
  native line went and findings by stable code), `Builder` (appends a
  native tree to a session, keeps native IDs in namespaced members,
  records provenance in the `agentimport:source` header member) and
  `CallIDs` (call IDs unique across a session, as the format requires).
