# Cline classic and Roo Code

Classic Cline (the VS Code extension before 4.0, and the standalone
hosts that kept its layout) and Roo Code, which forked from it in late
2024, keep each task in a directory of whole JSON documents: the
model's context as an array of Anthropic messages, the chat view's
messages beside it, and per-variant metadata. The two are one format
with a variant: the same layout, file names, message envelope, ask/say
model and tool history, differing in the task index, compaction,
reasoning shapes, usage and sub-tasks. The packages are `cline`, which
reads a task directory as it is, and `cline/convert`, which converts
it. Cline 4's SDK session store (`~/.cline/data/sessions`, a versioned
messages file and SQLite) is another format and not read here.

## Where the format is specified

Nowhere but the source. This package was written against:

- `github.com/cline/cline` at `v3.89.2` (`e1bdeeff`, 2026-06-11), the
  last release that ran the classic task, under
  `apps/vscode/src/`: `core/storage/disk.ts` (file names and writes),
  `shared/messages/content.ts` (the stored message and what is sent),
  `shared/ExtensionMessage.ts` (UI messages), `shared/HistoryItem.ts`,
  `core/context/context-management/ContextManager.ts` (the deleted
  range and context edits), `core/task/index.ts` (requests, retries,
  usage), `core/prompts/responses.ts` (the texts Cline writes), and the
  providers under `core/api/providers/` (what each one's input count
  means). Cline's HEAD (`c8f808b5`) keeps a reader of the classic task
  for its migration (`apps/vscode/src/sdk/legacy-task-handling.ts`),
  which rebuilds the deleted range the same way.
- `github.com/RooCodeInc/Roo-Code` at HEAD (`b867ec91`, 2026-05-15,
  v3.54.0), with `src/core/condense/index.ts` read at v3.40.0
  (`85055454`) and v3.30.0 (`00518662`) for the condense eras:
  `src/core/task-persistence/`, `src/core/task/Task.ts`,
  `src/core/context-management/index.ts` (sliding window),
  `src/core/webview/ClineProvider.ts` (sub-task delegation),
  `src/shared/cost.ts` and `packages/types/`.

Neither variant records a format version. Shapes are told apart
structurally and every guess is reported.

## Layout

```
<storage>/tasks/<id>/api_conversation_history.json   the model's context, one JSON array
<storage>/tasks/<id>/ui_messages.json                the chat view's messages, one JSON array
<storage>/tasks/<id>/task_metadata.json              files in context; classic: model switches, environments
<storage>/tasks/<id>/context_history.json            classic: the context edits
<storage>/tasks/<id>/settings.json                   classic, some releases: a settings snapshot
<storage>/tasks/<id>/history_item.json               Roo 3.5x: the task's index entry
<storage>/tasks/<id>/checkpoints/                    Roo: the task's shadow git repository
<storage>/state/taskHistory.json                     classic: every task's index entry
```

`<storage>` is VS Code's `globalStorage/saoudrizwan.claude-dev`
(classic), `globalStorage/rooveterinaryinc.roo-cline` (Roo, or the
`roo-cline.customStoragePath` setting), or `~/.cline/data` for the
standalone classic hosts. The earliest releases named the UI messages
`claude_messages.json`; it is read as UI messages, or as the API
history when its elements have a role.

The input unit is a task directory. `cline.FindSessions` lists every
directory inside a `tasks` directory holding an API history or UI
messages, by its main file, without walking checkpoint repositories;
the command detects a task by those file names in a task directory.
The variant is told by the storage directory's name when the task is
under one, else by shapes only one writes (`history_item.json`,
summaries, truncation markers, reasoning items, UI members
`apiProtocol` and `contextCondense` are Roo's; `context_history.json`,
`modelInfo`, `metrics` and metadata with model switches are classic),
and reported.

The index entry is read from the task's `history_item.json`, or from
the element of `state/taskHistory.json` naming the task. Older
releases of both kept the index in VS Code's state database
(`state.vscdb`, SQLite), which this package, the standard library
alone, does not read: a task with no entry has no working directory,
and classic's deleted range is then the UI messages' alone.

## Reading

