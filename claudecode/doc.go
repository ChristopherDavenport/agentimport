// Package claudecode reads Claude Code session transcripts: the JSONL
// files under ~/.claude/projects, one per session and one per
// sub-agent. It imports the standard library alone.
//
// The format is not specified. Anthropic calls the line union
// CLI-internal and changes it between releases; this package was
// written from the transcripts of Claude Code 2.1.271 to 2.1.300 and the
// Agent SDK's typings, which type the messages, the compaction metadata
// and the tool output sidecars but not the lines. So the reader keeps
// what it does not type: every object keeps its undeclared members and
// writes them back, a line of a shape it does not expect keeps its raw
// bytes, and a line that is not JSON is recorded and skipped, wherever
// it is in the file.
//
// [Read] and [Open] read; [BuildTree] applies the rules a reader of the
// format needs to see the conversation, which docs/formats/claudecode.md
// sets out. Converting to the Agent Session Format is the convert
// subpackage's.
package claudecode
