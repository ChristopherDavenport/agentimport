// Package clinesdk reads the sessions of the Cline SDK (@cline/core),
// which Cline's CLI, its JetBrains and SDK hosts, and the VS Code
// extension from 4.0 write under ~/.cline/data/sessions: one directory
// per session, holding its manifest (<id>.json), its canonical history
// (<id>.messages.json, a versioned JSON document), the compacted
// context of its latest compaction (<id>.compaction.json), and the
// histories of the sub-agents and team tasks it ran
// (<agentId>.messages.json, <agentId>__<teamTaskId>.messages.json).
// Classic Cline's task directories, which 4.0 still reads, are the cline
// package's.
//
// Cline also indexes its sessions in SQLite, ~/.cline/data/db/sessions.db,
// which alone records which agent spawned a sub-agent; when SQLite could
// not be opened it kept the same rows in sessions/sessions.index.json.
// This package imports the standard library alone: it reads the JSON
// index itself ([ReadFileIndex]) and declares the [Index] the
// clinesdk/sqlite package implements over sessions.db.
//
// The files are whole JSON documents, rewritten on every change, not
// logs. A messages array is read element by element: an element that
// is not an object is kept with its error and its neighbours still
// read, and an array cut short keeps the elements that end before the
// cut. Every object keeps the members it does not declare and writes
// them back, so an element read and written again is the same JSON
// value.
//
// [ReadSession] reads a session directory; [FindSessions] lists the
// session directories under a data directory. The package also ports
// the rules a reader of the files needs from Cline's own: the
// compaction sidecar's source-prefix hash ([SourcePrefixHash]), which
// decides whether the sidecar still applies, the user-input
// normalisation it hashes ([FormatDisplayUserInput]), and the
// sub-session IDs ([ParseSubSessionID]). Rebuilding what each request
// sent, and converting to the Agent Session Format, are the convert
// subpackage's; docs/formats/clinesdk.md is the mapping.
package clinesdk