- **Element by element.** Each array is read element by element; an
  element that is not an object is kept with its error and its
  neighbours read; an array cut short keeps the elements that end
  before the cut, and a file that is not an array is one fault. Every
  object keeps the members it does not declare, and a declared member
  of another JSON type is kept as written, so an element decoded and
  encoded again is the same JSON value. Content blocks decode one by
  one.
- **Context edits.** `context_history.json` is a serialised
  `Map<message, [editType, Map<block, update[]>]>`; each update is read
  on its own, and a message or block entry that does not read is one
  faulty update.
- **Timestamps** are ms epoch numbers (or strings of one); a value
  outside the years 1970 to 9999 is none.

### Units

A task is JSON documents, not lines, so the tally counts units: each
element of the API history and the UI messages, each context edit,
each fault reading one of those files, the metadata, the settings and
the index entry (when the task has one) as one unit each, and each
other file of the task directory, which is not read. A report says so
on its first line (`cline: 20 elements and files: …`). A finding's
`File` is the file's name in the task directory (a sub-task's prefixed
`../<id>/`), and its `Line` the element's 1-based position, 0 for a
file.

## What the model saw

### Classic

`api_conversation_history.json` is the history, rewritten whole on
every change; the resume merge (a trailing user message popped and
merged into the next) is already in it. Each request is built from it
by `ContextManager.getAndAlterTruncatedMessages`:

1. **The deleted range.** A request sends messages 0 and 1 and every
   message after the range's end. The range only grows (half, a
   quarter, or everything when summarising), and is recorded in the
   index entry (the final one) and on every UI message
   (`conversationHistoryDeletedRange`, the range in force when it was
   written, with `conversationHistoryIndex`, the last API index then).
   The first UI message carrying a new range was written while the
   request using it streamed, so its index names the user message that
   request sent; `summarize_task` and `condense` set the range while
   their own response streams, for the request after it, which grows
   it by 2 to hide the call and its result.
2. **The first message after the cut loses its tool results**, whose
   calls the range deleted.
3. **Context edits** replace a block's text, the latest per block,
   from the request after the one whose record's time they carry: the
   truncation notice into message 1, `[Continue assisting the user!]`
   into message 0, and a notice in place of a file read a later read
   repeats.
4. **Tool pairing.** Each call of a response gets a result in the next
   message: results first in call order, the last result for an ID, a
   missing one `result missing`. Message 1's calls lose their results
   to the range, so every truncated request sends `result missing` for
   them.
5. **Per provider**, thinking blocks with no signature are left out,
   and Cline's own members stripped.

The deleted range is a filter over a history the file keeps whole,
which is what the session format's `omit` is: at the request that
first applied a range, a `config` entry's `omit.items` names the item
entries of the messages it leaves out, and of the first message
after it the tool results; messages 0 and 1 stay where they are, so
the context is the request in its order, with nothing copied. A range
that grows adds what it newly hides; a reset (a checkpoint restore)
clears the omit and names again the reasoning still left out. 4 is
written as Cline sends it, except that the outputs of message 1's
calls stay in context with their own text where Cline sends `result
missing`. That and 3 the session format cannot hold: an item can be
left out of context but not replaced in place. Each context edit is a
custom entry where it first applied, and both are reported
`agentimport/context-replace`; the session keeps the original text.
5 is an `omit` config entry after the response.

On the summarise path with native tools, the range that hides the
`summarize_task` call also makes the message holding its result (the
continuation prompt with the summary) the first after the cut, and
Cline strips that result: from the next request on, the model never
sees the summary. The conversion rebuilds what Cline sends, so the
summary's output is omitted; this is Cline's behaviour, not a
converter's loss. In the XML era the result is user text and stays.

### Roo

`api_conversation_history.json` holds the full history; since 3.36
condensing and truncating only tag messages. A request is the history
filtered by `getEffectiveApiHistory` and cut by
`getMessagesSinceLastSummary`, and what those do depends on the
release:

