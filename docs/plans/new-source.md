# Adding a source

How a new importer is built, written down after Claude Code, pi and
Codex. `agentimport.md` is the design every source follows; this is
the working method, the mistakes the first three made and the
decisions they settled, so a fourth starts where they ended.

## What a source is

- `<source>/`: the native format. Line types that keep the members
  they do not declare (through `internal/jsonline`), a reader that
  reports a malformed line and goes on, a finder (`FindSessions`), and
  whatever structure the format implies (a tree, a replay, an index of
  related files). Standard library and `internal/` packages only; add
  the directory to `formatPackages` in `boundary_test.go`.
- `<source>/convert`: `Convert`, `ConvertFile` and `Options{Slim, …}`,
  returning `agentimport.Result` through `agentimport.Finish`.
- `docs/formats/<source>.md`: where the format is specified (or that it
  is not), the layout, the reading rules, the mapping table, an
  *Extension types* section, and every finding code.
- In `cmd/agentimport/detect.go`: an entry in `sources`, and a case in
  `detect` that tells the format from its name and first line without
  claiming another source's files. Related files converted with a main
  one (sub-agents) are skipped as inputs only when the main result
  holds them.
- `make corpus` and the source's `TestCorpus`, gated by
  `AGENTIMPORT_CORPUS_<SOURCE>`; the README table and
  `docs/formats/README.md` list; a CHANGELOG entry.

## The loop

Every source went through the same five steps, and every one of them
found defects the step before had passed.

1. **Survey.** Read the native files on this machine for shapes only:
   keys, types, enum values, version fields, how often each occurs.
   Never quote content, and never copy a file, a line, a session ID or a
   file name into the repository; fixtures are written by hand to the
   shape. Then read the owner's specification or source at a pinned
   commit or tag, and older tags where the format changed, and record
   which in the format document. Local data is often thin (one empty
   rollout, a handful of entry types); the source is the authority and
   the corpus the check.
2. **Build** against the defect list below from the start, with one
   fixture per shape, golden session and report per fixture, the tally
   assertion, `doccheck.Check`, a fuzz target for the reader and one
   for the converter, and `TestCorpus`.
3. **Review** by a separate agent that did not write the code: read
   only, no Go commands, against the format's own source (how *it*
   rebuilds the model's context on resume) and RFC 0001, with the
   defect list as the checklist. The builder's green checks missed real
   defects every time.
4. **Fix** every finding, or say why not, in a separate commit.
5. **Verify the head yourself**: `make check`, the new tests failing on
   the old code, the corpus run, and a look at the golden files where
   the change matters (where a link sits, what the context holds).

Run a fuzz target with `-fuzzminimizetime` bounded (a few seconds):
minimising a multi-kilobyte seed otherwise takes minutes and looks
like a hang. CI runs on Go 1.25 and stable; a golden that holds Go's
own error text fails on one of them.

Stacked pull requests land on their base branch, not on `main`, unless
each base branch is deleted before the next merges; merge bottom-up
with `--delete-branch`, or put the stack on one branch.

## Defects to build against

Each of these was found by a review in at least one of the first three
converters.

- **Context that differs from what the agent sent.** Read how the
  harness rebuilds history when it resumes a session and match it:
  what it drops (failed or aborted turns, UI-only lines, synthesised
  outputs), what it adds, where it puts lines written between the
  blocks of one response, which of two renderings it sends, how a
  rollback or rewind interacts with a compaction.
- **A panic or an abort on any JSON value.** `null` in an array, a
  member of the wrong JSON type, a non-object line, a huge line. Decode
  block by block, keep what does not fit as written, and make a bad
  item, window entry or related file a finding, never a failed
  conversion.
- **Reference resolution that depends on walk order.** A summary,
  label or edit that names an entry the walk has not reached yet;
  parents written after their children; IDs reused.
- **Members dropped with no finding.** Every undeclared member is kept
  (`<ns>:line`, `<ns>:block`, `<ns>:message`) or reported.
- **The leaf on the wrong branch.** Bind a native line to the end of
  its unit, so a leaf label lands on the response entry; let
  `Builder.Leaf` decide from what was appended.
- **State changed before an item decodes.** Decode first, then take a
  call ID or record a call.
- **Usage counted twice.** Responses copied into a fork or a sub-agent
  keep their usage in a member, not on the response entry.
- **Version-dependent shapes.** The record order, a tool's output
  shape or a field's name changed across releases; choose by the
  version the file records, guess structurally when it records none,
  and report the guess.
- **Text that changes with Go.** Render errors in findings through
  `agentimport.ErrorText`.
- **Related files lost or converted twice.** An index of related files
  must survive symlinks and unreadable directories, be built once per
  tree, never skip an input the main result does not hold, and handle
  cycles and a file naming itself.

