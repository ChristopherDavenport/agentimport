# OpenHands

The OpenHands software agent SDK (`OpenHands/software-agent-sdk`,
releases 1.0.0 to 1.55) keeps each conversation in a directory: its
state in `base_state.json` and its events in `events/`, one JSON file
per event. The OpenHands CLI, the agent server and Agent Canvas all
write this layout. The packages are `openhands`, which reads a
conversation directory as it is, and `openhands/convert`, which
converts it.

Two other OpenHands formats are not read here. The OpenHands app's V0
sessions (`~/.openhands/sessions/<sid>/`, app and CLI 0.x up to 1.6.0)
hold integer-numbered events of another shape, with pickled state. The
1.x app's `v1_conversations/<hex>/<event id>.json` is a mirror the app
server kept of the events its sandboxed agent server sent, with no
state, no order but timestamps and no usage; the authoritative SDK
directory was inside the sandbox.

## Where the format is specified

Nowhere but the source. This package was written against
`OpenHands/software-agent-sdk` at `d5995133` (v1.55.0, 2026-10-15),
with tags `v1.28.1` (`edaac806`, the SDK the OpenHands CLI 1.16.0
pins, so the likeliest files on a user's machine) and `1.0.0`
(`a612c0a6`) read where shapes changed, and tags between them read to
date a change:

- `openhands-sdk/openhands/sdk/conversation/`: `persistence_const.py`
  and `event_store.py` (file names, the read order, the event tree),
  `state.py` (`base_state.json`, the leaf), `impl/local_conversation.py`
  (forks, rejections, interrupted calls);
- `openhands-sdk/openhands/sdk/event/`: every event class;
- `openhands-sdk/openhands/sdk/llm/message.py`,
  `llm/utils/responses_serialization.py` and `llm/utils/metrics.py`:
  the message model, what is sent, and usage;
- `openhands-sdk/openhands/sdk/context/view/`: how the model's context
  is built (v1.12 and later; `context/view.py` before);
- `openhands-sdk/openhands/sdk/tool/schema.py` and
  `openhands-tools/openhands/tools/{terminal,task,browser_use,tom_consult,delegate}/`:
  how each tool's result is rendered, and the sub-agent tools;
- `openhands-agent-server/openhands/agent_server/models.py`:
  `meta.json`.

No file records a version: not `base_state.json`, not an event, not
`meta.json`. Shapes are told apart by structure and each guess is
reported: tools as chat tool params (1.0 to 1.6) or tool definitions
(1.7 and later), `parent_id` on events (1.31 and later), and the view
algorithm (sequential condensations from 1.12). Timestamps are
`datetime.now().isoformat()`: local time with no zone, read in
`Options.Location` (UTC by default) and reported.

## Layout

```
<base>/<conversation id, 32 hex digits>/
  base_state.json                  ConversationState: agent config, workspace, leaf, metrics
  meta.json                        agent server only: title, metrics snapshot, fork origin
  events/event-<index>-<id>.json   one event per file, index padded to 5 digits
  events/.eventlog.lock            sidecars, not units
  events/.eventlog-len-<n>.marker
  observations/                    tools' saved full outputs, not read
  subagents/<child id>/            a sub-agent's conversation, the same layout
```

`<base>` is `~/.openhands/conversations` for the CLI (or
`OPENHANDS_CONVERSATIONS_DIR`), `~/.openhands/agent-canvas/conversations`
for Agent Canvas, and `workspace/conversations` for the agent server
and SDK scripts by default. `openhands.FindSessions` lists every
directory holding a `base_state.json` that is OpenHands' state (an
object with `agent`, or `id` and `persistence_dir`) or an event file
below a root,
without walking `events`, `observations` or `subagents`; the command
detects a conversation by such a directory, its `base_state.json` or
one of its event files.

## Reading

- **The sequence.** OpenHands lists `events/`, takes the index from
  each name and reads indexes from 0 up to the first one missing. So
  does the reader: a file past a gap is `openhands/index-gap`, a second
  file for an index `openhands/duplicate-index` (the name that sorts
  last is read, where OpenHands keeps whichever its listing gives
  last), a name that is not an event file's `openhands/bad-file-name`.
  An event file that is empty, not JSON or not an object is
  `openhands/malformed-event`; OpenHands skips an empty one and fails on
  the rest.
