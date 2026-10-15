# agentimport

Converters from other coding agents' session logs into the
[Agent Session Format](https://github.com/ChristopherDavenport/agentsession):
Claude Code, pi and Codex. Each conversion returns the sessions it made
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

Each file's source is told from its name and first lines; `-from
claudecode|pi|codex` names it instead. A Claude Code session, and a
Codex rollout, is converted with its sub-agents, each a session of its
own linked to its parent; a Codex sub-agent's rollout given beside its
parent's is converted with the parent, not again on its own. A session already in the store is refused unless `-force`
replaces it. `-slim` drops what the model never saw rather than keep it
in namespaced members, and `-strict` exits 1 when anything was dropped
or had no place in the format. A Codex rollout compressed with zstd is
refused; decompress it with `zstd -d` first.

Each source is two packages: one that reads the native format as it
is, importing the standard library alone, and a `convert` package that
writes `agentsession` sessions.

| Source | Format package | Converter | What maps and what does not |
| --- | --- | --- | --- |
| Claude Code | `claudecode` | `claudecode/convert` | [docs/formats/claudecode.md](docs/formats/claudecode.md) |
| pi | `pi` | `pi/convert` | [docs/formats/pi.md](docs/formats/pi.md) |
| Codex | `codex` | `codex/convert` | [docs/formats/codex.md](docs/formats/codex.md) |

What the session format itself cannot hold, across all sources, is in
[docs/incompatibilities.md](docs/incompatibilities.md).

## Development

```sh
make check
```

See [CONTRIBUTING.md](CONTRIBUTING.md).
