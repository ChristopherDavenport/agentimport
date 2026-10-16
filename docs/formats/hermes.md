# Hermes Agent

Hermes Agent (NousResearch/hermes-agent), the Python agent behind the
`hermes` CLI, its TUI, its messaging gateway (Telegram, Discord, Slack
and others), its cron runner and the sub-agents its `delegate_task`
tool starts. Every session of a profile is a row of one SQLite
database, `state.db`, and every message a row of it. `hermes` reads
the rows, `hermes/sqlite` opens the database, and `hermes/convert`
converts. Its report counts **rows**: each row of `sessions` and each
row of `messages`.

## Where the format is specified

Nowhere but the source. The schema is `SCHEMA_SQL` in
`hermes_state_common.py`; the code that writes and reads the rows is
the `hermes_state_*.py` modules and `agent/`. This document and the
converter follow hermes-agent at `848fc0ad8b` (2026-10-16, just past
`v0.22.2`), schema version 31. Older schemas were not read from a
release of their age: what they lack is taken from the migrations in
`hermes_state_schema.py`, and the `v14` fixture is written to that
shape, not from a database of that age.

The corpus run read a database written by Hermes Agent v0.22.2, the
release commit `d923a5eb48` (2026-10-15). Both it and `848fc0ad8b`
record schema version 31, and their `messages` tables are the same,
yet `848fc0ad8b` adds `sessions.runtime_revision` and
`runtime_generation` and the unified runtime's tables
(`runtime_epoch`, `session_admissions`, `worker_executions`,
`worker_receipts`, `async_delegations.owner_execution_id`): a schema
version does not fix the columns. One reading rule differs:
`get_resume_conversations` at `d923a5eb48` does not leave out
`session_meta` rows from the model history, which `848fc0ad8b` does;
the converter leaves them out for either, as the gateway's own replay
always has (`_build_gateway_agent_history`), and the row holds no
content.

Hermes changes the schema often (version 24 on 2026-08-02, 30 on
2026-09-03) and adds columns declaratively: on open it compares each
table with `SCHEMA_SQL` and adds what is missing
(`_reconcile_columns`), with no gate per version. A database is
therefore read by column name: a column the converter does not know is
kept, one it expects and does not find reads as absent. The versioned
data migrations that matter here:

| Version | Change |
| --- | --- |
| 16 | sub-agent sessions tagged `model_config._delegate_from`; before it a sub-agent is told by `source = 'subagent'` |
| 20, 22 | `session_model_usage`, then its `task` column |
| 25 | system prompts moved to `system_prompts`, keyed by `sessions.system_prompt_hash`; `sessions.system_prompt` stays a read fallback |

In-place compaction's flags (`messages.active`, `compacted`,
`_compressed_summary`), `api_content`, the Codex carriers and the
display columns arrived without a version step; a database without
`active` has every row live.

Not inputs of this converter, though Hermes writes them: the ShareGPT
trajectories its batch runner and `trajectory_compressor.py` write for
training; the `trace` export, which writes a session as Claude Code
JSONL; the NeMo ATIF plugin and the `moa-traces` of its mixture of
agents; `~/.hermes/sessions/<id>.jsonl`, the transcript it diverts to
when the database cannot be written (`divert_session_transcript_jsonl`);
the legacy `session_<id>.json` files; and the export of
`hermes sessions export --format jsonl`, a projection of the rows this
converter reads directly.

## Layout

```
~/.hermes/state.db                      the default profile
~/.hermes/profiles/<name>/state.db      each further profile
```

`state.db` is in WAL mode and every Hermes process of a profile writes
it, the gateway's included. It is read from a copy of the database and
its log (`internal/sqlite`), never in place. A home or profile
directory names its `state.db`; `FindSessions` on a home lists the
home's database and each profile's, each converted whole.

The tables read:

