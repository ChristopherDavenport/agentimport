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
source as installed: the migrations and context building in
`session-manager.js`, the prompt replay in pi-ai's
`utils/transcript.js`, the request-time filtering in pi-ai's
`api/transform-messages.js`, the summary framing and bash rendering in
`messages.js`, and each provider's encoding of signatures and ids in
pi-ai's `api/*.js`.

Where the documents and the code disagree, the conversion follows the
code, since it is what pi sent:

- `message-types.md` says a system message with `replace: true`
  "discards the earlier state and establishes a complete new baseline";
  pi-ai's `getCurrentSystemMessage`, which builds every request's
  prompt, ignores `replace`. The conversion ignores it, and keeps it in
  `pi:message`.
- `session-format.md` documents `sections` as a record patched by name
  in order. pi holds it as a JavaScript object, which iterates keys
  that are array indices (`"2"`, `"10"`) first, in numeric order, then
  the rest as written; that is the order pi applies the patches and
  renders the prompt in, and the conversion's.

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
  native id for them. A file that reads as version 1, its header lost
  or damaged, whose entries carry ids keeps them: pi would chain them
  and flatten the tree.
- **Prompt and tools.** There is no prompt state entry: `message`
  entries of role `system` carry the prompt and tools, and replaying
  them in order builds the current state: a non-empty `content` is
  appended, `sections` are patched by name (`null` removes one), tools
  are removed then set by name. A compaction's `systemMessage` is the
  whole state at that point.
- **Lines pi skips.** A line that is not JSON, a line holding a NUL byte
  (JSON.parse refuses it) and `null` are skipped, as pi skips them; a
  line that is JSON but not an object (a number, a string, an array) is
  skipped too, though pi would keep it as an entry with no type and
  count it in a version 1 `firstKeptEntryIndex`. Blank lines are not
  lines.
- **Unions.** A line, a message and a content block are each a union of
  several shapes, so a member one shape declares can hold another's
  type. Such a member is kept as written, beside the object's other
  members, rather than failing the line; content blocks are read one by
  one, so a block that is not an object is kept as written and its
  neighbours still read.
- **Lone surrogates.** JavaScript writes a string cut inside an emoji
  with a `\ud83d` escape that is half a pair. Go would read it as
  U+FFFD; pi removes it from what it sends (pi-ai's
  `sanitizeSurrogates`), and so does the reader.

## Mapping

Entries are converted in file order: pi writes an entry after its
parent and after any entry it names, so a branch summary or label
naming another branch finds it converted.

| Native | Session | Notes |
| --- | --- | --- |
| header | session, `id` the native session id | `created_at` the header's timestamp, `cwd`, `harness` `pi`; version in header member `pi:version`; undeclared members in `pi:header`; header member `agentimport:source` |
| `parentSession` (a fork or clone) | `parent_session` the parent's id, read from that file, and a `fork_of` link as the first entry | the path in header member `pi:parent_session`; see below |
| `model_change` | `config` delta with `model` | provider in `pi:provider` |
| `thinking_level_change` | `config` delta with `reasoning.effort` | `off` is `none`; `max` is `xhigh` |
| `message`, role `system` | `config` delta: `instructions_parts` and `tools_added`, `tools_removed` against what is in force | parts `content/<n>` and `section/<name>`, in pi's order |
| `message`, role `user` | `item`, user `message` | images as `input_image` data URLs |
| `message`, role `assistant` | a `config` delta when its model is not the one in force; items for its content blocks in order; then a `response` | see below |
| `message`, role `assistant`, stopped `error` or `aborted` | as above, then a `config` delta whose `omit.items` names its items | pi leaves the attempt out of every later request |
| `message`, role `toolResult` | `item`, `function_call_output`, marked `pi:tool_kind` for a custom tool call | one text block is the output string; otherwise parts; `details` in `pi:details`; a `toolName` other than the call's in `pi:tool_name` |
| `message`, role `bashExecution` | `item`, user `message` with the text pi renders for it | the record in `pi:bash_execution`; one kept out of context is a `custom` entry |
| `message`, role `custom`; `custom_message` | `item`, user `message` with the content, `visible: false` when not displayed | `customType` in `pi:custom_type`, `details` in `pi:details` |
| `usage` | `custom`, `ns` `pi`, `data` with `usage` in the Responses shape and `model`: the format's record of a paid call with no response | native usage in `data["pi:usage"]` |
| `compaction` | `compaction`: `summary` the user message pi sends (its framing and the summary), `first_kept` the first entry made from `firstKeptEntryId`, `config` the settings in force with pi's `systemMessage` checkpoint, `tokens_before`, `usage` | `details`, `fromHook` and native usage in `pi:` members; see below |
| `branch_summary` | `branch_summary`: `from` the leaf left, `summary` the framed user message | as compaction |
| `context_edit`, `replacement: null` | `config` delta with `omit.items` naming the target's items | when the target is in context on the path; see below |
| `context_edit` with a replacement | see below | a gap |
| `custom` | `custom`, `ns` `pi`, `data` `{"line": the line}` | |
| `label` | `label` on the target's last entry; an empty or absent label clears | the label `leaf` is written `pi:leaf` |
| `session_info` | `info` with `name` | an empty name, which clears pi's, writes nothing: a gap |
| every entry | its id in member `pi:id` of each entry made from it; members not mapped in `pi:line` | |
| a line kept whole | `custom`, `ns` `pi`, `data` `{"line": the line}` | under `line`, so a line's own `usage` does not read as the format's paid-call record |

