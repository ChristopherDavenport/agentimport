# Changelog

All user-visible changes to this library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/); before v1.0.0 minor
versions may break the API.

## Unreleased

- Dependencies: agentsession v0.0.37 to v0.0.38, format 0.20 to 0.21,
  and openresponses v0.0.18 to v0.0.19; every converted session's
  header names `agentsession/0.21`, and the goldens are regenerated.

- **Fix (format 0.21): Claude Code's `human` is the person's words
  only (agentturn#303's rule).** A prompt's `origin.kind` was the
  item's source kind as written, so a message Claude Code composed
  whose origin said `human` read as the person's. Such a message
  (`isMeta`, a compact summary, or `promptSource` `system`) now has the
  kind `system`, and its origin stays in `claudecode:line`
  (`claudecode/composed-human`). Claude Code was not seen to write one.

- **Fix (format 0.21): an ATIF document of a sidecar session imports
  as a sidecar session.** agentsession v0.0.38's exporter names a
  sidecar session's `media` under `extra.agentsession`, and its raw
  items keep the `sidecar:` references the entry IDs hash over; the
  converter kept that member in `atif:extra`, so the rebuilt session's
  header said inline and its references read as text. The header's
  `media` is now `sidecar`, and the member leaves `atif:extra`.

- **Fix: cache writes are carried in the usage (#37) (breaking: the IDs
  of responses that wrote to the cache, and of every entry after one,
  move).** Each converter folded a response's cache writes into
  `input_tokens` and dropped the count, so a price table priced them as
  plain input. The Claude Code, pi, Cline and Roo, Cline SDK, OpenHands,
  OpenCode and Codex converters now set openresponses extensions §5's
  `openresponses-experimental.cache_write_tokens`, and
  `openresponses-experimental.cache_write_tokens_by_ttl` where the
  source splits it (Claude Code's `cache_creation`, pi's
  `cacheWrite1h`), as `providers/anthropic` does; the new
  `agentimport.CacheWrites` sets them. OpenHands and OpenCode no longer
  keep the count in their `:usage` member, Codex keeps its native usage
  only for members it does not map, and `agentimport/usage-detail` no
  longer says the writes have no place.

- **Fix: a usage whose cache reads and writes exceed its input is
  reported (#37).** openresponses extensions §5 counts both inside the
  input, so such a usage prices its uncached input below zero. Every
  converter keeps the counts as the source reported them and reports
  `agentimport/usage-inconsistent` once per session, naming the first;
  `agentimport.CheckUsage` checks a session built without a `Builder`.
  Codex's synthetic fixture had one (input 130, 100 cached, 40
  written); codex-rs counts the writes in `input_tokens`, so its input
  is now 150 and the IDs of its last entries move.

## v0.0.2 - 2026-11-04

- Dependencies: agentsession v0.0.35 to v0.0.37, format 0.18 to 0.20;
  every converted session's header names `agentsession/0.20`, and the
  goldens are regenerated. openresponses stays at v0.0.18.

- **Fix: ATIF sub-agents no call spawned are children the host started
  (format 0.19) (breaking: their session IDs, the links to them, and
  every entry ID after a link, move).** A reference on a result with no
  call, such as Terminus 2's summarisation helpers, was linked by a
  `subsession` link with neither `call_id` nor `spawn_id`, which
  agentsession v0.0.36 refuses, so such a document failed to convert.
  The link now carries a `spawn_id` the conversion chooses, `step-<n>`
  for the step's line and `step-<n>-2` on for a later one of the same
  step; the child's `spawned_by` is that `spawn_id`, and its ID is
  derived from the parent's and it, as a call's child's is from the
  `call_id`, the native `session_id` kept in `atif:session_id`.
  `atif/callless-subagent` is now `lossy`, no longer a gap, and leaves
  `docs/incompatibilities.md`. The step's line is its place in the
  document, as findings name it, also for a Claude Code sub-agent's
  session made from interleaved steps. The entry that takes the children's
  work in, the compaction a replace boundary becomes (Terminus 2's
  summarisers) or the user message of the result's content, names each
  child's leaf in `parents`, as RFC 0001 asks of a host-started child.

- Fix: every sub-agent an ATIF call's result names is a subsession of
  that call. The second and later were linked as sub-agents no call
  spawned (now host-started children), and a document agentsession's
  exporter wrote gave every child of one call the same session ID. Each
  is now linked by the call's `call_id` before the output, which names
  every child's leaf in `parents`; the first child's ID derives from the
  call's, and a later one keeps its own (`atif/subagent-shared-call`).

- Added: a document agentsession's exporter wrote, mapped from its
  steps, links the children the host started that it carries under a
  step's `extra.agentsession.subsessions` (format 0.19) by their
  `spawn_id`, where before each was a session with `parent_session`
  alone (`atif/host-spawn`, `atif/host-spawn-unresolved`).

- Fix: an ATIF continuation whose predecessor's instruction parts carry
  a keep, or a hash of their own text, keeps them, cleared of both, with
  their sources and trust, since agentsession v0.0.37's
  `ConfigFromRequestParts` refuses a part in a delta's form. Parts that
  cannot be written whole (one the path could not rebuild, a hash of
  another text, a part with no ID or a repeated one, parts that do not
  join to the instructions) no longer fail the conversion: the first
  config carries the instructions string, reported as
  `atif/continuation-parts`. Since agentsession v0.0.37 copies a part's
  trust, which it dropped before, a continuation whose predecessor's
  parts carry trust writes it in its first config, so that entry's ID,
  and every ID after it, moves even where nothing is cleared.

## v0.0.1 - 2026-11-03

- Added: derivation, opt-in (`-derive`, `Options.Derive` for every
  source; `Builder.DeriveAfter`). After a shell call whose output prints
  the commit it made, an `env` entry repeats the one in force, its
  workspace and `vcs` members included, with the revision git printed
  and no `dirty` (`agentimport/commit-revision`). A derived entry carries `agentimport:derived`
  (`agentimport.DerivedMember`) naming its rule and the call it read.
  The first rule writes what a shell call published, the refs a `git
  push` reports and the pull request `gh pr create` prints, as an
  `agentimport:published` custom entry after the call's output with the
  call's `call_id` (`agentimport/published-refs`); `git push` with
  `--dry-run` or `-n` publishes nothing. A session derivation ran on
  names the rules in its header's `agentimport:source` (`derived`),
  since later entry IDs differ. Without `-derive` the output is as
  before.

- Added: the leaf label a converter writes when a session resumes at an
  entry that is not its last, an entry no native line maps to, is
  reported (`agentimport/leaf-label`), and so is an ATIF continuation's
  first config, seeded with the settings its predecessor ended with
  (`atif/continuation-settings`), so the report says what the conversion
  added. The plan lists every structural entry and its code.

- **Added: Claude Code's instruction layers sent as messages carry RFC
  0001's `instructions_part` (format 0.17) (breaking: those entries'
  IDs, and every ID after them, move).** It has the well-known `source`:
  a `nested_memory` or one-file `instructions` attachment names its
  file under `agentsmd`, or `agentmemory` for Claude Code's memory, and
  the skill listing is `agentskill`. A message composed from several
  files names each, in message order, as format 0.18's list
  (`ItemEntry.InstructionsLayers`); one holding a file of an unknown
  type names none and is reported (`claudecode/instructions-layers`).
  Codex's AGENTS.md instructions message names `agentsmd` with the
  directory Codex names, and its skills catalogue `agentskill`. pi's
  `project_context` and `skills` system sections are instruction parts
  with `source` `agentsmd` and `agentskill`. OpenCode 2's `system` row
  for changed instruction files names each file under `agentsmd`. Each
  other source's format page says why it has no layer to name.

