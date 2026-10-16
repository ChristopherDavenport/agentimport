# agentimport

Converters from other coding agents' session logs into the
[Agent Session Format](https://github.com/ChristopherDavenport/agentsession):
Claude Code, pi, Codex, classic Cline and Roo Code, the Cline SDK
(Cline 4), OpenHands, OpenCode, and ATIF,
Harbor's trajectory format, which many more agents write. Each conversion returns the sessions it made
and a report that accounts for every native line and lists every
difference between the two formats by a stable code, so nothing is
lost without saying so.

```sh
go install github.com/ChristopherDavenport/agentimport/cmd/agentimport@latest

# What converting would change, added up across a corpus; writes nothing.
agentimport report ~/.claude/projects ~/.pi/agent/sessions ~/.codex/sessions

# Convert one session, or every session under a directory, into an
# agentsession jsonl store: <store>/<project-key>/<created-at>_<id>.jsonl.
agentimport convert -o ~/sessions ~/.claude/projects/<project>/<session>.jsonl
agentimport convert -o ~/sessions -report json ~/.codex/sessions
```

Each file's source is told from its name and first lines, an ATIF
document by its `schema_version`, a Cline or Roo task by its files'
names in a task directory, an OpenHands conversation by its directory
or its files, an OpenCode store by its data directory, a file in it or
its database, a Cline SDK session by its directory or a file in it;
`-from claudecode|pi|codex|atif|cline|clinesdk|openhands|opencode`
names it instead. A Claude Code session, and a
Codex rollout, is converted with its sub-agents, each a session of its
own linked to its parent; a Codex sub-agent's rollout given beside an
ancestor's is converted with it, not again on its own, and on its own
when the ancestor's conversion did not produce it. An ATIF document is
converted with the continuations and sub-agent files it names, which
are skipped as inputs when its conversion holds them. A Roo task is
converted with the sub-tasks it waited for, and an OpenHands
conversation with its sub-agents, which are skipped as inputs when its
conversion holds them. An OpenCode store, a data directory such as
`~/.local/share/opencode` or a database, converts whole: every session,
each with its sub-agents; its database is copied and read, never
written. A Cline SDK session directory converts with the sub-agents
and team tasks whose histories it holds, linked to their calls with
the help of Cline's `sessions.db`, likewise copied and read. A session already in
the store is kept as it is, listed, and is no failure, unless `-force`
replaces it; the other sessions of the same conversion are written all
the same, so converting a tree again adds what is new since. `-slim` drops what the model never saw rather than keep it
in namespaced members, and `-strict` exits 1 when anything was dropped
or had no place in the format. A Codex rollout compressed with zstd is
refused; decompress it with `zstd -d` first.

Each source is two packages: one that reads the native format as it
is, importing the standard library alone, and a `convert` package that
writes `agentsession` sessions. OpenCode and the Cline SDK have a
third, `opencode/sqlite` and `clinesdk/sqlite`, the packages that
import a database driver (`modernc.org/sqlite`, SQLite in Go, no cgo),
both through `internal/sqlite`.

| Source | Format package | Converter | What maps and what does not |
| --- | --- | --- | --- |
| Claude Code | `claudecode` | `claudecode/convert` | [docs/formats/claudecode.md](docs/formats/claudecode.md) |
| pi | `pi` | `pi/convert` | [docs/formats/pi.md](docs/formats/pi.md) |
| Codex | `codex` | `codex/convert` | [docs/formats/codex.md](docs/formats/codex.md) |
| ATIF (Harbor) | `atif` | `atif/convert` | [docs/formats/atif.md](docs/formats/atif.md) |
| Cline (classic, before 4.0) and Roo Code | `cline` | `cline/convert` | [docs/formats/cline.md](docs/formats/cline.md) |
| Cline SDK (Cline 4: CLI, VS Code from 4.0, JetBrains) | `clinesdk`, `clinesdk/sqlite` | `clinesdk/convert` | [docs/formats/clinesdk.md](docs/formats/clinesdk.md) |
| OpenHands (SDK conversations: CLI, agent server, Agent Canvas) | `openhands` | `openhands/convert` | [docs/formats/openhands.md](docs/formats/openhands.md) |
| OpenCode (storage files, its database's V1 and V2 tables) | `opencode`, `opencode/sqlite` | `opencode/convert` | [docs/formats/opencode.md](docs/formats/opencode.md) |

What the session format itself cannot hold, across all sources, is in
[docs/incompatibilities.md](docs/incompatibilities.md).

## Development

```sh
make check
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT; see [LICENSE](LICENSE). Some files port code of the programs
whose logs they read, under those programs' MIT and Apache 2.0
licenses; [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) lists them.
