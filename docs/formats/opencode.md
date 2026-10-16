# OpenCode

OpenCode (`anomalyco/opencode`; `sst/opencode` redirects there), the
TypeScript coding agent, keeps every session of a user in one data
directory, `$XDG_DATA_HOME/opencode` (`~/.local/share/opencode`), and
has stored them three ways:

| Generation | Releases | Store | Message model |
| --- | --- | --- | --- |
| G1 | 0.x to 1.1.65 | `storage/`, one JSON file per session, message and part | V1: `MessageV2` info and parts |
| G2 | 1.2.0 to 1.19 (the V1 line, still released) | `opencode.db`, tables `session`, `message`, `part` | V1, the same JSON in the rows' `data` |
| G3 | 2.0.0 on | the same `opencode.db`, tables `session_v2`, `session_message` | V2: one row per message, content inline |

The packages are `opencode`, which reads the storage tree and decodes
every record, `opencode/sqlite`, which opens the database and yields its
rows as `opencode`'s types, and `opencode/convert`, which converts.
`opencode` imports the standard library alone; `opencode/sqlite` is the
one package of this module that imports a database driver,
`modernc.org/sqlite`, a SQLite written in Go. The converter does not
import it either: `convert.Options.OpenDatabase` takes
`sqlite.OpenStore`, as the command passes it.

