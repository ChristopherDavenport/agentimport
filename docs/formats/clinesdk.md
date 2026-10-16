# Cline SDK

The Cline SDK (`@cline/core`) is the runtime of Cline's CLI, its
JetBrains and SDK hosts, and the VS Code extension from 4.0, which
moved the extension off the classic task (CHANGELOG 4.0.0). It keeps
each session in a directory of whole JSON documents under
`~/.cline/data/sessions`, and indexes the sessions in SQLite,
`~/.cline/data/db/sessions.db`. The packages are `clinesdk`, which
reads a session directory as it is, `clinesdk/sqlite`, which reads
`sessions.db` from a copy, and `clinesdk/convert`, which converts.
`clinesdk` imports the standard library alone; `clinesdk/sqlite`
imports `modernc.org/sqlite` through `internal/sqlite`, the copy it
reads; `clinesdk/convert` imports neither and takes
`sqlite.OpenIndex` in `Options.OpenIndex`, as the command passes it.
Classic Cline's task directories, which 4.0 still reads and migrates,
are the [cline](cline.md) source's.

## Where the format is specified

Nowhere but the source. This converter was written against
`github.com/cline/cline` at `c8f808b5` (v4.1.24, 2026-10-15), with the
history of the files below checked back to v4.0.0 (2026-06-26):

- `sdk/packages/shared/src/llms/messages.ts`: the message and its
  content blocks;
- `sdk/packages/core/src/services/session-data.ts`: the messages file,
  its envelope and the per-turn metrics;
  `session/models/session-manifest.ts`, `session-compaction.ts` (the
  sidecar, its hash and when it applies), `session-graph.ts` (the
  sub-session IDs), `services/session-artifacts.ts` (the files);
- `core/src/runtime/config/agent-message-codec.ts` (what a stored
  message becomes in the runtime and back),
  `session/services/message-builder.ts` (what each request rewrites),
  `shared/src/llms/ai-sdk-format.ts` (what each part is sent as),
  `shared/src/prompt/format.ts` (the user-input rules);
- `core/src/extensions/context/` (compaction),
  `extensions/tools/team/` and `session/team/` (sub-agents and team
  tasks), `agents/src/agent-runtime.ts` (refused calls),
  `shared/src/db/sqlite-db.ts` (the index), and
  `apps/vscode/src/sdk/legacy-task-handling.ts` (the migration of a
  classic task).

The messages file has a version, 1 throughout v4. Two changes within
v4 matter here: from v4.1.0 a compaction keeps the history whole and
writes the compacted context to a sidecar (before it, a compaction
rewrote the history, its summary a message of it), and from v4.1.23
an oversized result of an MCP or Composio tool is sent as a preview
naming a cache URI that holds the rest. The envelope's
`origin.version` is the client's version (the VS Code extension's,
the CLI's), which does not map onto SDK releases for every client, so
the converter tells the shapes apart by what the files hold.

## Layout

```
~/.cline/data/sessions/<id>/<id>.json               the manifest (SessionManifest, version 1)
~/.cline/data/sessions/<id>/<id>.messages.json      the history: {version, updated_at, agent, sessionId, origin, messages, system_prompt}
~/.cline/data/sessions/<id>/<id>.compaction.json    the latest compaction's context (SessionCompactionState, version 1)
~/.cline/data/sessions/<id>/<agent>.messages.json   a sub-agent's history; its session ID is <id>__<agent>
~/.cline/data/sessions/<id>/<agent>__<task>.messages.json   a team task's; its session ID <id>__teamtask__<agent>__<task>
~/.cline/data/sessions/sessions.index.json          the index, when Cline could not open SQLite
~/.cline/data/db/sessions.db                        the index (tables sessions, subagent_spawn_queue, …)
```

`CLINE_DIR`, `CLINE_DATA_DIR`, `CLINE_SESSION_DATA_DIR` and
`CLINE_DB_DATA_DIR` move them; the converter looks for the index
beside the sessions directory alone. A child's history lives in its
root session's directory whatever its nesting. Before the messages
file had a version it was a bare array of messages, which Cline still
reads.

