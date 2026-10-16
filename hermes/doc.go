// Package hermes reads the sessions of Hermes Agent
// (NousResearch/hermes-agent), the Python agent behind the hermes CLI,
// its TUI and its messaging gateway. Hermes keeps every session of a
// profile in one SQLite database, state.db, under its home directory
// (~/.hermes/state.db, and ~/.hermes/profiles/<name>/state.db for each
// further profile): the sessions table, a row per session, the messages
// table, a row per message in OpenAI's chat-completions shape, and the
// tables beside them a conversion reads, system_prompts,
// session_model_usage and async_delegations.
//
// It imports the standard library alone, so it does not open a
// database itself: the hermes/sqlite package opens one and yields its
// rows as this package's types, through the [Store] interface. Rows are
// read by column name, since Hermes adds columns to its tables as it
// needs them and an older database lacks the newer ones; a column this
// package does not know is kept with the row. The schema version the
// database records is read too, and the reading rules that depend on
// it are this package's.
//
// A row that cannot be read is a [Stray], never an error. The package
// does not interpret what the model saw: [Classify] tells a child
// session's relation to its parent as Hermes does, and the convert
// subpackage rebuilds each session's context; docs/formats/hermes.md is
// the mapping. Not read here: the ShareGPT trajectories, the trace
// export, the session JSON and JSONL files Hermes falls back to when
// the database is unavailable, and the export of hermes sessions export.
package hermes
