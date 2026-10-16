// Package atif reads ATIF, the Agent Trajectory Interchange Format that
// Harbor (github.com/laude-institute/harbor) defines in its RFC 0001 and
// its Pydantic models: one JSON document per trajectory, holding the
// agent, its steps, and optionally embedded sub-agent trajectories. It
// imports the standard library alone.
//
// A document is decoded block by block: the root, the agent, each step,
// each tool call, observation result, content part and reference on its
// own. Every object keeps the members it does not declare and writes
// them back, so a document read and written again is the same JSON
// value; a block that is not an object is kept as written with its
// error, and its neighbours still read. Numbers are kept as written, so
// a token count spelled 12.0 survives; [Int] and [Float] read them as
// leniently as Harbor's models do. A document cut short, which a
// producer that rewrites its file in place can leave, is salvaged: the
// root members and steps that end before the cut are read, and the cut
// is reported.
//
// [Read], [Parse] and [ReadFile] read a document; [Probe] tells an ATIF
// file from another JSON file; [FindSessions] lists the ATIF files under
// a directory and [Names] the related files a document names. The
// reader does not interpret: resolving references and continuations,
// and converting to the Agent Session Format, are the convert
// subpackage's; docs/formats/atif.md is the mapping.
package atif
