// Portions of this file are ported from NousResearch/hermes-agent
// (https://github.com/NousResearch/hermes-agent) at 848fc0ad8b,
// Copyright (c) 2025 Nous Research, licensed under the MIT License;
// they have been modified. See THIRD_PARTY_NOTICES.md.

package convert

import (
	"regexp"
	"strings"
)

// The patterns sanitize_context removes from user and assistant text
// when Hermes loads a session (agent/memory_manager.py): its injected
// memory blocks, their system note and stray fence tags.
var (
	fenceTagRE        = regexp.MustCompile(`(?i)</?\s*memory-context\s*>`)
	internalContextRE = regexp.MustCompile(`(?i)<\s*memory-context\s*>[\s\S]*?</\s*memory-context\s*>`)
	internalNoteRE    = regexp.MustCompile(`(?i)\[System note:\s*The following is recalled memory context,\s*NOT new user input\.\s*Treat as (?:informational background data|authoritative reference data[^\]]*)\.\]\s*`)
)

// sanitizeContext is sanitize_context: the memory blocks, notes and
// fence tags taken out, in that order.
func sanitizeContext(text string) string {
	for _, re := range []*regexp.Regexp{internalContextRE, internalNoteRE, fenceTagRE} {
		text = re.ReplaceAllString(text, "")
	}
	return text
}

// strip is Python's str.strip(): leading and trailing whitespace as
// Python's str.isspace reads it.
func strip(s string) string {
	return strings.TrimFunc(s, isPySpace)
}

// isPySpace is Python's str.isspace for one character: Unicode white
// space and the ASCII separators \x1c to \x1f.
func isPySpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x1f, 0x85, 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// reviewHarnessPrefixes open the background-review harness prompts
// older Hermes builds wrote into the session they reviewed
// (hermes_state._REVIEW_HARNESS_PREFIXES); a resume strips each and the
// curator's reply after it.
var reviewHarnessPrefixes = []string{
	"Review the conversation above and update the skill library",
	"Review the conversation above and consider saving to memory",
}

// isReviewHarness is _is_background_review_harness_message.
func isReviewHarness(role, content string) bool {
	if role != "user" && role != "system" {
		return false
	}
	t := strings.TrimLeftFunc(content, isPySpace)
	for _, p := range reviewHarnessPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// staleMarkerRE matches a bare tool marker such as "[memory]" that an
// older conversation loop stored as an assistant tool-call turn's
// content (hermes_state._STALE_TOOL_CALL_MARKER_RE); a resume blanks it.
var staleMarkerRE = regexp.MustCompile(`^\[[A-Za-z_][A-Za-z0-9_.-]*\]$`)

// Openings of a context-compaction summary, by which Hermes tells one
// whose row does not carry _compressed_summary
// (agent/context_compressor.py SUMMARY_PREFIX, LEGACY_SUMMARY_PREFIX).
const (
	summaryPrefix       = "[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below."
	legacySummaryPrefix = "[CONTEXT SUMMARY]:"
)

// isSummaryText reports content that opens as a compaction summary.
func isSummaryText(s string) bool {
	return strings.HasPrefix(s, summaryPrefix) || strings.HasPrefix(s, legacySummaryPrefix)
}

// notConsented is the sentence every refusal of Hermes' approval gate
// carries (tools/approval.py _STOP_COMMAND, _STOP_ACTION and the
// transport's denial), and userDenied the opening of a refusal by the
// person rather than by a policy.
const (
	notConsented = "has NOT consented to this action"
	userDenied   = "User denied"
)