| Table | What it holds |
| --- | --- |
| `sessions` | a row per session: source, routing columns of the gateway, model, `model_config`, system prompt or its hash, parent, times, end reason, working directory and git, token and cost totals, title |
| `messages` | a row per message, OpenAI chat-completions shaped: `role` (user, assistant, tool; also `session_meta`, `system`), `content`, `tool_calls`, `tool_call_id`, `timestamp` (REAL seconds), the reasoning carriers, `api_content`, the flags of compaction and rewind, display columns |
| `system_prompts` | prompt text by hash |
| `session_model_usage` | tokens and cost per session, model, billing route and task |
| `async_delegations` | background delegations: their ID, parent session and state |

The other tables (leases, heartbeats, routing, admissions, workers,
delivery obligations, conversation generations, FTS) are runtime
state and not read.

Other databases in a home, none of which holds a session's model
context, and none read:

| File | What it is |
| --- | --- |
| `shared-state.db` | the gateway's hosted Bot Mode rooms (`gateway/hosted_rooms.py`, `hosted_room_policy_checkpoint.py`): each room's members and an append-only event log, whose `message.user` and `message.member` events are a group chat's messages, with a bounded projection of it (`hosted_room_policy_transcript`) for choosing the next discussion. The turns an agent takes in a room are sessions of `state.db`. Kept at the root of the home, shared by its profiles. |
| `kanban.db` (and `kanban/boards/<slug>/kanban.db`) | the Kanban board shared across profiles (`hermes_cli/kanban_db.py`): tasks, their links, comments, events, runs with a summary and outcome, attachments. A worker's conversation is a session of `state.db`. |
| `telemetry/shared_metrics/metrics.sqlite3` | aggregate usage counters and the outbox of metric packages (`website/docs/developer-guide/relay-shared-metrics.md`); no conversation. |

## Reading

- Rows are read in `id` order, the order Hermes reads them in
  (`get_messages`: "id, never timestamp: clocks regress").
- `content` beginning `"\x00json:"` is JSON, multimodal parts or a
  dict; a reserved prefix holding no valid JSON stays text
  (`_decode_content`).
- `tool_calls` is a JSON array; a call's ID is `call_id` before `id`,
  and of a bridge ID `<call_id>|<item id>` the part before the bar
  (`coalesce_tool_call_id`). `tool_calls` that does not decode reads
  as none, as Hermes falls back to `[]`. Hermes writes every call it
  receives as `{id, call_id, response_item_id, type, function}`
  (`agent/chat_completion_helpers.py`): `id` and `call_id` are the same
  provider call ID, which a tool row's `tool_call_id` names, and
  `response_item_id` is the `fc_` item ID a Responses route needs for
  the call, derived from the call ID when the provider gave none
  (`_derive_responses_function_call_id`). The chat-completions
  transport strips `call_id` and `response_item_id` from what it sends
  (`_STRIP_TC_KEYS`). The converter's function call carries the call
  ID; `response_item_id` is kept in `hermes:call`.
- `display_metadata` is an object, under at most one extra layer of
  string (rows from before Hermes guarded the column).
- A timestamp outside Hermes' window (0 to 4.2e9 seconds) reads as
  none.
- A child session's relation to its parent is told as Hermes tells it
  (`hermes.Classify`): a tool session (`source = 'tool'`); a marker in
  `model_config` that names the parent (`_branched_from`,
  `_delegate_from`, `_reset_from`); a compression continuation, whose
  parent ended `compression` (`_is_compression_child_row`); a legacy
  branch, whose parent ended `branched` before it started; a legacy
  reset, whose parent ended at a reset boundary on the same routing
  key (`_legacy_reset_child_sql`); else a sub-agent run
  (`_ephemeral_child_sql`). One rule is the converter's: a session of
  source `subagent` whose parent is not one is a sub-agent, whatever
  its parent's end, since before schema 16 Hermes' own predicates take
  a sub-agent of a parent that later compressed for its continuation.

### Units