The input unit is a session directory. `clinesdk.FindSessions` lists
every directory holding `<name>.messages.json`, or a manifest naming
it, by its history (else its manifest), without walking into a session
directory; the command detects a session by those names. A child's
history is converted with its root's.

The index is the only place Cline records which agent spawned a
sub-agent (`parent_agent_id`), the sub-agent's own agent and
conversation, and its task (`prompt`); the manifest leaves them out.
A session directory converts without it, and the report says what it
would have added.

## Reading

- **Element by element.** A messages array is read element by
  element; an element that is not an object is kept with its error and
  its neighbours read; a file cut short keeps the messages that end
  before the cut, and a file that is not JSON is one fault. Every
  object keeps the members it does not declare, and a declared member
  of another JSON type is kept as written, so a message decoded and
  encoded again is the same JSON value. Content blocks decode one by
  one; a repeated `messages` member is read as JSON.parse reads it, the
  last standing.
- **Timestamps** are ms epoch numbers on messages and ISO times on the
  manifest and the sidecar; a value outside the years 1970 to 9999, or
  a string, is none.
- **JavaScript's values.** The sidecar's hash is over JSON.stringify of
  each message as JSON.parse read it: `internal/jsjson` holds a value
  so (UTF-16 strings, array-index members first, a repeated member at
  its first place with its last value, numbers as doubles) and writes
  it as JSON.stringify does. The user-input rules
  (`normalizeUserInput`, `formatDisplayUserInput`) are ported with
  JavaScript's `trim`. Both are checked against node running Cline's
  functions, transcribed. User text holding a lone surrogate is the one
  input the port cannot hash.

### Units

The tally counts each file the reader reads (the manifest, each
messages file, the sidecar), each message of each messages array, each
of the sidecar's messages, and each other file of the session
directory, which is not read: `clinesdk: 13 files and elements: …`. A
finding's `File` is the file's name in the session directory, and its
`Line` the message's 1-based position, 0 for a file.

## What the model saw

On resume Cline reads the history, drops its display-only messages,
and, when the sidecar applies, replaces the first
`source_message_count` of them with the sidecar's messages. Each
request then sends that context through `MessageBuilder`, which
rewrites a copy and stores none of it, and through the AI SDK
formatting. The converter rebuilds what each request sent:

- **Display-only messages** (`metadata.displayOnly`) are custom
  entries, out of context.
- **User text** is sent trimmed, each `<user_input mode="…">` and
  `<user_command slash="…">` element replaced by its content: the item
  holds that text, and the block as written, which names the mode, is
  `clinesdk:original`.
- **A file block** is sent as `<file_content path="…">` around its
  content, cut to 50,000 characters in the middle: the item holds that
  text.
- **A tool result** is cut to 8,000 characters in the middle, its text,
  its text and file entries and every string of its structured entries,
  for every request: the output holds the cut text, the content as
  written `clinesdk:original`. A string is sent as text, an array of
  text and image blocks as content parts, anything else as its JSON
  text (JavaScript's), the images it holds sent after it as parts.
- **A call no result answers** is answered, in every request after it,
  by `Tool execution was interrupted before a result was produced.
  Tool: <name>.`, an error: the output is written, marked
  `clinesdk:synthesized`. A call left unanswered at the end of the
  history is pending.
- **An outdated read**, a `read`/`read_files` result whose file a later
  read covers, is sent as `[outdated - see the latest file content]`
  from the request at which the outdated reads add up to 64 KiB, the
  batch MessageBuilder keeps for as long as its process runs. That is
  an edit the session cannot make in place: the result stays in context
  as first sent, and the rewritten output is a custom entry where it
  first applied (`agentimport/context-replace`). The converter runs
  MessageBuilder's index and batching request by request, assuming one
  process ran the session.
- **An assistant text** over 200,000 characters (12,000 when it repeats
  tool-call markup) is cut in every later request: the response keeps
  the text the model wrote, the cut one is a custom entry after it.
- **The 6 MB budget**, which cuts the largest results and texts of a
  request over it, is reported and not rebuilt; so are the media budget
  and the placeholder a model without image input gets, and the cache
  preview of an oversized MCP result from v4.1.23.
- **An empty message** is sent as `ERROR: EMPTY CONTENT`.

The system prompt is stored once, the latest; earlier requests may
have sent another. No tool list is stored.

## Compaction

From v4.1 the latest compaction's context is the sidecar, which
replaces the first `source_message_count` messages that are not
display-only when its `source_prefix_hash` still matches them (a
sidecar from before the hash: when its `source_last_message_key`
matches the last of them). The converter decides as Cline does on
resume:

- When it applies, a `compaction` is written before the history's
  message after its source: its summary the sidecar's first message (a
  user message, `Context summary:\n\n…`), `first_kept` the history
  message where the sidecar's own copies of the history start, the
  sidecar's messages between the two `pinned` (a basic compaction
  rewrites them), `tokens_before` the summary's
  `metadata.tokensBefore`, and the settings with the sidecar's system
  prompt. A compaction that keeps nothing of the history after its
  summary has a custom marker as its `first_kept`. The sidecar's copies
  are dropped units. Every later request sends the compacted context.
