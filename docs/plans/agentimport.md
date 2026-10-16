# agentimport

Converters from other coding agents' session logs into the Agent
Session Format (`agentsession`, RFC 0001). The RFC promises converters
for pi, Claude Code and Codex so the format arrives with existing
corpora behind it (RFC 0001, *Prior art*); this repository is where
they live.

## Goals

- Read each native log faithfully, as its own format, in a package that
  depends on the standard library alone, so a program that only wants to
  read Claude Code or pi transcripts can.
- Convert each into sessions that `agentsession.Read` and
  `VerifyRecords` accept, with the conversation the model saw on every
  path.
- Surface every difference between the two formats. The RFC requires a
  converter to document which native entries it maps and which it drops
  (RFC 0001, *Conformance*). Here that is a document per source and a
  structured report per conversion, and the two are kept in step by
  tests.

Not goals: writing native formats back, resuming a native session in its
own agent, or reading formats with no on-disk spec we can follow
(Antigravity's encrypted protobuf).

## Layout

One module, `github.com/ChristopherDavenport/agentimport`.

| Package | What it is | May import |
| --- | --- | --- |
| `agentimport` | the report, the shared building blocks every converter uses | agentsession, openresponses, stdlib |
| `claudecode` | the Claude Code transcript format: line types, reader, tree | stdlib |
| `claudecode/convert` | Claude Code → agentsession | the above |
| `pi` | the pi session format (session version 3) | stdlib |
| `pi/convert` | pi → agentsession | the above |
| `codex` | the Codex CLI rollout format | stdlib |
| `codex/convert` | Codex → agentsession | the above |
| `atif` | ATIF, Harbor's trajectory format (v1.0 to v1.8) | stdlib |
| `atif/convert` | ATIF → agentsession | the above |
| `cline` | classic Cline and Roo Code task directories | stdlib |
| `cline/convert` | Cline and Roo → agentsession | the above |
| `openhands` | OpenHands SDK conversation directories | stdlib |
| `openhands/convert` | OpenHands → agentsession | the above |
| `opencode` | OpenCode's storage files and the records of its database | stdlib |
| `opencode/sqlite` | opens OpenCode's database, copied, and yields its rows as `opencode`'s types | the above, `modernc.org/sqlite` |
| `opencode/convert` | OpenCode → agentsession; takes the database opener in its Options | `opencode`, agentsession, openresponses |
| `clinesdk` | the Cline SDK's session directories (Cline 4) and the JSON index | stdlib |
| `clinesdk/sqlite` | reads Cline's `sessions.db`, copied, as `clinesdk`'s index | the above, `internal/sqlite` |
| `clinesdk/convert` | Cline SDK → agentsession; takes the index opener in its Options | `clinesdk`, agentsession, openresponses |
| `cmd/agentimport` | the command-line program | all of the above |

A format package never imports `agentsession`; a test enforces it.
`opencode/sqlite`, `clinesdk/sqlite` and `internal/sqlite` are the
packages that import a database driver, which `make deps` holds them
to. The
converter packages are all named `convert`, since a program almost
always wants one source; `cmd/agentimport` imports them under aliases.

A new source is two packages, `<source>` and `<source>/convert`, a
`docs/formats/<source>.md`, and a case in the command's detection;
`new-source.md` has the method, the defects to build against and what
is known of the candidates, Gemini CLI and VS Code chat sessions first.

## Format packages

Each format package reads a file into typed lines and keeps everything
it does not type:

- Every struct has `Extra map[string]json.RawMessage` holding the
  members it does not declare, and marshals them back. A line decoded
  and re-encoded is the same JSON value; a test checks this over every
  fixture.
- A line of an unknown `type` decodes to a line holding only its
  envelope and its raw bytes.
- A malformed line never stops the reader. It is recorded with its line
  number and the reader goes on: Claude Code files hold runs of NUL bytes
  and truncated objects mid-file, not only at the end.
- The reader does not interpret. Building the conversation tree is a
  separate, exported step (`claudecode.BuildTree`, and the like), since it
  is where the native format's rules live and a reader of the format
  wants it without the conversion.

The formats are documented by their owners to different degrees: pi
ships `docs/session-format.md` and TypeScript types; Codex's are its
Rust source; Claude Code's are undocumented and change per release (the
Agent SDK calls the line union "CLI-internal"). The Claude Code package
is written from the transcripts of versions 2.1.271 to 2.1.300 and the
Agent SDK's typings, and says so.