The tally counts each row of `sessions` and each row of `messages`. A
session row is mapped (the header and the first entries); a message
row mapped, kept (a custom entry), or malformed; a message row of no
session, and a second row of one session ID, dropped.

## What the model saw

Hermes resumes a session from `get_resume_conversations`
(`hermes_state_messages.py`): the model history is the session's own
rows with `active = 1` and a role other than `session_meta`, in id
order, through `_rows_to_conversation`, and then
`repair_message_sequence` (`agent/agent_runtime_helpers.py`). A
compression's parents are display history only, never sent. The
converter builds each session so the context at its leaf is that
history:

- **Text.** User and assistant text is what Hermes sends: the
  `api_content` sidecar when the row holds one, substituted at send
  time (`substitute_api_content`, `agent/turn_context.py`), else the
  content through `sanitize_context(...).strip()`, which takes out its
  injected memory blocks. Every text is stripped, as the request's
  closing pass strips it. The content Hermes shows, when it differs, is
  kept in the row's columns.
- **Harness turns.** A background-review harness prompt an older build
  wrote into the session, and the curator's reply after it, are custom
  entries: a resume strips both (`_strip_background_review_harness`).
  An assistant tool-call turn whose content is a bare `[marker]` has no
  text (`_strip_stale_tool_call_markers`).
- **Repair.** A tool row's result pairs with a call of the assistant
  turn just before it, named by any of the call's aliases (`call_id`,
  `id`, `response_item_id`, each part of `a|b`, trimmed:
  `tool_call_id_variants`); an assistant or a user turn starts a new
  run (`_drop_stray_tool_results`). At the leaf, a result that pairs
  with no such call, and then a call the run of results right after it
  does not answer (`_prune_unanswered_tool_calls`), are left out by
  `omit`, as Hermes drops and prunes them. Before the leaf, a result
  names the latest call of its alias, as Hermes sent it then. Hermes
  also merges consecutive user turns and consecutive assistant turns;
  that is reported, not done.
- **Not reproduced.** What Hermes adds per request and does not store:
  the ephemeral system prompt, prefill messages, per-turn notes, the
  gateway's timestamps on user text and a Telegram channel's block of
  observed messages, the tool definitions; the provider-route policy
  that keeps or strips reasoning fields per provider
  (`apply_reasoning_content_policy`) and the Codex route's per-request
  call ID suffixes.

### Rows out of context

`active = 0` rows are out of context. With `compacted = 1` an in-place
compaction summarised them; with `compacted = 0` a rewind (`/undo`,
`/retry`, `rewind_in_transaction`) undid them, a compaction superseded
them with copies it carried (`archive_and_compact`), or Hermes
deactivated them. Each run of `compacted = 0` rows is a branch from the
entry before it, and the rows after the run continue from that entry,
so the path to the leaf holds no such row. Two rewinds in a row cannot
be told apart and read as one branch.

### In-place compaction

`archive_and_compact` sets every live row `active = 0, compacted = 1`,
the originals of the rows it carries `compacted = 0`, and writes the
compacted context after them as new live rows: the head it protects
(`protect_first_n`, three rows by default, which decays to none after
the first compaction), copied with their times; the summary row
(`_compressed_summary = 1`, role user or assistant, or merged into the
first tail row); and the tail, copied with the times of the rows they
copy (`_carry_parent_timestamps`). The rows are a run of generations:

- the last generation starts at the first live row after the last
  compacted one;
- an earlier one at its summary row, less the rows just before it that
  copy, in order, the first rows of the generation before;
- a continuation's or a branch's first summary belongs to its first
  generation, written by its parent's compaction.

Each generation after the first starts with a `compaction` entry whose
`first_kept` keeps nothing before it and whose `summary` is the
generation's first item; the rest of the generation's rows are entries
after it. The context is the generation's rows in Hermes' order. When
Hermes kept a head, that first item is the head's, verbatim, and the
summary row follows it: the session format puts a compaction's summary
first, so this is `agentimport/fold-head`. The head and the tail are
copies: their assistant rows are response entries marked
`hermes:copied`, as codex marks a fork's copied response
(`codex/fork-copied-response`); Hermes records no usage per response,
so none moves off the entry.