**Assistant messages.** pi keeps one message per model call with its
parts in order; each part is an item, tied to the response by
`response_id` (`responseId`, or `pi-<entry id>` when pi recorded none).

- `text` is an assistant `message`. From a Responses API
  (`openai-responses`, `azure-openai-responses`,
  `openai-codex-responses`) the `textSignature` gives the message
  item's `id` and `phase`, read as pi reads it: `{"v":1,"id":…,"phase":…}`,
  or else the whole signature as the id; an id over 64 characters is
  `msg_` and pi-ai's `shortHash` of it, as pi sends it.
- `thinking` from a Responses API holds the reasoning item itself,
  serialised in `thinkingSignature`; it is written as it was. From
  Anthropic and Bedrock the text is a `summary_text` and the signature
  `encrypted_content`, as openresponses' Anthropic provider carries it;
  Anthropic's redacted thinking is an `anthropic.redacted_thinking`
  item. Other APIs' signatures (`openai-completions` names the reasoning
  field there) are not replay data and go in `pi:thinking_signature`.
- **Gemini** (`google-generative-ai`, `google-vertex`) follows
  openresponses' Gemini provider: a thought's signature is the
  reasoning item's `encrypted_content`; a signature on any other part
  (pi keeps a text part's in `textSignature` and a call's in
  `thoughtSignature`) is a reasoning item holding the signature alone,
  written just before the part, which the provider's request encoder
  puts back on it.
- `toolCall` is a `function_call` with the arguments re-encoded. pi
  joins a Responses call's `call_id` and item id as `call_id|item_id`;
  they are split back, and the item id is kept only when it is an
  `fc_` id, as pi sends it. An item id of `ctc_` marks a Responses
  custom tool call, which pi stores as a tool call whose arguments hold
  its free-form input. A custom tool call is a tool call, so it is a
  `function_call` with those arguments and its result a
  `function_call_output`, which the session format's call rules then
  see; both carry `pi:tool_kind: "custom"`, and the call its `ctc_` id
  in `pi:item_id`, for a writer that sends it back to OpenAI as a
  custom tool call. pi itself keeps one tool definition and chooses per
  request whether to send it as a custom grammar tool. The native id is
  `pi:call_id` whenever the call ID differs from it.
- The `response` carries the model that answered (`responseModel`,
  else `model`), the status from `stopReason` (`stop` and `toolUse`
  completed, `length` incomplete, `error` failed with `errorMessage`),
  and the usage: pi's `input` excludes cache reads and writes, so
  `input_tokens` is input + cache read + cache write, `cached_tokens`
  cache read and `reasoning_tokens` `reasoning`. Native usage and its
  dollar cost are `pi:usage`; `api`, `provider`, `rawStopReason` and the
  rest are `pi:message`.
- An attempt that stopped with `error` or `aborted` is written, then
  left out of context with `omit`: pi-ai's `transformMessages` drops
  such a message from every request, for every provider, since its
  content may be partial. Its tool results stay in context, as they do
  in pi.

pi also adds, at request time, a "No result provided" error output for
a call that has none (`transformMessages`); the conversion writes what
the file holds, so such a call has no output on the path.

**Compactions.** pi keeps the entries on the path from
`firstKeptEntryId` up to the compaction. When that entry wrote nothing
to the session (an edit or a system message that changed nothing), the
kept range starts at the first later entry on the path that did. A
compaction that keeps nothing (its own id as `firstKeptEntryId`, or a
kept entry not on its path) names as `first_kept` the entry before it
when that puts nothing in context, and otherwise a custom marker entry
written before it; a compaction at a root, with nothing before it, gets
the marker as its root.

**Forks.** pi's `/fork` and `/clone` copy the parent's path into the
new file with the same ids, and name the parent by path. The copy is
converted as the fork's own entries, under a `fork_of` link; the
session format would write it as the prefix of a `base` in the parent,
which needs the parent converted to the same hashes, so it is reported
as lossy, with the number of entries copied. A fork made at the
parent's first message copies nothing and reports nothing.

