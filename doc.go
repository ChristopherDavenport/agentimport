// Package agentimport holds what every converter into the Agent Session
// Format shares: the [Report] of what a conversion did to its input, the
// [Builder] that appends a native log's entries to a session, and the
// [CallIDs] the format's call rules need.
//
// The converters are in the source packages' convert subpackages,
// claudecode/convert, pi/convert, codex/convert, atif/convert,
// cline/convert, clinesdk/convert, openhands/convert and
// opencode/convert; the native formats are in claudecode, pi, codex,
// atif, cline, clinesdk, openhands and opencode, which import the
// standard library alone, and OpenCode's and Cline's databases are read
// by opencode/sqlite and clinesdk/sqlite. docs/plans/agentimport.md is the
// design.
package agentimport