| Era | Releases | Shape | What a request sent |
| --- | --- | --- | --- |
| destructive condense | to 3.35 | an assistant summary, `isSummary`, no `condenseId`; earlier messages deleted | the first message, the summary, what follows |
| tagged condense | 3.36 to 3.42 | an assistant summary with `condenseId`, inserted before the kept messages; the hidden ones tagged `condenseParent` | the first message, the summary, the untagged messages after it |
| fresh-start condense | 3.43 and later | a user summary with `condenseId`, appended; every earlier message tagged | the summary and what follows |
| sliding window | 3.36 and later | a user marker `isTruncationMarker` with `truncationId` before the first kept message; the hidden ones tagged `truncationParent` | everything but the tagged messages, the marker among them |

Roo's current release no longer puts the first message back before an
assistant-role summary, so it would send an old task differently from
the release that wrote it; the conversion rebuilds what that release
sent, guessing the era from the summary's role and `condenseId`. A
tag naming a summary or marker the file no longer holds (a rewind
deleted it) hides nothing, as Roo reads it. Per request Roo also
merges consecutive user messages (written as separate user messages
here), drops images for models without image input, and strips
plain-text reasoning unless the model preserves it (an `omit` config
entry here).

## Roo's summaries and markers

Every Roo summary or marker is placed where it happened, at the time
of its UI record (`condense_context`, `sliding_window_truncation`),
matched by `condenseId` or `truncationId` first, every one, and the
rest paired in order. A fresh-start summary was the last message when
written, so it is placed where it stands; with no UI record an older
summary is placed after the 3 messages Roo kept after it, and a marker
at the end of the messages it kept, reported `cline/event-placed`.

At that point the converter works out the list of messages the
requests after it sent, as the release that wrote the file built it
from what existed then (summaries and markers written later, and tags
they added, ignored), and writes it as a `compaction`:

- `summary` is a user message of the text the requests carried in the
  summaries and markers among them (a sliding window's earlier marker
  stays in context beside a later one); a summary or marker is not
  written where it stands;
- `pinned` holds the item entries the requests sent with them: the
  first message Roo 3.35 and earlier, and 3.36 to 3.42, sent ahead of
  an assistant-role summary, the first message ahead of a marker, and
  the calls (and encrypted reasoning) a 3.36 to 3.42 summary carries
  for the results it kept, which are the entries written for those
  calls; the synthetic plain reasoning such a summary starts with is
  left out, as Roo leaves plain reasoning out;
- `first_kept` is the first message kept after them; when none is, a
  custom marker written for it, which puts nothing in context;
- `config` is the settings in force, its `omit` naming what stays
  omitted in the kept run and what the requests left out of it;
- `tokens_before` is the UI record's `prevContextTokens`; the record is
  `cline:ui`, the event `cline:event`, the summary's own members
  `cline:message`.

The session format puts the summary first and pinned items after it.
Roo before 3.43 sent the first message ahead of an assistant-role
summary (so the request starts with the user, as Anthropic and Bedrock
require), and a marker after the first message: the compaction holds
them in the other order, and an assistant summary as a user message,
since a request may not start with the assistant. That is the gap
`agentimport/fold-head`. After a fresh-start summary, Roo leaves out a
result whose call is before the summary; it is omitted.

Before 3.36 Roo's sliding window deleted the messages it hid and wrote
no marker: the file is the context with a gap the converter cannot
see.

## Mapping

Elements are converted in file order, a task is a line, never a tree.
UI records and context edits go between them where they were written:
classic's by the API index they carry, Roo's and the edits by time.