- Added: skill reads recorded as agentskill records them, a `custom`
  entry `agentskill:read` (`agentimport.SkillReadNS`,
  `agentimport.SkillRead`) with the skill's `name`, and when the log
  says them its `location`, `bytes` and `sha256` of the text served,
  and the serving call's `call_id`: Claude Code's `Skill` call and the
  hidden message carrying the instructions, and OpenCode 2's user-row
  skills, Codex's `<skill>` parts and OpenHands' `activated_skills`.
  `docs/formats/README.md` documents the shape relied on.

- Added: identity members in the report. A finding of code
  `agentimport/identity` (`agentimport.IdentityCode`,
  `Report.Identity`) names, by its path and never its value, a native
  member carried into the session that identifies a person, an account
  or organization, a machine or the native session; `Finding.Path`
  holds the path and a group's `paths` counts each. Every
  `docs/formats/<source>.md` has an Identity section listing its
  members, which a test holds the report to. The Claude Code converter
  reports its own: `sessionId`, `agentId`, `cwd`, a `credential_org`
  attachment's `organizationUuid`, a `session_context` attachment's
  `userEmail`, an `api-request` record's `classifier_context`
  `user_identity` and `home_dir`, and the paths that become an
  `instructions_part` id or an `agentskill:read` location. A Claude Code
  sub-agent is named in findings by its place (`subagents/agent-#<n>`),
  never its ID. Every other converter reports the native session ID and
  working directory it carries into the header. No report of any
  converter names a session by its ID: findings name a session, and a
  file named for one, by its place (`session #<n>`, `the parent
  session`; `Result.ScrubIdentity`).

