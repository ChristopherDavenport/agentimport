// Portions of this file are ported from openai/codex
// (https://github.com/openai/codex) at 7ae83199, Copyright 2025 OpenAI,
// licensed under the Apache License 2.0; they have been modified. See
// THIRD_PARTY_NOTICES.md.

package convert

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/ChristopherDavenport/agentimport/codex"
	"github.com/ChristopherDavenport/openresponses"
)

// This file ports how Codex tells the messages it writes itself from a
// person's, which decides where a rollback cuts and which messages a
// rebuilt compaction window keeps. The source is openai/codex at
// 7ae831991792744434027c431dfbc7e81dd5c12c:
//
//   - core/src/event_mapping.rs:40-95: CONTEXTUAL_DEVELOPER_PREFIXES,
//     is_contextual_user_message_content (any part matches),
//     is_contextual_dev_message_content (any part matches);
//   - core/src/context/contextual_user_message.rs:23-38, 92-98:
//     CONTEXTUAL_USER_FRAGMENT_MATCHERS and is_contextual_user_fragment;
//   - context-fragments/src/fragment.rs:150-164: matches_marked_text;
//   - core/src/context_manager/history.rs:1372-1391:
//     is_user_turn_boundary;
//   - protocol/src/protocol.rs:1029-1036:
//     InterAgentCommunication::from_message_content.

// summaryPrefix opens the user message Codex writes for a compaction's
// summary (prompts/templates/compact/summary_prefix.md). Such a message
// is no user turn.
const summaryPrefix = "Another language model started to solve this problem and produced a summary of its thinking process. You also have access to the state of the tools that were used by that language model. Use this to build on the work that has already been done and avoid duplicating work. Here is the summary produced by the other language model, use the information in this summary to assist with your own analysis:"

// contextualDeveloperPrefixes are CONTEXTUAL_DEVELOPER_PREFIXES, the
// constants they name written out (protocol/src/protocol.rs:126-145,
// core/src/context/approved_command_prefix_saved.rs:4).
var contextualDeveloperPrefixes = []string{
	"<permissions instructions>", "Approved command prefix saved:", "<model_switch>",
	"<managed_developer_instructions>", "<persistent_mode>", "<apps_instructions>",
	"<collaboration_mode>", "<multi_agent_role>", "<multi_agent_mode>",
	"<environments_instructions>", "<git_attribution>", "<plugins_instructions>",
	"<realtime_conversation>", "<skills_instructions>", "<tools>", "<personality_spec>",
	"<token_budget>", "<context_window>", "<context_window_guidance>", "<rollout_budget>",
}

// markedFragments are the user fragments matched by their markers, each
// fragment's type_markers.
var markedFragments = [][2]string{
	{"# AGENTS.md instructions", "</INSTRUCTIONS>"},                               // UserInstructions
	{"<environment_context>", "</environment_context>"},                           // EnvironmentsState
	{"<agent_message_board_notification>", "</agent_message_board_notification>"}, // AgentMessageBoardNotification
	{"<skill>", "</skill>"},                                                       // skills extension
	{"<user_shell_command>", "</user_shell_command>"},                             // UserShellCommand
	{"<turn_aborted>", "</turn_aborted>"},                                         // TurnAborted
	{"<subagent_notification>", "</subagent_notification>"},                       // SubagentNotification
	{"<recommended_plugins>", "</recommended_plugins>"},                           // RecommendedPluginsInstructions
}

// hasFoldPrefix is a case-insensitive prefix test on bytes, as Rust's
// get(..n) with eq_ignore_ascii_case.
func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func hasFoldSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

// matchesMarked is matches_marked_text.
func matchesMarked(start, end, text string) bool {
	if start == "" || end == "" {
		return false
	}
	t := strings.TrimLeft(text, " \t\n\r\v\f")
	if !hasFoldPrefix(t, start) {
		return false
	}
	return hasFoldSuffix(strings.TrimRight(t, " \t\n\r\v\f"), end)
}

var (
	internalContext = regexp.MustCompile(`^<codex_internal_context source="([a-z][a-z0-9_]*)">(?s:.*)</codex_internal_context>$`)
	hookPrompt      = regexp.MustCompile(`^<hook_prompt hook_run_id="([^"]*)"\s*>(?s:.*)</hook_prompt>$`)
)

