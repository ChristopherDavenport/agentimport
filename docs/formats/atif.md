# ATIF

ATIF, the Agent Trajectory Interchange Format, is Harbor's format for
an agent's run: one JSON document per trajectory, holding the agent,
its steps and, since v1.7, the trajectories of its sub-agents. Harbor's
agents write it, most of them by converting their own native log after
the run, and so does `agentsession/export` (`ToATIF`). The packages are
`atif`, which reads the format as it is, and `atif/convert`, which
converts it.

## Where the format is specified

Harbor specifies it: `rfcs/0001-trajectory-format.md` (v1.8) and the
Pydantic models in `src/harbor/models/trajectories/`, every one with
`extra="forbid"`, in `github.com/laude-institute/harbor`. This package
was written against commit `2ede27fb` (2026-10-15; latest tag v0.9.0),
with the models' history read back to v1.0 for what changed per
version, and against Harbor's own readers of ATIF for what a document
says the model saw: its SFT export (`src/harbor/utils/traces_utils.py`)
and its ATIF-to-native resume converters (`agents/installed/claude_code.py`,
`codex.py`).

Where the RFC and the models disagree, the package follows the models,
which are what validates a document:

- RFC v1.0 writes `subagent_trajectory_ref` as one object; the models
  take a list from the first. Both are read.
- RFC v1.5 lists `tool_definitions` alone; the models added
  `is_copied_context` and `continued_trajectory_ref` under the same
  version string. A v1.5 document may carry any of them.

No model gates a member by `schema_version`, so a member newer than the
declared version is read; a version outside v1.0 to v1.8 is read with
v1.8's members and reported.

agentsession's own `atif` package is not this one's reader: it decodes
a document whole, so one bad step fails it, its validator refuses
documents Harbor accepts (an empty `tool_call_id`, a `+0000` offset),
and it re-encodes `content: ""`, large integers and number spellings
differently. The converter imports `agentsession/export` only in tests.

## Layout

```
<jobs>/<job>/<trial>/agent/trajectory.json
<jobs>/<job>/<trial>/steps/<step>/agent/trajectory.json
<jobs>/<job>/<trial>/user-agent/trajectory.json
```

Beside a trajectory, Terminus 2 writes its continuations,
`trajectory.cont-<n>.json`, named by the root's
`continued_trajectory_ref`, and its summarisation helpers,
`trajectory.summarization-<n>-{summary,questions,answers}.json`, named
by a result's `subagent_trajectory_ref[].trajectory_path`. Images and
audio are files the parts name, by convention under `images/` and
`audio/` beside the document. agentsession's exporter writes
`<session>.json` and spills inline media the same way.

A file is told to be ATIF by its content: a JSON object whose
`schema_version` starts `ATIF-v`, read from the members before it
(`atif.Probe`). `atif.FindSessions` lists every such `*.json` under a
directory.

## Reading

- **One document, read block by block.** The root, the agent, each
  step, tool call, observation, result, reference, content part and
  metrics object is decoded on its own. A block that is not an object
  (a step that is a string, `null` in `steps`) is kept as written with
  its error, and its neighbours still read. Every object keeps the
  members it does not declare, which Harbor's models refuse, and a
  declared member of another JSON type is kept as written, so a
  document decoded and encoded again is the same JSON value.
- **Numbers as written.** Token counts, `step_id`, `llm_call_count`,
  `duration_sec` and `reasoning_effort` are kept as written; `atif.Int`
  reads `12.0` and `"12"` as 12, as the models' lax mode does.
- **Truncated documents.** Terminus 2 rewrites `trajectory.json` in
  place after every episode, so a run killed mid-write leaves a
  document cut short. The reader salvages it: every root member that
  parses whole, and every step of a cut `steps` array that ends before
  the cut. The step cut in two is a unit that could not be read.
- **What the reader refuses.** Bytes that are not a JSON object, nor the
  start of one, are not a document: `atif.Read` fails and so does the
  conversion. A document over 1 GiB is refused.

### Units

