# pi

pi writes one JSONL file per session, holding a tree of entries. The
packages are `pi`, which reads the format as it is, and `pi/convert`,
which converts it.

## Where the format is specified

pi documents it. The `@earendil-works/pi-coding-agent` package ships
`docs/session-format.md` (entry types, the tree, how context is built,
the versions), `docs/message-types.md` (the `AgentMessage` union,
content blocks, usage) and `docs/compaction.md`, with TypeScript types
in `dist/core/session-manager.d.ts` (`CURRENT_SESSION_VERSION = 3`) and
`@earendil-works/pi-ai`'s `dist/types.d.ts`. Where the documents say
how a value is used but not how it is encoded, the package follows pi's
source as installed: the migrations in `session-manager.js`, the
prompt replay in pi-ai's `utils/transcript.js`, the summary framing and
bash rendering in `messages.js`, and each provider's encoding of
signatures and ids in pi-ai's `api/*.js`.

This package was written against pi 1.0.1 (session version 3). It reads
versions 1 and 2 by applying pi's own migrations, and keeps every
member it does not type, so a later minor version's fields are carried.

## Layout

```
~/.pi/agent/sessions/--<cwd>--/<timestamp>_<session-id>.jsonl
```

`<cwd>` is the working directory without its leading separator and with
`/`, `\` and `:` replaced by `-`. The first line is the header,
`{"type":"session","version":3,"id":…,"timestamp":…,"cwd":…}`, with
`parentSession`, the path of another session file, on a fork or clone.

## Reading

- **A tree by id.** Every line after the header is an entry with `id`
  and `parentId` (null on a root). pi indexes them by id, the last
  entry with an id winning, so a `parentId` naming a repeated id names
  the later entry; `pi.BuildTree` does the same.
- **The leaf is the last line.** pi resumes at the last entry in the
  file, whatever its type; branching appends a child of an earlier
  entry.
- **Versions.** Version 1 entries have no ids and form a line; pi gives
  them ids, chains them in file order and turns a compaction's
  `firstKeptEntryIndex` (an index into the parsed lines, the header
  first) into the id of that entry. Version 2's message role
  `hookMessage` is version 3's `custom`. `pi.Migrate` applies both, in
  memory; ids it gives are `v1-<line>`, and the converter records no
  native id for them.
- **Prompt and tools.** There is no prompt state entry: `message`
  entries of role `system` carry the prompt and tools, and replaying
  them in order builds the current state: a non-empty `content` is
  appended, `sections` are patched by name (`null` removes one), tools
  are removed then set by name, and `replace: true` starts again. A
  compaction's `systemMessage` is the whole state at that point.
- **Malformed lines** are skipped, as pi skips them; blank lines are not
  lines.

## Mapping

| Native | Session | Notes |
| --- | --- | --- |
| header | session, `id` the native session id | `created_at` the header's timestamp, `cwd`, `harness` `pi`; version in header member `pi:version`; undeclared members in `pi:header`; header member `agentimport:source` |
| `parentSession` (a fork or clone) | `parent_session` the parent's id, read from that file's header, and a `fork_of` link as the first entry | the path in header member `pi:parent_session`; see below |
| `model_change` | `config` delta with `model` | provider in `pi:provider` |
| `thinking_level_change` | `config` delta with `reasoning.effort` | `off` is `none`; `max` is `xhigh` |
| `message`, role `system` | `config` delta: `instructions_parts` and `tools_added`, `tools_removed` against what is in force | parts `content/<n>` and `section/<name>`, in pi's order |
| `message`, role `user` | `item`, user `message` | images as `input_image` data URLs |
| `message`, role `assistant` | a `config` delta when its model is not the one in force; an `item` per content block in order; then a `response` | see below |
| `message`, role `toolResult` | `item`, `function_call_output` | one text block is the output string; otherwise parts; `details` in `pi:details` |
| `message`, role `bashExecution` | `item`, user `message` with the text pi renders for it | the record in `pi:bash_execution`; one kept out of context is a `custom` entry |
| `message`, role `custom`; `custom_message` | `item`, user `message` with the content, `visible: false` when not displayed | `customType` in `pi:custom_type`, `details` in `pi:details` |
| `usage` | `custom`, `ns` `pi`, `data` with `usage` in the Responses shape and `model`: the format's record of a paid call with no response | native usage in `data["pi:usage"]` |
| `compaction` | `compaction`: `summary` the user message pi sends (its framing and the summary), `first_kept` the first entry made from `firstKeptEntryId`, `config` the settings in force with pi's `systemMessage` checkpoint, `tokens_before`, `usage` | `details`, `fromHook` and native usage in `pi:` members |
| `branch_summary` | `branch_summary`: `from` the leaf left, `summary` the framed user message | as compaction |
| `context_edit`, `replacement: null` | `config` delta with `omit.items` naming the target's items on the path | |
| `context_edit` with a replacement | the `omit` above, then the replacement as a user message where the edit was, or a `custom` entry | a gap: see below |
| `custom` | `custom`, `ns` `pi`, `data` the line | |
| `label` | `label` on the target's last entry; a cleared label is a null label | the label `leaf` is written `pi:leaf` |
| `session_info` | `info` with `name` | |
| every entry | its id in member `pi:id` of each entry made from it; members not mapped in `pi:line` | |

**Assistant messages.** pi keeps one message per model call with its
parts in order; each part is an item, tied to the response by
`response_id` (`responseId`, or `pi-<entry id>` when pi recorded none).

- `text` is an assistant `message`. From a Responses API
  (`openai-responses`, `azure-openai-responses`,
  `openai-codex-responses`) the `textSignature`, `{"v":1,"id":…,"phase":…}`
  or an older bare id, gives the message item's `id` and `phase`.
- `thinking` from a Responses API holds the reasoning item itself,
  serialised in `thinkingSignature`; it is written as it was. From
  Anthropic, Bedrock and Gemini the text is a `summary_text` and the
  signature `encrypted_content`, as openresponses' providers carry it;
  Anthropic's redacted thinking is an `anthropic.redacted_thinking`
  item. Other APIs' signatures (`openai-completions` names the reasoning
  field there) are not replay data and go in `pi:thinking_signature`.
- `toolCall` is a `function_call` with the arguments re-encoded. pi
  joins a Responses call's `call_id` and item id as `call_id|item_id`;
  they are split back into `call_id` and the item's `id`.
- The `response` carries the model that answered (`responseModel`,
  else `model`), the status from `stopReason` (`stop` and `toolUse`
  completed, `length` incomplete, `error` failed with `errorMessage`),
  and the usage: pi's `input` excludes cache reads and writes, so
  `input_tokens` is input + cache read + cache write, `cached_tokens`
  cache read and `reasoning_tokens` `reasoning`. Native usage and its
  dollar cost are `pi:usage`; `api`, `provider`, `rawStopReason` and the
  rest are `pi:message`.

**Forks.** pi's `/fork` and `/clone` copy the parent's path into the
new file with the same ids, and name the parent by path. The copy is
converted as the fork's own entries, under a `fork_of` link; the
session format would write it as the prefix of a `base` in the parent,
which needs the parent converted to the same hashes, so it is reported
as lossy.

**Context edits.** pi's `context_edit` changes what an earlier entry
contributes to the next request without changing the entry. Leaving it
out is exactly the format's `omit`. Replacing its content in place has
no equivalent: the target is omitted, and a user or extension message's
replacement is written as a new user message where the edit was, so the
model sees the new text later than it did; an assistant or tool
result's replacement is kept as a custom entry.

**System messages mid-conversation.** pi sends a system message written
after the conversation started either folded into the prompt, or in
place as an update, depending on whether the provider accepts system
messages mid-conversation. The conversion writes the folded form, a
config delta, and reports the difference.

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead.

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/provenance` | gap | Once per session: the import is recorded in the header member `agentimport:source`. |
| `agentimport/reasoning-provider` | gap | A thinking signature from Anthropic, Bedrock or Gemini is in `encrypted_content`, which says nothing of the provider that can replay it; the entry carries `pi:provider`. |
| `agentimport/call-signature` | gap | A tool call carries Gemini's `thoughtSignature`; `function_call` has no member for it, so it is `pi:thought_signature`. |
| `agentimport/tool-error` | gap | A tool result had `isError`; `function_call_output` has no error flag, so it is `pi:is_error`. |
| `agentimport/usage-detail` | gap | Usage has members Responses usage lacks (cache writes, the dollar cost); they are in `pi:usage`. |
| `agentimport/context-replace` | gap | A context edit replaced an entry's content; the entry is omitted and the replacement written after, or kept aside. |
| `pi/bash-excluded` | kept | A command the user ran and kept out of context is a custom entry. |
| `pi/bash-execution` | kept, or dropped | A command the user ran is the user message pi renders from it; the record is `pi:bash_execution`. |
| `pi/block-members` | kept | A tool call's members a function call has no place for are `pi:block`. |
| `pi/branch-summary-empty` | kept | A branch summary with no text, which pi leaves out of context, is a custom entry. |
| `pi/branch-summary-unlinked` | kept | A branch summary whose left leaf was not converted before it is a custom entry. |
| `pi/call-id-renamed` | repaired | A call id another call of the session holds, or with characters providers refuse, was renamed; the native one is `pi:call_id`. |
| `pi/compaction-unmapped` | kept | A compaction with nothing before it is a custom entry. |
| `pi/context-edit-target` | dropped | A context edit of an entry that put nothing in context on its path changes nothing. |
| `pi/context-edit-unreadable` | kept | A context edit whose replacement does not decode is a custom entry. |
| `pi/custom` | kept | An extension's `custom` entry is a custom entry holding the line. |
| `pi/custom-message` | kept | An extension's message is a user message; its type is `pi:custom_type`. |
| `pi/dangling-parent` | repaired | An entry's parent is not in the file; the entry starts a root. |
| `pi/details` | kept, or dropped | A tool's or extension's `details`, which pi does not send the model, are `pi:details`. |
| `pi/duplicate-id` | repaired | An entry repeats an earlier entry's id; a parentId naming it names the later one, as pi resolves it. |
| `pi/duplicate-result` | kept | A second result for a call already answered on its path is a custom entry. |
| `pi/extra-header` | dropped | A second session line; the first is the header. |
| `pi/first-kept-missing` | repaired | A compaction's first kept entry is not on its path; nothing before it is kept, as pi reads it. |
| `pi/fork-prefix` | lossy | A fork's copy of its parent's path is converted as its own entries, not as the prefix of a base in the parent. |
| `pi/header-members` | kept, or dropped | Header members the conversion does not map are the header member `pi:header`. |
| `pi/hook-message` | repaired | A version 2 `hookMessage` is read as a `custom` message, as pi migrates it. |
| `pi/input-text-signature` | dropped | A text signature on user or tool result content, which no provider replays, is left out. |
| `pi/label-leaf` | lossy | The label `leaf`, which the session format reserves for the leaf, is written `pi:leaf`. |
| `pi/label-unlinked` | kept | A label of an entry not converted before it is a custom entry. |
| `pi/line-members` | kept, or dropped | An entry's members the conversion does not map are `pi:line`. |
| `pi/malformed-line` | repaired | A line that is not JSON was skipped, as pi skips it. |
| `pi/message-members` | kept, or dropped | A message's members beyond those mapped (`api`, `provider`, `rawStopReason`, `thinkingLevel` and the like) are `pi:message`. |
| `pi/no-header` | repaired | The file has no session line; the session id is taken from the file name. |
| `pi/no-message` | kept | A message entry with no message is a custom entry. |
| `pi/no-session-id` | repaired | Neither the header nor the file name gives a session id; one was made. |
| `pi/no-stop-reason` | lossy | An assistant message has no stop reason; the response has no status. |
| `pi/no-timestamp` | lossy | The file has no timestamp; `created_at` is the epoch. |
| `pi/nul-bytes` | repaired | NUL bytes were removed from a line. |
| `pi/orphan-result` | kept | A tool result for a call not on its path is a custom entry. |
| `pi/parent-cycle` | repaired | Parent links formed a cycle; the entry starts a root. |
| `pi/parent-unresolved` | lossy | The parent session file could not be read; its path is `pi:parent_session` and `parent_session` is unset. |
| `pi/reasoning-item-members` | kept | A Responses reasoning item pi kept holds members the item type does not; the item as kept is `pi:thinking_signature`. |
| `pi/redacted-thinking` | kept | Anthropic's redacted thinking is an `anthropic.redacted_thinking` item. |
| `pi/retain-none` | kept | A compaction that keeps nothing follows an item, so a custom marker entry is written to be its `first_kept`. |
| `pi/stop-reason` | lossy | A stop reason with no Responses status: `aborted` is incomplete with no reason, `deferred` in progress. |
| `pi/summary-message` | lossy | A `branchSummary` or `compactionSummary` message, which pi derives and does not persist, is a user message holding its framed summary. |
| `pi/system-content` | dropped | A non-text block in a system message, which pi does not render into the prompt. |
| `pi/system-unchanged` | dropped | A system message that changes neither the prompt nor the tools in force. |
| `pi/system-update` | lossy | A system message after the conversation started is a config delta; a provider that takes system messages mid-conversation was sent it in place. |
| `pi/text-signature` | kept | A text signature not from a Responses API is `pi:text_signature`. |
| `pi/thinking-empty` | lossy | A thinking block with neither text nor signature is an empty reasoning item. |
| `pi/thinking-level` | lossy | A thinking level with no Responses effort: `max` is `xhigh`; another is `pi:thinking_level` alone. |
| `pi/thinking-signature` | kept | A thinking signature that is not replay data for `encrypted_content` is `pi:thinking_signature`. |
| `pi/tool-members` | kept | Tool declaration members a function tool has no place for (`constrainedSampling`) are `pi:tool_members`. |
| `pi/tool-namespace` | kept | A Responses tool namespace on a call is `pi:namespace`. |
| `pi/tool-order` | lossy | A tool redeclared in place keeps its position in pi and moves to the end in the session format. |
| `pi/tool-usage` | kept | Usage of model work a tool did is `pi:usage` on its output; the format counts no paid call for it. |
| `pi/undecodable-line` | kept | A line that is JSON but not of its type's shape is a custom entry. |
| `pi/unknown-block` | kept | An assistant block of a type pi did not define is a `pi.<type>` item carrying it. |
| `pi/unknown-content` | kept | A user or tool result block of another type is a `pi.<type>` part carrying it. |
| `pi/unknown-entry-type` | kept | An entry of a type the converter does not know is a custom entry. |
| `pi/unknown-role` | kept | A message of a role the converter does not know is a custom entry. |
| `pi/unreached` | dropped | A line the conversion did not reach; a converter bug. |
| `pi/v1-first-kept-index` | repaired | A version 1 compaction named its first kept entry by index; the index became the entry. |
| `pi/v1-linear` | repaired | A version 1 file has no ids: its entries are chained in file order, as pi migrates them, and carry no `pi:id`. |