## Decisions already made

- **Gaps are the session format's alone.** A `gap` is something RFC
  0001 has no place for, listed in `docs/incompatibilities.md`. A
  payload type the Open Responses spec does not define is a
  slug-prefixed extension, prefixed by whoever defines it
  (`anthropic.`, `openai.`, `<source>.`), carried verbatim and reported
  `kept`.
- **A core type under another name is the core type.** A Responses
  custom tool call is a `function_call` whose arguments wrap its input,
  marked `<ns>:tool_kind`, so the session's call rules see it.
- **Payload follows `openresponses/providers`.** Anthropic thinking:
  summary text plus the signature in `encrypted_content`; Gemini
  thought signatures: a reasoning item holding only `encrypted_content`
  just before the part; Anthropic usage: cache reads and writes folded
  into input tokens, the rest in `<ns>:usage`.
- **Sub-agents by lifetime.** A callee the call waited for is a
  `subsession`: its own session with `parent_session` and `spawned_by`,
  the link before the call's output, the output's `parents` naming the
  child's leaf. A callee that outlives the call (a background agent, a
  teammate, a spawned thread) is a `peer`: the link *after* the call's
  output, since that output is an acknowledgement and the RFC reads an
  output following a link as the callee's answer; the child keeps
  `parent_session` and no `spawned_by`.
- **Copied history.** A session that starts from a copy of another's
  history (pi's fork, a Claude Code fork agent, a Codex forked thread)
  names the other in `parent_session`; the copy is converted as the
  session's own entries and reported, since writing it as a `base`
  needs the other converted to identical hashes, and copied responses
  keep their usage off the response entry. pi adds a `fork_of` link.
- **Rejections are decisions.** A permission refusal maps to a `reject`
  decision before the output; approvals stay native, since the RFC asks
  writers not to record a bare `proceed`.
- **Provenance.** `agentimport.SetSource`, native IDs in `<ns>:` members,
  `records` left empty, timestamps from the native lines, never sorted.

## Candidates

What is known of the formats not yet imported. Read each one's source
at a pinned commit before building; these are starting points.

### Gemini CLI

`google-gemini/gemini-cli`; the recording types are in
`packages/core/src/services/chatRecordingTypes.ts` and
`chatRecordingService.ts`, the paths in
`packages/core/src/config/storage.ts`.

- Files: `~/.gemini/tmp/<project>/chats/session-<time>-<id>.jsonl`;
  older releases wrote one `session-*.json` holding the whole record,
  migrated to JSONL on resume. Sub-agents in
  `chats/<parent session id>/<session id>.jsonl` with `kind: "subagent"`.
- Records have no `type`; they are told apart by their keys: a
  metadata record, messages (`type` user, gemini, info, error,
  warning; content as `@google/genai` parts; tool calls with their
  results inside the gemini message; thought summaries; per-message
  token counts), and the mutations `$set`, `$patch` and `$rewindTo`.
- The file is a log to replay: apply `$set` and `$patch` in order, and
  turn each `$rewindTo` into a branch from the message before it.
  Split a gemini message into its items and its calls and outputs;
  info, error and warning messages are UI, not context. No version
  field and no compaction record (compression changes only the
  in-memory history). `/chat save` writes a raw `Content[]` checkpoint,
  a separate thing.
- Note that `~/.gemini` may hold Antigravity instead of Gemini CLI;
  the two share the directory.

### VS Code Copilot Chat

Per-workspace `chatSessions/*.jsonl` under VS Code's
`workspaceStorage` (older releases wrote `.json`). The specification
is VS Code's source (`src/vs/workbench/contrib/chat/common/chatModel.ts`
and the serializer of its JSONL operations).

- A mutation log: the first line is a full snapshot (`kind` 0,
  `version` 3), then `kind` 1 sets a value at a path and `kind` 2
  pushes or splices into an array at a path.
- What it records is the chat UI's turns, not the model's transcript:
  one request with response parts (markdown, serialized tool
  invocations, thinking as display text, edit groups), no call and
  result split, no signatures. A converter replays the log, then
  projects each request onto messages, calls and outputs, lossy by
  nature; say so in the findings.

### Antigravity

Google's agent IDE and its `agy` CLI, under `~/.gemini/antigravity*`.
The IDE's conversation files are encrypted; the CLI keeps one SQLite
database per conversation with steps as protobuf and no published
schema, one step per tool action. The only readable surface is a
flattened `transcript.jsonl` export of user and model text. Not
practical; an importer would carry little beyond that text.

### Not seen

opencode, aider, goose, Cursor, Amp and Continue chat sessions were not
installed where the first sources were surveyed; Copilot CLI kept
configuration only. Each is a candidate once someone has its files and
its source.