ATIF is one JSON value per file, not lines, so the converter's tally
counts units: each document's root and each of its steps, for every
document the conversion reads (the main one, its continuations, the
sub-agent files it names) and every trajectory they embed, at any
depth. A report says so on its first line (`atif: 18 roots and steps:
…`), from `Report.Unit`. A root is `mapped` when its document
converts; a step is `mapped`, `kept`, `dropped` or `malformed` as a
line would be. A finding's `Line` is the step's 1-based position in
its `steps` array, not its `step_id`, which may be wrong, and 0 for the
root; its `File` names a related file relative to the conversion's
directory, and an embedded trajectory by `#` and its `trajectory_id`
(or its index) after the document that embeds it.

## What the model saw

A step is a delta: a system prompt or message, a user message, or one
agent response with the observations its actions produced. ATIF does
not say how the next request was built from them, and Harbor's own
readers disagree: its SFT export sends every result as a user message
and system steps as user messages; its Claude Code resume drops system
steps and matches results to calls by `source_call_id`; its Codex
resume sends system steps as developer messages. The conversion
follows the Responses shape the session format holds:

- results matched to their calls by `source_call_id` are those calls'
  outputs, as both resume converters do;
- a result naming no call is a user message after the step's outputs,
  as the SFT export sends it;
- system steps before the first user or agent step are the
  instructions, a setting the session format carries across a
  compaction; a later one is a system message;
- RFC 0001's context-management convention is applied as its one
  normative statement of context: after a system step with
  `extra.context_management.boundary` `replace`, the context is the
  boundary's results' content and what follows.

## Mapping

Steps are converted in document order, each step's entries after the
previous step's: a document is a line, never a tree.

| Native | Session | Notes |
| --- | --- | --- |
| root | session | `id` the `session_id` (see below); `created_at` the earliest step timestamp; `harness` `agent.name` and `agent.version`; header members `atif:version`, `atif:trajectory_id`, `atif:session_id` when the ID is not it, `atif:continued_trajectory_ref`; header member `agentimport:source` |
| `agent.model_name`, `agent.tool_definitions`, leading system steps | the first `config`: `model`, `tools_added`, `instructions` (one step) or `instructions_parts` `step/<n>` (several) | a tool definition that is not a chat-completions function tool is kept in `atif:tool_definitions`; a leading step's extra and members in `atif:system_steps` |
| `agent.extra`, `notes`, `final_metrics`, root `extra` | header members `atif:agent_extra`, `atif:notes`, `atif:final_metrics`, `atif:extra` | `final_metrics` is never usage: some producers count sub-agents in it |
| undeclared root or agent members | header members `atif:root`, `atif:agent_members` | Harbor's models refuse them |
| `user` step | `item`, user `message` | text as `input_text`, parts as below |
| leading `system` steps | the first `config` | see above |
| later `system` step | `item`, `system` message | its results as below |
| `system` step, `context_management` boundary `replace` | `compaction`: `summary` the user message of its results' content, `first_kept` a marker (nothing before the boundary is kept), `config` the settings in force | with no content, Terminus 2's is rebuilt as below; another's takes the user step after it as its summary, as Vibe writes it, when that step carries no results; with neither, a custom entry; the step's message in `atif:step` |
| `system` step, boundary `append` or type `injection` | each result's content a user message | |
| `system` step, boundary `truncate` or type `pruning` | `custom` `{"step": …}` | a gap: it names nothing removed |
| `system` step, another boundary | `custom` `{"step": …}` | |
| `agent` step | a `config` delta when its `model_name` or `reasoning_effort` is not in force; a `reasoning` item of `reasoning_content` (`summary_text`); an assistant `message`; a `function_call` per tool call; a `response`; its results | items carry `response` `atif-step-<n>` |
| `tool_calls[]` | `function_call`: `arguments` the object as written; ids through `agentimport.CallIDs` | a renamed id's native one in `atif:call_id`; the call's extra in `atif:extra` |
| `metrics` | `response.usage`: `input_tokens` `prompt_tokens` (cached included), `cached_tokens`, `output_tokens` `completion_tokens`, `reasoning_tokens` from `extra.reasoning_tokens`, `extra.reasoning_output_tokens` or `extra.completion_tokens_details.reasoning_tokens` | the rest (`cost_usd`, `extra`, token ids, `logprobs`) in `atif:metrics` |
| `llm_call_count` above 1 | `response.attempts` | |
| `llm_call_count` 0 | items with no `response`, the calls marked `atif:llm_call_count: 0` | a gap |
| `observation.results[]` naming a call of the step | `item`, `function_call_output` of that call | a string is the output; parts are `input_text` and `input_image` parts; the result's extra in `atif:extra` |
| a result naming a call of an earlier step | that call's output | |
| results naming no call, at least one per call, on a step with calls | the first one per call the calls' outputs in order, the rest user messages after them | mini-swe-agent's chat shape: tool messages, then the environment's user messages |
| another result naming no call | `item`, user `message` after the step's outputs | |
| a result naming a call already answered | `custom` `{"result": …}` | |
| image part | `input_image`: a URL as written; a file inside the conversion's directory read and inlined as a data URL | anything else a text placeholder `[image: <path>]` |
| audio part, or an unknown part type | an `atif.<type>` extension part carrying the part | the file is not read |
| `is_copied_context: true` | the session's own entries, marked `atif:copied`; the response's usage in `atif:usage` | see *Copied history* |
| `timestamp` | `ts` of every entry made from the step | naive times are UTC; none, or one that does not parse, takes the entry before's |
| `step_id` | member `atif:step_id` on every entry made from the step | |
| step `extra` | `atif:extra` on the step's last entry | |
| undeclared step members; agent members on another step | `atif:step` on the step's last entry | |
| `continued_trajectory_ref` | the next document converted as its own session, with `parent_session`; this one ends with a `continued_in` link | |
| `subagent_trajectory_ref` | see *Sub-agents* | |