### Split compaction

`publish_compression_child` ends the session `compression` and writes
the compacted context as the rows of a child session, which a resume
reads alone (`resolve_resume_session_id` follows the chain to its tip).
The child is its own session, `parent_session` naming the parent, and
the parent ends with a `continued_in` link naming it, as
`agentsession.Continue` rolls a session over: its context is its own
rows, the head and the tail copies among them.

### Branches and resets

A `/branch` (`hermes_state_mutation_branch.py`) clones the parent's
live rows into the child with their times. The child is a session of
its own: `parent_session` and a `fork_of` link name the parent, the
copied rows, told by matching the parent's live rows in order by
content and time, are its own entries, and their assistant rows are
response entries marked `hermes:copied`. A reset (`hermes_state_mutation_reset.py`) starts a new
conversation on the same chat and copies nothing: it is a session with
no `parent_session`, the row's `parent_session_id` kept with the
session row.

## Mapping

| Hermes | Session format |
| --- | --- |
| session row | header: `id` the session's ID, `created_at` from `started_at`, `cwd`, harness `hermes`; `agentimport:source`; the row's other columns in `hermes:session`, `session_model_usage`'s rows in `hermes:model_usage`, the schema version in `hermes:schema_version` |
| `model`, system prompt | the first `config`: `model`, `instructions` from `system_prompts` by hash, else the row's column; `model_config` in `hermes:model_config` |
| `title` | `info` name |
| `git_branch`, `git_repo_root` | `env` with `vcs.system` git, the branch in `vcs.hermes:branch`, the root in `hermes:repo_root` |
| user row | `message` item, role user; `visible: false` for `display_kind` hidden or `display_metadata.model_only` |
| system row | `message` item, role system, where it stands |
| assistant row | reasoning items, a `message` item, `function_call` items, then a `response` (`response_id` `row-<id>`); `finish_reason` length or content_filter make it incomplete |
| tool row | `function_call_output` item of the call its `tool_call_id` names |
| summary row | `message` item, no response; or a compaction's summary |
| `session_meta` row, unknown role | `custom` entry holding the row |
| refused approval | `decision` reject before the output, by the user when the text says the user denied it, else by policy |
| compression child | session with `parent_session`; `continued_in` link in the parent |
| branch | session with `parent_session` and a `fork_of` link |
| sub-agent | session; `subsession` or `peer` link (below) |

Every entry made from a row carries the row's ID in `hermes:row`, on
the row's first entry, with the columns no entry maps in
`hermes:columns`, those that are null, empty or zero left out.

Every column of `messages` at schema version 31:

| Column | Where it goes |
| --- | --- |
| `id` | the order of the walk; `hermes:row`; a response's ID, `row-<id>` |
| `session_id` | the session the row is converted with |
| `role` | the kind of entry (Mapping above) |
| `content` | the item's text or parts, unless `api_content` is sent in its place; then `hermes:columns` |
| `tool_call_id` | the output's `call_id`, through the call it names |
| `tool_calls` | `function_call` items; entries that are not objects keep the column in `hermes:columns` |
| `tool_name` | `hermes:columns` (the call's name is on the call) |
| `effect_disposition` | `hermes:columns`: whether an interrupted or refused call may have had its effect (`none`, `unknown`) |
| `timestamp` | the entries' `ts` |
| `token_count` | `hermes:columns`; no usage (Usage below) |
| `finish_reason` | the response's status, and `hermes:finish_reason` |
| `reasoning`, `reasoning_content`, `reasoning_details`, `codex_reasoning_items` | reasoning items from the first carrier that yields one (Payload below); the others, and one with entries no item holds, in `hermes:columns` |
| `codex_message_items` | the message's phase and ID when it holds one; else `hermes:columns` |
| `platform_message_id` | `hermes:columns`: the platform's ID of a gateway message |
| `observed` | `hermes:columns`: a group message Hermes saw and was not addressed by (`hermes/observed`) |
| `_compressed_summary` | tells a compaction's summary row |
| `active`, `compacted` | where the row stands: in context, summarised, or out of context (What the model saw) |
| `api_content` | the item's text, when set, for a user or assistant row |
| `display_kind`, `display_metadata` | `visible: false` for hidden and model-only rows; both in `hermes:columns` |
| `display_identity`, `display_order` | Hermes' display index, derived from the other columns; not kept (`hermes/display-index`) |
| `message_uid` | `hermes:columns`: the row's durable identity |
| `absorbed_message_uids` | `hermes:columns`: on a user row a consecutive-user merge kept, the `message_uid` of each row merged into it (`record_absorbed_message`) |
| `tool_call_uids`, `tool_call_uid` | `hermes:columns`: per-occurrence identities of an assistant's calls and of the call an output answers | A renamed call keeps the native ID in
`hermes:call_id`, and a call's members beyond `id`, `type` and
`function` (such as `extra_content`) are in `hermes:call`.

### Payload

- **Reasoning**, from the carrier the replaying route reads first:
  - `codex_reasoning_items`, Responses items the Codex route keeps:
    each `reasoning` or `compaction` item verbatim, without its `id` and
    Hermes' `_issuer_kind` and `_issuer_model`, which are in
    `hermes:item`;
  - else `reasoning_details`: an Anthropic `thinking` block is a
    reasoning item, its text the summary and its signature in
    `encrypted_content`, `redacted_thinking` an
    `anthropic.redacted_thinking` item, as `providers/anthropic` maps
    them; OpenRouter's `reasoning.text` the same way when it carries a
    signature, `reasoning.summary` a summary and `reasoning.encrypted`
    `encrypted_content`; thinking text no signature covers (an unsigned
    `reasoning.text`, as OpenRouter writes for most models, or an
    unsigned `thinking` block) is the item's `content`, as
    `openresponses/chatcompletions` reads plain reasoning text and
    replays it; a provider's native carrier (`*.native_assistant`) is
    no item, kept in the row's columns;
  - else the text, `reasoning_content` when not blank, else
    `reasoning`, as a reasoning item's `content`, as
    `openresponses/chatcompletions` reads a chat-completions response.
  A whitespace pad Hermes writes for a must-echo provider is no
  reasoning. Encrypted content carries `hermes:provider`.
- **Gemini thought signature** on a tool call
  (`extra_content.google.thought_signature`): a reasoning item holding
  only `encrypted_content` before the call, as `providers/gemini` reads
  one.
- **Text phase**: `codex_message_items`' phase on the message, and its
  ID unless the turn carried encrypted reasoning, which Hermes drops it
  for (`_replay_message_items`).
- **Content parts**: `text` to input or output text, `image_url` to an
  input image, Responses parts as themselves, other chat parts to
  `openai.<type>` extension parts, a part of no type to `hermes.part`.
- **Arguments**: a string as it is, an object compacted.

### Usage

Hermes counts usage per session (`sessions`' token and cost columns)
and per model of a session (`session_model_usage`), never per model
call: `messages.token_count` is written only by imports, and a parent's
`estimated_cost_usd` includes its sub-agents' (`_rollup_children_cost`).
No response entry carries usage; the totals are in the header's
members, and a reader that sums them across a compression chain, a
branch or a parent and its sub-agents counts some twice: a
continuation and a branch start their counters at zero, a parent's cost
holds its children's.

### Sub-agents

`delegate_task` builds each child agent with `parent_session_id` naming
the parent (`tools/delegate_tool.py`), and neither side records the
other's call: the output does not name the child. The converter ties
a child to a call by a heuristic, reported as one in every link's
finding:

- A child is a call's when it started while the call's run was open:
  after the call's row, and no later than the run's end. A call that
  waited ends at its output. A background run ends when the last of its
  units' results is delivered, a user row naming the delegation in
  `display_metadata.delegation_id`, or, without one, when
  `async_delegations` records it completed; until then it is open,
  since Hermes creates a child's session on its first turn
  (`run_agent.py` `_ensure_db_session`), on the async worker, after the
  handle row. A call with no output ends at the next turn.
- When several calls' runs were open, the child is the call's whose
  goals (its `tasks`' goals, or the handle's `goals`) its first message
  holds, as Hermes sends a child its goal as that message, else the
  call made last before it started; calls of one row that the goals do
  not tell apart tie, and the child is linked to none.

- A call whose output is the handle of a background run
  (`{"status": "dispatched", "mode": "background", "delegation_id": …}`,
  `_dispatched_payload`) ran its children as **peers**: a `peer` link
  after the output, carrying `hermes:delegation_id`, the unit's
  delegation (a handle of several units lists each unit's ID and task
  indexes), and the state `async_delegations` records for it; the
  child's answer arrives later as a user row naming the delegation. A
  task of the handle's `inline_results`, which ran inline before the
  call returned, is a subsession.
- Any other call waited for its children: each is a **subsession**,
  linked before the output, which names the children's leaves in
  `parents`. A call's only child takes the session ID derived from the
  call (`SubsessionID`); the children of a call that ran several keep
  their own IDs.
- A sub-agent no call is tied to, and a tool session (`source =
  'tool'`, which no call names), is converted after its parent with
  `parent_session` and no link.

## Extension types

| Type | Where |
| --- | --- |
| `anthropic.redacted_thinking` | item: an Anthropic redacted thinking block from `reasoning_details` |
| `openai.<type>` | content part: a chat-completions part Open Responses does not define, such as `input_audio` |
| `hermes.part` | content part: a part of no type, or one that does not read |

## Findings

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/fold-head` | gap | An in-place compaction that kept a head: the compaction's summary is the head's first item, verbatim; see [incompatibilities](../incompatibilities.md). |
| `agentimport/reasoning-provider` | gap | Encrypted reasoning names no provider; the item entry carries `hermes:provider`. |
| `agentimport/vcs-branch` | gap | The session's git branch, in the `env` entry's `vcs.hermes:branch`. |
| `hermes/alternation` | lossy | User messages follow one another in the context at the leaf; a resume merges them, which is not reproduced. |
| `hermes/api-content` | lossy | The row's `api_content`, what Hermes sent, is the item; the content it shows is in the row's columns. |
| `hermes/branch-copy` | lossy | A branch starts with rows copied from its parent; they are its own entries, their responses marked `hermes:copied`. |
| `hermes/call-id` | repaired | A call ID empty, malformed or used before in the session was renamed; the native ID is in `hermes:call_id`. |
| `hermes/call-malformed` | repaired | Entries of `tool_calls` that are not objects; they are kept in the row's columns. |
| `hermes/child-alone` | lossy | A child session converted on its own, by `Alone`, the bound on nesting or its parent's failure; no link names it. |
| `hermes/compaction` | lossy | An in-place compaction, a `compaction` entry whose summary is its generation's first item. |
| `hermes/compaction-empty` | lossy | A generation of in-place compaction holds no item; no compaction is written and the rows before stay in context. |
| `hermes/content-part` | kept | Content parts Open Responses does not define, carried as extension parts. |
| `hermes/continuation` | lossy | A compression continued the session in a child: a `continued_in` link. |
| `hermes/copied-row` | lossy | An assistant row Hermes copied (a compaction's head or tail, a continuation's, a branch's): a response entry marked `hermes:copied`, the model call of the turn it copies. |
| `hermes/created-at` | lossy | The session row has no start time Hermes accepts; `created_at` is its first message's. |
| `hermes/delegation-delivery` | kept | A background delegation's result, delivered as a user message; its delegation is in the row's columns. |
| `hermes/denial-text` | lossy | An output carrying the approval gate's refusal; a reject decision, told by the text. |
| `hermes/display-index` | dropped | `display_identity` and `display_order`, the display index Hermes derives from a row's other columns, are not kept. |
| `hermes/duplicate-session` | dropped | A second row of one session ID. |
| `hermes/inactive-rows` | lossy | Runs of rows out of context (rewound, superseded, deactivated), each a branch. |
| `hermes/malformed` | repaired | A row that cannot be read (no id, no role) is skipped. |
| `hermes/members` | kept | Native values kept in a namespaced member, or dropped under `Slim`. |
| `hermes/mistyped` | repaired | A column does not hold what Hermes writes; it is kept as written and read as Hermes reads it. |
| `hermes/native-compaction` | lossy | A provider's compaction item among the Codex reasoning items; Hermes replays it only to a request with context management. |
| `hermes/observed` | lossy | An observed group-chat message; a Telegram channel's block of them is not reproduced. |
| `hermes/orphan` | dropped | A message row of no session that can be read. |
| `hermes/orphan-output` | repaired | An output of no call before it in the context at the leaf, left out there by `omit`. |
| `hermes/parent-cycle` | repaired | A session's parents form a cycle; it is converted with no parent. |
| `hermes/parent-missing` | lossy | The session's parent has no row; `parent_session` names it. |
| `hermes/reasoning-carrier` | kept | Entries of a reasoning carrier no item holds, kept in the row's columns. |
| `hermes/reasoning-malformed` | repaired | Entries of a reasoning carrier that are not objects. |
| `hermes/reasoning-text` | lossy | Reasoning from the row's text columns, a reasoning item's content. |
| `hermes/reset` | lossy | A reset child: a new conversation, with no `parent_session`. |
| `hermes/review-harness` | kept | A background-review harness prompt or its reply, a custom entry, out of context. |
| `hermes/schema-version` | lossy | The database records no schema version, or one newer than 31. |
| `hermes/session-failed` | dropped | A session could not be converted; its rows are dropped. |
| `hermes/session-id` | lossy | The session's ID is derived, as the native one is not a store name or is taken. |
| `hermes/session-meta` | kept | The gateway's `session_meta` row, a custom entry. |
| `hermes/session-row` | kept | The session row's columns the header has no place for, in `hermes:session`, or dropped under `Slim`. |
| `hermes/session-usage` | gap | Usage recorded per session and per model, not per response, kept in the header's members. |
| `hermes/stale-marker` | repaired | A bare tool marker as an assistant's content is no text, as a resume blanks it. |
| `hermes/subagent` | lossy | A sub-agent a foreground call ran: a subsession, tied by its start time. |
| `hermes/subagent-cycle` | dropped | A sub-agent already being converted or converted is not converted again. |
| `hermes/subagent-peer` | lossy | A sub-agent a background call ran: a peer, linked after the call's output. |
| `hermes/subagent-unlinked` | lossy | A sub-agent or tool session no call is tied to; `parent_session` alone names its parent. |
| `hermes/summary-row` | lossy | A compaction's summary written as an assistant message; no response. |
| `hermes/system-prompt` | lossy | The instructions are the stored system prompt; what Hermes adds per request is not stored. Dropped when the row has none. |
| `hermes/system-prompt-missing` | lossy | The session names a system prompt hash `system_prompts` does not hold. |
| `hermes/system-row` | lossy | A system row is in context where it stands; the gateway's replay leaves it out. |
| `hermes/tools` | dropped | Hermes stores no tool definitions. |
| `hermes/unanswered-call` | repaired | A call with no output in the context at the leaf, left out there by `omit`. |
| `hermes/unknown-role` | kept | A row of a role Hermes' readers do not send, a custom entry. |
| `hermes/unreached` | dropped | A row read and nothing made of it; a converter bug. |
| `hermes/usage-rollup` | lossy | The session ran sub-agents, whose estimated cost Hermes adds to its own. |
