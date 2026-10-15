# Codex

The Codex CLI writes one JSONL rollout per thread. The packages are
`codex`, which reads the format as it is, and `codex/convert`, which
converts it.

## Where the format is specified

Nowhere in prose. The format is the Rust source of
[openai/codex](https://github.com/openai/codex), and this module was
written against commit `7ae831991792744434027c431dfbc7e81dd5c12c`
(2026-10-15):

- `codex-rs/history/src/lib.rs` and `rollout_payload.rs`: the
  `RolloutItem` union, its wire form `{timestamp, ordinal?, type,
  payload}`, `CompactedItem`.
- `codex-rs/protocol/src/models.rs`: `ResponseItem` and its content,
  reasoning and output types.
- `codex-rs/protocol/src/protocol.rs`: `SessionMeta`, `SessionSource`,
  `TurnContextItem`, `EventMsg`, `TokenUsage`, `TokenUsageRecord`,
  `InterAgentCommunication`.
- `codex-rs/rollout/src/policy.rs`: which items and events are written
  at all.
- `codex-rs/core/src/session/rollout_reconstruction.rs` and
  `core/src/context_manager/history.rs`: how Codex rebuilds the history
  it sends from a rollout on resume. The conversion follows them.

Earlier layouts were checked against the source of the time:
`codex-rs/core/src/rollout.rs` at `9dbe7284` (2025-08-29) for the layout
before the envelope, and the `rust-v0.98.0` tag for `ghost_snapshot`
items and the turn context's `truncation_policy`.

The one rollout on the machine this was written on held no turn, so
every shape is covered by synthetic fixtures built from the source, and
`make corpus CODEX=…` is how a real corpus is checked.

## Layout

```
~/.codex/sessions/YYYY/MM/DD/rollout-<YYYY-MM-DDTHH-MM-SS>-<thread-id>.jsonl
                                  ...-<thread-id>_<rollout-id>.jsonl   a thread with several rollouts
                                  ...jsonl.zst                         compressed when cold
```

Each line is `{"timestamp":…, "ordinal":…, "type":…, "payload":…}`; a
response item may carry `metadata`, the harness's own. The first line is
`session_meta`. There is no version field; `cli_version` in the session
meta is the nearest. Before September 2025 the first line was a bare
session meta `{id, timestamp, instructions, git}` and the items followed
bare, with `{"record_type":"state"}` lines between.

`~/.codex/state_5.sqlite` indexes threads and sub-agent spawns; the
conversion does not read it.

## Reading

- **A line, not a tree.** Lines have no id and no parent. Order is the
  file's; a rollback is an event, not a parent link.
- **Lines Codex skips.** A line that is not JSON, holds a NUL byte, or is
  not an object is skipped and reported, as Codex skips it. A JSON
  object of an unknown shape is kept with its raw bytes. Blank lines are
  not counted.
- **Compressed rollouts** (`.jsonl.zst`) are refused with
  `codex.ErrCompressed`: the standard library has no zstd. Run
  `zstd -d` and convert the `.jsonl`.
- **The session meta** is the first `session_meta` line. A forked
  thread's file holds copies of its ancestors' records, their session
  meta among them; those are not the thread's.
- **Unknown members are kept.** Every typed object keeps the members it
  does not declare, and a member of an unexpected JSON type is kept as
  written and listed by `MistypedMembers`.

## Mapping

| Native | Session |
| --- | --- |
| `session_meta` | the header: `id` the thread id, `created_at`, `cwd`, `harness` `{codex, cli_version}`, `parent_session` the parent or fork origin; `base_instructions` a first `config`; `git` an `env` with `vcs`; the rest the header member `codex:session_meta` |
| `forked_from_id` | a `fork_of` link, the session's first entry |
| `response_item` that is model output (assistant message, reasoning, calls, compaction items) | `item` entries tied to a `response` entry written after them |
| `response_item` that is input (user, developer messages, outputs, harness-written assistant messages) | `item` entries |
| a type openresponses types (`message`, `function_call`, `function_call_output`, `reasoning`, `compaction`) | that item; members it has no place for in `codex:item` |
| `custom_tool_call` | a `function_call`: `call_id`, `name`, and the free-form input as the arguments `{"input": "<text>"}` (the item's own arguments when it records them); the entry carries `codex:tool_kind: "custom"`, and a `ctc_` item id as `codex:item_id`, since a function call's id is an `fc_` id |
| `custom_tool_call_output` | a `function_call_output` for that call, `codex:tool_kind: "custom"` |
| a type it does not | an extension item; see below |
| `token_count` event | the response's `usage` (its `last_token_usage`) and `status: completed`; the running total in `codex:token_count`, rate limits in `codex:rate_limits` |
| `token_usage_record` | the response's `response_id`, and its usage when no token count gave one |
| `turn_context` | a `config` delta of what changed: `model`, `reasoning`; other settings in `codex:turn_context`; an `env` when `cwd` moved |
| `compacted` | a `custom` entry holding the record, then a `compaction` entry (see below) |
| `thread_rolled_back` | a branch (see below) whose first entry is a `custom` entry holding the event |
| `inter_agent_communication` | the `codex.agent_message` item Codex sends for it |
| `turn_started`, `turn_complete`, `turn_aborted` and other events | `custom` entries |
| `user_message`, `agent_message`, `agent_reasoning*`, `context_compacted` events | dropped: they echo items for Codex's display |
| `world_state`, `retained_context`, `security_risk_score`, `realtime_item`, `inter_agent_communication_metadata`, state records | `custom` entries |

Every entry made from a line carries its identity: `codex:ordinal` when
the rollout writes ordinals, `codex:line` (its line number) otherwise.

### Responses

Codex records a response's output items as they complete and a
`token_count` event when the response completes, but no response
boundary and, before token usage records, no response id. A run of
output items is one response; it ends at the next input item, a turn
boundary, a compaction or rollback, or a second response's output after
its usage arrived. Its `response` entry is written after the items and
whatever was recorded meanwhile. Without a usage record it is named
`codex-line-<n>`, after its first item's line.

Codex's usage is the Responses shape already: `input_tokens` counts the
cached input, as OpenAI counts it. Cache writes have no Responses slot.

### What Codex sends that the session holds as it was recorded

The conversion follows Codex's history rebuild, which leaves some
recorded items out of what it sends:

- system-role messages, `ghost_snapshot` items, `compaction_trigger`
  items, configuration updates the harness did not write, and item types
  Codex reads as `Other` are custom entries, not context;
- an output whose call is not on the path is a custom entry, as Codex
  drops orphan outputs.

And some it changes when it sends them, which the session cannot
reproduce:

- tool output is cut to the model's truncation policy (10,000 bytes
  unless the turn context or the item's metadata named another); the
  session holds the output whole and reports it;
- a call with no output gets an output Codex makes up, "aborted"; the
  session holds the call alone and reports it.

### Compaction

Codex replaces the model's window with a new history, which a
`compacted` line carries as `replacement_history` (older rollouts carry
only the summary, and Codex rebuilds the window on resume from the user
messages within 20,000 tokens and the summary; the conversion rebuilds
it the same way, estimating tokens from bytes). The session writes:

1. a `custom` entry holding the compaction record without its window;
2. a `compaction` entry whose `first_kept` names that custom entry, so
   nothing from before it is kept; whose `summary` is the window's first
   item; whose `pinned` items are the items after it that copy items
   already on the path, in order; and whose `config` is the settings in
   force;
3. the rest of the window as hidden item entries marked `codex:window`,
   in order: the summary message Codex wrote and anything it rewrote.

The context after the compaction is the window Codex installed, item
for item. A call the window copies but cannot pin is written again with
a renamed call id, the native one in `codex:call_id`.

### Rollback

`thread_rolled_back {num_turns}` drops the last `num_turns` user turns
from what Codex sends. A user turn starts at a person's message or a
message from another agent, not at a message Codex wrote itself
(environment context, instructions, notices) or a compaction summary.
The session branches from the entry before the first dropped turn,
moved back over the context messages Codex wrote just before it, as
Codex trims them; the dropped turns stay in the file on the branch left,
and the settings in force return to what they were there. Codex tells
its own messages from a person's by a matcher per fragment; the
conversion matches the common markers, which is an approximation.

### Sub-agents and forks

A sub-agent's rollout names its parent thread in `parent_thread_id` and
its source; the parent's rollout records the spawn as a tool call whose
shape the conversion does not interpret. A sub-agent converts to a
session whose `parent_session` names the parent, with no `spawned_by`
and no link. A fork gets a `fork_of` link; the records it copied from
its parent are converted as its own entries.

## Extension types

openresponses types the items of the Open Responses specification.
Codex sends items beyond it; each is written as an extension item, its
type slug-prefixed by who defines it and its body otherwise verbatim.
A custom tool call is not among them: it is a tool call, and is written
as a function call (see Mapping), so the format's call rules see it.
Codex records no tool definitions, so there is no custom tool
definition to map; the tools an `additional_tools` item adds are
carried in that item as Codex sent them.

| Native type | Extension item | What it carries |
| --- | --- | --- |
| `local_shell_call` | `openai.local_shell_call` | a shell command the model asked to run: `call_id`, `status`, `action` (`exec`, `command`, `timeout_ms`, …); its output is a `function_call_output` |
| `web_search_call` | `openai.web_search_call` | a hosted web search: `status`, `action` (`search`, `open_page`, `find_in_page`) |
| `image_generation_call` | `openai.image_generation_call` | a hosted image generation: `status`, `revised_prompt`, `result` |
| `tool_search_call` | `openai.tool_search_call` | a search over deferred tools: `call_id`, `execution`, `arguments` |
| `tool_search_output` | `openai.tool_search_output` | the tools it loaded: `call_id`, `status`, `execution`, `tools` |
| `agent_message` | `codex.agent_message` | a message between agents of a multi-agent thread: `author`, `recipient`, `content` (text or encrypted parts); also what an `inter_agent_communication` line becomes |
| `additional_tools` | `codex.additional_tools` | tools added mid-thread, as Codex sends them: `role`, `tools` |
| `configuration_update` | `codex.configuration_update` | a reasoning setting the harness changed at that point of the history: `reasoning` |
| `context_compaction` | `codex.context_compaction` | an encrypted server-side compaction of the window: `encrypted_content` |

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead. The codes
every converter can report are in [README.md](README.md).

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/reasoning-provider` | gap | Encrypted reasoning is in `encrypted_content`, which says nothing of the provider that can replay it; the entry carries `codex:provider`, the session's model provider. |
| `agentimport/usage-detail` | gap | A response's usage counts cache writes, which Responses usage has no place for; the native usage is `codex:usage`. |
| `agentimport/vcs-branch` | gap | The git branch is the vcs member `codex:branch`; the format's vcs has no branch. |
| `codex/assistant-input` | kept | An assistant message the harness wrote (a delivered or inherited message, compaction output) is input, not a response's output; it is marked `codex:input`. |
| `codex/call-id-renamed` | repaired | A call id another call of the session holds, or with characters providers refuse, was renamed; the native one is `codex:call_id`. |
| `codex/call-without-output` | lossy | A call on the path had no output when a new user turn began; Codex sends later requests an output it makes up, which the rollout does not record. |
| `codex/codex-item` | kept | An item of Codex's own is a `codex.<type>` extension item, its body verbatim. |
| `codex/compaction` | kept | A compaction is a custom entry holding its record and a compaction entry over the window Codex installed; see Compaction. |
| `codex/compaction-alias` | repaired | A `compaction_summary` item, which Codex reads as `compaction`, is a compaction item. |
| `codex/compaction-empty` | lossy | A compaction installed an empty window; the format's compaction needs a summary, so it is the summary text as an assistant message, which Codex did not send. |
| `codex/compaction-rebuilt` | lossy | A compaction without its replacement history; the window is rebuilt as Codex rebuilds it, with tokens estimated from bytes, and a message over the budget is cut by bytes. |
| `codex/copied-session-meta` | kept | A later session meta, a forked thread's copy of an ancestor's, is a custom entry. |
| `codex/custom-tool-call` | kept | A Responses custom tool call or its output is a function call or its output, marked `codex:tool_kind: "custom"`; the free-form input is the arguments `{"input": …}`, and a `ctc_` item id is `codex:item_id`. |
| `codex/effort` | lossy | A reasoning effort Responses has no value for; it is in `codex:turn_context` alone. |
| `codex/event` | kept | An event the model does not see is a custom entry. |
| `codex/event-echo` | dropped | A `user_message`, `agent_message`, `agent_reasoning`, `agent_reasoning_raw_content` or `context_compacted` event repeats an item or record for Codex's display. |
| `codex/fork-prefix` | lossy | A forked thread's copies of its parent's records are converted as its own entries, not as the prefix of a base. |
| `codex/ghost-snapshot` | kept | A ghost snapshot, the git state Codex kept for undo and never sent, is a custom entry. |
| `codex/history-base` | lossy | The thread inherits a prefix of another rollout by reference; it is not in this file or the session's context. |
| `codex/inter-agent` | kept, or dropped | A message between agents as recorded (recipients, whether it started a turn) is `codex:inter_agent`. |
| `codex/item-members` | kept, or dropped | Members of an item its Open Responses item has no place for, which Codex sent beside it (`internal_chat_message_metadata_passthrough`, a message's `encrypted_content`, a call's `namespace`), are `codex:item`. |
| `codex/legacy-instructions` | lossy | The legacy session meta's user instructions are the header member `codex:instructions`; how they reached the model is not recorded. |
| `codex/legacy-layout` | lossy | The rollout predates the envelope: its items carry no timestamps, so entries take the time of the last line that had one. |
| `codex/line-members` | kept, or dropped | Envelope members the conversion does not map are `codex:envelope`. |
| `codex/malformed-line` | repaired | A line that is not a JSON object, or holds a NUL byte, is skipped, as Codex skips it. |
| `codex/metadata` | kept, or dropped | A response item's harness metadata, which the model never saw, is `codex:metadata`. |
| `codex/mistyped-member` | kept | Members whose value is not of the type Codex defines are kept as written. |
| `codex/no-base-instructions` | lossy | The session meta records no base instructions; Codex rendered the model's default, which is not recorded. |
| `codex/no-model` | lossy | No turn context named the model before a response; it has no model. |
| `codex/no-session-id` | repaired | Neither the session meta nor the file name gives a thread id; one was made. |
| `codex/no-session-meta` | repaired | The rollout has no session meta; the thread id is taken from the file name. |
| `codex/no-status` | lossy | No token count followed a response, which Codex writes when one completes; it has no status and no usage. |
| `codex/no-timestamp` | lossy | The rollout has no timestamps; created_at is the epoch. |
| `codex/not-sent` | kept | A compaction trigger, or a configuration update the harness did not write, which Codex does not send, is a custom entry. |
| `codex/openai-item` | kept | An OpenAI item type openresponses does not type is an `openai.<type>` extension item, its body verbatim. |
| `codex/orphan-output` | kept | An output whose call is not on its path, which Codex drops from what it sends, is a custom entry. |
| `codex/output-truncation` | lossy | A tool output is longer than the budget Codex cut tool output to before sending it; it is written whole, as recorded. |
| `codex/rate-limits` | kept, or dropped | The rate limits reported with a response's usage are `codex:rate_limits`. |
| `codex/rate-limits-only` | kept | A token count with no usage, a rate limit report, is a custom entry. |
| `codex/reasoning-summary` | lossy | A reasoning summary setting Responses has no value for; it is in `codex:turn_context` alone. |
| `codex/record` | kept | A record the model does not see (world state, retained context, a risk score, realtime state, inter-agent metadata, a legacy state record, an unplaceable usage record) is a custom entry. |
| `codex/repository-url` | kept | The repository URL is the vcs member `codex:repository_url`. |
| `codex/response-id` | lossy | The rollout names no response ids; a response is named `codex-line-<n>` after its first item's line. |
| `codex/rollback` | kept | A rollback is a branch from the entry before the first dropped turn; the dropped turns stay on the branch left, and the event is a custom entry. |
| `codex/rollback-first-turn` | lossy | A rollback dropped every user turn; Codex also strips model-switch and mode notices from the first turn's developer messages, which the conversion does not. |
| `codex/rollback-into-compaction` | lossy | A rollback cut into a compaction's window; the session keeps the whole window. |
| `codex/rollback-nothing` | kept | A rollback with no user turn to drop changes nothing; it is a custom entry. |
| `codex/session-meta-members` | kept, or dropped | Session meta members the header does not map are the header member `codex:session_meta`. |
| `codex/subagent-unlinked` | lossy | A sub-agent's parent is `parent_session`, but the parent's spawning call is not in the rollout: no `spawned_by`, no link. |
| `codex/system-message` | kept | A system message, which Codex never keeps in the history it sends, is a custom entry. |
| `codex/token-count` | kept, or dropped | A token count's running total and context window are `codex:token_count`. |
| `codex/token-count-repeat` | dropped | A token count repeating the one before it. |
| `codex/tools-unrecorded` | lossy | Codex does not record the tool definitions it sends; the session's config has none. |
| `codex/turn-context` | kept, or dropped | A turn context's other settings (approval and sandbox policy, collaboration mode) are `codex:turn_context`; they reach the model as messages the rollout records. |
| `codex/turn-context-repeat` | dropped | A turn context with the settings of the one before it, the turn id aside. |
| `codex/turn-event` | kept | A `turn_started`, `turn_complete` or `turn_aborted` event is a custom entry. |
| `codex/undecodable-item` | kept | An item that does not decode as its type is a custom entry; Codex's own parser refuses it too. |
| `codex/undecodable-line` | kept | A JSON object that is not a rollout line is a custom entry. |
| `codex/undecodable-payload` | kept | A line whose payload does not decode as its type is a custom entry. |
| `codex/unknown-item` | kept | An item of a type Codex reads as `Other`, and neither sends nor keeps, is a custom entry. |
| `codex/unknown-line-type` | kept | A line of a type this package does not know is a custom entry. |
| `codex/unplaced-usage` | kept | A token count with no response before it is a custom entry. |
| `codex/unreached` | dropped | A line the converter did not reach. A bug; tests fail on it. |
| `codex/usage-record` | kept, or dropped | A token usage record's turn and thread totals are `codex:usage_record`. |
| `codex/usage-snapshots` | lossy | Two token counts for one response with other numbers; the last is used. |
| `codex/window-item-skipped` | lossy | An item of a replacement history Codex does not send, or that does not decode, is left out of the window. |
| `codex/world-state` | lossy | Codex renders its world state into each request; the rollout records the state, not the text, so it is not in context. |