**Context edits.** pi's `context_edit` changes what an earlier entry
contributes to the next request without changing the entry, and only
while the entry is in context: an edit of an entry a compaction has
dropped, or on another branch, changes nothing, and is not written.

- Leaving the entry out (`replacement: null`) is the format's `omit`.
  A branch summary cannot be omitted, since `omit` names item entries
  alone; it stays in context, a gap.
- Replacing the content of a user or extension message has no
  equivalent: the message is omitted and the replacement written as a
  new user message where the edit was, so the model sees the new text
  later than it did. A later compaction that keeps the replacement but
  drops the message also omits the replacement, as pi would no longer
  apply the edit.
- Replacing the content of an assistant message or a tool result is
  kept as a custom entry, and the original stays in context. Leaving it
  out would leave a call without its output, or an output without its
  call, which a provider refuses; the model sees the original content
  where pi sent the replacement.
- pi does not replace the content of other roles (a bash execution, a
  branch summary); such an edit is not written.

**System messages mid-conversation.** pi sends a system message written
after the conversation started either folded into the prompt, or in
place as an update, depending on whether the provider accepts system
messages mid-conversation. The conversion writes the folded form, a
config delta, and reports the difference.

## Extension types

Open Responses carries types outside its spec as slug-prefixed
extensions, kept verbatim. The converter writes these, each prefixed
by whoever defines the type:

