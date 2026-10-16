// Portions of this file are ported from NousResearch/hermes-agent
// (https://github.com/NousResearch/hermes-agent) at 848fc0ad8b,
// Copyright (c) 2025 Nous Research, licensed under the MIT License;
// they have been modified. See THIRD_PARTY_NOTICES.md.

package hermes

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// ToolCall is one entry of an assistant row's tool_calls: OpenAI's
// {id, type, function: {name, arguments}}, and on a Responses route the
// call_id the pairing uses. Undeclared members are kept.
type ToolCall struct {
	ID       string    `json:"id"`
	CallID   string    `json:"call_id"`
	Type     string    `json:"type"`
	Function *Function `json:"function"`

	Members
}

type plainToolCall ToolCall

// UnmarshalJSON decodes the call, keeping undeclared members and
// members of another JSON type.
func (c *ToolCall) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainToolCall)(c), &c.Members)
}

// MarshalJSON writes the call back as it was read.
func (c ToolCall) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainToolCall(c), &c.Members)
}

// Function is a call's function: its name and arguments, a JSON string
// as OpenAI writes them or an object as some providers do.
type Function struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`

	Members
}

type plainFunction Function

// UnmarshalJSON decodes the function, keeping undeclared members.
func (f *Function) UnmarshalJSON(data []byte) error {
	if !isObject(data) {
		return errNotObject
	}
	return jsonline.DecodeLenient(data, (*plainFunction)(f), &f.Members)
}

// MarshalJSON writes the function back as it was read.
func (f Function) MarshalJSON() ([]byte, error) {
	return jsonline.Encode(plainFunction(f), &f.Members)
}

// PairingID is the call's ID as Hermes pairs its result:
// coalesce_tool_call_id, call_id before id, and of a bridge ID
// "<call_id>|<item id>" the part before the bar.
func (c *ToolCall) PairingID() string {
	for _, v := range []string{c.CallID, c.ID} {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if head, _, ok := strings.Cut(v, "|"); ok && strings.TrimSpace(head) != "" {
			return strings.TrimSpace(head)
		}
		return v
	}
	return ""
}

// Aliases are every spelling that names the call, as Hermes pairs a
// result with it (agent/message_sanitization.py
// tool_call_id_variants): its call_id, id and response_item_id,
// trimmed, and each part of one of the form "a|b".
func (c *ToolCall) Aliases() []string {
	var rid string
	if raw, ok := c.Extra["response_item_id"]; ok {
		_ = json.Unmarshal(raw, &rid)
	}
	return expandAliases(c.CallID, c.ID, rid)
}

// ResultAliases are the spellings a tool row's tool_call_id names a
// call by (tool_result_id_variants).
func ResultAliases(id string) []string { return expandAliases(id) }

// expandAliases is _expand_tool_id_variants.
func expandAliases(values ...string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v string) {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		add(v)
		for _, part := range strings.Split(v, "|") {
			add(strings.TrimSpace(part))
		}
	}
	return out
}

// Name is the function's name.
func (c *ToolCall) Name() string {
	if c.Function == nil {
		return ""
	}
	return c.Function.Name
}

// ArgumentsText is the call's arguments as the string a function call
// carries: a JSON string's text, any other JSON value compacted, "{}"
// for none.
func (c *ToolCall) ArgumentsText() string {
	if c.Function == nil {
		return "{}"
	}
	t := bytes.TrimSpace(c.Function.Arguments)
	if len(t) == 0 || string(t) == "null" {
		return "{}"
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s
		}
	}
	var b bytes.Buffer
	if json.Compact(&b, t) == nil {
		return b.String()
	}
	return string(t)
}

// DecodeToolCalls reads a tool_calls column: a JSON array of calls.
// An entry that is not an object is returned in bad. err is set when
// the column is not a JSON array, which Hermes reads as no calls; a
// single object is read as a list of one, as _as_list does.
func DecodeToolCalls(data []byte) (calls []*ToolCall, bad []json.RawMessage, err error) {
	t := bytes.TrimSpace(data)
	if !json.Valid(t) {
		return nil, nil, errNotArray
	}
	if t[0] == '{' {
		t = append(append([]byte("["), t...), ']')
	}
	var raws []json.RawMessage
	if json.Unmarshal(t, &raws) != nil {
		return nil, nil, errNotArray
	}
	for _, raw := range raws {
		c := &ToolCall{}
		if c.UnmarshalJSON(raw) != nil {
			bad = append(bad, raw)
			continue
		}
		calls = append(calls, c)
	}
	return calls, bad, nil
}

var errNotArray = jsonError("not a JSON array")

type jsonError string

func (e jsonError) Error() string { return string(e) }
