// Package codex reads the Codex CLI's rollout files: the JSONL files
// under ~/.codex/sessions/YYYY/MM/DD, one per thread. It imports the
// standard library alone.
//
// Codex does not document the format. Its definition is the Rust source
// of openai/codex: the RolloutItem union and its wire form in
// codex-rs/history (lib.rs, rollout_payload.rs), the ResponseItem,
// SessionMeta, TurnContextItem and EventMsg types in codex-rs/protocol
// (models.rs, protocol.rs), and which items are written at all in
// codex-rs/rollout/src/policy.rs. This package follows the source at
// commit 7ae831991792744434027c431dfbc7e81dd5c12c (2026-10-15), and the
// layout rollouts had before September 2025, when the first line was a
// bare session meta and the items followed bare, with "record_type"
// state lines between (codex-rs/core/src/rollout.rs at 9dbe7284).
//
// Every object keeps the members it does not declare and writes them
// back; a line of a shape the package does not expect keeps its raw
// bytes; a line that is not JSON is recorded and skipped, as Codex skips
// it. A zstd-compressed rollout (.jsonl.zst) is refused with
// [ErrCompressed]: the standard library has no zstd.
//
// [Read] and [ReadFile] read; converting to the Agent Session Format is
// the convert subpackage's, and docs/formats/codex.md is the mapping.
package codex
