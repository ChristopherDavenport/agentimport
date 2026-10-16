package convert

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// envelope are the members every event carries, mapped to the entry's
// envelope or its id member.
var envelope = []string{"kind", "id", "timestamp", "source", "parent_id"}

// rest returns the members of v, an object this package's types encode,
// other than the envelope and mapped: what the conversion has no place
// for.
func rest(v any, mapped ...string) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	for _, k := range envelope {
		delete(all, k)
	}
	for _, k := range mapped {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

// keep sets members, the members of event i the conversion has no
// place for, as the member name on e, or drops them under Slim, and
// reports which.
func (c *conv) keep(i int, e agentsession.Entry, name string, members map[string]json.RawMessage) error {
	if len(members) == 0 {
		return nil
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if c.slim {
		c.at(agentimport.Dropped, "openhands/event-members", i, "members %s are left out", strings.Join(keys, ", "))
		return nil
	}
	raw, err := json.Marshal(members)
	if err != nil {
		return err
	}
	if raw, err = c.clean(i, raw); err != nil {
		return err
	}
	c.at(agentimport.Kept, "openhands/event-members", i, "members %s are kept in %s:%s", strings.Join(keys, ", "), Source, name)
	return c.b.Member(e, name, json.RawMessage(raw))
}

// partRest is what a content part carries beyond its type and payload:
// a cache_prompt flag that is set, OpenHands' meta, members of other
// releases.
func partRest(p *openhands.Content) map[string]json.RawMessage {
	r, err := rest(p, "type", "text", "image_urls", "cache_prompt")
	if err != nil {
		return nil
	}
	if p.CachePrompt {
		if r == nil {
			r = map[string]json.RawMessage{}
		}
		r["cache_prompt"] = json.RawMessage("true")
	}
	return r
}

// inputParts maps content to input parts: a text part to input_text,
// each URL of an image part to input_image. A part of another type, or
// one that is not an object, is left out and reported; the members a
// part carries beyond its payload are returned by position.
func (c *conv) inputParts(i int, cs openhands.Contents, what string) (openresponses.Contents, map[string]json.RawMessage) {
	var out openresponses.Contents
	var members map[string]json.RawMessage
	if cs.IsText {
		return openresponses.Contents{&openresponses.InputText{Text: cs.Text}}, nil
	}
	for k, p := range cs.Items {
		if p.Err != nil {
			c.at(agentimport.Dropped, "openhands/bad-content", i, "a %s part that is not an object is left out", what)
			continue
		}
		switch p.Type {
		case openhands.ContentText:
			out = append(out, &openresponses.InputText{Text: p.Text})
		case openhands.ContentImage:
			for _, u := range p.ImageURLs {
				out = append(out, &openresponses.InputImage{ImageURL: u})
			}
		default:
			c.at(agentimport.Dropped, "openhands/bad-content", i, "a %s part of type %q is left out", what, p.Type)
			continue
		}
		if r := partRest(p); r != nil {
			raw, err := json.Marshal(r)
			if err == nil {
				if members == nil {
					members = map[string]json.RawMessage{}
				}
				members[fmt.Sprint(k)] = raw
			}
		}
	}
	return out, members
}

// outputParts maps assistant content to output_text parts.
func (c *conv) outputParts(i int, cs openhands.Contents) (openresponses.Contents, map[string]json.RawMessage) {
	if cs.IsText {
		if cs.Text == "" {
			return nil, nil
		}
		return openresponses.Contents{&openresponses.OutputText{Text: cs.Text}}, nil
	}
	var out openresponses.Contents
	var members map[string]json.RawMessage
	for k, p := range cs.Items {
		switch {
		case p.Err != nil:
			c.at(agentimport.Dropped, "openhands/bad-content", i, "an assistant part that is not an object is left out")
			continue
		case p.Type != openhands.ContentText:
			c.at(agentimport.Dropped, "openhands/bad-content", i, "an assistant part of type %q is left out; OpenHands sends assistant text alone", p.Type)
			continue
		}
		out = append(out, &openresponses.OutputText{Text: p.Text})
		if r := partRest(p); r != nil {
			if raw, err := json.Marshal(r); err == nil {
				if members == nil {
					members = map[string]json.RawMessage{}
				}
				members[fmt.Sprint(k)] = raw
			}
		}
	}
	return out, members
}

// reasoning maps a response's reasoning to items: Anthropic thinking
// blocks as providers/anthropic decodes them (the text a summary, the
// signature in encrypted_content, a redacted block the
// anthropic.redacted_thinking item), a Responses reasoning item as it
// is, and reasoning_content alone as a summary. With thinking blocks
// or a reasoning item, reasoning_content is litellm's copy of their
// text, and is returned to keep beside them when it is not.
func (c *conv) reasoning(i int, blocks []*openhands.ThinkingBlock, item *openhands.ReasoningItem, text string) (items []openresponses.Item, providers []string, extra string) {
	for _, b := range blocks {
		if b == nil {
			continue
		}
		switch b.Type {
		case openhands.BlockThinking:
			r := &openresponses.ReasoningItem{Summary: openresponses.Contents{}, EncryptedContent: b.Signature}
			if b.Thinking != "" {
				r.Summary = append(r.Summary, &openresponses.SummaryText{Text: b.Thinking})
			}
			items = append(items, r)
			providers = append(providers, provider(b.Signature, "anthropic"))
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				continue
			}
			if raw, err = c.clean(i, raw); err != nil {
				continue
			}
			typ := b.Type
			if typ == "" {
				typ = "unknown"
			}
			ext, err := anthropicItem(typ, raw)
			if err != nil {
				continue
			}
			items = append(items, ext)
			providers = append(providers, "")
			c.at(agentimport.Kept, "openhands/extension-item", i, "a %s block is the extension item %s", typ, ext.Type)
		}
	}
	if item != nil {
		r := &openresponses.ReasoningItem{ID: item.ID, Status: openresponses.Status(item.Status), Summary: openresponses.Contents{}, EncryptedContent: item.EncryptedContent}
		for _, s := range item.Summary {
			r.Summary = append(r.Summary, &openresponses.SummaryText{Text: s})
		}
		for _, s := range item.Content {
			r.Content = append(r.Content, &openresponses.ReasoningText{Text: s})
		}
		items = append(items, r)
		providers = append(providers, provider(item.EncryptedContent, "openai"))
	}
	switch {
	case text == "":
	case len(items) == 0:
		items = append(items, &openresponses.ReasoningItem{Summary: openresponses.Contents{&openresponses.SummaryText{Text: text}}})
		providers = append(providers, "")
	case text != thinkingText(blocks):
		extra = text
	}
	return items, providers, extra
}

func provider(enc, name string) string {
	if enc == "" {
		return ""
	}
	return name
}

// thinkingText is the thinking blocks' text joined as litellm joins it
// into reasoning_content.
func thinkingText(blocks []*openhands.ThinkingBlock) string {
	var parts []string
	for _, b := range blocks {
		if b != nil && b.Type == openhands.BlockThinking {
			parts = append(parts, b.Thinking)
		}
	}
	return strings.Join(parts, "")
}

// anthropicItem is the item openresponses' Anthropic provider emits for
// a block the specification has no item for.
func anthropicItem(typ string, block json.RawMessage) (*openresponses.UnknownItem, error) {
	item := &openresponses.UnknownItem{Type: "anthropic." + typ, Status: openresponses.StatusCompleted}
	raw, err := json.Marshal(struct {
		Type   string               `json:"type"`
		Status openresponses.Status `json:"status"`
		Block  json.RawMessage      `json:"block"`
	}{item.Type, item.Status, block})
	item.Raw = raw
	return item, err
}

// tools maps a system prompt's tools: SDK 1.0 to 1.6 wrote chat tool
// params, the schema the model saw; 1.7 and later write ToolDefinition
// dumps, which hold a name and a description but no parameter schema,
// except an MCP tool's input schema. legacy says which shape was seen.
func (c *conv) tools(i int, raws []json.RawMessage) (out openresponses.Tools, legacy, dumps int) {
	unrecorded := 0
	for _, raw := range raws {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			c.at(agentimport.Dropped, "openhands/bad-tool", i, "a tool that is not an object is left out")
			continue
		}
		var typ, kind string
		_ = json.Unmarshal(obj["type"], &typ)
		_ = json.Unmarshal(obj["kind"], &kind)
		switch {
		case typ == "function" && obj["function"] != nil:
			var f struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Parameters  json.RawMessage `json:"parameters"`
				Strict      *bool           `json:"strict"`
			}
			if json.Unmarshal(obj["function"], &f) != nil || f.Name == "" {
				c.at(agentimport.Dropped, "openhands/bad-tool", i, "a chat tool with no name is left out")
				continue
			}
			params := f.Parameters
			if params != nil {
				var err error
				if params, err = c.clean(i, params); err != nil {
					params = nil
				}
			}
			out = append(out, &openresponses.FunctionTool{Name: f.Name, Description: f.Description, Parameters: params, Strict: f.Strict})
			legacy++
		case kind != "":
			var desc string
			_ = json.Unmarshal(obj["description"], &desc)
			name := toolName(kind)
			var params json.RawMessage
			var mcp map[string]json.RawMessage
			if json.Unmarshal(obj["mcp_tool"], &mcp) == nil {
				var n string
				if json.Unmarshal(mcp["name"], &n) == nil && n != "" {
					name = n
				}
				if s := mcp["inputSchema"]; !openhands.IsNull(s) {
					if cl, err := c.clean(i, s); err == nil {
						params = cl
					}
				}
			}
			if params == nil {
				unrecorded++
			}
			out = append(out, &openresponses.FunctionTool{Name: name, Description: desc, Parameters: params})
			dumps++
		default:
			c.at(agentimport.Dropped, "openhands/bad-tool", i, "a tool that is neither chat tool params nor a tool definition is left out")
		}
	}
	if unrecorded > 0 {
		c.at(agentimport.Lossy, "openhands/tools-unrecorded", i, "%d tools are recorded without the parameter schema the model was sent, as OpenHands records them from 1.7; the config carries their names and descriptions, and the definitions are kept in %s:event", unrecorded, Source)
	}
	return out, legacy, dumps
}

var (
	camelA = regexp.MustCompile(`(.)([A-Z][a-z]+)`)
	camelB = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// toolName is the name a ToolDefinition's class gives the tool: its
// kind in snake case without the _tool suffix, as the SDK derives it,
// with the classes that name themselves.
func toolName(kind string) string {
	switch kind {
	case "ClassifyAndSwitchLLMTool":
		return "route_task_to_model"
	case "VisionInspectTool":
		return "inspect_image_with_vision"
	}
	s := camelA.ReplaceAllString(kind, "${1}_${2}")
	s = strings.ToLower(camelB.ReplaceAllString(s, "${1}_${2}"))
	return strings.TrimSuffix(s, "_tool")
}

// textOf is a message's text parts joined, as a sub-agent's prompt is
// compared.
func textOf(cs openhands.Contents) string {
	if cs.IsText {
		return cs.Text
	}
	var b strings.Builder
	for _, p := range cs.Items {
		if p.Err == nil && p.Type == openhands.ContentText {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}
