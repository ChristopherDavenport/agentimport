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
| `hermes` | Hermes Agent's database rows (`state.db`), read by column, and its session relations | stdlib |
| `hermes/sqlite` | opens `state.db`, copied, and yields its rows as `hermes`'s types | the above, `internal/sqlite` |
| `hermes/convert` | Hermes → agentsession; takes the database opener in its Options | `hermes`, agentsession, openresponses |
| `cmd/agentimport` | the command-line program | all of the above |

A format package never imports `agentsession`; a test enforces it.
`opencode/sqlite`, `clinesdk/sqlite`, `hermes/sqlite` and `internal/sqlite` are the
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

### Entries no line maps to

Converters also write entries no native line maps to, of two kinds:

- **Structural** entries are ones the format needs: a session's first
  `config`, a `leaf` label, pi's `append`, a `fork_of` link a header
  implies. They say nothing the native log does not; each is reported
  under a code of its own so the report says what was added. The
  structural entries the converters write, and their codes:

  | Entry | Converters | Code |
  | --- | --- | --- |
  | a `leaf` label, when the session resumes at an entry that is not its last | all, through the builder | `agentimport/leaf-label` |
  | a continuation's first `config`, the settings where its predecessor ended | ATIF | `atif/continuation-settings` |
  | an output for a call no result answers, as the harness sends it | Cline, Cline SDK, OpenCode | `cline/result-missing`, `clinesdk/result-missing`, `opencode/call-unfinished` |
  | a `fork_of` link, and the copied prefix of a fork | Claude Code, pi, Codex, Cline SDK, Hermes | `claudecode/fork-context`, `pi/fork-prefix`, `codex/fork-prefix`, `clinesdk/fork-copied`, `hermes/branch-copy` |
  | a decision before an output a refusal or an interruption implies | Claude Code, Cline, Cline SDK, OpenHands, OpenCode | the source's `rejection`, `interrupted` and `permission-*` codes |

  A first `config` made from a header line, a session row or a
  document's root, and an `env` made from one, are that unit's mapping
  and counted with it.
- **Derived** entries are inferred from the content of the converted
  path: a revision a `git commit` printed, a pull request a command
  opened (#29). Only an importer infers entries from content, so by
  RFC 0001's core test the mark is a harness detail in this module's
  namespace, not a format member.

The rule, for any importer:

> An entry no native line maps to is structural, written because the
> format requires it, or derived, inferred from the content of the
> converted path. A derived entry carries `agentimport:derived` naming
> its rule and its inputs (a `call_id` or entry IDs), is reported under
> that rule's code, is written only by an opt-in pass, and states only
> what its input states.

A derived record about a call carries the call's `call_id`, so since
format 0.15 it goes with the call when a redaction leaves the call out,
and every rewriter keeps the namespaced mark.

Where the pass runs (decided; noted on #28). A derived entry stands after
its input, say after a call's output, and an entry's id hashes its
parent, so appending one after conversion in the middle of a path
would branch it, and putting it in line would move every id after it.
The pass therefore runs inside each converter, as a rule set the
converter consults when it writes an output (`Options.Derive`), before
`Verify`, with derivation off writing byte-identical output to today's.
A rule names the source's shell tool and how its output is shaped, so
each source opts in rule by rule. Two rules run, for every source's
shell tools (listed in `docs/formats/README.md`; the command takes
`-derive`): commit revision (`agentimport.CommitRevision`) reads the
revision a commit prints and writes an `env` entry after the call's
output repeating the working directory, `vcs.revision` the abbreviation
git printed; published refs (`agentimport.PublishedRefs`) reads what a
`git push` reports it updated and the URL `gh pr create` prints, and
writes an `agentimport:published` custom entry carrying the call's
`call_id`. Converters that append through the builder's tail call
`Builder.DeriveAfter`, which reads the call the builder saw appended. A session derivation ran on says so in its
header's `agentimport:source`, `derived` naming the rules, since the
entries after a derived one have other ids than a plain conversion
gives them.

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
  usage summed into Responses usage; a tool result's `is_error`, which
  is the output's `failed` and reaches no request.
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
numbers, prints as text or JSON, and answers `Has(kinds...)`.
`DocumentedGap` says whether a gap code is in the table, which
`agentimport convert -strict` uses to refuse a lossy conversion.

### Identity and provenance

- The session ID is the native session ID when it is one; a sub-agent
  session's ID is `agentsession.SubsessionID(parent, call_id)`, as the
  RFC derives it.
- `harness` names the source agent and the version on its first line.
- The header carries `agentimport:source`, a namespaced member:
  `{format, native_id, converter}` (the converter's module version).
  The RFC defines no import provenance, and its core test puts a
  writer's own detail in a member under the writer's namespace, which
  is where this is; it is reported `kept` (`agentimport/provenance`).
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
  read + cache creation, `cached_tokens` is cache read, the cache
  creation is `openresponses-experimental.cache_write_tokens` split by TTL in
  `openresponses-experimental.cache_write_tokens_by_ttl` (openresponses
  extensions §5); server tool counts and service tier are kept, `lossy`.
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
agentimport convert -o <store> [-from auto|claudecode|pi|codex|atif|cline|clinesdk|openhands|opencode|hermes] [-strict] [-slim] [-force] [-report text|json] <file|dir>...
agentimport report [-from auto|claudecode|pi|codex|atif|cline|clinesdk|openhands|opencode|hermes] [-strict] [-slim] [-json] <file|dir>...
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
finding and on a `gap` code `docs/incompatibilities.md` does not list;
a listed gap is a known shape of the format and says nothing of one
transcript. `-accept code,...` names codes that do not fail it. A
failed conversion exits 1 either way.

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
