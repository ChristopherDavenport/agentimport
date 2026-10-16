# Changelog

All user-visible changes to this library. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
uses [Semantic Versioning](https://semver.org/); before v1.0.0 minor
versions may break the API.

## Unreleased

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
