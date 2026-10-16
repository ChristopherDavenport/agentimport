// Package openhands reads the conversation directories of the OpenHands
// software agent SDK (OpenHands/software-agent-sdk, 1.0.0 to 1.55), the
// format the OpenHands CLI, the agent server and Agent Canvas write: one
// directory per conversation holding base_state.json, the conversation's
// state, and events/event-<index>-<event id>.json, one event per file.
// Sub-agents' conversations nest under subagents/<id>/ in the same
// layout; the agent server adds meta.json. It imports the standard
// library alone. The OpenHands app's V0 sessions and the 1.x app's
// v1_conversations mirror are other formats and are not read here.
//
// Events are read as OpenHands reads them: in index order from 0 to the
// first missing index, a file of the events directory that is not in
// that sequence recorded with why. An event's envelope is decoded
// leniently, and its kind's members on demand ([Event.Message],
// [Event.Action] and the like), so an event of a kind this package does
// not know, or one whose members do not fit, is still read. Every object
// keeps the members it does not declare and writes them back, so an
// event decoded and encoded again is the same JSON value.
//
// [ReadConversation] reads a conversation directory, [Parse] its files'
// contents; [FindSessions] lists the conversation directories under a
// directory. No file records a format version; shapes are told apart
// by the converter. The reader does not interpret: rebuilding what the
// model saw, and converting to the Agent Session Format, are the
// convert subpackage's; docs/formats/openhands.md is the mapping.
package openhands
