// Package pi reads pi's session files: the JSONL files under
// ~/.pi/agent/sessions, one per session. It imports the standard
// library alone.
//
// pi specifies the format: docs/session-format.md and
// docs/message-types.md in the @earendil-works/pi-coding-agent package,
// and its TypeScript types (session-manager.d.ts, pi-ai's types.d.ts).
// This package follows them at session version 3, and reads versions 1
// and 2 through [Migrate], which applies pi's own migrations. Every
// object keeps the members it does not declare and writes them back, a
// line of a shape it does not expect keeps its raw bytes, and a line
// that is not JSON is recorded and skipped, as pi skips it.
//
// [Read] and [ReadFile] read; [BuildTree] links the entries as pi does.
// Converting to the Agent Session Format is the convert subpackage's;
// docs/formats/pi.md is the mapping.
package pi
