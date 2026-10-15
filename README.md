# agentimport

Converters from other coding agents' session logs into the
[Agent Session Format](https://github.com/ChristopherDavenport/agentsession):
Claude Code, pi and Codex. Each conversion returns the sessions it made
and a report that accounts for every native line and lists every
difference between the two formats by a stable code, so nothing is
lost without saying so.

```sh
go install github.com/ChristopherDavenport/agentimport/cmd/agentimport@latest

agentimport convert -o ~/sessions ~/.claude/projects/<project>/<session>.jsonl
agentimport report ~/.pi/agent/sessions        # what a conversion would lose, across a corpus
```

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
