// Portions of this file are ported from NousResearch/hermes-agent
// (https://github.com/NousResearch/hermes-agent) at 848fc0ad8b,
// Copyright (c) 2025 Nous Research, licensed under the MIT License;
// they have been modified. See THIRD_PARTY_NOTICES.md.

package hermes

// Relation is how a session relates to the session its
// parent_session_id names, as Hermes tells it.
type Relation int

const (
	// Root is a session with no parent.
	Root Relation = iota
	// Continuation is a compression child: its parent ended with
	// end_reason "compression" and it carries the conversation on.
	Continuation
	// Branch is a /branch copy: model_config._branched_from names the
	// parent, or, before the marker, the parent ended "branched" no
	// later than the child started.
	Branch
	// Reset is a reset child: a new conversation on the parent's chat,
	// marked by _reset_from, or, before the marker, a parent ended at a
	// reset boundary on the child's routing key.
	Reset
	// Delegate is a sub-agent's session: _delegate_from names the
	// parent, its source is "subagent", or nothing else tells it.
	Delegate
	// Tool is a session a tool ran, source "tool".
	Tool
)

// String names the relation as the documents do.
func (r Relation) String() string {
	switch r {
	case Continuation:
		return "compression continuation"
	case Branch:
		return "branch"
	case Reset:
		return "reset"
	case Delegate:
		return "sub-agent"
	case Tool:
		return "tool session"
	}
	return "root"
}

// ResetEndReasons are the end reasons of a parent that a legacy reset
// child follows: hermes_state_common._RESET_END_REASONS.
var ResetEndReasons = map[string]bool{
	"session_reset": true, "session_switch": true, "idle": true, "daily": true, "suspended": true, "resume_pending_expired": true,
}

// Classify tells a session's relation to its parent, the row its
// parent_session_id names, which may be nil when the database does not
// hold it. It follows Hermes' predicates: _is_explicit_fork_child_row
// (a tool session, or a marker that names the parent), then
// _is_compression_child_row (a parent ended "compression"), then the
// legacy branch and reset heuristics of _BRANCH_CHILD_SQL and
// _legacy_reset_child_sql, and else a sub-agent run, as
// _ephemeral_child_sql takes every other child. One rule is this
// package's: a session whose source is "subagent" is a sub-agent
// whatever its parent's end, unless its parent is one too, whose
// compression child it may be. marked says the
// relation was told by a marker or a source rather than by a
// heuristic.
func Classify(s, parent *Session) (rel Relation, marked bool) {
	if s.ParentID == "" {
		return Root, true
	}
	if s.Source == "tool" {
		return Tool, true
	}
	switch s.ParentID {
	case s.Marker(MarkerBranch):
		return Branch, true
	case s.Marker(MarkerDelegate):
		return Delegate, true
	case s.Marker(MarkerReset):
		return Reset, true
	}
	// A sub-agent's source is "subagent", which a compression child
	// takes from its parent: before the delegate marker (schema version
	// 16) Hermes' own predicates take a sub-agent of a parent that later
	// ended "compression" for its continuation.
	if s.Source == "subagent" && (parent == nil || parent.Source != "subagent") {
		return Delegate, true
	}
	if parent == nil {
		return Delegate, s.Source == "subagent"
	}
	if parent.EndReason == "compression" {
		return Continuation, true
	}
	if parent.EndReason == "branched" && !parent.Ended.IsZero() && !s.Started.Before(parent.Ended) {
		return Branch, false
	}
	if ResetEndReasons[parent.EndReason] && s.SessionKey != "" && s.SessionKey == parent.SessionKey {
		return Reset, false
	}
	return Delegate, false
}