- **Changed: an ATIF document agentsession's exporter wrote with its
  entries (format 0.17) is rebuilt to the document's own ids
  (breaking: such an import's entry ids, and a redacted session's
  header, change).** Each entry is put back together from
  `extra.agentsession.entries` and its step, its id checked and, as
  format 0.18 asks, the document's and its step's visible fields held to
  the hashes the export records, the media put back inline first (a
  document without them rebuilds no entry to its id); an entry
  that matches keeps its history members, any other is a new entry,
  listed (`atif/entry-not-rebuilt`), its history members `atif:<name>`.
  The header's `redacted` and `redacted_from` are restored exactly when
  every entry matched over the whole path, which reaches the document's
  leaf with every step naming an entry (`atif/redaction-not-restored`
  says why not; a step that names none is `atif/step-not-rebuilt`). Embedded sub-agent trajectories are rebuilt
  with it, each into the session its own document names. A document
  that continues in another is still mapped from its steps
  (`atif/not-rebuilt`),
  where `failed`, `trust`, `copied_from`, `from_link` and
  `instructions_part` now come back as core members
  (`atif/meaning-restored`) and a response's `legacy_id` and
  `normalised` are `atif:` members, not its own.

- **Changed: a failed call's output carries RFC 0001's `failed` (format
  0.14), not `<ns>:is_error` (breaking for readers of that member).**
  Every source maps its error flag (Claude Code's, Cline's and the
  Cline SDK's `is_error`, pi's `isError`, OpenHands' `is_error` and
  agent errors, OpenCode's `error` status), and an output the harness
  wrote for a call it gave up with an error flag or status of its own
  (the Cline SDK's missing result, OpenHands' interrupted call,
  OpenCode's interrupted or unfinished call). Classic Cline's `result
  missing` carries no flag, so it is not marked. `failed` is never inferred from an
  output's text or an exit status; Codex records no failure, and none
  is read from its abort texts. `agentimport/tool-error` is no longer a
  gap: it is `lossy` where the model was sent a flag a request rebuilt
  from `function_call_output` cannot carry.
- Added: `agentimport:exit_code` (`agentimport.ExitCodeMember`,
  `SetExitCode`), a command's exit status where the source structures
  it (OpenHands' terminal metadata, pi's `bashExecution`, OpenCode's
  `bash` metadata and OpenCode 2's shell rows; `agentimport/exit-code`)
  or wraps it in a fixed header around a shell tool's output (Codex's
  `Exit code:`, `Process exited with code`, and the older structured
  output, for `shell`, `shell_command`, `local_shell`, `container.exec`,
  `exec_command` and `write_stdin`; `codex/exit-code`). It
  is kept under `-slim` and says nothing of `failed`.

- **Changed: Codex `env` entries are whole snapshots, as RFC 0001 0.17
  reads them (breaking: the IDs of `env` entries after a move inside the
  session's directory, and every ID after them, move).** A `turn_context` that moves the working directory inside
  the session's repeats the session meta's `vcs`, reported as a guess
  (`codex/vcs-carried`, lossy), and one that moves outside it writes
  none (`codex/vcs-after-cwd`); before, every later `env` dropped the
  git state. Members of `git` Codex adds are kept as `codex:<name>` vcs
  members (`codex/git-members`); they were lost.

- **Fixed: an OpenCode 2 user row's skills kept only their text
  (breaking: the IDs of user rows with skills, and every ID after them,
  move).** The text is sent; the rest of each element, the skill's name among it, was
  lost unreported. Each element but its text is now `opencode:skills`
  on the item (`opencode/members`, dropped under `-slim`), and a skill
  with no text, which OpenCode does not send, is reported
  (`opencode/skill-untexted`).

- **Fixed: `-slim` drops a Claude Code attachment that rendered
  nothing (breaking: a slim conversion's IDs after one move)** into the
  request (`credential_org`, `deferred_tools_record`), which
  the model never saw, and reports it `dropped`; it kept each as a
  custom entry, carrying the account's organization ID into every slim
  import.

- **Changed: `-strict` fails only on a dropped finding and on a gap
  `docs/incompatibilities.md` does not list (breaking for scripts that
  relied on it failing on every gap).** A listed gap is a known shape
  of the session format and says nothing of one transcript, so every
  conversion failed `-strict` on `agentimport/provenance` alone.
  `-accept code,...` names finding codes `-strict` lets through.
  `agentimport.DocumentedGap` reads the table, embedded in the module.
- **Changed: `agentimport/provenance` and `agentimport/usage-detail`
  are `kept` findings, no longer gaps (breaking for readers of the
  report's kinds).** Both are the converter's or the source's own
  detail in a member under its namespace, where RFC 0001's core test
  puts it, and leave `docs/incompatibilities.md`. Under `-slim`, ATIF's
  usage detail is reported `dropped`.

- **Changed: sessions are written in format 0.18 (breaking: a reader of
  0.17 refuses them, and some entry IDs move)** (agentsession v0.0.35; first v0.0.34, format 0.17,
  openresponses v0.0.18; were v0.0.28 and v0.0.15). No entry hash
  changes but where an item now keeps a member it does not define: an
  Open Responses item keeps every member as written (openresponses
  v0.0.17), so Codex's `internal_chat_message_metadata_passthrough`, a
  message's `encrypted_content` and a call's `namespace` stay in the
  item instead of `codex:item`, and pi's Responses reasoning items keep
  their undeclared members, moving those entries' IDs.
- Fixed: `agentimport convert` wrote a leaf label into a stored session
  before the first entry of each later branch, as agentsession's store
  now marks the head when an append leaves its branch; each append now
  starts from its own parent, so the stored session is the conversion.

- Fixed: `make release` can cut the first release. The release guard
  refused every tag while no `v*` tag was published; it now takes a
  plain `vX.Y.Z` of the module path's major version as the first
  release when origin, read fail-closed, and the local repository hold
  none. Prereleases and other `v*` tags no longer raise the floor, and
  the guard refuses them. `scripts/release-guard-test.sh` tests it
  (`make guard-test`, part of `make check`).

- Added: Hermes Agent (NousResearch/hermes-agent), which keeps every
  session of a profile in SQLite, `~/.hermes/state.db` and
  `~/.hermes/profiles/<name>/state.db`. `hermes` reads its sessions and
  messages rows by column name, as Hermes adds columns as it needs them,
  keeping the columns it does not know, and tells a child session's
  relation to its parent as Hermes does (compression continuation,
  branch, reset, sub-agent, tool session). `hermes/sqlite` reads the
  database from a copy made by `internal/sqlite`. `hermes/convert`
  rebuilds the context a resume sends: the live rows in id order,
  `api_content` where Hermes sent it, its memory blocks stripped,
  background-review harness turns out of context, orphaned outputs and
  unanswered calls left out as its sequence repair drops them. An
  in-place compaction is a compaction entry per generation of rows, a
  split compaction a successor session with a `continued_in` link,
  rewound rows a branch, a branch a session with a `fork_of` link, and a
  delegate_task's sub-agents subsessions, or peers when the call ran
  them in the background, tied to the call by when they started; a
  background run's delivered report names its delegation in the item's
  `source` (kind `delegation`, `ref` the delegation ID). Usage,
  which Hermes records per session and per model only, is kept in the
  header (`hermes/session-usage`, a gap). The command detects a
  database by its tables, or the home or profile directory holding it
  (`-from hermes`), and `make corpus HERMES=~/.hermes` reads a corpus.

- Added: `THIRD_PARTY_NOTICES.md`, the license notices for the code
  ported from OpenHands, OpenCode, Cline, Roo Code, Codex and pi, and
  credits for the behaviour matched from the AI SDK and Harbor. Each
  file holding a ported portion names its source in a comment at its
  top. The README gains a License section.
- Added: the Cline SDK (Cline 4: its CLI, the VS Code extension from
  4.0, JetBrains), which keeps each session in a directory under
  `~/.cline/data/sessions` and indexes them in SQLite. `clinesdk` reads
  a session directory, its manifest, its versioned messages file (or the
  bare array from before the version), the latest compaction's sidecar
  and its sub-agents' and team tasks' histories, keeping every member it
  does not declare, and Cline's JSON index; it ports the sidecar's
  source-prefix hash, which `internal/jsjson` makes reproducible by
  holding values as JavaScript's JSON.parse does and writing them as
  JSON.stringify does. `clinesdk/sqlite` reads `sessions.db`, which alone
  records which agent spawned a sub-agent, from a copy made by
  `internal/sqlite`. `clinesdk/convert` rebuilds what each request sent:
  MessageBuilder's rewrites where the files determine them (user text
  unwrapped, tool results cut to 8,000 characters, file blocks framed,
  unanswered calls answered, outdated reads replaced from the request
  its batching commits them, as context edits), the sidecar's compaction
  where Cline applies it, sub-agents as subsessions of the
  `spawn_agent` call matched to them and team tasks as peers of their
  `team_run_task` calls, forks and classic tasks carried on by the VS
  Code extension as copied history named in `parent_session`. The
  command detects a session by its directory or its files
  (`-from clinesdk`); `make corpus CLINESDK=~/.cline/data`.
- Added: OpenCode (`anomalyco/opencode`), all three ways it has stored
  sessions under its data directory: the `storage/` files (up to
  1.1.65), the V1 tables of `opencode.db` (1.2 to 1.19) and its V2
  tables (2.0 on). `opencode` reads the files and decodes every record,
  keeping the members it does not declare; `opencode/sqlite` opens the
  database, a copy of it and its write-ahead log so the original is
  never written and committed frames not yet checkpointed are read, and
  retries a copy a running OpenCode changed under it. It is the one
  package that imports a database driver, `modernc.org/sqlite`
  (v1.59.0, Go 1.25), which `make deps` now enforces through
  `scripts/check-deps.sh`; the module's go line reads `go 1.25.0`.
  `opencode/convert` converts each session once, from its most faithful
  copy: the V1 tables over the storage files they were imported from;
  a session OpenCode 2 migrated and carried on is two, the V1 record
  and OpenCode 2's own, which starts from the migration's copy (told by
  the migration's own marks) and names the V1 session as its parent. It
  rebuilds what each request sent as the release that made it did,
  guessed from the creating release and the request's time and
  reported: V1's `filterCompacted` and
  `toModelMessages`, an assistant message split into one response per
  step as the AI SDK's `convertToModelMessages` splits it, failed turns
  left out by `omit`, compaction cuts left out by `omit` and a tail
  moved after the summary (1.14.40) as a compaction (the gap
  `agentimport/fold-head` now names OpenCode), pruned outputs and
  another model's reasoning (the gap `agentimport/context-replace`);
  V2's history from the latest compaction, its rows rendered as
  `toLLMMessages` does. Usage folds cache reads and writes into input,
  output counting reasoning up to 1.3.15, each step's rule read from its
  total where that settles it. Anthropic, OpenAI and Gemini
  reasoning metadata map as the provider packages read them. Task and
  subagent calls link their child sessions, a subsession for the one
  foreground call that ran a child and a peer otherwise; refused calls
  are reject decisions, calls that never finished reject or answer
  decisions; an OpenCode 2 fork names its origin. The command detects a
  data directory, a file in one or a database (not Crush's
  `.opencode/opencode.db`), converts a store whole, and `-from opencode`
  names it.
- Added: OpenHands, the software agent SDK's conversation directories
  (SDK 1.0 to 1.55: the OpenHands CLI, the agent server, Agent
  Canvas). `openhands` reads a conversation as OpenHands does, events
  in index order up to the first gap, keeping every member it does not
  declare. `openhands/convert` rebuilds the context OpenHands built at
  each request, as the release it takes the conversation for built it
  (told by the members the files hold, and reported): the path to the
  leaf through the event tree (1.31 and later), condensations, and the
  view's properties, at every request before 1.17 and from 1.17 only
  at the reloads the files show, the difference written as `omit`
  config entries; a
  condensation with a summary is a compaction pinning the head
  OpenHands kept ahead of the summary (the gap `agentimport/fold-head`,
  now naming OpenHands). Tool results are rendered as each observation
  class's `to_llm_content` renders them, ported from the SDK, with the
  cuts the tools and OpenHands make. Usage, model and latency are joined
  from `base_state.json` by response ID, the sub-agents' copies left
  out; rejections and refused calls are reject decisions, an
  interrupted call an answer, a critic's result an eval outcome.
  Sub-agents under `subagents/` are matched to their task or delegate
  calls by prompt and time: a task sub-agent one call waited for is a
  subsession, one that a later call resumed, or a delegate sub-agent,
  outlives its calls and is a peer of each. A fork
  names its source in `parent_session`. The command detects a
  conversation by its directory or files, `-from openhands` names it,
  and a sub-agent's directory given beside its parent is skipped when
  the parent's conversion holds it.
- Added: classic Cline (the VS Code extension before 4.0) and Roo Code
  task directories, one format with a variant told by the storage
  directory or, failing that, by shape. `cline` reads a task's JSON
  arrays element by element, keeping every member it does not declare,
  and its index entry from `history_item.json` or
  `state/taskHistory.json`. `cline/convert` rebuilds what each request
  sent: classic's deleted range as an `omit` config entry at the
  request that first applied it (messages 0 and 1 in place, the first
  message after the cut without its tool results), and Roo's three
  condense eras and sliding window, as the release that wrote the file
  built them, each a compaction at the time it happened whose summary
  is Roo's text and whose pinned items are the entries Roo sent with
  it, the calls a 3.36 to 3.42 summary carries among them. Classic's
  in-place context edits, and the `result missing` Cline sends for
  calls a range cut, are reported under `agentimport/context-replace`;
  Roo's first message sent ahead of a summary is the new gap
  `agentimport/fold-head`. Request records give each response its
  usage, by the provider's and the Roo release's meaning of the input
  count, once per request however often it was retried or resumed;
  calls with no response keep their usage as a custom entry's top-level
  `usage`. Refusals as Cline and Roo write them are reject decisions;
  XML-era tool calls stay text, parsed into `cline:xml_tools`; a Roo
  sub-task the parent waited for is a subsession, converted with it. The command detects a task by its
  files' names, `-from cline` names it, and a Roo sub-task given beside
  its parent is skipped when the parent's conversion holds it.
- Added: ATIF, Harbor's Agent Trajectory Interchange Format (v1.0 to
  v1.8), the format Harbor's agents and `agentsession/export` write.
  `atif` reads a document block by block, so a bad step, call, result
  or part is a finding and its neighbours still read, keeps every member
  it does not declare and every number as written, and salvages a
  document cut short. `atif/convert` converts it: leading system steps
  are the instructions, results are the outputs of the calls they name
  (or user messages), a `replace` context boundary is a compaction, as
  ATIF's RFC has it, sub-agents referenced from a call's result are
  subsessions, those referenced from no call are linked with no
  `call_id` (a gap), embedded trajectories no step names are sessions
  with `parent_session`, continuations are sessions the previous
  one is `continued_in`, and the sub-agent steps Harbor's Claude Code
  adapter interleaves leave the main session for their own. A document agentsession's exporter wrote is
  rebuilt from the raw items it carries, its compaction's kept window
  put back before it. Related files are read inside the document's
  directory alone, each once. The command detects ATIF by its
  `schema_version`, converts a document with the files it names and
  skips those as inputs. Four new gap codes are in
  `docs/incompatibilities.md`.
- Added: `Report.Unit` names what a tally counts when it is not lines;
  ATIF's counts each document's root and steps.

- Added: Codex sub-agents are linked. A thread converts with the
  threads it spawned, at any depth, each a session of its own after its
  parent's, and the parent gets a `peer` link after each `spawn_agent`
  output naming the thread it started, read from the output
  (`multi_agent_v1`, 0.98's collab tool), the `collab_agent_spawn_end`,
  `sub_agent_activity` or `item_completed` record beside it, or, for
  MultiAgentV2's output alone, the child spawned at the agent path it
  names. `spawn_agent` returns once the agent starts and the agent works
  on until closed, so by the RFC's lifetime rule it is a peer. A child
  that cannot be converted is a finding and costs only itself.
  `codex.IndexDir`, `codex.IndexFlat`, `codex.IndexFor`,
  `codex.ReadMeta` and `codex.SessionsRoot` index a sessions tree by
  each rollout's session meta line; `ConvertFile` indexes the tree a
  rollout is in, and `Options.Index` and `Options.Alone` choose
  otherwise.
- Changed: the command writes each session of a conversion on its own:
  a session already in the store is kept, listed, and is no failure
  unless `-force` replaces it, and a session that fails to write does
  not stop its siblings, so converting a tree again adds what is new.
  It indexes each Codex sessions tree once, and converts a Codex
  sub-agent's rollout with an ancestor among its inputs, or on its own
  when the ancestor's conversion did not produce it.
- Changed: a Claude Code background agent's or teammate's `peer` link
  now follows the call's output, which is the CLI's acknowledgement,
  rather than preceding it: RFC 0001 reads an output that follows a link
  as the callee's answer.
- Changed: `codex/subagent-unlinked` now marks only a sub-agent
  converted without its parent, or whose parent's rollout names no spawn
  of it; new codes `codex/subagent-missing`, `codex/subagent-foreign`,
  `codex/subagent-rollouts`, `codex/subagent-failed`,
  `codex/spawn-unresolved`, `codex/index-failed` and
  `codex/index-skipped`.
- Fixed: `claudecode.FindSessions` skipped every directory named
  `subagents`, `tool-results` or `memory`, wherever it was, so a
  project or fixture directory of that name, or a root of that name,
  hid its transcripts. It now skips only a session's own directory, the
  `<session-id>/` beside its transcript.
- Fixed: an ATIF document agentsession's exporter wrote from a redacted
  session no longer yields redacted entries. The response body the
  exporter carries held the entry's `redacted` member, which the
  rebuild passed through, so the converted session, not a redaction,
  held an entry format 0.13 reads as redacted. That member is now
  `atif:redacted` on the response entry, and the steps' redaction marks
  (`extra.agentsession`'s `redacted` and `redacted_entries`, which the
  rebuild dropped) are `atif:extra`; new code `atif/redacted` (kept, or
  dropped under `-slim`). No converter writes the `redacted` member.
- Dependencies: agentsession v0.0.28, format 0.13: converted sessions
  are written with `agentsession/0.13` headers, so a reader of 0.12
  refuses them until it is upgraded. Its jsonl store (since v0.0.26)
  refuses a session ID or working directory that would put a file
  outside its root; the command's own check, which did the same, is
  gone. v0.0.28, up from v0.0.27, makes a redacted session hash
  independently: a redactor gives each redacted response the
  `request_hash` of the request the redacted session rebuilds for it,
  so readers verify and replay it with no exception for redacted
  entries. The exporter test's redacted session is made that way, with
  `SetRequestHash`, and its response verifies. openresponses stays at
  v0.0.15, its current release.
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