## The conversion

Every converter follows the same rules.

### Every native line is accounted for

Each native line ends in exactly one of four places, and the report
counts them:

| Disposition | Meaning |
| --- | --- |
| `mapped` | it became core entries: items, responses, config, compaction, env, info, link |
| `kept` | it became a namespaced extension other readers carry but do not interpret: a `custom` entry with the source's `ns`, or a namespaced member |
| `dropped` | it is not in the output |
| `malformed` | it could not be read |

`mapped + kept + dropped + malformed` equals the number of lines read;
a test asserts it for every fixture. The default drops only lines that
carry no information beyond what the output already says (a line whose
content is fully re-derived elsewhere); every drop has a finding.

### Findings

Below line level, a finding records each place the output differs from
the input:

```go
type Finding struct {
	Code   string // stable: "claudecode/thinking-signature"
	Kind   Kind   // lossy, kept, dropped, repaired, gap
	File   string // the source file, relative to the input
	Line   int    // 1-based, 0 when not tied to a line
	Detail string
}
```

- `lossy`: mapped, with a change of meaning or precision. Anthropic
  usage summed into Responses usage; a tool result's `is_error`.
- `kept`: carried as a namespaced extension. Opaque to other readers,
  but present.
- `dropped`: not in the output.
- `repaired`: the source broke a rule of its own and the converter
  fixed it. A skipped malformed line, a repeated call ID renamed, an
  orphaned tool result reattached.
- `gap`: the agentsession format has no place for this. Every `gap`
  code is listed in `docs/incompatibilities.md` as a question for the
  RFC, and the converter falls back to `kept` or `dropped` for it.

Codes are stable strings, namespaced by source, and each is documented
in `docs/formats/<source>.md`. A test fails when a converter emits a
code its document does not list, so the document cannot fall behind the
code.

`Report` groups findings by code with counts and the first few line
numbers, prints as text or JSON, and answers `Has(kinds...)` so a
caller can refuse a lossy conversion (`agentimport convert -strict`).

### Identity and provenance

- The session ID is the native session ID when it is one; a sub-agent
  session's ID is `agentsession.SubsessionID(parent, call_id)`, as the
  RFC derives it.