ATIF records no response status: every response has none.

### Terminus 2's summarisation

Terminus 2 records a summarisation as a replace boundary with no
content, three helper files and a handoff user step. What it sends the
model next is not the RFC's rule: it keeps its first message, the
questions helper's prompt and the questions it asked, then the handoff
(`terminus_2.py`, `_summarize`). The conversion rebuilds that: the
compaction's summary is the first user step's message, its pinned items
are the questions helper's last user and agent steps, read from its
file, and the handoff converts as the user step it is.

### Session IDs

The main session's ID is its `session_id`, or its `trajectory_id`, or
an ID derived from its content. A sub-agent spawned by a call is
`agentsession.SubsessionID(parent, call_id)`, as RFC 0001 derives one.
Another document keeps its `session_id` unless a session of the
conversion has it already, which a sub-agent sharing its parent's run
ID, as ATIF allows, does; it is then derived from its parent's ID and
the document, and the native one kept in `atif:session_id`.

A session store names a file by the session ID, and agentsession's
stores take letters, digits, `.`, `_` and `-`, at most 200 of them. A
`session_id` or `trajectory_id` outside that rule (`run 1`, an
exporter's `<session>/<leaf>`) is not the ID: one is derived from it,
the same on every conversion, and the native one kept in
`atif:session_id` or `atif:trajectory_id`.

### Sub-agents

ATIF says nothing of a sub-agent's lifetime. A reference on a call's
result is read as a callee the call waited for:

| Native | Session |
| --- | --- |
| a reference on a call's result, naming an embedded trajectory of the same document by `trajectory_id` or a file by `trajectory_path` | its own session with `parent_session` and `spawned_by`; the parent gets a `subsession` link before the output, whose `parents` names the child's leaf |
| a reference on a result with no call (Terminus 2's summarisation helpers, RFC 0001's compaction example) | its own session with `parent_session` and no `spawned_by`; the parent gets a `subsession` link with no `call_id` |
| an embedded trajectory no reference names (Qwen Code's orphans, Muse Code's children) | its own session with `parent_session`, no link |
| a reference that cannot be resolved (a URL, a missing file, `session_id` alone as before v1.7, a file outside the conversion's directory) | kept in `atif:refs` on the output, or a custom entry |
| Claude Code steps with `extra.is_sidechain`, by `extra.agent_id` | its own session, out of the main one: a subsession of the call whose result's `extra.tool_result_metadata.tool_use_result.agentId` names the agent, linked as above, when its `status` is `completed` or absent; else a `peer` of that call, the link after its output and no `spawned_by`; with no such call, `parent_session` alone |

Harbor's Claude Code adapter writes a session and its sub-agents into
one trajectory, sorted by time, marking a sub-agent's steps
`is_sidechain` (`claude_code.py`). The main model never saw them, so
they leave the main session. Their findings name the document with
`#sidechain-<agent>` after it, and a step by its position in the
document.

Resolution is scoped as ATIF scopes it: a `trajectory_id` names an
embedded trajectory of the document holding the reference. Each file
and each embedded trajectory converts once: a reference to one already
converted (itself, an ancestor, another reference's), through a
symbolic link or not, is reported and not converted again, and a
continuation chain that comes back on itself ends. Whatever names a
document, the conversion follows sub-agents and continuations 32 deep
and never into a document it is converting already. `Convert` reads no
file unless `Options.Dir` names the directory; `ConvertFile` reads the
document's own directory, and nothing outside it once symbolic links
are resolved, so a document cannot pull another file into a session.

### Copied history

Terminus 2 starts a continuation, and a summarisation helper, with the
history it carries over re-materialised as steps marked
`is_copied_context`. As the module's other converters do with copied
history, the steps are converted as the session's own entries, and a
response's usage is kept off the response entry, in `atif:usage`, so
it is counted once.

### Documents agentsession's exporter wrote

A document whose root `extra.agentsession.format` starts
`agentsession/` was written by `agentsession/export`. Its steps carry
the raw items they were built from in `extra.openresponses`, and each
such step is rebuilt from them rather than mapped from its declared
fields; a step without them falls back to the mapping above.

| Exporter step | Session |
| --- | --- |
| `openresponses.item` | the item entry, with `visible`, `source` and `queued_from` |
| `openresponses.items`, `openresponses.response` | the item entries in order, each with the response's `response_id`, then the response entry the exporter carried whole |
| a result's `openresponses.item` | the output entry, after the link to the sub-agent its reference names |
| `context_management` with `openresponses.item`, `pinned`, `first_kept` | a compaction with its summary and pinned items; the copied steps after it, the window it kept, are put back before it, the first of them its `first_kept` |
| `branch_from` with `openresponses.item` | a branch summary |
| `omitted` | a config entry at the end whose `omit.items` names the entries |
| a namespaced member the exporter merged in | back on the entry |
| the exporter's records (`run`, `calls`, `labels`, `info`, `custom`, config ids, `environment`, `links`, `outcome` and the like) | `atif:extra` on the step's last entry; `records` stays empty |

The exporter does not write the settings (instructions, the
compaction's checkpoint): the first config is the agent's model and
tools, and a step's model a config delta. An image the exporter spilled
to `images/` is read back as a data URL.

## Extension types

Open Responses carries types outside its spec as slug-prefixed
extensions, kept verbatim. The converter writes these:

| Extension | Kind | Written for |
| --- | --- | --- |
| `atif.audio` | content part | an audio part, `source` with its path, media type and duration |
| `atif.image` | content part | an image in an agent's message, which an assistant message has no part for |
| `atif.<type>` | content part | a part of a type ATIF does not define |

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead. The codes
every converter can report are in [README.md](README.md).

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/usage-detail` | gap | Metrics carry a cost or extra counts Responses usage has no place for; they are in `atif:metrics`. |
| `atif/callless-subagent` | gap | A sub-agent no call spawned is linked by a `subsession` link with no `call_id`, which RFC 0001 does not define. |
| `atif/compaction-unrecorded` | gap | A replace boundary records no summary, in its results or a user step after it; it is a custom entry and the context keeps what came before. |
| `atif/context-pruning` | gap | A pruning or truncate boundary names nothing it removed; it is a custom entry and the context keeps everything. |
| `atif/deterministic-dispatch` | gap | A step with `llm_call_count` 0: calls no model made are function calls marked `atif:llm_call_count`, with no response. |
| `atif/continuation-missing` | dropped | The continuation cannot be read; its name is `atif:continued_trajectory_ref`. |
| `atif/empty-step` | dropped | A step with nothing to convert. |
| `atif/media-unread` | dropped | An image file outside the conversion's directory, or named while no directory is given, is not read; it is a text placeholder. |
| `atif/metrics` | kept, or dropped | What the metrics hold beside the three counts is `atif:metrics`. |
| `atif/part-malformed` | dropped | A content part that is not an object. |
| `atif/ref-unresolved` | dropped | A sub-agent reference resolves nothing; it is kept, and no session is made. |
| `atif/result-empty` | dropped | A result with no content, no call and no sub-agent. |
| `atif/result-malformed` | dropped | A result that is not an object. |
| `atif/subagent-nesting` | dropped | A sub-agent or continuation nested past 32 documents, or one the conversion is converting already, is not converted; its units are dropped. |
| `atif/subagent-failed` | dropped | A sub-agent's or continuation's document could not be converted; its units are dropped. |
| `atif/tool-call-malformed` | dropped | A tool call that is not an object. |
| `atif/unreached` | dropped | The converter did not reach a step; a bug if it occurs. |
| `atif/branch-summary` | lossy | An exporter's branch summary names an entry of the exported session as `from`. |
| `atif/call-without-result` | lossy | A tool call no result answers is left without an output. |
| `atif/compaction-summary-next` | lossy | A replace boundary's results hold no content; the user step after it, which carries no results, is the summary, as Vibe writes it. |
| `atif/terminus-handoff` | lossy | Terminus 2's summarisation: the compaction's summary is its first message and its pinned items the questions helper's prompt and questions; without the helper's file they are missing. |
| `atif/context-injection` | lossy | An append boundary's results are user messages. |
| `atif/copied-context` | lossy | Copied history is converted as the session's own entries, its usage in `atif:usage`. |
| `atif/exporter-config` | lossy | The exporter writes no compaction checkpoint; the compaction's config is the settings where it is rebuilt. |
| `atif/folded-history` | lossy | The exporter's kept window, written after its compaction, is put back before it. |
| `atif/llm-call-count` | lossy | `llm_call_count` above 1 is the response's `attempts`, which counts retries of one call. |
| `atif/media-missing` | lossy | An image names no file, a URL no request can name, or a file that cannot be read; it is a text placeholder. |
| `atif/metrics-invalid` | lossy | A token count that is not a count is left out of the usage and kept in `atif:metrics`. |
| `atif/native-available` | lossy | Harbor's adapter for Claude Code, Codex or pi wrote the document from a log this module converts directly, with less lost. |
| `atif/no-response` | lossy | An exporter's agent step carries no response entry. |
| `atif/no-timestamp` | lossy | The document has no timestamp; `created_at` is the time of the step naming it, the file's modification time, the time the conversion was given, or the epoch. |
| `atif/omitted` | lossy | The exporter's list of items the request at the leaf left out is a config entry omitting them, whatever rule left them out. |
| `atif/raw-items` | lossy | An exporter step's raw items do not decode; it is mapped from its declared fields. |
| `atif/raw-mismatch` | lossy | An exporter step's declared fields disagree with its raw items; the raw items are converted. |
| `atif/reasoning-effort` | lossy | A reasoning effort Responses does not define, or a number. |
| `atif/reasoning-text` | lossy | `reasoning_content` is summary text with no `encrypted_content`. |
| `atif/ref-repeated` | lossy | A reference names a document the conversion already converted; it is not converted again or linked. |
| `atif/response-status` | lossy | ATIF records no response status. |
| `atif/result-unknown-call` | lossy | A result names a call no step made; it is a user message, the id in `atif:source_call_id`. |
| `atif/result-without-call` | lossy | A result that names no call is a user message after the step's outputs. |
| `atif/subagent-unlinked` | lossy | An embedded trajectory no reference names is a session with `parent_session` alone. |
| `atif/synthetic-tool-calls` | lossy | Terminus 2 parsed its calls from the model's text and rewrote its message. |
| `atif/sidechain` | lossy | Claude Code sub-agent steps the adapter interleaved with the main session's are the sub-agent's own session: a subsession or peer of the call whose result names the agent, or one with `parent_session` alone. |
| `atif/system-message` | lossy | A system step after the conversation started is a system message. |
| `atif/system-prompt` | lossy | A system step before the first user or agent step is the instructions. |
| `atif/tool-definition` | lossy | A tool definition that is not a chat-completions function tool is kept in `atif:tool_definitions`, not in the settings. |
| `atif/version` | lossy | `schema_version` is absent or not one Harbor defines; the document is read with v1.8's members. |
| `atif/agent-media` | kept | An image in an agent's message is an `atif.image` part. |
| `atif/agent-only-field` | kept, or dropped | A step that is not an agent's carries members of agent steps alone; they are `atif:step`. |
| `atif/audio` | kept | An audio part is an `atif.audio` part. |
| `atif/compaction-marker` | kept | A custom marker entry is the `first_kept` of a compaction that keeps nothing. |
| `atif/context-management` | kept | A context-management step of a type and boundary the RFC does not define is a custom entry. |
| `atif/duplicate-result` | kept | A second result for an answered call is a custom entry. |
| `atif/exporter-records` | kept, or dropped | The exporter's records of entries that are not items are `atif:extra`. |
| `atif/extra` | kept, or dropped | An `extra` object is `atif:extra` on the entry made from its owner, or a header member. |
| `atif/final-metrics` | kept, or dropped | `final_metrics` is the header member `atif:final_metrics`. |
| `atif/mistyped-member` | kept, or dropped | A declared member of another JSON type is kept with its object's unmapped members. |
| `atif/notes` | kept, or dropped | `notes` is the header member `atif:notes`. |
| `atif/part-type` | kept | A part of a type ATIF does not define is an `atif.<type>` part. |
| `atif/source` | kept | A step of a source ATIF does not define is a custom entry. |
| `atif/unknown-member` | kept, or dropped | Members Harbor's models do not declare are kept with their object's unmapped members. |
| `atif/arguments` | repaired | A tool call's arguments are absent, a string, or not an object; they are `{}`, the string, or their JSON. |
| `atif/call-id` | repaired | A call id that is empty, repeats another's or holds characters providers refuse was renamed; the native one is `atif:call_id`. |
| `atif/continuation-repeated` | repaired | A continuation names a document already converted; the chain ends. |
| `atif/metrics-count` | repaired | A token count is written as a float or a string; it is read as the integer. |
| `atif/no-agent` | repaired | The document has no agent, or one with no name; the session names no harness. |
| `atif/raw-tool-result` | repaired | Harbor's Claude Code adapter appends the tool's stdout and metadata to a result; the output is the block the model saw. |
| `atif/ref-object` | repaired | `subagent_trajectory_ref` is one object, as RFC v1.0 wrote it; it is read as a list of one. |
| `atif/result-foreign-call` | repaired | A result names a call of an earlier step, which Harbor's validator refuses; it is that call's output. |
| `atif/result-paired-by-order` | repaired | A step's results name no call, at least one per call; the first one per call are the calls' outputs in order, the rest user messages. |
| `atif/session-id` | repaired | The session ID is not the document's `session_id`: it has none, a session store refuses it, or another session of the conversion has it. |
| `atif/step-id` | repaired | `step_id` values are not the steps' positions; steps are read in document order. |
| `atif/step-malformed` | repaired | A step that is not an object, or the step a truncated document is cut inside, is skipped. |
| `atif/subagent-id` | repaired | An embedded trajectory has no `trajectory_id`, or shares one; no reference can name it, or a reference names the first. |
| `atif/subagent-malformed` | repaired | An embedded trajectory that is not an object is skipped. |
| `atif/timestamp` | repaired | A step timestamp has no time zone and is read as UTC, or does not parse and takes the time before it. |
| `atif/tool-call-name` | repaired | A tool call has no function name. |
| `atif/truncated` | repaired | The document is cut short; the members and steps before the cut are converted. |