| Native | Session | Notes |
| --- | --- | --- |
| task directory | session `id` the task ID (a derived one when a session store would refuse it) | `created_at` the earliest timestamp; `cwd` classic `cwdOnTaskInitialization` (or `shadowGitConfigWorkTree`), Roo `workspace`; `harness` `cline` with `task_metadata` `environment_history[0].cline_version`, or `roo-code` with none; header members `cline:variant` (`variant`, how it was told, `tool_calls` native, xml, mixed or none) and `agentimport:source` |
| index entry | header member `cline:history_item`; the `task` text's first line (at most 200 characters) the `info` name | the authority for classic's final range and Roo's sub-tasks; with Slim, used and dropped |
| `task_metadata.json`, `settings.json` | header members `cline:task_metadata`, `cline:settings` | dropped with Slim |
| classic `modelInfo.modelId` | the first `config`'s `model`; a `config` delta where it changes; the response's `model` | `modelInfo` and `metrics` stay in `cline:message` |
| Roo, which records no model | the first `config` with no model and members `cline:api_config_name` and `cline:mode` | |
| user message: text, images, documents | `item`, user `message`: `input_text`, `input_image` (data URL), `input_file` (a base64 document); another block an `anthropic.<type>` extension part | the blocks' other members in `cline:blocks` by index; the message's in `cline:message` |
| user `tool_result` answering a call of the response before | `item`, `function_call_output`, before the message's other content | `is_error` as `cline:is_error` (gap `agentimport/tool-error`); members of the blocks inside its content in `cline:content_blocks` by index; a result answering no call a custom entry `{"orphan_result": …}` |
| a call the next user message does not answer (classic) | a `function_call_output` `result missing`, marked `cline:synthesized` | what Cline sends |
| a refusal (`The user denied this operation.` with optional `<feedback>`, Roo's `{"status":"denied"}`; `.clineignore`, Roo's `access_denied`, a command permission) | a `reject` `decision` before the output, `by` `human` or `policy`, `reason` the feedback or the text | the output follows as written |
| assistant `text` | `item`, assistant `message`, `output_text` | XML-era tool calls stay text; parsed into `cline:xml_tools` |
| `thinking` | `reasoning`: `summary_text`, the signature in `encrypted_content`, `cline:provider` `anthropic` | unsigned: an `omit` config entry after the response |
| `redacted_thinking`, server tools, any other block | an `anthropic.<type>` item carrying the block | |
| `tool_use` | `function_call`, `arguments` the input re-encoded; ids through `agentimport.CallIDs` | a renamed id's native one in `cline:call_id` |
| a Gemini `signature` on a text or tool_use block (classic), Roo's `thoughtSignature` block | a `reasoning` item holding only `encrypted_content` before the part it signs, `cline:provider` `gemini` | Roo's trailing block signs the first call |
| Roo `reasoning` with `encrypted_content`, as a block or an element of its own | `reasoning` with its `id`, `encrypted_content` and `summary`, `cline:provider` `openai` | sent as Responses reasoning items |
| Roo `reasoning` with plain `text` | `reasoning` with `summary_text`, then an `omit` config entry | left out unless the model preserves reasoning, which the file does not say |
| `reasoning_details`, `reasoning_content`, `call_id`, other block and message members | `cline:block`, `cline:message` | dropped with Slim |
| assistant message | `response`: `response_id` the message's `id` (`resp_<n>` when it has none or a repeated one), `model`, `usage`, `status` | `incomplete` for `[Response interrupted by user]` or a `user_cancelled` request; `failed` for `[Response interrupted by API Error]`, `Failure: I did not provide a response.` or a failed request; the text stays, since later requests send it |
| UI `api_req_started` | its request's usage on the response it produced, matched by `conversationHistoryIndex` (classic) or time; `api_req_retried` after it count `attempts` | the record minus its `request` text is `cline:usage`; one with no response a custom entry, or dropped when it holds nothing |
| other UI messages | dropped when they restate the API history or are prompts carrying nothing (`say` text, tool, command, completion; `ask` followup, tool, resume); else a custom entry `{"ui_message": …}` | a restating message carrying a checkpoint is kept; partial messages dropped |
| `context_history.json` update | a custom entry `{"context_edit": …}` after the user message of the first request that sent it | gap `agentimport/context-replace` |
| a classic range | a `config` delta whose `omit.items` names what it leaves out, as above | |
| a Roo summary or marker | a `compaction`, as above | |

### Usage

