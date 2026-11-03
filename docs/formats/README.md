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
- [clinesdk.md](clinesdk.md): the Cline SDK's session directories
  (Cline 4), with its `sessions.db` index. Its report counts each file
  of a session directory and each message of its arrays.
- [opencode.md](opencode.md): OpenCode's data directory, its storage
  files and its database's V1 and V2 tables. Its report counts records:
  each storage file and each row of the session tables.
- [hermes.md](hermes.md): Hermes Agent's database, `state.db`. Its
  report counts rows: each row of `sessions` and of `messages`.

## Codes every converter can report

The shared builder reports these, whatever the source.

| Code | Kind | Meaning |
| --- | --- | --- |
| `agentimport/same-instant` | repaired | Two entries identical in type, body and parent at one instant would be one entry in the session format; the second was moved a nanosecond later. |
| `agentimport/invalid-text` | repaired | Native text copied into a member or a custom entry held invalid UTF-8 or a lone surrogate, which the session format's I-JSON cannot hold in a member name; it was replaced with U+FFFD. |
| `agentimport/repeated-member` | repaired | A native object copied into a member or a custom entry named a member twice, which I-JSON forbids; the last is kept, as JavaScript's JSON.parse keeps it. |
| `agentimport/identity` | kept | A native member that identifies a person, an account or organization, a machine or the native session was carried into the session; the finding names the member by its path (`Path`, and the group's `paths` with a count each), never its value. Each source lists its members in its document's Identity section, which a test holds the report to. |
| `agentimport/exit-code` | kept | A command's exit status, recorded structured by the source (OpenHands' terminal metadata, pi's `bashExecution`, OpenCode's `bash` metadata and V2 shell rows), is the item entry's `agentimport:exit_code`. It is the tool's own detail, kept under Slim, and says nothing of `failed`: a command may exit nonzero without the call failing. |
| `agentimport/provenance` | kept | Once per conversion: the header's `agentimport:source` member records the import, the converter, the source format and the native ID. The member is the converter's own detail under its namespace, as RFC 0001's core test places it, not a gap. |