| Extension | Kind | Written for |
| --- | --- | --- |
| `anthropic.redacted_thinking` | item | Anthropic redacted thinking, shaped as `openresponses/providers/anthropic` shapes it |

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead. The codes
every converter can report are in [README.md](README.md).

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/reasoning-provider` | gap | An Anthropic, Bedrock or Gemini signature is in `encrypted_content`, which says nothing of the provider that can replay it; the entry carries `pi:provider`. |
| `agentimport/tool-error` | gap | A tool result had `isError`; `function_call_output` has no error flag, so it is `pi:is_error`. |
| `agentimport/usage-detail` | gap | Usage has members Responses usage lacks (cache writes, the dollar cost); they are in `pi:usage`. |
| `agentimport/context-replace` | gap | A context edit replaced a message's content: a user or extension message is omitted and its replacement written after; an assistant message or tool result stays and the edit is a custom entry. |
| `agentimport/omit-summary` | gap | A context edit left a branch summary out of context; `omit` names items alone, so it stays. |
| `agentimport/info-clear` | gap | A `session_info` with an empty name cleared pi's session name; the format cannot clear one, so nothing is written. |
| `pi/custom-tool-call` | kept | A Responses custom tool call is a `function_call` whose arguments wrap its free-form input, and its result a `function_call_output`; both carry `pi:tool_kind: "custom"`, the call its `ctc_` id in `pi:item_id`. |
| `pi/bash-excluded` | kept | A command the user ran and kept out of context is a custom entry. |
| `pi/bash-execution` | kept, or dropped | A command the user ran is the user message pi renders from it; the record is `pi:bash_execution`. |
| `pi/block-members` | kept | Members of an assistant block an item has no place for are `pi:block`. |
| `pi/branch-summary-empty` | kept | A branch summary with no text, which pi leaves out of context, is a custom entry. |
| `pi/branch-summary-unlinked` | kept | A branch summary whose left leaf is not in the file before it is a custom entry. |
| `pi/call-id-renamed` | repaired | A call id another call of the session holds, or with characters providers refuse, was renamed; the native one is `pi:call_id`. |
| `pi/context-edit-ignored` | dropped | A context edit replaces the content of a role pi does not edit (a bash execution, a branch summary); pi leaves it as it was. |
| `pi/context-edit-target` | dropped | A context edit of an entry not in context on its path (dropped by a compaction, on another branch, or never written) changes nothing. |
| `pi/context-edit-unreadable` | kept | A context edit whose replacement does not decode is a custom entry. |
| `pi/custom` | kept | An extension's `custom` entry is a custom entry holding the line. |
| `pi/custom-message` | kept | An extension's message is a user message; its type is `pi:custom_type`. |
| `pi/dangling-parent` | repaired | An entry's parent is not in the file; the entry starts a root. |
| `pi/details` | kept, or dropped | A tool's or extension's `details`, which pi does not send the model, are `pi:details`. |
| `pi/duplicate-id` | repaired | An entry repeats an earlier entry's id; a parentId naming it names the later one, as pi resolves it. |
| `pi/duplicate-result` | kept | A second result for a call already answered on its path is a custom entry. |
| `pi/extra-header` | dropped | A second session line; the first is the header. |
| `pi/failed-turn` | lossy | An attempt that stopped with `error` or `aborted` is written and left out of context with `omit`, as pi leaves it out of every later request. |
| `pi/first-kept-missing` | repaired | A compaction's first kept entry is not on its path; nothing before it is kept, as pi reads it. |
| `pi/fork-prefix` | lossy | A fork's copy of its parent's path is converted as its own entries, not as the prefix of a base in the parent. |
| `pi/header-members` | kept, or dropped | Header members the conversion does not map are the header member `pi:header`. |
| `pi/hook-message` | repaired | A version 2 `hookMessage` is read as a `custom` message, as pi migrates it. |
| `pi/input-block-members` | dropped | Members of a user or tool result block a content part has no place for are left out. |
| `pi/input-text-signature` | dropped | A text signature on user or tool result content, which no provider replays, is left out. |
| `pi/invalid-block` | dropped | A content block that is not an object (null, a number) is left out. |
| `pi/label-leaf` | lossy | The label `leaf`, which the session format reserves for the leaf, is written `pi:leaf`. |
| `pi/label-unlinked` | kept | A label of an entry not in the file before it is a custom entry. |
| `pi/line-members` | kept, or dropped | An entry's members the conversion does not map are `pi:line`. |
| `pi/lone-surrogate` | repaired | A lone UTF-16 surrogate escape, half of a character cut short, was removed, as pi removes it from what it sends. |
| `pi/malformed-line` | repaired | A line pi cannot use (not JSON, holding a NUL byte, null, or not an object) was skipped. |
| `pi/message-members` | kept, or dropped | A message's members beyond those mapped (`api`, `provider`, `rawStopReason`, `thinkingLevel`, `replace` and the like) are `pi:message`. |
| `pi/mistyped-member` | kept | Members whose value is not of the type pi defines are kept as written with their object's other unmapped members. |
| `pi/no-header` | repaired | The file has no session line; the session id is taken from the file name. |
| `pi/no-message` | kept | A message entry with no message is a custom entry. |
| `pi/no-session-id` | repaired | Neither the header nor the file name gives a session id; one was made. |
| `pi/no-stop-reason` | lossy | An assistant message has no stop reason; the response has no status. |
| `pi/no-timestamp` | lossy | The file has no timestamp; `created_at` is the epoch. |
| `pi/orphan-result` | kept | A tool result for a call not on its path is a custom entry. |
| `pi/parent-cycle` | repaired | Parent links formed a cycle; the entry starts a root. |
| `pi/parent-unresolved` | lossy | The parent session file could not be read; its path is `pi:parent_session` and `parent_session` is unset. |
| `pi/reasoning-item-members` | kept | A Responses reasoning item pi kept holds members the item type does not; the item as kept is `pi:thinking_signature`. |
| `pi/redacted-thinking` | kept | Anthropic's redacted thinking is an `anthropic.redacted_thinking` item. |
| `pi/retain-none` | kept | A compaction that keeps nothing follows an item, or nothing, so a custom marker entry is written to be its `first_kept`. |
| `pi/stop-reason` | lossy | A stop reason with no Responses status: `aborted` is incomplete with no reason, `deferred` in progress. |
| `pi/summary-message` | lossy | A `branchSummary` or `compactionSummary` message, which pi derives and does not persist, is a user message holding its framed summary. |
| `pi/system-content` | dropped | A non-text block in a system message or a compaction's checkpoint, which pi does not render into the prompt. |
| `pi/system-unchanged` | dropped | A system message that changes neither the prompt nor the tools in force. |
| `pi/system-update` | lossy | A system message after the conversation started is a config delta; a provider that takes system messages mid-conversation was sent it in place. |
| `pi/text-signature` | kept | A text signature not from a Responses or Gemini API is `pi:text_signature`. |
| `pi/thinking-empty` | lossy | A thinking block with neither text nor signature is an empty reasoning item. |
| `pi/thinking-level` | lossy | A thinking level with no Responses effort: `max` is `xhigh`; another is `pi:thinking_level` alone. |
| `pi/thinking-signature` | kept | A thinking signature that is not replay data for `encrypted_content` is `pi:thinking_signature`. |
| `pi/thought-signature` | kept | A thought signature on a call from an API other than Gemini's is `pi:thought_signature`. |
| `pi/tool-members` | kept | Tool declaration members a function tool has no place for (`constrainedSampling`), in a system message or a compaction's checkpoint, are `pi:tool_members`. |
| `pi/tool-name` | kept | A tool result names another tool than its call; the result's name is `pi:tool_name`. |
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
| `pi/version-ids-kept` | repaired | The file reads as version 1, by its header or for want of one, but its entries carry ids; they are kept, so its branches stand. |
