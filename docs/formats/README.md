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
| `agentimport/identity` | kept | A native member that identifies a person, an account or organization, a machine or the native session was carried into the session; the finding names the member by its path (`Path`, and the group's `paths` with a count each), never its value. Each source lists its members in its document's Identity section, which a test holds the report to. No finding names a session by its ID: every converter's result is scrubbed (`Result.ScrubIdentity`) so a file named for a session, or a detail naming a child or a parent, names it by its place, `session #<n>` among the result's sessions, or `the parent session`. |
| `agentimport/commit-revision` | kept | A derived entry, written only with `-derive`: after a shell call whose output prints the commit it made (`[<branch> <revision>] <subject>`), an `env` entry repeating the one in force, an `env` entry being a whole snapshot (its working directory, workspace, tools and `vcs` members such as a branch or the git members a source keeps), with the revision git printed, abbreviated as it printed it, and no `dirty`, which a commit leaves unknown; file hashes, which say what the tree held before, are left out. With no `env` in force it holds the working directory and a git `vcs` (RFC 0001 0.17 reads an abbreviated revision as git resolves it), carrying `agentimport:derived` `{rule: "commit-revision", call_id}`. A quiet or dry-run commit yields none. |
| `agentimport/published-refs` | kept | A derived entry, written only with `-derive` (`Options.Derive`): what a shell call published, as its output says (the refs a `git push` reports, the URL `gh pr create` prints), is a `custom` entry `agentimport:published` after the call's output, carrying its `call_id` and `agentimport:derived` `{rule: "published-refs", call_id}`. A dry run, a quiet push or output that does not say yields none. Every source's shell tools: Claude Code `Bash`, Codex `shell`, `shell_command`, `local_shell`, `container.exec`, `exec_command`, `write_stdin`, pi `bash`, Cline `execute_command`, the Cline SDK `run_commands` and `execute_command`, OpenHands `terminal` and `execute_bash`, OpenCode `bash`, Hermes `terminal`, and in an ATIF document the shell tools of the harnesses it projects (`bash_command`, `bash`, `Bash`, `shell`, `shell_command`, `exec_command`, `terminal`, `execute_bash`, `execute_command`). |
| `agentimport/leaf-label` | lossy | A structural entry: the session resumes at an entry that is not the last one written, so a `leaf` label, which no native line maps to, names it (the plan's "Entries no line maps to"). |
| `agentimport/exit-code` | kept | A command's exit status, recorded structured by the source (OpenHands' terminal metadata, pi's `bashExecution`, OpenCode's `bash` metadata and V2 shell rows), is the item entry's `agentimport:exit_code`. It is the tool's own detail, kept under Slim, and says nothing of `failed`: a command may exit nonzero without the call failing. |
| `agentimport/provenance` | kept | Once per conversion: the header's `agentimport:source` member records the import, the converter, the source format and the native ID. The member is the converter's own detail under its namespace, as RFC 0001's core test places it, not a gap. |

## Skill reads

A skill whose instructions or files reached the model is recorded as
agentskill's `skill` tool has agentturn's recorder record one, so a
reader finds a skill the same way in a session a harness recorded and
in an imported one: a `custom` entry with `ns` `agentskill:read`
(`agentimport.SkillReadNS`), `call_id` the call that served it when one
did, and `data` the shape of agentskill's `Read`:

| Member | Holds |
| --- | --- |
| `name` | the skill's name, as the call or the log names it |
| `location` | the skill's `SKILL.md`, when the log says where it is |
| `path` | the file inside the skill that was served; absent for the instructions |
| `bytes` | the size of the text served, when the log holds it |
| `sha256` | the hex SHA-256 digest of the text served, when the log holds it |

agentskill writes `location`, `bytes` and `sha256` always; an imported
record leaves out what the log does not say. This module cannot import
agentskill, so the shape is held here as a contract
(`agentimport.SkillRead`).

Where the record sits: before the serving call's output when that output
carries what was served, as agentskill's tool returns it; otherwise
after the item that carried the skill to the model. Claude Code serves
a skill's instructions in a hidden user message after the `Skill`
call's output, which says only that the skill launched, so its record
follows that message and names the call; OpenCode 2 sends a skill's
text in the user message, with no call, so its record follows the
message and names none; so does Codex for a `<skill>` part of a user
message (`<name>` and `<path>` read from it), and OpenHands for each
of a user message's `activated_skills`, whose knowledge it sends in the
extended content (by name alone).
Sources that record skill reads: Claude Code (a `Skill` call and the
hidden message that carries the instructions), OpenCode 2 (a user
row's `skills`), Codex (a `<skill>` part) and OpenHands
(`activated_skills`).

## Instruction layers sent as messages

An item a harness composed from an instruction layer and sent as a
message names the layer in `instructions_part` with RFC 0001's
well-known `source` (`agentsmd`, `agentskill`, `agentmemory`); a message
of several layers names each, in the order it holds them (format 0.18's
list), and the item's `trust` carries any trust the source records of a
layer (none does). Claude Code (`nested_memory`, `instructions` and
`skill_listing` attachments) and Codex (the AGENTS.md instructions
message and the skills catalogue) send layers as messages of their
own, and their items name them. Every other source's page says, under
Instruction layers, why it has none to name: the layers are inside a
system prompt stored whole or not at all, or beside a person's text.