- When the history holds user text the hash here cannot reproduce, the
  sidecar applies when its kept messages copy the history's, reported.
- A sidecar Cline would not apply (refused by its schema, a hash that
  no longer matches, a source longer than the history) is a custom
  entry after the last message.

Only the latest compaction survives: the requests before it are
written with the whole history, though an earlier compaction,
overwritten, may have cut it. Before v4.1 a compaction rewrote the
history itself: its summary is a message of the file, written as one,
and nothing before it survives.

## Mapping

| Native | Session |
| --- | --- |
| session directory | session `id` the session ID; `created_at` the manifest's `started_at`, else the first message's time; `cwd` the manifest's; `harness` `cline`, version the envelope's `origin.version`; header `agentimport:source` |
| manifest | header member `clinesdk:manifest`; its title an `info` entry |
| envelope (`version`, `agent`, `origin`, …) | header member `clinesdk:file` |
| `system_prompt` | the first `config`'s `instructions` |
| model (the first assistant message's `modelInfo`, else the manifest's) | the first `config`'s `model`, a later change a config delta; the provider `clinesdk:provider` |
| user `text`, `image`, `file` blocks | a user `message` of `input_text` and `input_image` (data URL) parts, in order |
| `tool_result` | `function_call_output`, in the order the blocks stand; `is_error` `clinesdk:is_error` |
| `thinking` | `reasoning`: `summary_text`, the signature in `encrypted_content` |
| `redacted_thinking` | `anthropic.redacted_thinking` |
| `text` with a Gemini `signature` | a `reasoning` holding only `encrypted_content`, then the `output_text` |
| `tool_use` | `function_call`, arguments the input as JSON.stringify writes it; a Gemini `signature` a `reasoning` before it |
| `media`, any other block | `cline.<type>` item carrying the block |
| assistant message | a `response` after its items: `response_id` made from its position, `model`, `usage`, status completed |
| `metrics` | `usage`: `input_tokens` is `inputTokens`, the whole input already; `cached_tokens` `cacheReadTokens`; the metrics, cache writes and cost included, `clinesdk:usage` |
| a message's other members (`id`, `metadata`, …) | `clinesdk:message` on its first entry |
| refused call (`… -- NOT a tool or system failure. …`, or a tool its policy disables) | a `reject` `decision` before the output, by `human`, or `policy` when no approval callback or the policy refused |
| `metadata.importedFrom` | header member `clinesdk:imported_from` |

### Sub-agents and team tasks