- **IDs.** OpenHands indexes events by the IDs their file names hold,
  the first index of an ID winning, and resolves `parent_id`, the
  legacy parent (the previous file's ID), the leaf and a fork's event
  through that index; so does the converter, and an event whose `id`
  differs is reported. A sub-agent directory that is a symbolic link is
  followed.
- **Lenient decoding.** An event's envelope (`kind`, `id`,
  `timestamp`, `source`, `parent_id`) is decoded member by member, and
  its kind's members on demand. Every object keeps the members it does
  not declare (deprecated `Message` members such as `cache_enabled`,
  `TextContent.enable_truncation`, members of newer releases), and a
  declared member of another JSON type is kept as written, so an event
  decoded and encoded again is the same JSON value. A content list is
  read element by element.

### Units

A conversation is JSON files, not lines, so the tally counts files:
`base_state.json`, `meta.json`, each file of `events/` whose name does
not start with `.` (events, files past a gap, repeated indexes, other
names), and each other file of the directory. The sidecars and the
`observations/` directory are not units. A sub-agent's files count in
its parent's report. A finding's `File` is `events` with the event's
1-based index as its `Line`, or the file's name (a sub-agent's
prefixed `subagents/<id>/`).

## What the model saw

OpenHands rebuilds the model's context from the events at every step
(`Agent.step`, `View.from_events`), and the conversion rebuilds the
same thing:

1. **The path.** Events from 1.31 carry `parent_id`, and the context is
   the path from the leaf (`base_state.leaf_event_id`) to the root; an
   event without one has the event before it as its parent, and
   `__root__` starts a new root. Without a leaf, it is the last event
   that is not a `ConversationStateUpdateEvent` or
   `ConversationErrorEvent`, which OpenHands stamps with a parent but
   never makes the head. Each event's entries follow its parent's, so
   the tree maps one to one onto the session's branches, and a
   `label: leaf` names the leaf when it is not the last entry. Those
   two kinds are written inline after their parent's entries, and the
   parent's later children follow them, so they make no branches of
   their own.
2. **Condensations**, in order: a `Condensation` drops the events it
   forgets (`forgotten_event_ids`, which can name an earlier summary as
   `<condensation id>-summary`) from the view, then, with a `summary`
   and a `summary_offset`, inserts the summary as a user message at the
   offset of what is left. Releases before 1.12 applied only the last
   condensation's summary; the two differ only when a condensation
   keeps an earlier summary, which is reported as a guess.
3. **Properties**, enforced until none removes anything: a second
   answer to a call is dropped; when an action of a response is gone,
   the rest of the response goes; an action with no answer, or an
   answer with no action, is dropped (so a call awaiting confirmation is
   not sent); when part of a tool loop (a run of actions and answers
   from an action with Anthropic thinking blocks) is gone, the rest
   goes.
4. **Messages.** Actions of one response become one assistant message,
   the first action's thought and reasoning with every call; adjacent
   plain user messages are merged into one.
5. **Send time.** A tool result's text over 50,000 characters is cut in
   the middle; images go only to a vision model; prompt-cache markers;
   for the Responses API the system message is the `instructions`.

The conversion writes the events where they are on the path and
reconciles the session's context with OpenHands' view at each request
(the first event of each response) and at each condensation: a
`config` entry's `omit.items` names the items OpenHands left out there
(`openhands/view-enforced`), after one clearing the omit in force when
something comes back. A condensation is:

- with a summary and an offset, a `compaction`: `summary` a user
  message of the condensation's summary; `pinned` the items of the
  events OpenHands kept ahead of the summary (the first user message,
  with the condenser's `keep_first`); `first_kept` the first entry of
  the events kept after it; `config` the settings in force with an
  `omit` naming what lies between `first_kept` and the compaction that
  OpenHands did not keep; `usage` the condenser's call. OpenHands puts
  the summary after the kept head, and a compaction puts its summary
  first: the gap `agentimport/fold-head`. An earlier summary OpenHands
  still sends is restated in the summary
  (`openhands/summary-restated`), since only the last compaction
  contributes and a summary cannot be pinned.
- without a summary or an offset, an `omit` of the items of what it
  forgot; a summary with no offset is not sent, and is kept in
  `openhands:event`.

Steps 4 and 5 are kept apart: the session keeps a batch's calls as one
response and adjacent user messages as separate items
(`openhands/merged-user-messages`), and outputs carry the send-time cut
(`openhands/output-truncation`). A batch with an unknown tool has an
`AgentErrorEvent` between its actions, and OpenHands sends it as
several assistant messages around the error; the session writes the
response's calls together, then the outputs (`openhands/split-batch`).

### Tool results

What the model saw for a tool result is `Observation.to_llm_content`,
code per observation class. The converter ports the renderings, with
the SDK version each was read at:

| Observation | Rendering | Read at |
| --- | --- | --- |
| default (every other kind of 1.0 to 1.55: file editor, MCP, delegate, task tracker, think, finish, glob, grep, …) | `[An error occurred during execution.]\n` when `is_error`, then the content | 1.0.0, 1.28.1, 1.55.0 |
| `TerminalObservation` (`ExecuteBashObservation` in 1.0) | error header; `metadata.prefix` + text + `suffix`, then `[Current working directory: …]`, `[Python interpreter: …]`, `[Command finished with exit code N]` lines, cut to 30,000 characters (the notice names the file the tool saved the whole output to, `<full_output_save_dir>/terminal_output_<sha256[:8]>.txt`, when it had a directory) | 1.0.0, 1.28.1, 1.55.0 |
| `TaskObservation` | `Task ID / Subagent / Status` lines, the error header, the content | 1.28.1, 1.55.0 |
| `BrowserObservation` | error header; the text cut to 50,000; `Screenshot saved to: …` when it had a directory; the screenshot as an image (MIME from the base64 prefix) | 1.0.0, 1.28.1, 1.55.0 |
| `ConsultTomObservation`, `SleeptimeComputeObservation` | their own texts | 1.28.1, 1.55.0 |

A kind with no port is rendered as the default
(`openhands/observation-render`). `UserRejectObservation` is
`Action rejected: <reason>`, `AgentErrorEvent` its error. The
observation event's `extended_content` (path-scoped rules) follows.
One text is the output string; anything else, images included, is a
list of `input_text` and `input_image` parts. The structured
observation is kept beside the output in `openhands:observation`.

## Mapping

| Native | Session | Notes |
| --- | --- | --- |
| conversation directory | session `id` the state's `id` (the directory name dashed when there is none; a derived one when a store would refuse it) | `created_at` the first timestamp; `cwd` `workspace.working_dir`; `harness` `openhands`, no version; header members `openhands:state` (the state but its metrics), `openhands:stats` (the metrics' totals and costs), `openhands:meta`, and `agentimport:source` |
| `SystemPromptEvent` | the first `config` on a path: `instructions_parts` `system` and `dynamic_context`, `tools_added`, `model` and `reasoning.effort` from the state's LLM | a later one is a delta; tool definitions (1.7+) carry no parameter schema, kept in `openhands:event` |
| `MessageEvent` from the user | `item`, user `message`: `input_text`, `input_image` per URL, then `extended_content` as `input_text` | `activated_skills`, `sender` and other members in `openhands:event`; from the environment or a hook, `openhands:source`; OpenHands' content-filter nudge, which it writes as the user, `openhands:source` `harness`. The error OpenHands writes as the user when the model's function call does not validate is the error's own text and cannot be told from a person's message |
| `MessageEvent` from the agent with `llm_response_id` | reasoning items, an assistant `message`, a `response` | without one, an assistant message with no response (`openhands/assistant-input`) |
| `ActionEvent`s of one `llm_response_id` | from the first: reasoning items and the `thought` message; then one `function_call` per action (`call_id` the tool call's id through `agentimport.CallIDs`, `arguments` as stored, `id` the Responses `fc_` id); a `response` | the action, `security_risk`, `summary` and the tool call's `origin` in `openhands:event` |
| `ObservationEvent` | `function_call_output`, rendered as above | `is_error` as `openhands:is_error` (gap `agentimport/tool-error`) |
| `UserRejectObservation` | a `reject` `decision`, `by` `human` (the user) or `policy` (a hook), `reason` the rejection's, then the output | |
| `AgentErrorEvent` for a call OpenHands could not run (unknown tool, invalid arguments) | a `reject` `decision` by `policy`, then the output | |
| `AgentErrorEvent` `Tool call interrupted before completion.` | an `answer` `decision` by `policy`, then the output | |
| other `AgentErrorEvent` | the output, `openhands:is_error` | |
| `Condensation` | a `compaction` or an `omit`, as above | members in `openhands:event` |
| `critic_result` | an `outcome` of kind `eval` on the judged entry, `score` the critic's, `details` the result | |
| `CondensationRequest`, `PauseEvent`, `InterruptEvent`, `ConversationErrorEvent`, `ServerErrorEvent`, `HookExecutionEvent`, `SecurityAnalysisEvent`, `ACPToolCallEvent` | a `custom` entry, `ns` `openhands`, the event whole | a hook's `call_id` the call it ran for |
| `ConversationStateUpdateEvent`, `TokenEvent`, `LLMCompletionLogEvent`, `StreamingDeltaEvent` | a `custom` entry | dropped with Slim |
| an unknown kind, or an event that does not decode as its kind | a `custom` entry | |

Approvals in confirmation mode are not recorded: the next run finds the
pending calls and runs them. A call that ran under `AlwaysConfirm` or
`ConfirmRisky` was approved or not asked about; the session holds no
`proceed` decision, as the RFC asks of a bare approval, and the policy
is reported. A call left pending at the end is written with no output
(`openhands/pending-call`): OpenHands sends it only once it is run or
rejected, and a session reads it as pending.

### Payload

- **Reasoning.** Anthropic `thinking_blocks` are `reasoning` items as
  `openresponses/providers/anthropic` decodes them: the text a
  `summary_text`, the signature in `encrypted_content`,
  `openhands:provider` `anthropic`; a `redacted_thinking` block is the
  `anthropic.redacted_thinking` item. A Responses
  `responses_reasoning_item` is a `reasoning` item with its `id`,
  `summary` strings as `summary_text`, `content` as `reasoning_text`
  and `encrypted_content`, `openhands:provider` `openai`. A
  `reasoning_content` with neither is a `reasoning` item with a
  `summary_text`; beside them it is litellm's copy of their text, kept
  in `openhands:reasoning_content` when it differs. Gemini's thought
  signatures have no place in OpenHands' tool calls and are not
  recorded.
- **Tool calls.** OpenHands stores a call as it replays it, not as the
  model sent it: the name normalised, the arguments repaired and
  re-encoded with Python's separators, `summary` and `security_risk`
  still inside, unparseable arguments replaced by
  `{"_openhands_malformed_tool_call": true, …}`. That is what the
  session holds.
- **Content.** `cache_prompt` flags that are set, and other part
  members, are kept in `openhands:event` by position.
- **Secrets.** OpenHands masks registered secrets in the model's text
  but not in tool outputs or user messages, and keeps its secret
  registry (encrypted or masked) in the state. The converter redacts
  nothing.

### Usage

Usage, cost and model are not on the events: `base_state.json`'s
`stats.usage_to_metrics` maps a usage ID (`agent`, `condenser`, an
LLM's own) to every call's token usage, latency and cost. A response
entry takes the token usage whose `response_id` is its
`llm_response_id`, and its latency: `input_tokens` the prompt tokens,
which are litellm's and count cache reads and writes, `cached_tokens`
the cache reads, `output_tokens` and `reasoning_tokens`; cache writes
(gap `agentimport/usage-detail`), the context window and the per-turn
tokens are `openhands:usage`. An `ACPAgent` counts the cache apart, so
reads and writes are folded into its input as providers/anthropic
does. The condenser's call is its compaction's `usage`. A token usage
no event names (a title, a refused condensation, an ACP turn, whose
usage carries the ACP session's ID) is a custom entry with a top-level
`usage` at the end of the leaf's path, as RFC 0001 records a paid call
with no response. Costs name no response and stay in `openhands:stats`
(`openhands/cost-unplaced`). The `task:<id>` and `delegate:<id>`
metrics are copies of the sub-agents' own, which their sessions carry,
and are not counted (`openhands/child-usage`). A crash between an event
and the next state save loses that call's usage.

### Sub-agents

The `task` and `delegate` tools run sub-agents as conversations under
`subagents/<id>/`, and record no link between a call and a sub-agent:
the sub-agent's state names no parent and the parent's events name no
child. Each user message of a sub-agent is matched to the parent's
task call whose `prompt`, or delegate call one of whose `tasks`, is
its text, and which it falls between the call and its answer in time
(`sender`, the parent's display name, is not in the parent's files and
is kept, not matched). Every match is reported.

| Native | Session |
| --- | --- |
| the first call that sends a sub-agent its prompt | a `subsession`: the sub-agent's own session `SubsessionID(parent, call_id)` (with `/<child id>` for the second sub-agent of one delegate call) with `parent_session` and `spawned_by`, the link before the output, the output's `parents` naming the sub-agent's last entry before its next prompt, or its leaf |
| a later call resuming the same sub-agent (`resume`, or a later `delegate`) | a `peer` link after the output, naming the sub-agent's session and the call: gap `openhands/resumed-subagent`, since one sub-agent answers several calls and a subsession is derived from one |
| a sub-agent no call matches, or two calls could have sent | its own session with its own ID, `parent_session` naming the parent alone (`openhands/subagent-unlinked`) |
| a sub-agent directory converted on its own | `parent_session` the directory's parent conversation |

Sub-agents nest; they are followed 16 deep at most, never into a
directory being converted already. The command skips a sub-agent's
directory given as an input when its parent's conversion holds it.

### Forks

A fork is a new directory beside its source holding copies of the
source's events with the same IDs. The agent server's `meta.json`
names the source (`forked_from_conversation_id`, in `parent_session`)
and the event it forked at (`forked_from_event_id`): the copies are
the events up to it, else those whose IDs the source's `events/`
names. The copies convert as the session's own and are reported
(`openhands/fork-copied`); a copied response keeps the usage the fork's
metrics hold for it, which a fork made with `reset_metrics=False`
copies from the source, in `openhands:copied_usage`. Without
`meta.json` a fork is not recognised.

## Extension types

Open Responses carries types outside its spec as slug-prefixed
extensions, kept verbatim. The converter writes this:

| Extension | Kind | Written for |
| --- | --- | --- |
| `anthropic.<type>` | item | a thinking block other than `thinking`: `redacted_thinking`, as openresponses' Anthropic provider emits it |

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead. The codes
every converter can report are in [README.md](README.md).

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/fold-head` | gap | OpenHands put a condensation's summary after the events it kept at the head of the context; the compaction puts its summary first and pins the head after it. |
| `agentimport/reasoning-provider` | gap | A reasoning signature (Anthropic's, OpenAI's encrypted reasoning) is in `encrypted_content`, tagged `openhands:provider`. |
| `agentimport/tool-error` | gap | A tool result's `is_error`, or an agent error, is `openhands:is_error`. |
| `agentimport/usage-detail` | gap | Cache writes, counted in the prompt tokens, are `openhands:usage`. |
| `openhands/resumed-subagent` | gap | A call resumed a sub-agent an earlier call started; a peer link after its output names it. |
| `openhands/bad-content` | dropped | A content part that is not an object or not text or an image, or extended content on an assistant message. |
| `openhands/bad-file-name` | dropped | A file of `events/` whose name is not an event file's. |
| `openhands/bad-metrics` | dropped | A usage ID's metrics that are not an object. |
| `openhands/bad-tool` | dropped | A tool of a system prompt that is neither chat tool params nor a tool definition. |
| `openhands/batch-member-content` | dropped | Thought or reasoning on an action after the first of its response, which OpenHands does not send. |
| `openhands/dir-unread` | dropped | A directory of the conversation the converter does not read. |
| `openhands/file-unread` | dropped | A file of the conversation directory the converter does not read. |
| `openhands/index-gap` | dropped | An event file after a missing index, which OpenHands does not read. |
| `openhands/observations-dir` | dropped | The directory where tools saved full outputs is not read. |
| `openhands/subagent-cycle` | dropped | A sub-agent directory being converted, or converted already; it is not converted again. |
| `openhands/subagent-failed` | dropped | A sub-agent that could not be converted. |
| `openhands/subagent-nesting` | dropped | Sub-agents nested past 16 conversations. |
| `openhands/subagent-unreadable` | dropped | A sub-agent directory, or the `subagents` directory, that cannot be read. |
| `openhands/subagents-alone` | dropped | Sub-agents not converted, with `Options.Alone`. |
| `openhands/unreached` | dropped | An event the converter did not reach; a bug if it occurs. |
| `openhands/acp-agent` | lossy | The agent is an `ACPAgent`, which records an external agent's tool calls rather than the model's context. |
| `openhands/bad-system-prompt` | lossy | A system prompt that is missing or not text; the instructions are empty. |
| `openhands/condensation` | lossy | A condensation: a compaction, or an omit of what it forgot. |
| `openhands/confirmation-policy` | lossy | The conversation ran under a confirmation policy, and approvals are not recorded. |
| `openhands/critic` | lossy, or dropped | A critic result is an eval outcome; one that is not an object is dropped. |
| `openhands/empty-head` | lossy | The state's head is deliberately empty; the last event is the leaf. |
| `openhands/fork-copied` | lossy | A fork's first events are copies of its source's, converted as its own. |
| `openhands/fork-copy-unknown` | lossy | A fork whose copied events cannot be told apart. |
| `openhands/instructions-join` | lossy | The system prompt and the dynamic context are two parts, joined with a blank line, where OpenHands sends two blocks or a newline. |
| `openhands/interrupted` | lossy | A call a pause or a crash cut: an answer decision before OpenHands' output. |
| `openhands/invalid-call` | lossy | A call OpenHands could not run, its arguments as OpenHands stored them. |
| `openhands/merged-user-messages` | lossy | A user message OpenHands merges with the one before; the session keeps both. |
| `openhands/model-from-state` | lossy | The model is the state's last saved one; a switch is not recorded per event. |
| `openhands/no-observation` | lossy | An observation event with no observation; the output is empty. |
| `openhands/no-state` | lossy | The conversation has no `base_state.json`: no working directory, leaf or usage. |
| `openhands/no-system-prompt` | lossy | A path starts with no system prompt; a config of the state's model starts it. |
| `openhands/no-timestamp` | lossy | No timestamp; `created_at` is `Options.CreatedAt` or the epoch. |
| `openhands/no-tool-call` | lossy | An action with no tool call; the call is made from its name and ID with empty arguments. |
| `openhands/no-version` | lossy | OpenHands records no version; the harness names none. |
| `openhands/observation-render` | lossy | An observation kind whose rendering is not ported; the default rendering is used. |
| `openhands/output-path` | lossy | An output names a file the tool saved, rebuilt from its directory and hash. |
| `openhands/output-truncation` | lossy | An output is cut as the tool, or OpenHands at send time, cut it. |
| `openhands/pending-call` | lossy | A call nothing answers; OpenHands sends it once it is run or rejected, and the session reads it as pending. |
| `openhands/rejected` | lossy | A call rejected by the user, a hook or OpenHands: a reject decision before the output. |
| `openhands/session-id` | lossy | The session ID is derived from the conversation's. |
| `openhands/split-batch` | lossy | An output written between the calls of a response; the session writes the calls together. |
| `openhands/subagent-alone` | lossy | A sub-agent converted on its own names its parent in `parent_session` alone. |
| `openhands/subagent-match` | lossy | A sub-agent matched to its call by prompt and time. |
| `openhands/subagent-unlinked` | lossy | A sub-agent no call is linked to; `parent_session` alone. |
| `openhands/summary-event` | lossy | A condensation summary written as an event, which OpenHands only synthesizes. |
| `openhands/summary-restated` | lossy | Earlier summaries OpenHands still sends are restated in the compaction's summary. |
| `openhands/system-prompt-again` | lossy | A later system prompt is a config delta. |
| `openhands/timestamp-zone` | lossy | Timestamps with no zone are read in `Options.Location`. |
| `openhands/tools-unrecorded` | lossy | Tool definitions with no parameter schema; the config carries names and descriptions. |
| `openhands/usage-joined` | lossy | No usage is recorded for a response. |
| `openhands/version-guess` | lossy | A shape that tells the SDK release, or does not: chat tool params (1.0 to 1.6), an earlier summary kept in the view (1.12 and later). |
| `openhands/view-enforced` | lossy | OpenHands leaves an event out of the context while building the view; an omit leaves out its items. |
| `openhands/child-usage` | kept, or dropped | The sub-agents' copies of their metrics are in `openhands:stats` and not counted. |
| `openhands/cost-unplaced` | kept, or dropped | Per-call costs, which name no response, are in `openhands:stats`. |
| `openhands/event-members` | kept, or dropped | Event members the session has no place for are `openhands:event`. |
| `openhands/meta` | kept, or dropped | `meta.json` is `openhands:meta`. |
| `openhands/observation` | kept, or dropped | The structured observation is `openhands:observation`. |
| `openhands/record` | kept, or dropped | An event that never enters the context is a custom entry. |
| `openhands/state` | kept, or dropped | The state beyond what is mapped is `openhands:state`. |
| `openhands/assistant-input` | kept | An assistant message no response produced is an input item. |
| `openhands/compaction-marker` | kept | A custom marker is the first_kept of a compaction that keeps nothing after its summary. |
| `openhands/condensation-noop` | kept | A condensation that forgets nothing the context holds is a custom entry. |
| `openhands/duplicate-output` | kept | A second answer to a call is a custom entry. |
| `openhands/extension-item` | kept | A redacted thinking block is the `anthropic.redacted_thinking` item. |
| `openhands/fork-usage` | kept | A response a fork copied keeps its usage in `openhands:copied_usage`. |
| `openhands/harness-user-message` | kept | A user message the environment, a hook or OpenHands wrote carries `openhands:source`. |
| `openhands/orphan-output` | kept | An answer to a call no action on its path made is a custom entry. |
| `openhands/reasoning-content` | kept | A `reasoning_content` that differs from the reasoning items is `openhands:reasoning_content`. |
| `openhands/undecodable-event` | kept | An event whose members do not fit its kind is a custom entry. |
| `openhands/unknown-kind` | kept | An event of a kind the converter does not know is a custom entry. |
| `openhands/unmatched-usage` | kept | A call no event names is a custom entry with its usage. |
| `openhands/usage-member` | kept | The context window and per-turn tokens are `openhands:usage`. |
| `openhands/bad-forgotten-id` | repaired | A forgotten event ID that is not a string forgets nothing. |
| `openhands/bad-timestamp` | repaired | A timestamp that does not read; the entries take the time before. |
| `openhands/call-id` | repaired | A repeated or ill-formed call ID is renamed; the native one is in `openhands:event`. |
| `openhands/call-id-mismatch` | repaired | An action's `tool_call_id` differs from its tool call's id; outputs naming either answer it. |
| `openhands/duplicate-event-id` | repaired | An event ID another event has; a parent naming it names the first. |
| `openhands/id-mismatch` | repaired | An event whose `id` is not the one its file name holds; parents resolve by the file name's, as OpenHands resolves them. |
| `openhands/duplicate-index` | repaired | Two event files with one index; one is read. |
| `openhands/leaf-missing` | repaired | `leaf_event_id` names no event read; the last event is the leaf. |
| `openhands/malformed-event` | repaired | An event file that is empty, not JSON or not an object is skipped. |
| `openhands/malformed-file` | repaired | `base_state.json` or `meta.json` that does not read is skipped. |
| `openhands/mistyped-member` | repaired | An event member of another JSON type is kept as written and not mapped. |
| `openhands/no-response-id` | repaired | An action with no `llm_response_id`; its response gets one. |
| `openhands/parent-after` | repaired | A `parent_id` naming an event not before it; the event before is the parent. |
| `openhands/parent-missing` | repaired | A `parent_id` naming no event; the event before is the parent. |
| `openhands/response-id-repeated` | repaired | A response ID another response has; this one gets a suffix. |
