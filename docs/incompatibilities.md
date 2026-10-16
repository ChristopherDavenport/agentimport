# What the session format cannot hold

Each entry is a `gap` finding: something one or more native formats
record that the Agent Session Format (RFC 0001, format 0.12) has no
place for. The converters keep it in a namespaced member or drop it,
and report it under the code given. Each is a question for the RFC;
the converter changes when the answer lands.

A payload type the Open Responses spec does not define is not a gap.
Open Responses carries such types as slug-prefixed extensions, kept
verbatim (`anthropic.redacted_thinking`, `openai.custom_tool_call`),
and each source needs some. They are `kept` findings, and each
source's document lists the extension types it writes.

The per-source documents under `docs/formats/` list every finding
code, including the lossy, kept and dropped ones that are this
module's choices rather than the format's limits.

| Code | Sources | What is missing | Fallback |
| --- | --- | --- | --- |
| `agentimport/provenance` | all | No member says a session was imported, from which format and native ID, by which converter. | Header member `agentimport:source`. |
| `agentimport/reasoning-provider` | Claude Code, pi, Codex, Cline/Roo, OpenHands | A `reasoning` item's `encrypted_content` is opaque and untagged (Anthropic's thinking signature, Gemini's thought signature): nothing says which provider and model family can replay it. The response entry's `model` says which model wrote it, but only for items that carry a `response`. | Kept as `encrypted_content`, as `providers/anthropic` does; the item entry carries `<ns>:provider`. |
| `agentimport/tool-error` | Claude Code, pi, Cline/Roo, OpenHands | `function_call_output` has no error flag; Anthropic's `is_error` and pi's `isError` tell the model the call failed. | Output kept; the item entry carries `<ns>:is_error`. |
| `agentimport/usage-detail` | Claude Code, pi, Codex, ATIF, Cline/Roo, OpenHands | Responses usage has input, cached input, output and reasoning tokens only: no cache-write tokens, cache TTL split, server tool counts, service tier or cost. | Folded where it has a meaning (cache writes into input); the rest on the response entry as `<ns>:usage`. |
| `agentimport/vcs-branch` | Claude Code, Codex | An `env` entry's `vcs` has `system`, `revision` and `dirty`; no branch. Claude Code records the branch on every line and no revision. | vcs member `<ns>:branch`. |
| `agentimport/context-replace` | pi, Cline | An item can be left out of context (`omit`) but not replaced in place. pi's `context_edit` with a replacement changes an earlier message's content where it stands, branch-relative; classic Cline's `context_history.json` replaces a block's text in every request after the edit (the truncation notice in message 1, `[Continue assisting the user!]` in message 0, a notice in place of a repeated file read). | pi: a user or extension message is omitted and its replacement written as a new user message where the edit was; an assistant message or tool result stays in context as it was, since leaving out a call or its output breaks the pair a provider needs, and the replacement is a custom entry. Cline: every edit is a custom entry where it first applied and the original stays in context; the outputs of message 1's calls, for which Cline sends `result missing` once a range deleted their results, stay as they were. |
| `agentimport/fold-head` | Cline/Roo, OpenHands | A compaction's `summary` comes first in context, before `pinned`. Roo before 3.43 sent the task's first message ahead of an assistant-role summary, and a sliding-window marker after the first message, so a request never starts with the assistant. OpenHands inserts a condensation's summary at an offset after the events it keeps at the head (the system prompt and the first user message, with its condenser's `keep_first`). Putting the head in `summary` would dress a verbatim message as a summary, and an assistant summary first would build a request providers refuse. | The compaction's summary is a user message of Roo's summary or marker text, or OpenHands' summary, the head pinned after it. |
| `agentimport/omit-summary` | pi | `omit` names item entries alone; a branch summary or compaction summary cannot be left out of context. pi's `context_edit` with a null replacement leaves out any entry that contributed to context, a branch summary among them. | The summary stays in context. |
| `agentimport/info-clear` | pi | An `info` entry sets a session name but none clears it: an empty name is ignored. pi clears its session name with a `session_info` whose name is empty. | Nothing is written; the earlier name stands. |
| `atif/callless-subagent` | ATIF | A link to a sub-agent no call spawned: a harness's helper, such as Terminus 2's summarisers, referenced from a result with no `source_call_id`. A `subsession` link needs the `call_id` that spawned it, and a `peer` is one the call did not wait for. | A `subsession` link with no `call_id`; the child has `parent_session` and no `spawned_by`. |
| `atif/compaction-unrecorded` | ATIF | A compaction whose summary is not recorded: a replace boundary with no results' content and no user step after it. `compaction` needs a `summary`. | A custom entry holding the step; the context keeps everything before it. |
| `atif/context-pruning` | ATIF | Pruning that does not name what it removed: a `truncate` or `pruning` boundary says older turns were dropped, not which. `omit.items` needs the entries named. | A custom entry holding the step; the context keeps everything. |
| `atif/deterministic-dispatch` | ATIF | A function call no model produced: ATIF's `llm_call_count: 0` step, a harness dispatching a tool by rule. A `function_call` item is model output and belongs to a response. | Function call items with no response entry, marked `atif:llm_call_count: 0`. |

Rows are added by the converters as they find more; a test checks
that every `gap` code a converter emits is in this table.
