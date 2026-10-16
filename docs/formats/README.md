# Source formats

One document per source: what the native format is, where it is
documented, and every finding code its converter reports, with the
mapping behind it. A converter's tests fail on a code its document
does not list.

- [claudecode.md](claudecode.md): Claude Code.
- [pi.md](pi.md): pi.
- [codex.md](codex.md): Codex CLI.
- [atif.md](atif.md): ATIF, Harbor's Agent Trajectory Interchange
  Format. Its report counts each document's root and steps rather than
  lines.
- [cline.md](cline.md): classic Cline (before 4.0) and Roo Code task
  directories, one format with a variant. Its report counts each
  element of a task's JSON arrays and each other file.
- [openhands.md](openhands.md): the OpenHands software agent SDK's
  conversation directories (CLI, agent server, Agent Canvas). Its
  report counts each file of a conversation directory.

## Codes every converter can report

The shared builder reports these, whatever the source.

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/same-instant` | repaired | Two entries identical in type, body and parent at one instant would be one entry in the session format; the second was moved a nanosecond later. |
| `agentimport/invalid-text` | repaired | Native text copied into a member or a custom entry held invalid UTF-8 or a lone surrogate, which the session format's I-JSON cannot hold in a member name; it was replaced with U+FFFD. |
| `agentimport/repeated-member` | repaired | A native object copied into a member or a custom entry named a member twice, which I-JSON forbids; the last is kept, as JavaScript's JSON.parse keeps it. |
| `agentimport/provenance` | gap | The header's `agentimport:source` member records the import; see [incompatibilities](../incompatibilities.md). |