Not read here: Crush (`charmbracelet/crush`), once the Go program named
opencode (`opencode-ai/opencode`), whose per-project
`.opencode/opencode.db` holds tables `sessions`, `messages` and `files`
(detection opens a database's schema and passes it over); OpenCode 2's
preview `opencode-next.db`; and the layout OpenCode wrote before its
MessageV2 model, `project/<dir>/storage/session/{info,message,part}`,
which OpenCode 1.1 moved into `storage/` on start and which is reported
(`opencode/legacy-layout`), not converted. A message file of that
model's shape (its parts inline) is reported malformed.

## Where the format is specified

Nowhere but the source. This converter was written against
`anomalyco/opencode` at `a448d5b` (`dev`, 2.0.30, 2026-10-15) and the
tags `v2.0.0`, `v1.19.1`, `v1.14.40`, `v1.14.39`, `v1.14.19`,
`v1.14.18`, `v1.3.16`, `v1.3.15`, `v1.2.0` and `v1.1.65`, and against
`vercel/ai` at `ai@6.0.168` (the AI SDK the V1 line pins at 1.19.1),
`ai@5.0.124` (1.1.65's), `@ai-sdk/anthropic@3.0.111`,
`@ai-sdk/openai@3.0.88` and `@ai-sdk/google@3.0.73`:

- `packages/opencode/src/storage/storage.ts` (1.1.65): the file layout
  and its read order; `storage/json-migration.ts` (1.2.0): the import of
  the files into the tables;
- `packages/core/src/session/sql.ts` (1.19.1) and
  `packages/core/src/database/schema.gen.ts` (2.0.30): the tables;
- `packages/opencode/src/session/message-v2.ts` (each V1 tag):
  `filterCompacted` and `toModelMessages`, which rebuild a request;
  `session/compaction.ts`, `session/processor.ts`, `session/prompt.ts`,
  `session/revert.ts`, `tool/task.ts`, `core/src/v1/permission.ts`;
- `packages/ai/src/ui/convert-to-model-messages.ts` (AI SDK): how one V1
  assistant message becomes several model calls;
- `packages/schema/src/session-message.ts`,
  `core/src/session/history.ts`, `core/src/session/runner/to-llm-message.ts`,
  `core/src/session/projector.ts` (forks, reverts),
  `core/src/tool/plugin/subagent.ts`, `core/src/session/usage.ts` (2.0.30):
  the V2 rows, how a request is rebuilt, sub-agents and usage;
- `packages/core/src/database/v1-migration.bun.ts` (2.0.30): how
  OpenCode 2 copies the V1 tables into its own.

The only version a session records is `version`, the release that
created it. Behaviour that changed across releases is keyed on it, and a
session plainly resumed by a later release is reported
(`opencode/version-resumed`).

## Layout

```
~/.local/share/opencode/
  opencode.db (-wal, -shm)                         G2 and G3; WAL mode
  opencode-<channel>.db                            a dev build's database, its own store
  storage/session/<project>/<session id>.json      G1: Session.Info
  storage/message/<session id>/<message id>.json   MessageV2.Info
  storage/part/<message id>/<part id>.json         MessageV2.Part
  storage/project/<project>.json                   worktree, vcs
  storage/session_diff, share, todo, permission    not read
```

A store converts whole: one conversion per data directory or database,
every root session followed by its sub-agents' sessions. The command
takes a data directory, any file in it (a storage file, `opencode.db`)
naming the directory, or a database file elsewhere.
`opencode.FindSessions` lists each data directory (one holding
`opencode.db` or `storage/session`), each `opencode-<channel>.db` and
each such database outside a data directory; the command keeps those
whose database holds OpenCode's tables.

## Reading

- **Order.** G1 messages by file name, their IDs ascending in time; G2
  messages by `time_created`, then ID, as OpenCode's paged read gives
  them; parts by ID in both; V2 rows by `seq`, never by time.
- **The database.** SQLite creates `-wal` and `-shm` files beside a WAL
  database even for a read-only connection, and the last connection to
  close may checkpoint, so `opencode/sqlite` never opens it in place:
  it copies the database and its `-wal` to a private temporary directory
  and opens the copy, which SQLite recovers as a crashed writer's, every
  committed frame of the log included. A running OpenCode keeps
  writing: the database file's size and time and the log's header
  (which a checkpoint that restarts the log rewrites) must not change
  while it copies, else it retries five times, 200 ms apart, and fails.
  Detection reads a database's schema opened `immutable`, which takes no
  lock and writes nothing.
- **Lenient decoding.** Every object keeps the members it does not
  declare, and a declared member of another JSON type is kept as
  written (`opencode/mistyped`), so a record decoded and encoded again
  is the same JSON value. A file or row that is not JSON is
  `opencode/malformed`, skipped; its message's parts are left out.
- **Copies.** One session ID may be in the storage files, the V1 tables
  and the V2 tables at once: OpenCode 1.2 imports the files into the
  tables when `opencode.db` does not exist and never deletes them;
  OpenCode 2 copies each V1 session into its tables and never deletes
  the V1 tables. Each session converts once, from the copy closest to
  what its model saw: the V1 tables over the storage files, which no
  later release writes (`opencode/duplicate-store`, or
  `opencode/duplicate-diverged` when the files hold messages the tables
  do not); the V1 record over OpenCode 2's copy of it, which keeps one
  assistant row per V1 message and drops step boundaries, per-step
  usage, retries and snapshots. The V2 rows past the copy, those up to
  the last whose ID is a V1 message's, and the system row the migration
  appends, are rows OpenCode 2 wrote itself: they continue the session
  from its V1 leaf (`opencode/generation-join`).

### Units

The tally counts records: each storage file of a session, a message or
a part, and each row of `session`, `message`, `part`, `session_v2`,
`session_message`, `session_pending` and `session_inbox`. A record of
no session (a message of a session with no record, a part of a message
not read) is `opencode/orphan`. A finding's `File` is a storage file's
path under the data directory, or `<database>:<table>/<id>`.

## What the model saw

### OpenCode 1 (G1, G2)

Every step re-reads the session, cuts it with `filterCompacted`, and
renders what is left with `toModelMessages`, which builds AI SDK UI
messages that the SDK's `convertToModelMessages` turns into model
messages. The converter follows each rule:

- **Steps.** One assistant message holds every step of a turn: parts
  between `step-start` parts. `convertToModelMessages` splits the
  message at each `step-start` into an assistant message (its text,
  reasoning and calls, provider-run calls with their results) and a
  tool message (the other calls' results), parts before the first
  `step-start` a block of their own. Each block is a model call: its
  items, a `response` (usage and finish reason from its `step-finish`),
  then its calls' outputs. A step with no `step-finish` did not finish
  (`opencode/step-unfinished`).
- **What a message sends.** A user message's text unless ignored or
  empty, synthetic text included; files but `text/plain` and
  directories, which were expanded into synthetic text when the prompt
  was made (`opencode/file-expanded`); `What did we do so far?` for a
  compaction part; `The following tool was executed by the user` for a
  subtask. Agent parts and the rest are not sent (`opencode/unsent-part`).
  A message that sends nothing is skipped (`opencode/message-empty`). An
  assistant's empty text beside signed Anthropic reasoning is sent as a
  space (`opencode/text-space`, 1.19's rule).
- **Failed turns.** An assistant message with an error is skipped in
  every later request, but for an abort after output other than
  reasoning. Its items are written and left out from then on by an
  `omit` of their entries (`opencode/error-turn`).
- **Calls.** A completed call's output, with data-URL attachments as
  media; an error's text, flagged (`opencode:is_error`); a call cleaned
  up after an abort (`metadata.interrupted`) sends the output it had
  (`opencode/call-interrupted`). A call still `pending` or `running`
  sends `[Tool execution was interrupted]`: written after a `reject`
  decision by `policy` for one that never started, an `answer` for one
  that may have run (`opencode/call-unfinished`). For a provider whose
  AI SDK package takes no media in a tool result, OpenCode moves the
  media to a user message after the assistant's; that depends on the
  package and is reported, not reproduced (`opencode/media-relocated`).
- **Pruned outputs.** OpenCode's prune sets `time.compacted` on old
  outputs; every request after it sends `[Old tool result content
  cleared]` instead. The output stays in context as it was first sent,
  and a custom entry `{"pruned": [call IDs], "replacement": …}` stands
  before the first message created after the prune
  (`opencode/tool-output-pruned`, gap `agentimport/context-replace`).
- **Reasoning not sent back.** `@ai-sdk/anthropic` sends a thinking
  block only with its signature or redacted data, and `@ai-sdk/openai`
  a reasoning item only with its item ID or encrypted content; the rest
  they drop. Such reasoning is written and left out by `omit` after its
  own step (`opencode/reasoning-unsent`).
- **Model switches.** A request's model differs from an earlier
  message's: OpenCode sends that message's reasoning without its
  provider metadata (1.1) or as plain text (1.19). The session sets
  `omit: {reasoning: other_models}` at the first switch, so a replay
  sends no other model's signature (gap `agentimport/context-replace`).
- **Compactions.** A user message with a `compaction` part and the
  assistant `summary` answering it, finished without an error, cut the
  history. Walking back from the newest message, `filterCompacted`
  stops at the request of the latest such compaction; from 1.14.19 it
  walks on to the message the part's `tail_start_id` names; from
  1.14.40 it moves that tail after the summary. A compaction entry
  reproduces the result: its summary is the first message OpenCode then
  sends (the request, or, from 1.14.19 to 1.14.39, the tail's first
  message), the rest kept from `first_kept` when it lies on the path in
  that order, pinned when it does not (`opencode/compaction`, gap
  `agentimport/fold-head`). The summary's own request sent the
  compaction agent one user message, the conversation serialized as
  text, not the path's context (`opencode/compaction-request`). A
  failed summary cuts nothing (`opencode/compaction-incomplete`).
- **Staged reverts.** `session.revert` hides a message and those after
  it, or a message's parts from one on, until the next prompt deletes
  them. Hidden messages are on a branch the session does not resume;
  hidden parts are left out (`opencode/revert-staged`).
- **Not stored.** The system prompt and the tool list are built per
  request (`opencode/system-prompt`); a user message's `system` text was
  added to it (`opencode/user-system`); plan-mode reminders are added in
  memory (`opencode/reminder-unrecorded`); the last allowed step's
  prefill is not stored.

### OpenCode 2 (G3)

`SessionHistory` reads the rows from the latest completed compaction on,
by `seq`, and `toLLMMessages` renders each:

- `user`: its skills' texts, its text, then its attachments: `text/plain`
  and directories inline as text under an `Attached file:` or
  `Attached directory:` header, images and PDFs as media (an image
  attached twice under one mention once), other types not sent
  (`opencode/attachment-mime`).
- `synthetic` and `skill`: user text; `system`: a system message;
  `shell`: user text giving the command and its output, but for a
  background command, whose result comes back later
  (`opencode/shell-background`); `location-switched`: user text saying
  the directory changed, and an `env` entry.
- `assistant`: one model call. Its text, reasoning and calls; then each
  call's result, for those `completed` or `error` alone: a call still
  `streaming` or `running` is sent with no result
  (`opencode/call-unfinished`). A failed call sends
  `{"error":…,"content":…}` as JSON text. An assistant row with an error
  is sent all the same, its reasoning as text, as is interrupted
  reasoning (gap `agentimport/context-replace`; the items stay as the
  model wrote them).
- `compaction`, completed: a user message
  `<conversation-checkpoint>…<summary>…</summary><recent-context>…</recent-context></conversation-checkpoint>`,
  the compaction's summary, keeping nothing before it. A provider's own
  checkpoint (`providerContext`) is its messages, carried as an
  `opencode.native_checkpoint` item (`opencode/native-checkpoint`). A
  running or failed one cuts nothing.
- `agent-switched`, `model-switched`: settings; `idle`: nothing, a
  custom entry. A switch of a model's variant sends an effort marker
  of OpenCode 2's own (`opencode/effort-marker`).
- Staged reverts hide the row they name and those after it, as in V1.
- Queued input, `session_pending` and `session_inbox`, is not in the
  history: custom entries (`opencode/pending-input`).

## Mapping

| Native | Session | Notes |
| --- | --- | --- |
| session record | session `id` the native `ses_…` (derived when a store would refuse it, `opencode/session-id`) | `created_at` its creation time; `cwd` `directory`; `harness` `opencode` and `version`; `parent_session` its parent, or a V2 fork's origin; header member `opencode:session` (the record but what the header maps) and `agentimport:source` |
| the first request's model, provider, agent and variant | the first `config`: `model`, members `opencode:provider`, `opencode:agent`, `opencode:variant` | a later change is a delta |
| `title` | an `info` entry | |
| project with `vcs` `git` | an `env` entry, `vcs.system` `git` | `opencode:worktree` when not the directory |
| V1 user message | `item`, user `message` of the parts sent | hidden (`visible: false`) when OpenCode wrote it: synthetic text, a compaction or subtask request; `opencode:id`, `opencode:message` (the info but its envelope), `opencode:unsent` (the parts not sent), `opencode:synthetic`, `opencode:subagent_result` (a background task's result) |
| V1 assistant step | items, then a `response` (`response_id` `<message id>:<step>`), then outputs | items carry `opencode:id` (the part) and `opencode:part`; the response `opencode:finish`, `opencode:step_start`, `opencode:step_finish` (snapshots), `opencode:parts` (parts not sent: patches, retries), and on the last step `opencode:message_id`, `opencode:message`, `opencode:error` |
| V1 `tool` part | `function_call` (`call_id` the part's `callID` through `agentimport.CallIDs`, `name` the tool, `arguments` the input), its output after the response | `opencode:state` (title, metadata, times); a renamed call `opencode:call_id` |
| V1 task call run by OpenCode for a subtask | its items with no `response` (`opencode/subtask`) | |
| V2 row | as above; `assistant`: items, a `response` (`response_id` the row's ID), outputs | `opencode:id`, `opencode:type`, `opencode:row` (the data but what is mapped), `opencode:content` (content not sent) |
| a refused call: V1 error text of PermissionV1 (`opencode/permission-text`, told by the text), V2 `permission.rejected` or `aborted` "The user declined this tool call" (`opencode/permission-rejected`) | a `reject` `decision` (`by` `human`, or `policy` for a rule), then the output | approvals are not recorded |
| `snapshot`, `patch`, `retry` parts, V2 `snapshot`, `retry` | members, as above | dropped with Slim |

### Payload

- **Reasoning.** V1 keeps the AI SDK's provider metadata per namespace
  (`{"anthropic": {"signature": …}}`), V2 the bare record of the session's
  provider (a row its migration copied keeps the V1 form,
  `opencode/state-namespaced`). Checked against the provider packages
  pinned: Anthropic's `signature` goes in `encrypted_content`, as
  `openresponses/providers/anthropic` does, and its `redactedData` is the
  `anthropic.redacted_thinking` item; OpenAI's `itemId` is the item's
  `id` and `reasoningEncryptedContent` its `encrypted_content`, the
  parts of one item ID merged into one item with a summary each, as
  `@ai-sdk/openai` sends them; Gemini's `thoughtSignature` on a reasoning
  part is its `encrypted_content`, and on a text or a call a reasoning
  item holding only `encrypted_content` just before it, as
  `providers/gemini` reads one. `opencode:provider` names the namespace
  (gap `agentimport/reasoning-provider`).
- **Text.** OpenAI's `itemId` and `phase` are the message's `id` and
  `phase`.
- **Files.** A data URL inline: an image as `input_image`, anything else
  as `input_file` `file_data`; another URL by reference, which the
  provider fetched (`opencode/file-url`).
- **Provider-run tools** (V1 `providerExecuted`, V2 `executed`): an
  `opencode.hosted_tool` item holding the part, in the response
  (`opencode/hosted-tool`).

### Usage

OpenCode's `tokens.input` counts neither cache reads nor writes, so both
are folded into `input_tokens`, `cached_tokens` the reads, as
`providers/anthropic` does. `tokens.output` counts reasoning up to
1.3.15 and leaves it out from 1.3.16 (and in OpenCode 2): `output_tokens`
is `output`, or `output + reasoning`, by the session's `version`.
Cache writes, the cost and OpenCode's `total` are `opencode:usage` (gap
`agentimport/usage-detail` when there are cache writes). V1 usage is per
step, from `step-finish`; the message's own tokens and cost (the last
step's, the steps' sum) stay in `opencode:message`.

### Sub-agents

A V1 `task` call names its child in its state's `metadata.sessionId`
(or input `task_id`, continuing one); a V2 `subagent` call in
`metadata.sessionID` (or input `sessionID`). The child is a session of
the store whose `parent_id` is the caller's.

- A foreground call that alone ran its child: a `subsession`. The child
  converts as `agentsession.SubsessionID(parent, call_id)` with
  `parent_session` and `spawned_by`; the link stands before the call's
  output, whose `parents` names the child's leaf (`opencode/subagent`).
- A call run in the background, or a child several calls ran: a `peer`
  of every call, the child under its own ID with `parent_session`, a
  link after each call's output, which is OpenCode's acknowledgement
  (`opencode/subagent-peer`). A background result comes back as
  synthetic user text (V1, `opencode:subagent_result`) or a `synthetic`
  row (V2).
- A child no call names: its own session, `parent_session` alone
  (`opencode/subagent-unlinked`). A call naming a session the store does
  not hold: `opencode/subagent-missing`. Sub-agents nest to 16 sessions;
  with `Options.Alone`, or past that, a child converts on its own
  (`opencode/subagent-alone`). Parents that form a cycle are broken
  (`opencode/parent-cycle`).

### Forks

An OpenCode 2 fork has `fork_session_id` and `fork_boundary`: its
session names the origin in `parent_session` and opens with a
`fork_of` link (`opencode:fork` the boundary). Its first rows, IDs
`msg_<event>_<seq>`, are the origin's copied: converted as its own, each
response's usage in `opencode:copied_usage` and off the entry
(`opencode/fork-copied`, `opencode/fork-usage`). An OpenCode 1 fork
records no origin: told by its title, `… (fork #n)`, and reported
(`opencode/fork-unlinked`), its copied usage counted again.

## Extension types

| Extension | Kind | Written for |
| --- | --- | --- |
| `anthropic.redacted_thinking` | item | Anthropic's redacted thinking, `{"block": {"type": "redacted_thinking", "data": …}}`, as openresponses' Anthropic provider emits it |
| `opencode.hosted_tool` | item | a tool the provider ran, `{"part": …}` (V1) or `{"tool": …}` (V2) |
| `opencode.native_checkpoint` | item | a provider's own compaction checkpoint, `{"provider_context": …}`, as a compaction's summary |

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead. The codes
every converter can report are in [README.md](README.md).

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/context-replace` | gap | OpenCode replaced an item in later requests: a pruned output, another model's reasoning, a failed V2 step's reasoning sent as text. The item stays as first sent. |
| `agentimport/fold-head` | gap | OpenCode's compacted context starts with a message it sent verbatim; the compaction's summary is that message and the rest follows it. |
| `agentimport/reasoning-provider` | gap | A reasoning signature or encrypted content is in `encrypted_content`, its provider in `opencode:provider`. |
| `agentimport/tool-error` | gap | A failed call's output carries `opencode:is_error`. |
| `agentimport/usage-detail` | gap | Cache writes, folded into input, and the cost are `opencode:usage`. |
| `opencode/attachment-mime` | dropped | An OpenCode 2 attachment of a type it does not send. |
| `opencode/attachment-repeated` | lossy | An image attached twice under one mention, sent once. |
| `opencode/call-id` | repaired | A call ID that is empty, malformed or repeated is renamed; the native one is `opencode:call_id`. |
| `opencode/call-interrupted` | lossy | A V1 call cleaned up after an abort sends the output it had. |
| `opencode/call-unfinished` | lossy | A call that never finished: V1 sends a placeholder output, after a reject or answer decision; V2 sends none. |
| `opencode/compaction` | lossy | A completed compaction, and how its context is expressed. |
| `opencode/compaction-incomplete` | lossy | A compaction that did not complete cuts nothing. |
| `opencode/compaction-marker` | kept | A compaction that keeps nothing names a custom marker as `first_kept`. |
| `opencode/compaction-orphan` | lossy | A summary message that answers no compaction request cuts nothing. |
| `opencode/compaction-request` | lossy | A compaction's summary request sent the conversation serialized, not the path's context. |
| `opencode/content-malformed` | kept, or dropped | An OpenCode 2 content item that is no item, kept in `opencode:content`. |
| `opencode/created-at` | lossy | The session records no creation time; `created_at` is its first record's. |
| `opencode/custom` | kept, or dropped | A record that is not context, as a custom entry. |
| `opencode/duplicate-diverged` | lossy | A copy of a session not converted holds messages the converted copy does not. |
| `opencode/duplicate-store` | dropped | A copy of a session not converted: a storage file the database holds, or OpenCode 2's migration copy. |
| `opencode/effort-marker` | lossy | OpenCode 2's marker of a change of reasoning effort is not held. |
| `opencode/empty-text` | dropped, or kept | An empty text, which OpenCode does not send. |
| `opencode/error-turn` | lossy | A V1 assistant message that ended in an error, left out of later requests by `omit`. |
| `opencode/file-expanded` | kept, or dropped | A text or directory file part OpenCode expanded into text and does not send, in `opencode:unsent`. |
| `opencode/file-url` | lossy | A file sent by a URL that is not a data URL. |
| `opencode/fork-copied` | lossy | An OpenCode 2 fork's copied rows, converted as its own. |
| `opencode/fork-unlinked` | lossy | An OpenCode 1 fork, which records no origin. |
| `opencode/fork-usage` | kept | A copied response's usage is `opencode:copied_usage`. |
| `opencode/generation-join` | lossy | A session OpenCode 2 carried on after migrating it: its V1 record, then the rows OpenCode 2 wrote. |
| `opencode/hosted-tool` | kept | A tool the provider ran is an `opencode.hosted_tool` item. |
| `opencode/ignored-text` | kept, or dropped | An ignored text part, which OpenCode does not send, in `opencode:unsent`. |
| `opencode/legacy-layout` | dropped | A directory of the layout before MessageV2, not read. |
| `opencode/malformed` | repaired | A file or row that cannot be read is skipped. |
| `opencode/media-relocated` | lossy | Tool-result media OpenCode may have moved to a user message for the provider. |
| `opencode/members` | kept, or dropped | Native members the session has no place for, in a namespaced member. |
| `opencode/message-empty` | kept, or dropped | A message OpenCode sends nothing of, a custom entry. |
| `opencode/migration-notice` | lossy | OpenCode 2's migration told the model of renamed tools in a system message. |
| `opencode/mistyped` | repaired | Members of another JSON type than OpenCode writes, kept as written and read as absent. |
| `opencode/native-checkpoint` | kept | A provider's own compaction checkpoint is an `opencode.native_checkpoint` summary. |
| `opencode/orphan` | dropped | A record that belongs to no session or message read. |
| `opencode/parent-cycle` | repaired | Sessions whose parents form a cycle; one converts with no parent. |
| `opencode/pending-input` | kept, or dropped | OpenCode 2's queued input, not yet in the history. |
| `opencode/permission-rejected` | lossy | An OpenCode 2 call the user declined is a reject decision. |
| `opencode/permission-text` | lossy | A V1 call refused, told by its error text alone, is a reject decision. |
| `opencode/reasoning-unsent` | lossy | Reasoning the AI SDK's Anthropic or OpenAI provider does not send back, with no signature or item ID, left out by `omit` after its own request. |
| `opencode/redacted-thinking` | kept | Anthropic's redacted thinking is an `anthropic.redacted_thinking` item. |
| `opencode/reminder-unrecorded` | lossy | Plan-mode reminders OpenCode adds in memory are not in the requests. |
| `opencode/revert-staged` | lossy | A staged revert: what it hides is on a branch not resumed, or left out. |
| `opencode/session-failed` | dropped | A session that could not be converted; its records are dropped. |
| `opencode/session-id` | lossy | A session ID derived from the native one. |
| `opencode/session-record` | kept, or dropped | The session record's members, in the header's `opencode:session`. |
| `opencode/shell-background` | kept, or dropped | A background shell command, which enters context through its result. |
| `opencode/state-namespaced` | lossy | Provider state under its namespace in a row OpenCode 2's migration copied. |
| `opencode/step-part` | kept, or dropped | A part of a step that is not sent: a patch, a retry, a snapshot. |
| `opencode/step-unfinished` | lossy | A step that did not finish, its usage unrecorded. |
| `opencode/subagent` | kept | A sub-agent the call alone ran in the foreground, a subsession. |
| `opencode/subagent-alone` | lossy | A sub-agent converted on its own: Alone, nesting, or a parent that failed. |
| `opencode/subagent-cycle` | dropped | A sub-agent being converted or converted already is not converted again. |
| `opencode/subagent-missing` | lossy | A call names a session the store does not hold, or one not its child. |
| `opencode/subagent-peer` | lossy | A sub-agent that outlives its calls, a peer. |
| `opencode/subagent-unlinked` | lossy | A child session linked to no call. |
| `opencode/subtask` | lossy | A user subtask: OpenCode sends a fixed text and runs the task tool itself. |
| `opencode/system-prompt` | dropped | The system prompt and tool list are not stored. |
| `opencode/text-space` | lossy | An empty text beside signed reasoning, sent as a space. |
| `opencode/tool-output-pruned` | lossy | A pruned output stays as first sent. |
| `opencode/tool-unsent` | kept, or dropped | A tool part with no state OpenCode sends. |
| `opencode/unknown-part` | kept, or dropped | A part of a type this converter does not know. |
| `opencode/unknown-type` | kept, or dropped | A V2 row of a type this converter does not know. |
| `opencode/unreached` | dropped | A record read and not converted; a converter bug. |
| `opencode/unreadable` | dropped | A directory that cannot be read. |
| `opencode/unsent-part` | kept, or dropped | A user message's part OpenCode does not send. |
| `opencode/usage-member` | kept | The cost and the total are `opencode:usage`. |
| `opencode/user-system` | lossy | A user message's system text, not in the session's instructions. |
| `opencode/version-resumed` | lossy | A session a later release than its creator's resumed, read by the later rules. |
| `opencode/version-unknown` | lossy | A session with no readable release, read by the newest rules. |
