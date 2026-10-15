# What the session format cannot hold

Each entry is a `gap` finding: something one or more native formats
record that the Agent Session Format (RFC 0001, format 0.12) and its
Open Responses payload have no place for. The converters keep it in a
namespaced extension or drop it, and report it under the code given.
Each is a question for the RFC or for `openresponses`; the converter
changes when the answer lands.

The per-source documents under `docs/formats/` list every finding
code, including the lossy, kept and dropped ones that are this
module's choices rather than the format's limits.

| Code | Sources | What is missing | Fallback |
| --- | --- | --- | --- |
| `agentimport/provenance` | all | No member says a session was imported, from which format and native ID, by which converter. | Header member `agentimport:source`. |
| `agentimport/reasoning-provider` | Claude Code, pi | A `reasoning` item's `encrypted_content` is opaque and untagged: nothing says which provider and model family can replay it. The response entry's `model` says which model wrote it, but only for items that carry a `response`. | Kept as `encrypted_content`, as `providers/anthropic` does; the item entry carries `<ns>:provider`. |
| `agentimport/tool-error` | Claude Code, pi | `function_call_output` has no error flag; Anthropic's `is_error` and pi's `isError` tell the model the call failed. | Output kept; the item entry carries `<ns>:is_error`. |
| `agentimport/usage-detail` | Claude Code, pi, Codex | Responses usage has input, cached input, output and reasoning tokens only: no cache-write tokens, cache TTL split, server tool counts, service tier or cost. | Folded where it has a meaning (cache writes into input); the rest on the response entry as `<ns>:usage`. |
| `agentimport/vcs-branch` | Claude Code | An `env` entry's `vcs` has `system`, `revision` and `dirty`; no branch. Claude Code records the branch on every line and no revision. | vcs member `<ns>:branch`. |
| `openresponses/provider-content-part` | Claude Code | `providers/anthropic` passes `anthropic.<type>` items and tools through, but has no content part of that kind: a `function_call_output` or message holding an `anthropic.tool_reference` part (every ToolSearch result) is refused as unsupported, so the session cannot be replayed through the adapter. | The part is kept as `anthropic.<type>` carrying the block; openresponses needs a part extension for the adapter to send it. |
| `openresponses/slug-separator` | Claude Code | Provider extension items are `anthropic.<block>` in `providers/anthropic` and `acme:<type>` in the openresponses package doc; a reader cannot tell which separator marks an extension. | Follows `providers/anthropic` (`anthropic.`). |

Rows are added by the converters as they find more; a test checks
that every `gap` code a converter emits is in this table.
