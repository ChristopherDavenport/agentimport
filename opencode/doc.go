// Package opencode reads the sessions of OpenCode, the TypeScript
// coding agent (anomalyco/opencode, formerly sst/opencode), in each of
// the three ways it has stored them under its data directory,
// ~/.local/share/opencode:
//
//   - G1, up to 1.1.65: the storage/ tree, one JSON file per session,
//     message and part ([Tree]);
//   - G2, 1.2.0 to 1.19: the V1 tables of opencode.db, session, message
//     and part, whose rows hold the same JSON;
//   - G3, 2.0.0 on: the V2 tables of the same database, session_v2 and
//     session_message, one row per message.
//
// It imports the standard library alone, so it does not open a
// database itself: the opencode/sqlite package opens one and yields its
// rows as this package's types, through the [Store] interface the
// storage tree implements too. Not read here: Crush, the Go program
// once named opencode (opencode-ai/opencode), whose .opencode/opencode.db
// holds another schema; OpenCode 2's preview opencode-next.db; and the
// layout OpenCode wrote before its MessageV2 model,
// project/<dir>/storage/session, which [Tree.Legacy] lists.
//
// Every object keeps the members it does not declare and writes them
// back, so a record decoded and encoded again is the same JSON value. A
// record that cannot be read is a [Stray], never an error. The package
// does not interpret: rebuilding what each request sent, and converting
// to the Agent Session Format, are the convert subpackage's;
// docs/formats/opencode.md is the mapping.
package opencode