A request's record has `tokensIn`, `tokensOut`, `cacheReads`,
`cacheWrites` and `cost`. As openresponses' Anthropic provider does,
`input_tokens` holds the cache: classic's `tokensIn` is the provider's
own input count, which leaves the cache out for Anthropic, Bedrock,
Vertex, OpenRouter, Cline, DeepSeek, Gemini, Groq, OpenAI's native
(Responses) and a few more, so those get reads and writes added, and
includes it for the OpenAI-compatible rest; an unknown provider is
read as Anthropic's and reported. Roo 3.30 and later write the total
input, cache included, whatever the `apiProtocol`; earlier releases
wrote the provider's raw count (3.25 to 3.29 beside `apiProtocol`,
before 3.25 with none), which leaves the cache out for the Anthropic
protocol. A Roo count smaller than its cache cannot be a total, so the
cache is added; a record with no protocol is reported either way.
`cached_tokens` is the cache reads. Retries reuse one record, so a
request is counted once; a request whose retries were exhausted keeps
its record, and the request a resume makes after it names the same
API index, so the last record naming an index answers it and earlier
ones count as its `attempts`. With no record, a classic message's
`metrics` give the usage, whose `cached` adds reads and writes, so
`cached_tokens` is unset. A count above 2^40 or below 0 is read as 0.

Model calls no response entry holds keep their usage as a custom
entry's top-level `usage`, which RFC 0001 has readers count: a request
that produced no response, the requests a checkpoint restore deleted
(`deleted_api_reqs`, `api_req_deleted`), and classic's in-memory
sub-agents (`subagent_usage`). Roo's condense call records only a
cost, which Responses usage has no member for; it stays in the
compaction's `cline:ui`.

### Sub-tasks

| Native | Session |
| --- | --- |
| Roo 3.35+ `new_task` whose output names the child (`Subtask <id> completed.` or `Delegated to child task <id>`) | the child's task directory, beside the parent's, converted as a `subsession`: its own session `SubsessionID(parent, call_id)` with `parent_session` and `spawned_by`, a link before the output, the output's `parents` naming the child's leaf. The parent waits while the child runs. Roo wrote the output as `Delegated to child task <id>` and rewrote it in place when the child completed; the model saw only the result (lossy) |
| Roo before 3.35: the output an acknowledgement, the result a later user message `[new_task completed] Result: …` | by the RFC's lifetime rule a `peer`, but neither file names the child: unlinked, reported; the child converts on its own |
| Roo's fallback when the parent's history holds no `new_task` call: a user text `Subtask <id> completed.` | unlinked: a subsession link names the call that spawned it, and there is none; the child converts on its own, `parent_session` naming the parent |
| a Roo task converted on its own whose index entry names a parent | `parent_session` the parent task's ID, no `spawned_by`, no link |
| classic `new_task` | starts a new chat with no recorded link: unlinked, reported |
| classic `use_subagents` | ran in memory and wrote no transcript: reported; the UI `subagent` records are custom entries |

A sub-task is read from the directory its ID names beside the parent,
only when the ID is one path element, 32 deep at most, and never one
being converted already. The command converts a Roo task whose parent
is among its inputs after the parent, and skips it when the parent's
conversion holds it.

### Rewinds and checkpoints

Restoring a checkpoint is destructive in both: the API and UI messages
after it are deleted from the files (classic also drops the context
edits after it and resets the range; Roo clears tags naming a deleted
summary). Nothing of the abandoned branch can be recovered; classic's
`deleted_api_reqs` and Roo's `api_req_deleted` UI records keep only the
deleted requests' usage, and are custom entries. Checkpoint
repositories are not read; the UI records naming their commits are
kept.

## Extension types

Open Responses carries types outside its spec as slug-prefixed
extensions, kept verbatim. The converter writes these:

| Extension | Kind | Written for |
| --- | --- | --- |
| `anthropic.<type>` | item | an assistant block the specification has no item for: `redacted_thinking`, server tools, anything newer, as openresponses' Anthropic provider emits it |
| `anthropic.<type>` | content part | a user or tool-result block other than text, a base64 image or a base64 document |

## Findings

