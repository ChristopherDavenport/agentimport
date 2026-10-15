// Package agentimport holds what every converter into the Agent Session
// Format shares: the [Report] of what a conversion did to its input, the
// [Builder] that appends a native log's entries to a session, and the
// [CallIDs] the format's call rules need.
//
// The converters are in the source packages' convert subpackages,
// claudecode/convert, pi/convert, codex/convert and atif/convert; the
// native formats are in claudecode, pi, codex and atif, which import
// the standard library alone. docs/plans/agentimport.md is the design.
package agentimport