- `harness` names the source agent and the version on its first line.
- The header carries `agentimport:source`, a namespaced member:
  `{format, native_id, converter}` (the converter's module version).
  The RFC defines no import provenance; this is a `gap`.
- Every entry made from a native line carries the native identity in a
  namespaced member beside the body (`claudecode:uuid`, `pi:id`), as
  the RFC suggests for a native call ID. The converter keeps its own
  native-to-hash map; `legacy_id` is migration's and is not used.
- `records` is left empty: a converter over a log without run,
  dispatch and decision entries promises none (RFC 0001, `records`).

### Building

`agentimport.Builder` wraps `agentsession.Session` for the converters:

- Appends in topological order, parent first, whatever the order of
  the native file (Claude Code writes children before their parents).
- Takes timestamps from the native lines, never from the clock, and
  never sorts by them; Claude Code's are not monotonic in file order.
- Two identical entries under one parent at one instant are one entry
  in agentsession; the builder nudges the second by a nanosecond and
  reports `repaired`.
- `CallIDs` assigns call IDs unique across the session, as the RFC
  requires: a repeated or empty native ID gets the native ID with
  characters outside `[A-Za-z0-9_-]` made `_`, cut to fit 64 with a
  `_n` suffix, the scheme `export.ItemsFrom` and agentturn use. The
  native ID is kept in `<ns>:call_id`.
- The output is verified before it is returned: written, read back
  with `agentsession.Read`, and `VerifyRecords` run on every leaf. A
  converter that produces an invalid session is a bug, not a finding.

### Payload mapping

Payload choices follow `openresponses/providers` so that a converted
session replays through the same provider adapter:

- Anthropic `thinking` → `reasoning`, the text as a `summary_text`, the
  signature in `encrypted_content`, as `providers/anthropic` does.
  `redacted_thinking`, server tools and blocks newer than the converter
  → `anthropic.<block type>` items carrying the block.
- Anthropic usage → Responses usage: `input_tokens` is input + cache
  read + cache creation, `cached_tokens` is cache read; the cache-write
  split, server tool counts and service tier are kept, `lossy`.
- A tool call's arguments are its input re-encoded as a JSON string.
- Images stay inline as data URLs.

### Sub-sessions

A sub-agent with its own transcript (Claude Code `subagents/`, Codex
child rollouts) becomes its own session with `parent_session`, and the
parent gets a link to it. Which link follows RFC 0001's lifetime rule:
an agent the call waited for, whose result is the call's output, is a
`subsession`, linked before the output, and its header carries
`spawned_by`; an agent the call only started, which works on after the
call returns, is a `peer`, linked after the output, which is the
harness's acknowledgement and not the agent's answer (the RFC reads an
output that follows a link as the callee's answer), and its
`parent_session` is provenance alone.
Codex's `spawn_agent` always returns once the agent is started, so its
sub-agents are peers. One conversion returns the main session and its
sub-agents' sessions, each after its parent's; the command writes each
as its own file. Codex rollouts live in separate files of one sessions
tree, so the Codex converter indexes that tree (the session meta line
of each rollout) to find a thread's children.

## The command

```
agentimport convert -o <store> [-from auto|claudecode|pi|codex|atif|cline|clinesdk|openhands|opencode] [-strict] [-slim] [-force] [-report text|json] <file|dir>...
agentimport report [-from auto|claudecode|pi|codex|atif|cline|clinesdk|openhands|opencode] [-strict] [-slim] [-json] <file|dir>...
```

`convert` writes every session a conversion makes into the jsonl store
rooted at `-o` through the store's own API, so the result is a store
`agentsession` and dax read, and prints the report: in full for one
input, a line per input and the reports added up per source for
several. Each session is written on its own: one the store holds is
kept as it is and listed, and is no failure, unless `-force` replaces
it, and a session that fails to write does not stop its siblings, so
converting a tree again adds the sub-agents spawned since.
`report` converts without writing and adds the findings up per source,
which is how a corpus is surveyed. `-strict` exits 1 on any `dropped`
or `gap` finding; a failed conversion exits 1 either way.

With `-from auto`, a directory is searched with every source's finder
and each file is detected by name and first lines: a Codex rollout by
its `rollout-*` name, a `session_meta` or `record_type` line or the 2025
bare layout's first object; a pi session by a first line of type
`session` that is not an agentsession header; a Claude Code transcript
by a `sessionId` on one of its first lines. A Claude Code sub-agent
transcript is converted with its main session. A Codex rollout whose
thread has an ancestor among the inputs, at any depth, waits for it:
it is skipped when the ancestor's result holds its session, and
converted on its own when none does, so no input is lost. The command
indexes each Codex sessions tree once and passes the index to every
conversion in it;
an agentsession file is skipped.

The store names a session's file by its ID and its directory by its
working directory, both taken from the native log. The jsonl store
keeps both under its root (agentsession v0.0.26): it refuses an ID
that is not a file name with `jsonl.ErrBadName` and files a working
directory of `/..` under `default`, so a crafted log cannot write
outside `-o`.

## Tests

Fixtures are synthetic and hand-written per shape, under
`<source>/testdata` and `<source>/convert/testdata`; never copied from a
real session. Golden outputs (the session file and the report) are
regenerated with `-update`. A corpus test runs every converter over a
local directory named by `AGENTIMPORT_CORPUS_<SOURCE>` and asserts that
each conversion verifies and accounts for every line; it is skipped
without the variable and is not part of `make check`.