Cline records no call ID for a child. `spawn_agent` waits for its
sub-agent and returns its result, so a sub-agent is a **subsession**
of the one call matched to it: the call whose `task` is the sub-agent's
first message, among the calls of the agent the index names as its
parent (any history's calls without an index), made before the
sub-agent started and answered after it. Its session ID is
`SubsessionID(parent, call_id)`, its header names the parent and the
call (`spawned_by`), the link comes before the call's output, and the
output's `parents` name the sub-agent's leaf; the call's `systemPrompt`
is its instructions when its history holds none. A sub-agent that more
than one call could have run is left unlinked, its session naming the
parent in `parent_session` alone.

A team task outlives the `team_run_task` call that queued it, or ran it
as one of several: it is a **peer** of each call for its agent whose
`task` is the message that started the task (the index's prompt, else
the latest such message of its history), linked after the call's
output, its session under its own ID naming the root in
`parent_session`. A teammate that continues its conversation copies the
earlier task's messages into the next task's history: that copy is
converted as the task's own entries, their usage kept off the
responses. Children are followed 16 deep; a child reached twice is
converted once.

### Copied history

- **Forks.** A session whose manifest has `metadata.fork` names
  `forkedFromSessionId` in `parent_session` and by a `fork_of` link.
  Its first messages are a copy of the source's: as many as the source's
  history beside it shares, else those written before `forkedAt`, a
  guess. The copy is converted as the session's own entries, the
  responses' usage in `clinesdk:copied_usage`.
- **A classic task carried on.** The VS Code extension carries a
  classic task on as a session under the task's ID, starting it with a
  lossy copy of the task's history (thinking without its signature,
  redacted thinking, documents and reasoning details left out, results
  made text, the whole task's usage put on the last assistant message)
  and the user message `Warning: this is a legacy conversation, …`. The
  cline source converts the task under its ID, so this session's ID is
  derived, `SubsessionID(task ID, "clinesdk")`; it names the task's
  session in `parent_session` and by a `fork_of` link, and converts
  the copy as its own entries, their usage kept off the responses. A
  session nothing was written in after the copy is the task's alone and
  is not converted.

## Extension types

| Type | What it carries |
| --- | --- |
| `anthropic.redacted_thinking` | a `redacted_thinking` block, `{type, status, block}` |
| `cline.media` | model-generated media, the block |
| `cline.<type>` | any other block Cline wrote, the block |

## Findings

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/context-replace` | gap | Cline sends an item rewritten from a request on: an outdated read, a cut assistant text. The item stays as first sent; the rewrite is a custom entry. |
| `agentimport/reasoning-provider` | gap | A thinking or Gemini thought signature is in `encrypted_content`, the message's provider in `clinesdk:provider`. |
| `agentimport/tool-error` | gap | A failed call's output carries `clinesdk:is_error`. |
| `agentimport/usage-detail` | gap | Cache writes, in the input already, and the cost are `clinesdk:usage`. |
| `clinesdk/child-failed` | dropped | A sub-agent's or team task's history that could not be converted. |
| `clinesdk/children-alone` | dropped | A child not converted, with `Options.Alone`. |
| `clinesdk/compaction-copy` | dropped | The sidecar's copy of a history message, which the compaction keeps from `first_kept`. |
| `clinesdk/duplicate-store` | dropped | A classic task carried on with nothing written after its copy: the task's alone, not converted. |
| `clinesdk/file-unread` | dropped | A file of the session directory the converter does not read. |
| `clinesdk/null-block` | dropped | A content block that is not an object. |
| `clinesdk/subagent-cycle` | dropped | A child being converted, or converted already, reached again. |
| `clinesdk/unreached` | dropped | A unit the converter did not reach; a bug if it occurs. |
| `clinesdk/compaction` | lossy | The sidecar's compaction, where it is written and what tied it to the history. |
| `clinesdk/compaction-conversation` | lossy | A sidecar naming another conversation, which Cline applies when it is the agent's; applied. |
| `clinesdk/compaction-inline` | lossy | A compaction summary in the history: before 4.1 a compaction rewrote it. |
| `clinesdk/compaction-latest-only` | lossy | Only the latest compaction survives; the requests before it are written with the whole history. |
| `clinesdk/compaction-summary` | lossy | A sidecar whose first message is no compaction summary, or holds no text. |
| `clinesdk/compaction-unverified` | lossy | A sidecar tied to the history by its copies alone, the hash not reproducible. |
| `clinesdk/empty-message` | lossy | A message with no content, which Cline sends as `ERROR: EMPTY CONTENT`. |
| `clinesdk/file-block` | lossy | A file block is the text Cline sends for it. |
| `clinesdk/fork-copied` | lossy | A fork's first messages are its source's, converted as its own. |
| `clinesdk/imported-session` | lossy | A session Cline imported from another agent. |
| `clinesdk/index-unread` | lossy | The index is not read, or cannot be; the session directory is read alone. |
| `clinesdk/invalid-input` | lossy | A tool_use input that is not a JSON object. |
| `clinesdk/legacy-copy` | lossy | A classic task carried on: the copy of its history is converted as the session's own. |
| `clinesdk/legacy-flag` | lossy | A manifest marking a classic task carried on, with no copy of one in the history. |
| `clinesdk/mistyped-member` | lossy | A member of another JSON type, kept and read as empty. |
| `clinesdk/no-index` | lossy | A session with children and no index read: they are matched by task and time alone. |
| `clinesdk/no-messages` | lossy | A messages file with no messages array. |
| `clinesdk/no-response-id` | lossy | Cline records no response ID; one is made from the message's position. |
| `clinesdk/no-stop-reason` | lossy | Cline records no stop reason; every response is completed. |
| `clinesdk/no-system-prompt` | lossy | A history with no system prompt. |
| `clinesdk/no-timestamp` | lossy | No time at all; `created_at` is the epoch. |
| `clinesdk/orphan-result` | lossy | A tool result for no call before it, a custom entry. |
| `clinesdk/outdated-read` | lossy | A read result Cline sends as a notice from a request on, or whole again. |
| `clinesdk/pending-call` | lossy | A call with no result at the end of the history. |
| `clinesdk/rejection` | lossy | A refused call: a reject decision before the output. |
| `clinesdk/request-budget` | lossy | A request over Cline's 6 MB text budget, cut by Cline, not rebuilt. |
| `clinesdk/request-projection` | lossy | What a request sends differs from the file: a result or file block cut, an assistant text cut later, images checked against a budget. |
| `clinesdk/result-malformed` | lossy | A tool result with no content, or content that is not JSON. |
| `clinesdk/session-id` | lossy | The session ID is derived from the native one. |
| `clinesdk/structured-output` | lossy | A structured tool result is the JSON text Cline sends. |
| `clinesdk/subagent-matched` | lossy | A sub-agent matched to its call by task, parent and time. |
| `clinesdk/subagent-unlinked` | lossy | A sub-agent linked to no call: `parent_session` alone. |
| `clinesdk/system-prompt-from-call` | lossy | A sub-agent's instructions are its spawning call's `systemPrompt`. |
| `clinesdk/system-prompt-latest` | lossy | The system prompt is the latest Cline stored. |
| `clinesdk/teammate-continued` | lossy | A team task whose history starts with an earlier task's conversation. |
| `clinesdk/teammate-peer` | lossy | A team task is a peer of the calls that ran it, linked after their outputs. |
| `clinesdk/teammate-unlinked` | lossy | A team task linked to no call: `parent_session` alone. |
| `clinesdk/user-input` | lossy | User text sent without its wrapper; the block is `clinesdk:original`. |
| `clinesdk/version` | lossy | A messages file of another version, or a bare array from before the version. |
| `clinesdk/compaction-marker` | kept | A custom marker is the `first_kept` of a compaction that keeps nothing after its summary. |
| `clinesdk/content-malformed` | kept, or dropped | A message whose content is neither text nor blocks, a custom entry. |
| `clinesdk/copied-usage` | kept | A copied response's usage is `clinesdk:copied_usage`. |
| `clinesdk/display-only` | kept, or dropped | A display-only message, a custom entry. |
| `clinesdk/extension-item` | kept | A block Open Responses has no item for, an extension item. |
| `clinesdk/manifest` | kept, or dropped | The manifest is `clinesdk:manifest`. |
| `clinesdk/members` | kept, or dropped | Members the session has no place for, `clinesdk:message` or `clinesdk:block`. |
| `clinesdk/stale-compaction` | kept, or dropped | A sidecar Cline would not apply, a custom entry. |
| `clinesdk/unexpected-block` | kept | A block a user message does not hold, an extension item. |
| `clinesdk/unknown-role` | kept, or dropped | A message of another role, a custom entry. |
| `clinesdk/call-id-renamed` | repaired | A repeated or ill-formed call ID is renamed; the native one is `clinesdk:call_id`. |
| `clinesdk/malformed` | repaired | An element that is not a message is skipped. |
| `clinesdk/malformed-file` | repaired | A file that cannot be read whole; what it holds before the fault is read. |
| `clinesdk/result-missing` | repaired | A call no result answers gets the output Cline sends in its place. |