Every code the converter reports. Kinds are those of `agentimport`:
`gap` (the session format has no place for it), `dropped`, `lossy`,
`kept` (a namespaced extension) and `repaired`. With `Options.Slim`,
the codes marked "kept, or dropped" are dropped instead. The codes
every converter can report are in [README.md](README.md).

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/context-replace` | gap | A classic context edit replaces a block's text in later requests, or Cline sends `result missing` for message 1's calls after a range; the session keeps the original and an edit is a custom entry. |
| `agentimport/fold-head` | gap | A Roo request sent a message ahead of the summary it folded the history into, or the summary as an assistant message; the compaction puts its summary, as a user message, first. |
| `agentimport/reasoning-provider` | gap | A reasoning signature (Anthropic's, Gemini's, OpenAI's encrypted reasoning) is in `encrypted_content`, tagged `cline:provider`. |
| `agentimport/tool-error` | gap | A tool result's `is_error` is `cline:is_error`. |
| `agentimport/usage-detail` | gap | Cache writes are folded into the input tokens; the request record's counts and cost are `cline:usage`. |
| `cline/checkpoints` | dropped | The task's checkpoint repository is not read. |
| `cline/file-unread` | dropped | A file of the task directory the converter does not read: a hook's leftover transcript, a temporary file, a newer release's. |
| `cline/null-block` | dropped | A content block that is not an object. |
| `cline/request-placeholder` | dropped | A request record with no usage and no response. |
| `cline/request-text` | dropped | A request record's `request` text, the user message rendered as Markdown. |
| `cline/roo-subtask-cycle` | dropped | A sub-task that is the task itself or one being converted; it is not converted again. |
| `cline/roo-subtask-failed` | dropped | A sub-task that could not be converted. |
| `cline/roo-subtask-missing` | dropped | A sub-task whose directory is not beside the parent's, cannot be read, or is not read (`Options.Alone`). |
| `cline/roo-subtask-nesting` | dropped | A sub-task nested past 32 tasks. |
| `cline/summary-content` | dropped | A Roo summary or marker holds content the compaction cannot carry: a block other than text, images, calls and reasoning, or a call or reasoning item no earlier message wrote. |
| `cline/special-unsent` | dropped | A Roo summary or marker the requests after it do not send. |
| `cline/ui-partial` | dropped | A UI message still streaming when the file was written. |
| `cline/ui-restated` | dropped | A UI message that restates the API history or is a prompt carrying nothing. |
| `cline/unreached` | dropped | A unit the converter did not reach; a bug if it occurs. |
| `cline/call-without-result` | lossy | A call no result answers: the next response came, or the task ended, first. |
| `cline/compaction-empty` | lossy | The summaries and markers the requests sent hold no text; no compaction is written. |
| `cline/deleted-range` | lossy | A classic deleted range: an omit config entry names what requests leave out from here; the context edits and `result missing` of truncated requests are reported apart. |
| `cline/empty-message` | lossy | An assistant message with no content is a response with no items. |
| `cline/empty-response` | lossy | The model returned nothing; Cline stored `Failure: I did not provide a response.`, which later requests send; the response failed. |
| `cline/event-placed` | lossy | A compaction placed by a rule, since nothing recorded when it happened. |
| `cline/interrupted-response` | lossy | Cline stored an interrupted response with a marker appended, which later requests send; the response is incomplete or failed. |
| `cline/invalid-input` | lossy | A tool_use input that is not a JSON object: its JSON text, or `{}` when it is not JSON. |
| `cline/legacy-file-name` | lossy | `claude_messages.json`, the earliest releases' name, is read. |
| `cline/missing-index` | lossy | The task has no index entry: no working directory, and classic's range from the UI messages alone. |
| `cline/mistyped-member` | lossy | A block member of another JSON type is kept in `cline:block` and read as empty. |
| `cline/new-task-unlinked` | lossy | Classic `new_task` records no link to the task it started. |
| `cline/no-api-history` | lossy | The task has no API history; the session holds the UI records alone. |
| `cline/no-model` | lossy | Nothing names the model. |
| `cline/no-response-id` | lossy | A message with no response ID; the response gets `resp_<n>`. |
| `cline/no-timestamp` | lossy | The task holds no timestamp; `created_at` is `Options.CreatedAt` or the epoch. |
| `cline/range-invalid` | lossy | A deleted range that is not two counts in order is ignored. |
| `cline/rejection` | lossy | A refused call: a reject decision precedes its output. |
| `cline/result-malformed` | lossy | A tool result whose content is neither text nor blocks is an empty output. |
| `cline/results-reordered` | lossy | Classic tool results stood in another order than the calls; they are written in the calls' order, as Cline sends them. |
| `cline/results-first` | lossy | A user message held content before its tool results; the outputs are written first, as sent. |
| `cline/rewound` | lossy | A checkpoint restore deleted messages here; nothing of them can be recovered. |
| `cline/roo-condense` | lossy | A Roo summary, its era guessed: a compaction as that release sent it. |
| `cline/roo-condensed-result` | lossy | Roo leaves out a result whose call is before the last fresh-start summary; it is omitted. |
| `cline/roo-no-first-message` | lossy | No user message precedes an assistant-role summary; Roo's stand-in is not written. |
| `cline/roo-no-model` | lossy | Roo records no model ID. |
| `cline/roo-plain-reasoning` | lossy | Roo's plain-text reasoning, left out of requests, is omitted after the response. |
| `cline/roo-subtask-alone` | lossy | A Roo sub-task converted on its own names its parent in `parent_session` alone. |
| `cline/roo-subtask-output-rewritten` | lossy | Roo rewrote a `new_task` output in place to the sub-task's result. |
| `cline/roo-subtask-pending` | lossy | A sub-task that had not completed: the output is Roo's placeholder. |
| `cline/roo-subtask-unlinked` | lossy | A sub-task's result that names no call or no child: not linked. |
| `cline/roo-thought-signature` | lossy | Roo's trailing `thoughtSignature` is written before the first call. |
| `cline/roo-truncation` | lossy | A Roo sliding-window marker: a compaction as Roo sends it. |
| `cline/session-id` | lossy | The task ID is not a name a session store takes; the session ID is derived from it. |
| `cline/subagents-in-memory` | lossy | Classic `use_subagents` ran its sub-agents in memory: no transcript. |
| `cline/tool-era` | lossy | The task's tool calls are XML in the text, or mixed, told by shape. |
| `cline/unsigned-thinking` | lossy | A thinking block with no signature, which Cline never sends back, is omitted after the response. |
| `cline/usage-by-order` | lossy | Neither side records an index or a time; request records pair with responses in order. |
| `cline/usage-from-metrics` | lossy | No request record matched; the usage is the message's metrics, with no cache split. |
| `cline/usage-out-of-range` | lossy | A usage count that is negative or above 2^40 is read as 0. |
| `cline/usage-semantics` | lossy | The meaning of the input count is assumed: an unknown provider, or a Roo record with no protocol. |
| `cline/variant-guessed` | lossy | No storage directory names the agent; the variant is told by shape. |
| `cline/xml-tool-calls` | lossy | XML-era tool calls and results stay text, as the model saw them; the calls are `cline:xml_tools`. |
| `cline/anthropic-block` | kept | An assistant block with no Open Responses item is an `anthropic.<type>` item. |
| `cline/compaction-marker` | kept | A custom marker is the `first_kept` of a compaction that keeps nothing. |
| `cline/content-malformed` | kept | A message whose content is neither text nor blocks is a custom entry. |
| `cline/extension-part` | kept | A user block with no Open Responses part is an `anthropic.<type>` part. |
| `cline/history-item` | kept, or dropped | The index entry is `cline:history_item`. |
| `cline/members` | kept, or dropped | Members the conversion does not map are `cline:message`, `cline:block`, `cline:blocks` or `cline:content_blocks`. |
| `cline/orphan-result` | kept | A tool result answering no call of the response before it is a custom entry. |
| `cline/task-file` | kept, or dropped | The metadata or settings are `cline:task_metadata`, `cline:settings`. |
| `cline/ui-record` | kept, or dropped | A UI message the API history does not hold is a custom entry. |
| `cline/usage-without-response` | kept | The usage of model calls no response entry holds (a failed request, requests a restore deleted, sub-agents run in memory) is the custom entry's top-level `usage`, which readers count. |
| `cline/unknown-role` | kept | An element of another role is a custom entry. |
| `cline/call-id-renamed` | repaired | A repeated or ill-formed call ID is renamed; the native one is `cline:call_id`. |
| `cline/malformed-element` | repaired | An element or context edit that does not read is skipped. |
| `cline/malformed-file` | repaired | A file that does not read whole: the elements before the fault are read. |
| `cline/response-id-repeated` | repaired | A response ID another response has; the response gets `resp_<n>`. |
| `cline/result-missing` | repaired | A call with no result: Cline sends `result missing`, which is written. |
