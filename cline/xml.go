package cline

import (
	"sort"
	"strings"
)

// ToolNames are the tools classic Cline and Roo let a model call in
// the XML style, writing <tool><param>value</param></tool> in its text.
var ToolNames = []string{
	// Both.
	"execute_command", "read_file", "write_to_file", "search_files",
	"list_files", "list_code_definition_names", "browser_action",
	"use_mcp_tool", "access_mcp_resource", "ask_followup_question",
	"attempt_completion", "new_task",
	// Classic Cline.
	"replace_in_file", "plan_mode_respond", "act_mode_respond",
	"load_mcp_documentation", "condense", "summarize_task", "report_bug",
	"new_rule", "web_fetch", "web_search", "use_subagents", "focus_chain",
	// Roo.
	"apply_diff", "insert_content", "search_and_replace", "codebase_search",
	"switch_mode", "update_todo_list", "fetch_instructions",
	"run_slash_command", "generate_image", "search_replace", "edit_file",
	"apply_patch",
}

var toolSet = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range ToolNames {
		m[n] = true
	}
	return m
}()

// longParams are the parameters whose value may hold its own closing
// tag, which Cline's parser reads to the last one before the tool's.
var longParams = map[string]bool{"content": true, "diff": true}

// XMLToolCall is one tool call a model wrote as XML in its text.
type XMLToolCall struct {
	Name   string            `json:"name"`
	Params map[string]string `json:"params,omitempty"`
	// Partial says the call has no closing tag: the text ends inside
	// it.
	Partial bool `json:"partial,omitempty"`
}

// ParseXMLTools reads the tool calls a text holds in the XML style, as
// Cline's parser reads them: a known tool's opening tag, its
// parameters as child elements, its closing tag. Text outside a call is
// left alone. It reads what it can and never fails.
func ParseXMLTools(text string) []XMLToolCall {
	var out []XMLToolCall
	for i := 0; i < len(text); {
		lt := strings.IndexByte(text[i:], '<')
		if lt < 0 {
			break
		}
		lt += i
		name, end := openTag(text, lt)
		if name == "" || !toolSet[name] {
			i = lt + 1
			continue
		}
		closing := "</" + name + ">"
		body := text[end:]
		call := XMLToolCall{Name: name}
		if c := strings.Index(body, closing); c >= 0 {
			body = body[:c]
			i = end + c + len(closing)
		} else {
			call.Partial = true
			i = len(text)
		}
		call.Params = params(body)
		out = append(out, call)
	}
	return out
}

// params reads a call's child elements.
func params(body string) map[string]string {
	var out map[string]string
	for i := 0; i < len(body); {
		lt := strings.IndexByte(body[i:], '<')
		if lt < 0 {
			break
		}
		lt += i
		name, end := openTag(body, lt)
		if name == "" {
			i = lt + 1
			continue
		}
		closing := "</" + name + ">"
		var c int
		if longParams[name] {
			c = strings.LastIndex(body[end:], closing)
		} else {
			c = strings.Index(body[end:], closing)
		}
		if out == nil {
			out = map[string]string{}
		}
		if c < 0 {
			out[name] = strings.TrimSpace(body[end:])
			break
		}
		out[name] = strings.TrimSpace(body[end : end+c])
		i = end + c + len(closing)
	}
	return out
}

// openTag reads an opening tag <name> at text[lt], returning the name
// and the index after the tag; "" when there is none.
func openTag(text string, lt int) (string, int) {
	j := lt + 1
	for j < len(text) && (text[j] >= 'a' && text[j] <= 'z' || text[j] == '_' || text[j] >= '0' && text[j] <= '9' && j > lt+1) {
		j++
	}
	if j == lt+1 || j >= len(text) || text[j] != '>' {
		return "", 0
	}
	return text[lt+1 : j], j + 1
}

// Names lists the distinct tool names of calls, sorted.
func Names(calls []XMLToolCall) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range calls {
		if !seen[c.Name] {
			seen[c.Name] = true
			out = append(out, c.Name)
		}
	}
	sort.Strings(out)
	return out
}
