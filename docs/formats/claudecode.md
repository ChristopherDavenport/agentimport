# Claude Code

Claude Code writes one JSONL transcript per session, and one per
sub-agent beside it. The packages are `claudecode`, which reads the
format as it is, and `claudecode/convert`, which converts it.

## Where the format is specified

Nowhere. Anthropic does not document the transcript format and says it
changes between releases. The Agent SDK's typings call the union of
line types "CLI-internal and not part of the SDK API surface" and tell
adapters to treat lines as pass-through blobs (`SessionStoreEntry` in
`@anthropic-ai/claude-agent-sdk`'s `sdk.d.ts`). What the SDK does type
are near relatives: `SDKAssistantMessage`, `SDKUserMessage` and
`SDKCompactBoundaryMessage` are camel-to-snake twins of the transcript's
lines, and `ToolOutputSchemas` in `sdk-tools.d.ts` are the shapes of the
`toolUseResult` sidecar. The content blocks and usage are the Messages
API's own (`@anthropic-ai/sdk`).

This package was written from transcripts of Claude Code 2.1.271 to
2.1.300 and those typings. Older releases wrote shapes not seen there
(`summary` and `custom-title` lines, `redacted_thinking`, server tool
blocks); the reader keeps every member it does not type, and the
converter keeps every line type it does not know, so a line from
another release is carried rather than lost.

## Layout

```
~/.claude/projects/<project>/
  <session-id>.jsonl                      the main transcript
  <session-id>/subagents/
    agent-<agent-id>.jsonl                a sub-agent's transcript
    agent-<agent-id>.meta.json            its type, description, spawning call
  <session-id>/tool-results/<id>.txt      tool output too large to inline
```

`<project>` is the working directory with `/` and `.` replaced by `-`.
Nested sub-agents sit in the same flat directory and name their parent
in `parentAgentId`. Files under `tool-results/` are not read: the
transcript's tool result names the file, and the conversion carries that
text as the model saw it.

## Reading

Every line is a JSON object with a `type`. `user`, `assistant`,
`attachment` and `system` lines are the conversation tree, linked by
`uuid` and `parentUuid`. Every other type is a side record: state the
CLI appends with no uuid, the last one written in force.

The reader's rules, which `claudecode.BuildTree` applies:

- **Index before linking.** A parent is sometimes written after its
  child.
- **Never order by timestamp.** Timestamps go backwards in most files:
  an assistant line carries the time its block finished, later lines
  the time they were written.
- **One response is many lines.** The CLI writes a line per content
  block; the lines of one API response share `message.id` and
  `requestId` and count `apiBlockIndex` from 0. They need not be
  contiguous, and each repeats the whole `usage`, so usage is taken
  once per response, from its last line that has it.
- **Tool results hang from their own call.** Each `tool_result` line's
  parent is the line holding its `tool_use`, while the next block of the
  response hangs from the line written last. The results of parallel
  calls are therefore dead ends on disk; the tree puts them back in
  their response.
- **Compaction relinks.** A `compact_boundary` line is a root on disk
  (`parentUuid` null) with `logicalParentUuid` naming the line the
  conversation was at; the summary hangs from it, and the lines kept
  verbatim are named in `compactMetadata.preservedSegment` and
  `preservedMessages` with their on-disk parents unchanged.
- **Torn lines.** Runs of NUL bytes and truncated objects occur
  mid-file, not only at the end. The reader removes NULs, records a line
  that still is not JSON, and goes on.
- **The current leaf** is the `leafUuid` of the last `last-prompt`
  record.

Sub-agents: an agent's lines carry `agentId` and the parent's
`sessionId`. Its meta file's `toolUseId` names the `Agent` call that
spawned it, and that call's `toolUseResult.agentId` names the agent.
Team members have no `toolUseId`; one is linked when a call's
`toolUseResult` (`status: teammate_spawned`) names it. A fork's
transcript starts with a `fork-context-ref` record: its context is the
parent's path up to `parentLastUuid`, by reference.

## Mapping

| Native | Session | Notes |
| --- | --- | --- |
| main transcript | session, `id` the native session ID | `created_at` the earliest timestamp, `cwd` the first, `harness` `claude-code` and the first `version`; header member `agentimport:source` |
| sub-agent transcript | session, `id` `SubsessionID(parent, call_id)`, `parent_session`, `spawned_by` | a `subsession` link in the parent before the call's output; meta file in header member `claudecode:agent_meta` |
| first model below a root | `config` with `model`, first entry of the root | `requestedModel` when the line has it |
| model change between responses | `config` delta with `model` | |
| `cwd`, `gitBranch` | `env` with `cwd`; branch in vcs member `claudecode:branch` | written at a root and where either changes |
| user prompt | `item`, user `message` | `isMeta` lines are `visible: false` |
| assistant `text` block | `item`, assistant `message` with `output_text` | one per block, as the provider replays them |
| assistant `thinking` block | `item`, `reasoning`: text as `summary_text`, signature in `encrypted_content` | entry member `claudecode:provider` |
| assistant `tool_use` block | `item`, `function_call`, input re-encoded as `arguments` | see arguments below |
| other assistant blocks | `item`, `anthropic.<type>` carrying `block` | the openresponses Anthropic provider's shape |
| a response's lines | `response` after its items: `response_id` the message id, `model` the one that answered, `status` from the stop reason, `usage` | native usage in `claudecode:usage`, `requestId` in `claudecode:request_id`, stop reason in `claudecode:stop_reason` |
| `tool_result` | `item`, `function_call_output`, after the response, in call order | images as `input_image` data URLs; sidecar in `claudecode:tool_use_result` |
| synthetic API-error line | `response`, `status: failed`, `error` the code and text | |
| attachment with rendered text | `item`, `message` in `renderedRole` (user when absent), `visible: false` | payload in `claudecode:attachment` |
| `prompt_snapshot` attachment, `api-request-shape` record | `config` delta: `instructions`, `tools_added`, `tools_removed` | |
| `compact_boundary` and its summary | `compaction` under the logical parent: `summary` the summary message, `first_kept` the first preserved line, `config` the settings in force, `tokens_before` | metadata in `claudecode:compact_metadata` |
| `ai-title`, `custom-title`, `summary` | `info` with `name` | kind in `claudecode:title_kind` |
| last `last-prompt` | the leaf: a `leaf` label when the file's last entry is not on its branch | |
| other side records, system lines, attachments with nothing rendered | `custom`, `ns` `claudecode`, `data` the line | |
| every line | its uuid in member `claudecode:uuid` of each entry made from it; members not mapped in `claudecode:line` | |

**Arguments.** `content[].input` is the input after the CLI normalised
it; `wireToolInputs` holds what the model wrote. When a
`prompt_snapshot` says `echoWireToolInputs`, the CLI replays the model's
version in later requests, so that is the call's `arguments` and the
normalised input is `claudecode:input`; otherwise the normalised input
is the arguments and the model's is `claudecode:wire_input`.

**Usage.** Anthropic's `input_tokens` excludes cached tokens. As the
provider does, `input_tokens` is input + cache read + cache creation,
`cached_tokens` is cache read and `reasoning_tokens` is
`output_tokens_details.thinking_tokens`. The cache-write split, TTLs,
server tool counts, service tier and the rest stay in `claudecode:usage`.

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead.

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/provenance` | gap | Once per session: the import is recorded in the header member `agentimport:source`. |
| `agentimport/reasoning-provider` | gap | A thinking signature is in `encrypted_content`, which says nothing of the provider that can replay it; the entry carries `claudecode:provider`. |
| `agentimport/tool-error` | gap | A tool result had `is_error`; `function_call_output` has no error flag, so it is `claudecode:is_error`. |
| `agentimport/usage-detail` | gap | A response's usage has members Responses usage lacks; they are in `claudecode:usage`. |
| `agentimport/vcs-branch` | gap | The git branch is the vcs member `claudecode:branch`; the format's vcs records a revision, not a branch. |
| `agentimport/async-subsession` | gap | An `Agent` call only started its agent (`async_launched`, `teammate_spawned`): the link and the output are written, but the agent's result reaches the parent later as a user message (`<task-notification>`), which the format cannot tie to the call. |
| `claudecode/agent-meta` | kept, or dropped | A sub-agent's meta file is the header member `claudecode:agent_meta`. |
| `claudecode/agent-meta-only` | dropped | A sub-agent has a meta file and no transcript: it never wrote a line. |
| `claudecode/agent-unlinked` | lossy | No call in the transcripts spawned the agent (team members with no spawning call); its session names the parent in `parent_session` alone, with no link. |
| `claudecode/anthropic-block` | kept | An assistant block with no Responses item (`redacted_thinking`, server tools, newer blocks) is an `anthropic.<type>` item carrying it. |
| `claudecode/anthropic-content` | kept | A user or tool-result block with no input part (`tool_reference`, newer blocks) is an `anthropic.<type>` part carrying it. |
| `claudecode/attachment` | kept, or dropped | An attachment's payload, beside the message its rendering became, is `claudecode:attachment`. |
| `claudecode/attachment-unrendered` | kept | An attachment that rendered nothing into the request is a custom entry. |
| `claudecode/call-id-renamed` | repaired | A tool_use ID another call of the session holds was renamed; the native one is `claudecode:call_id`. |
| `claudecode/compact-metadata` | kept, or dropped | A compaction's metadata is `claudecode:compact_metadata`. |
| `claudecode/compaction-kept` | lossy | A compaction preserved no line on the path; `first_kept` is the entry before the compaction, the least the format can keep. |
| `claudecode/compaction-unmapped` | kept | A compact boundary without its summary or its logical parent is a custom entry. |
| `claudecode/dangling-parent` | repaired | A line's parent is not in the transcript; the line starts a root. |
| `claudecode/duplicate-result` | kept | A second result line for a call already answered is a custom entry. |
| `claudecode/empty-line` | dropped | An assistant line with no content block. |
| `claudecode/file-history` | kept, or dropped | A `file-history-snapshot` or `file-history-delta` record (rewind checkpoints) is a custom entry. |
| `claudecode/fork-context` | lossy | A fork's `fork-context-ref` is a custom entry; the context the fork inherited, the parent's path, is not in its session. |
| `claudecode/last-prompt` | dropped | A `last-prompt` record repeats a user message; the last one's leaf decides the session's leaf. |
| `claudecode/line-members` | kept, or dropped | A line's members the conversion does not map (`promptId`, `origin`, `permissionDecision` and the like) are `claudecode:line`. |
| `claudecode/malformed-line` | repaired | A line that is not JSON was skipped. |
| `claudecode/message-members` | kept, or dropped | Members of an assistant message beyond content, id, model, stop reason and usage are `claudecode:message` on the response. |
| `claudecode/no-message` | kept | A user or assistant line with no message is a custom entry. |
| `claudecode/no-stop-reason` | lossy | No line of a response carries a stop reason (it is written before the API reports one); the response has no status. |
| `claudecode/no-timestamp` | lossy | The transcript has no timestamp; `created_at` is the epoch. |
| `claudecode/normalised-input` | kept | The call's arguments are the model's, which the CLI echoes; the CLI's normalised input is `claudecode:input`. |
| `claudecode/nul-bytes` | repaired | NUL bytes were removed from a line. |
| `claudecode/orphan-result` | kept | A tool result for a call not in the transcript is a custom entry. |
| `claudecode/parent-cycle` | repaired | Parent links formed a cycle; the line starts a root. |
| `claudecode/request-log` | kept, or dropped | An `api-request` or `api-request-blob` record (2.1.300's log of each request) is a custom entry. |
| `claudecode/request-snapshot` | lossy | A `prompt_snapshot` or `api-request-shape` is a config delta: the system blocks are joined into one instructions string, and requests before it record no instructions. |
| `claudecode/side-record` | kept | A side record with no core entry (`mode`, `permission-mode`, `queue-operation`, `pr-link`, `cost-state` and the like) is a custom entry. |
| `claudecode/side-record-repeat` | dropped | A side record restating the value in force. |
| `claudecode/stop-reason` | lossy | A stop reason with no response status; the response has none. |
| `claudecode/synthetic-line` | kept | A line the CLI wrote inside a model response is a custom entry. |
| `claudecode/system-line` | kept | A `system` line (`turn_duration`, `local_command`, `away_summary` and the like) is a custom entry. |
| `claudecode/thinking-empty` | lossy | A thinking block with neither text nor signature is an empty reasoning item. |
| `claudecode/tool-use-result` | kept, or dropped | The `toolUseResult` sidecar, the tool's structured output the model never saw, is `claudecode:tool_use_result`. |
| `claudecode/undecodable-line` | kept | A line that is JSON but not of its type's shape is a custom entry. |
| `claudecode/unknown-line-type` | kept | A line of a type the converter does not know is a custom entry. |
| `claudecode/unreached` | dropped | A line the conversion did not reach; a converter bug. |
| `claudecode/usage-snapshots` | lossy | The lines of one response carry different usage; the last is used. |
| `claudecode/wire-input` | kept | The call's arguments are the CLI's normalised input; what the model wrote is `claudecode:wire_input`. |