// contextualUserText is is_contextual_user_fragment for one text part.
func contextualUserText(text string) bool {
	for _, m := range markedFragments {
		if matchesMarked(m[0], m[1], text) {
			return true
		}
	}
	trimmed := strings.TrimSpace(text)
	// AdditionalContextUserFragment: <external_KEY>…</external_KEY>.
	if rest, ok := strings.CutPrefix(trimmed, "<external_"); ok {
		if key, body, ok := strings.Cut(rest, ">"); ok && strings.HasSuffix(body, "</external_"+key+">") {
			return true
		}
	}
	// InternalModelContextFragment, and its legacy goal context.
	if strings.HasPrefix(trimmed, "<goal_context>") && strings.HasSuffix(trimmed, "</goal_context>") {
		return true
	}
	if internalContext.MatchString(trimmed) {
		return true
	}
	// The legacy warnings matched by their opening words.
	if strings.HasPrefix(trimmed, "Warning: The maximum number of unified exec processes you can keep open is") ||
		strings.HasPrefix(trimmed, "Warning: Your account was flagged for potentially high-risk cyber activity") ||
		strings.HasPrefix(trimmed, "Warning: apply_patch was requested via ") && strings.HasSuffix(trimmed, "Use the apply_patch tool instead of exec_command.") {
		return true
	}
	// A hook's prompt, parse_hook_prompt_fragment: a hook_prompt element
	// with a run id.
	if m := hookPrompt.FindStringSubmatch(trimmed); m != nil && strings.TrimSpace(m[1]) != "" {
		return true
	}
	return false
}

// inputTexts returns the text of a message's input_text parts.
func inputTexts(m *openresponses.Message) []string {
	var out []string
	for _, p := range m.Content {
		if t, ok := p.(*openresponses.InputText); ok {
			out = append(out, t.Text)
		}
	}
	return out
}

// contextualUser is is_contextual_user_message_content: any input_text
// part is a fragment Codex wrote.
func contextualUser(m *openresponses.Message) bool {
	for _, t := range inputTexts(m) {
		if contextualUserText(t) {
			return true
		}
	}
	return false
}

// contextualDeveloper is is_contextual_dev_message_content: any
// input_text part opens with one of the prefixes, after leading space,
// in any case.
func contextualDeveloper(m *openresponses.Message) bool {
	for _, t := range inputTexts(m) {
		t = strings.TrimLeft(t, " \t\n\r\v\f")
		for _, p := range contextualDeveloperPrefixes {
			if hasFoldPrefix(t, p) {
				return true
			}
		}
	}
	return false
}

// isSummary reports whether any input_text part is a compaction's
// summary.
func isSummary(m *openresponses.Message) bool {
	for _, t := range inputTexts(m) {
		if strings.HasPrefix(t, summaryPrefix+"\n") {
			return true
		}
	}
	return false
}

// interAgentText reports whether a message's content is an
// InterAgentCommunication written as JSON: one input_text or output_text
// part that decodes as one.
func interAgentText(parts []codex.ContentItem) bool {
	if len(parts) != 1 || parts[0].Type != "input_text" && parts[0].Type != "output_text" {
		return false
	}
	var v struct {
		Author      *string `json:"author"`
		Recipient   *string `json:"recipient"`
		Content     *string `json:"content"`
		TriggerTurn *bool   `json:"trigger_turn"`
	}
	return json.Unmarshal([]byte(parts[0].Text), &v) == nil &&
		v.Author != nil && v.Recipient != nil && v.Content != nil && v.TriggerTurn != nil
}

// userTurn is is_user_turn_boundary over an item in context: a message
// from another agent, a person's message, or an assistant message that
// carries another agent's instruction.
func userTurn(item openresponses.Item) bool {
	switch v := item.(type) {
	case *openresponses.UnknownItem:
		return v.Type == extensionType(codex.ItemAgentMessage)
	case *openresponses.Message:
		switch v.Role {
		case openresponses.RoleUser:
			return !contextualUser(v) && !isSummary(v)
		case openresponses.RoleAssistant:
			var parts []codex.ContentItem
			for _, p := range v.Content {
				switch p := p.(type) {
				case *openresponses.InputText:
					parts = append(parts, codex.ContentItem{Type: "input_text", Text: p.Text})
				case *openresponses.OutputText:
					parts = append(parts, codex.ContentItem{Type: "output_text", Text: p.Text})
				default:
					parts = append(parts, codex.ContentItem{Type: p.ContentType()})
				}
			}
			return interAgentText(parts)
		}
	}
	return false
}

// boundaryItem is userTurn for a native item.
func boundaryItem(it *codex.Item) bool {
	switch it.Type {
	case codex.ItemAgentMessage:
		return true
	case codex.ItemMessage:
		switch it.Role {
		case "user":
			var m openresponses.Message
			if json.Unmarshal(it.Content, &m.Content) != nil {
				return true
			}
			m.Role = openresponses.RoleUser
			return !contextualUser(&m) && !isSummary(&m)
		case "assistant":
			return interAgentText(it.Parts())
		}
	}
	return false
}
