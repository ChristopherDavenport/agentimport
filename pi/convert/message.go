package convert

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/pi"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// The framing pi wraps a summary in when it sends it to the model, from
// its messages.ts.
const (
	compactionPrefix = "The conversation history before this point was compacted into the following summary:\n\n<summary>\n"
	compactionSuffix = "\n</summary>"
	branchPrefix     = "The following is a summary of a branch that this conversation came back from:\n\n<summary>\n"
	branchSuffix     = "</summary>"
)

// message converts a message entry by its role.
func (c *converter) message(l *pi.Line, tail string, st *pathState) (string, error) {
	m := l.Message
	if m.Role != pi.RoleSystem {
		defer func() { st.conversed = true }()
	}
	switch m.Role {
	case pi.RoleSystem:
		return c.system(l, m, tail, st)
	case pi.RoleUser:
		e := itemEntry(openresponses.UserMessage(c.contents(l, m.Content)...))
		return c.contextItems(l, tail, e)
	case pi.RoleAssistant:
		return c.assistant(l, tail, st)
	case pi.RoleToolResult:
		return c.toolResult(l, tail, st)
	case pi.RoleBashExecution:
		return c.bashExecution(l, tail)
	case pi.RoleCustom:
		e := itemEntry(openresponses.UserMessage(c.contents(l, m.Content)...))
		if !m.Display {
			hidden := false
			e.Visible = &hidden
		}
		if err := c.extensionMembers(e, l, m.CustomType, m.Details); err != nil {
			return "", err
		}
		return c.contextItems(l, tail, e)
	case pi.RoleBranchSummary, pi.RoleCompactionSummary:
		text := branchPrefix + m.Summary + branchSuffix
		if m.Role == pi.RoleCompactionSummary {
			text = compactionPrefix + m.Summary + compactionSuffix
		}
		c.addf(agentimport.Lossy, "pi/summary-message", l,
			"a persisted %s message, which pi derives and does not write, is a user message holding its framed summary", m.Role)
		return c.contextItems(l, tail, itemEntry(openresponses.UserText(text)))
	}
	return c.custom(l, tail, "pi/unknown-role", "a message of unknown role %q is a custom entry", m.Role)
}

// contextItems appends item entries made from all of line l, the first
// carrying the line's unmapped members, and records them as the line's
// contribution to context.
func (c *converter) contextItems(l *pi.Line, tail string, es ...*agentsession.ItemEntry) (string, error) {
	if len(es) > 0 {
		if err := c.lineMembers(es[0], l); err != nil {
			return "", err
		}
		if err := c.messageMembers(es[0], l, nil); err != nil {
			return "", err
		}
	}
	var err error
	for _, e := range es {
		if tail, err = c.append(tail, e, l); err != nil {
			return "", err
		}
		c.items[nodeKeyLine(l)] = append(c.items[nodeKeyLine(l)], tail)
	}
	c.mark(l, agentimport.LineMapped)
	return tail, nil
}

// mappedMessageMembers are the message members each role maps; the
// rest are kept in pi:message.
var mappedMessageMembers = map[string][]string{
	pi.RoleUser:          {"role", "content", "timestamp"},
	pi.RoleAssistant:     {"role", "content", "timestamp", "model", "responseModel", "responseId", "usage", "stopReason", "errorMessage"},
	pi.RoleToolResult:    {"role", "content", "timestamp", "toolCallId", "toolName", "isError", "details", "usage"},
	pi.RoleCustom:        {"role", "content", "timestamp", "customType", "display", "details"},
	pi.RoleBashExecution: {"role", "timestamp", "command", "output", "exitCode", "cancelled", "truncated", "fullOutputPath", "excludeFromContext"},
	pi.RoleSystem:        {"role", "content", "timestamp", "sections", "toolsAdded", "toolsRemoved"},
}

// messageMembers keeps the members of line l's message the converter
// does not map, beside those in also, in the entry's member pi:message.
func (c *converter) messageMembers(e agentsession.Entry, l *pi.Line, also map[string]json.RawMessage) error {
	m := l.Message
	if m == nil {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	for _, k := range mappedMessageMembers[m.Role] {
		delete(all, k)
	}
	for k, v := range also {
		all[k] = v
	}
	all = nonNull(all)
	if len(all) == 0 {
		return nil
	}
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "pi/message-members", l, "left out: %s", memberNames(all))
		return nil
	}
	c.addf(agentimport.Kept, "pi/message-members", l, "kept in %s:message: %s", Source, memberNames(all))
	return c.b.Member(e, "message", all)
}

// extensionMembers keeps an extension's type and details on the item
// it injected.
func (c *converter) extensionMembers(e agentsession.Entry, l *pi.Line, customType string, details json.RawMessage) error {
	if err := c.b.Member(e, "custom_type", customType); err != nil {
		return err
	}
	if len(details) > 0 && string(details) != "null" {
		if c.opts.Slim {
			c.addf(agentimport.Dropped, "pi/details", l, "an extension's details are left out")
			return nil
		}
		if err := c.b.Member(e, "details", details); err != nil {
			return err
		}
		c.addf(agentimport.Kept, "pi/details", l, "an extension's details, which pi does not send the model, are %s:details", Source)
	}
	c.addf(agentimport.Kept, "pi/custom-message", l, "an extension's message (%s) is a user message; its type is %s:custom_type", customType, Source)
	return nil
}

// contents maps user, custom or tool result content to input parts:
// text and images; a block of another type is kept as a pi.<type>
// part carrying the block.
func (c *converter) contents(l *pi.Line, content *pi.Content) openresponses.Contents {
	out := openresponses.Contents{}
	if content == nil {
		return out
	}
	if content.IsText {
		return append(out, &openresponses.InputText{Text: content.Text})
	}
	for _, b := range content.Blocks {
		if b.Invalid() {
			c.addf(agentimport.Dropped, "pi/invalid-block", l, "a content block that is not an object (%s) is left out", compactJSON(b.Raw()))
			continue
		}
		if extra := nonNull(b.Extra); len(extra) > 0 {
			c.addf(agentimport.Dropped, "pi/input-block-members", l, "members of an input block a part has no place for are left out: %s", memberNames(extra))
		}
		switch b.Type {
		case pi.BlockText:
			out = append(out, &openresponses.InputText{Text: b.Text})
			if b.TextSignature != "" {
				c.addf(agentimport.Dropped, "pi/input-text-signature", l, "a text signature on input content is left out")
			}
			continue
		case pi.BlockImage:
			out = append(out, &openresponses.InputImage{ImageURL: "data:" + b.MimeType + ";base64," + b.Data})
			continue
		}
		raw, err := json.Marshal(b)
		if err != nil {
			raw = []byte("{}")
		}
		typ := extensionType(b.Type)
		part, _ := json.Marshal(struct {
			Type  string          `json:"type"`
			Block json.RawMessage `json:"block"`
		}{typ, raw})
		out = append(out, &openresponses.UnknownContent{Type: typ, Raw: part})
		c.addf(agentimport.Kept, "pi/unknown-content", l, "a block of type %s is a %s part", b.Type, typ)
	}
	return out
}

// bashExecution converts a command the user ran: the user message pi
// renders from it, or a custom entry when pi keeps it out of context.
func (c *converter) bashExecution(l *pi.Line, tail string) (string, error) {
	m := l.Message
	if m.ExcludeFromContext {
		return c.custom(l, tail, "pi/bash-excluded", "a command the user ran and kept out of context is a custom entry")
	}
	e := itemEntry(openresponses.UserText(bashText(m)))
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "pi/bash-execution", l, "the record of a command the user ran is left out; its rendering is the message")
	} else {
		raw, err := json.Marshal(m)
		if err != nil {
			return "", err
		}
		if err := c.b.Member(e, "bash_execution", json.RawMessage(raw)); err != nil {
			return "", err
		}
		c.addf(agentimport.Kept, "pi/bash-execution", l, "a command the user ran is the user message pi renders from it; the record is %s:bash_execution", Source)
	}
	if err := c.lineMembers(e, l); err != nil {
		return "", err
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.items[nodeKeyLine(l)] = append(c.items[nodeKeyLine(l)], id)
	c.mark(l, agentimport.LineMapped)
	return id, nil
}

// bashText renders a command as pi's bashExecutionToText does.
func bashText(m *pi.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Ran `%s`\n", m.Command)
	if m.Output != "" {
		fmt.Fprintf(&b, "```\n%s\n```", m.Output)
	} else {
		b.WriteString("(no output)")
	}
	if m.Cancelled {
		b.WriteString("\n\n(command cancelled)")
	} else if m.ExitCode != nil && *m.ExitCode != 0 {
		fmt.Fprintf(&b, "\n\nCommand exited with code %d", *m.ExitCode)
	}
	if m.Truncated && m.FullOutputPath != "" {
		fmt.Fprintf(&b, "\n\n[Output truncated. Full output: %s]", m.FullOutputPath)
	}
	return b.String()
}

// customMessage converts an extension's message, which pi sends the
// model as a user message.
func (c *converter) customMessage(l *pi.Line, tail string, st *pathState) (string, error) {
	st.conversed = true
	e := itemEntry(openresponses.UserMessage(c.contents(l, l.Content)...))
	if !l.Display {
		hidden := false
		e.Visible = &hidden
	}
	if err := c.extensionMembers(e, l, l.CustomType, l.Details); err != nil {
		return "", err
	}
	return c.contextItems(l, tail, e)
}

// responsesAPIs are the APIs whose thinking signature is a serialised
// Responses reasoning item and whose text signature names the message
// item.
var responsesAPIs = map[string]bool{
	"openai-responses": true, "azure-openai-responses": true, "openai-codex-responses": true,
}

// anthropicAPIs are the APIs whose thinking signature is Anthropic's,
// which openresponses' Anthropic provider carries in encrypted_content,
// and whose redacted thinking is Anthropic's redacted_thinking block.
var anthropicAPIs = map[string]bool{"anthropic-messages": true, "bedrock-converse-stream": true}

// geminiAPIs are the APIs whose signatures are Gemini's thought
// signatures. openresponses' Gemini provider carries one on a thought
// in the reasoning item, and one on any other part in a reasoning item
// holding the signature alone, just before the part.
var geminiAPIs = map[string]bool{"google-generative-ai": true, "google-vertex": true}

// failedStops are the stop reasons of an attempt pi leaves out of every
// later request (pi-ai's transformMessages): its content may be partial.
var failedStops = map[string]bool{pi.StopError: true, pi.StopAborted: true}

// assistant converts an assistant message: a config delta when it was
// asked of another model, an item per content block in order, then the
// response entry. An attempt that failed or was aborted is written and
// then left out of context, as pi leaves it out.
func (c *converter) assistant(l *pi.Line, tail string, st *pathState) (string, error) {
	m := l.Message
	var err error
	if m.Model != "" && m.Model != st.model {
		e := &agentsession.ConfigEntry{Model: m.Model}
		if err := c.b.Member(e, "provider", m.Provider); err != nil {
			return "", err
		}
		if tail, err = c.append(tail, e, l); err != nil {
			return "", err
		}
		st.model = m.Model
	}
	respID := m.ResponseID
	if respID == "" {
		respID = Source + "-" + l.ID
		if l.ID == "" || l.Synthetic {
			respID = fmt.Sprintf("%s-line-%d", Source, l.No)
		}
	}
	var blocks []*pi.Block
	if m.Content != nil {
		blocks = m.Content.Blocks
		if m.Content.IsText && m.Content.Text != "" {
			blocks = []*pi.Block{{Type: pi.BlockText, Text: m.Content.Text}}
		}
	}
	first := true
	var written []string
	for _, b := range blocks {
		es, err := c.blockEntries(l, b, st)
		if err != nil {
			return "", err
		}
		for _, e := range es {
			e.ResponseID = respID
			if first {
				if err := c.lineMembers(e, l); err != nil {
					return "", err
				}
				first = false
			}
			if tail, err = c.append(tail, e, l); err != nil {
				return "", err
			}
			written = append(written, tail)
		}
	}
	re, err := c.responseEntry(l, respID)
	if err != nil {
		return "", err
	}
	if first {
		if err := c.lineMembers(re, l); err != nil {
			return "", err
		}
	}
	if tail, err = c.append(tail, re, l); err != nil {
		return "", err
	}
	if failedStops[m.StopReason] && len(written) > 0 {
		omit := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: written}}
		if tail, err = c.append(tail, omit, l); err != nil {
			return "", err
		}
		c.addf(agentimport.Lossy, "pi/failed-turn", l,
			"an attempt that ended %s is written and left out of context by omit, as pi leaves it out of every later request", m.StopReason)
	} else {
		c.items[nodeKeyLine(l)] = append(c.items[nodeKeyLine(l)], written...)
	}
	c.mark(l, agentimport.LineMapped)
	return tail, nil
}

// extensionType is the item or part type a pi block with no Responses
// equivalent is carried as.
func extensionType(typ string) string {
	if typ == "" {
		typ = "block"
	}
	return Source + "." + typ
}

// blockEntries maps one assistant content block to item entries: one,
// or two when a Gemini thought signature on the part rides in a
// reasoning item before it.
func (c *converter) blockEntries(l *pi.Line, b *pi.Block, st *pathState) ([]*agentsession.ItemEntry, error) {
	m := l.Message
	if b.Invalid() {
		c.addf(agentimport.Dropped, "pi/invalid-block", l, "a content block that is not an object (%s) is left out", compactJSON(b.Raw()))
		return nil, nil
	}
	var e *agentsession.ItemEntry
	var err error
	sig := ""
	switch b.Type {
	case pi.BlockText:
		e, err = c.text(l, b)
		if geminiAPIs[m.API] {
			sig = b.TextSignature
		}
	case pi.BlockThinking:
		e, err = c.thinking(l, b)
	case pi.BlockToolCall:
		e, err = c.callEntry(l, b, st)
		if geminiAPIs[m.API] {
			sig = b.ThoughtSignature
		}
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		typ := extensionType(b.Type)
		item := &openresponses.UnknownItem{Type: typ, Status: openresponses.StatusCompleted}
		item.Raw, err = json.Marshal(struct {
			Type   string               `json:"type"`
			Status openresponses.Status `json:"status"`
			Block  json.RawMessage      `json:"block"`
		}{typ, item.Status, raw})
		if err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/unknown-block", l, "a block of type %s is a %s item", b.Type, typ)
		return []*agentsession.ItemEntry{itemEntry(item)}, nil
	}
	if err != nil {
		return nil, err
	}
	if extra := nonNull(b.Extra); len(extra) > 0 {
		if err := c.b.Member(e, "block", extra); err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/block-members", l, "members of a %s block an item has no place for are %s:block: %s", b.Type, Source, memberNames(extra))
	}
	if sig == "" {
		return []*agentsession.ItemEntry{e}, nil
	}
	r := itemEntry(&openresponses.ReasoningItem{Status: openresponses.StatusCompleted, EncryptedContent: sig})
	if err := c.b.Member(r, "provider", m.Provider); err != nil {
		return nil, err
	}
	c.addf(agentimport.Gap, "agentimport/reasoning-provider", l,
		"a Gemini thought signature on a %s part is a reasoning item holding it alone, before the part, as openresponses' Gemini provider writes it; tagged %s:provider", b.Type, Source)
	return []*agentsession.ItemEntry{r, e}, nil
}

// text maps a text block to an assistant message. From a Responses API
// the text signature names the message item and its phase, as pi reads
// it back: {"v":1,"id":…,"phase":…}, or the whole signature as the id.
func (c *converter) text(l *pi.Line, b *pi.Block) (*agentsession.ItemEntry, error) {
	m := l.Message
	msg := &openresponses.Message{
		Role:    openresponses.RoleAssistant,
		Status:  openresponses.StatusCompleted,
		Content: openresponses.Contents{&openresponses.OutputText{Text: b.Text, Annotations: openresponses.Annotations{}}},
	}
	e := itemEntry(msg)
	switch {
	case b.TextSignature == "", geminiAPIs[m.API]:
	case responsesAPIs[m.API]:
		id, phase := textSignature(b.TextSignature)
		if len(id) > 64 {
			// pi sends a long id as msg_ and a hash of it, as OpenAI
			// takes 64 characters at most.
			id = "msg_" + shortHash(id)
		}
		msg.ID, msg.Phase = id, phase
	default:
		if err := c.b.Member(e, "text_signature", b.TextSignature); err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/text-signature", l, "a text signature from %s has no place on a message; it is %s:text_signature", m.API, Source)
	}
	return e, nil
}

// textSignature decodes the signature pi's Responses provider gives a
// text block as pi's parseTextSignature does: {"v":1,"id":…,"phase":…},
// and otherwise the whole signature as the item id.
func textSignature(sig string) (id string, phase openresponses.Phase) {
	if strings.HasPrefix(sig, "{") {
		var s struct {
			V     any `json:"v"`
			ID    any `json:"id"`
			Phase any `json:"phase"`
		}
		if json.Unmarshal([]byte(sig), &s) == nil {
			if v, ok := s.V.(float64); ok && v == 1 {
				if id, ok := s.ID.(string); ok {
					if p, ok := s.Phase.(string); ok {
						switch p := openresponses.Phase(p); p {
						case openresponses.PhaseCommentary, openresponses.PhaseFinalAnswer:
							phase = p
						}
					}
					return id, phase
				}
			}
		}
	}
	return sig, ""
}

// shortHash is pi-ai's shortHash: two 32-bit multiplicative hashes over
// the string's UTF-16 code units, in base 36.
func shortHash(s string) string {
	h1, h2 := uint32(0xdeadbeef), uint32(0x41c6ce57)
	for _, ch := range utf16.Encode([]rune(s)) {
		h1 = (h1 ^ uint32(ch)) * 2654435761
		h2 = (h2 ^ uint32(ch)) * 1597334677
	}
	h1 = ((h1 ^ (h1 >> 16)) * 2246822507) ^ ((h2 ^ (h2 >> 13)) * 3266489909)
	h2 = ((h2 ^ (h2 >> 16)) * 2246822507) ^ ((h1 ^ (h1 >> 13)) * 3266489909)
	return strconv.FormatUint(uint64(h2), 36) + strconv.FormatUint(uint64(h1), 36)
}

// thinking maps a thinking block to a reasoning item by the API that
// wrote it: a Responses API's own reasoning item, serialised in the
// signature, as it was; Anthropic's and Gemini's signatures in
// encrypted_content, as openresponses' providers carry them, with
// Anthropic's redacted thinking an anthropic.redacted_thinking item;
// anything else the text alone, with the signature beside it.
func (c *converter) thinking(l *pi.Line, b *pi.Block) (*agentsession.ItemEntry, error) {
	m := l.Message
	sig := b.ThinkingSignature
	if responsesAPIs[m.API] && strings.HasPrefix(strings.TrimSpace(sig), "{") {
		if item, err := openresponses.UnmarshalItem([]byte(sig)); err == nil && item.ItemType() == openresponses.ItemTypeReasoning {
			e := itemEntry(item)
			again, err := json.Marshal(item)
			if err != nil {
				return nil, err
			}
			if !sameJSON([]byte(sig), again) {
				if err := c.b.Member(e, "thinking_signature", sig); err != nil {
					return nil, err
				}
				c.addf(agentimport.Kept, "pi/reasoning-item-members", l,
					"the reasoning item pi kept holds members the item type does not; the item as pi kept it is %s:thinking_signature", Source)
			}
			return e, nil
		}
	}
	if b.Redacted && anthropicAPIs[m.API] {
		block, err := json.Marshal(map[string]string{"type": "redacted_thinking", "data": sig})
		if err != nil {
			return nil, err
		}
		item := &openresponses.UnknownItem{Type: "anthropic.redacted_thinking", Status: openresponses.StatusCompleted}
		item.Raw, err = json.Marshal(struct {
			Type   string               `json:"type"`
			Status openresponses.Status `json:"status"`
			Block  json.RawMessage      `json:"block"`
		}{item.Type, item.Status, block})
		if err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/redacted-thinking", l, "redacted thinking is an %s item, as openresponses' Anthropic provider writes it", item.Type)
		return itemEntry(item), nil
	}
	r := &openresponses.ReasoningItem{Status: openresponses.StatusCompleted}
	if b.Thinking != "" {
		r.Summary = openresponses.Contents{&openresponses.SummaryText{Text: b.Thinking}}
	}
	e := itemEntry(r)
	switch {
	case sig != "" && (anthropicAPIs[m.API] || geminiAPIs[m.API]):
		r.EncryptedContent = sig
		if err := c.b.Member(e, "provider", m.Provider); err != nil {
			return nil, err
		}
		c.addf(agentimport.Gap, "agentimport/reasoning-provider", l,
			"a thinking signature from %s in encrypted_content, tagged %s:provider", m.API, Source)
	case sig != "":
		if err := c.b.Member(e, "thinking_signature", sig); err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/thinking-signature", l,
			"a thinking signature from %s is not a Responses encrypted_content; it is %s:thinking_signature", m.API, Source)
	case b.Thinking == "":
		c.addf(agentimport.Lossy, "pi/thinking-empty", l, "a thinking block with neither text nor signature is an empty reasoning item")
	}
	return e, nil
}

// callEntry maps a toolCall block. pi joins a Responses call's call_id
// and item id as "<call_id>|<item_id>"; they are split back. An item
// id of ctc_ marks a Responses custom tool call, which pi stores as a
// tool call whose arguments hold its free-form input.
func (c *converter) callEntry(l *pi.Line, b *pi.Block, st *pathState) (*agentsession.ItemEntry, error) {
	native := b.ID
	callID, itemID, _ := strings.Cut(native, "|")
	id, renamed := c.b.Calls.Assign(callID)
	custom := strings.HasPrefix(itemID, "ctc_")
	st.addCall(native, call{id: id, name: b.Name, custom: custom})
	var e *agentsession.ItemEntry
	if custom {
		input, ok := customInput(b)
		if !ok {
			c.addf(agentimport.Lossy, "pi/custom-input", l, "the custom tool call's arguments hold no single text input; its input is the arguments as JSON")
		}
		raw, err := json.Marshal(struct {
			Type   string `json:"type"`
			ID     string `json:"id,omitempty"`
			Status string `json:"status"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Input  string `json:"input"`
		}{"openai.custom_tool_call", itemID, "completed", id, b.Name, input})
		if err != nil {
			return nil, err
		}
		e = itemEntry(&openresponses.UnknownItem{Type: "openai.custom_tool_call", ID: itemID, Status: openresponses.StatusCompleted, Raw: raw})
		c.addf(agentimport.Kept, "pi/custom-tool-call", l,
			"a Responses custom tool call is the extension item openai.custom_tool_call")
	} else {
		args := compactJSON(b.Arguments)
		if len(b.Arguments) == 0 || args == "null" {
			args = "{}"
		}
		fc := &openresponses.FunctionCall{Status: openresponses.StatusCompleted, CallID: id, Name: b.Name, Arguments: args}
		if strings.HasPrefix(itemID, "fc_") {
			// pi sends only a function call item id of the right kind.
			fc.ID = itemID
		}
		e = itemEntry(fc)
	}
	if native != id {
		if err := c.b.Member(e, "call_id", native); err != nil {
			return nil, err
		}
	}
	if renamed {
		c.addf(agentimport.Repaired, "pi/call-id-renamed", l, "call %q is recorded as %q, unique in the session", native, id)
	}
	if b.Namespace != "" {
		if err := c.b.Member(e, "namespace", b.Namespace); err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/tool-namespace", l, "the call's Responses tool namespace is %s:namespace", Source)
	}
	if b.ThoughtSignature != "" && !geminiAPIs[l.Message.API] {
		if err := c.b.Member(e, "thought_signature", b.ThoughtSignature); err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/thought-signature", l, "a thought signature from %s, not a Gemini API, is %s:thought_signature", l.Message.API, Source)
	}
	return e, nil
}

// customInput finds a custom tool call's free-form input in the
// arguments pi stores: the property its customInput names, the single
// member, or input; failing those, the arguments as JSON.
func customInput(b *pi.Block) (string, bool) {
	var args map[string]json.RawMessage
	if json.Unmarshal(b.Arguments, &args) != nil {
		return compactJSON(b.Arguments), false
	}
	prop := ""
	var ci struct {
		Property string `json:"property"`
	}
	if raw, ok := b.Extra["customInput"]; ok && json.Unmarshal(raw, &ci) == nil {
		prop = ci.Property
	}
	if prop == "" && len(args) == 1 {
		for k := range args {
			prop = k
		}
	}
	if prop == "" {
		prop = "input"
	}
	var s string
	if raw, ok := args[prop]; ok && json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	return compactJSON(b.Arguments), false
}

// responseEntry is the envelope of the call: the provider's response
// id, the model that answered, the status from the stop reason, the
// usage.
func (c *converter) responseEntry(l *pi.Line, respID string) (*agentsession.ResponseEntry, error) {
	m := l.Message
	e := &agentsession.ResponseEntry{ResponseID: respID, Model: m.Model}
	if m.ResponseModel != "" {
		e.Model = m.ResponseModel
	}
	switch m.StopReason {
	case pi.StopStop, pi.StopToolUse:
		e.Status = openresponses.ResponseStatusCompleted
	case pi.StopLength:
		e.Status = openresponses.ResponseStatusIncomplete
		e.Incomplete = &openresponses.IncompleteDetails{Reason: openresponses.IncompleteReasonMaxOutputTokens}
	case pi.StopError:
		e.Status = openresponses.ResponseStatusFailed
		e.Error = &openresponses.ErrorPayload{Code: "error", Message: m.ErrorMessage}
	case pi.StopAborted:
		e.Status = openresponses.ResponseStatusIncomplete
		c.addf(agentimport.Lossy, "pi/stop-reason", l, "stop reason aborted has no Responses status; the response is incomplete with no reason")
	case pi.StopDeferred, pi.StopPending:
		e.Status = openresponses.ResponseStatusInProgress
		c.addf(agentimport.Lossy, "pi/stop-reason", l, "stop reason %s has no Responses status; the response is in progress", m.StopReason)
	case "":
		c.addf(agentimport.Lossy, "pi/no-stop-reason", l, "the message has no stop reason; the response has no status")
	default:
		c.addf(agentimport.Lossy, "pi/stop-reason", l, "stop reason %q has no Responses status; the response has none", m.StopReason)
	}
	if m.StopReason != "" {
		if err := c.b.Member(e, "stop_reason", m.StopReason); err != nil {
			return nil, err
		}
	}
	if m.ErrorMessage != "" && m.StopReason != pi.StopError {
		if err := c.b.Member(e, "error_message", m.ErrorMessage); err != nil {
			return nil, err
		}
	}
	if m.Usage != nil {
		e.Usage = usage(m.Usage)
		if err := c.b.Member(e, "usage", m.Usage); err != nil {
			return nil, err
		}
		c.addf(agentimport.Gap, "agentimport/usage-detail", l,
			"cache writes are folded into input_tokens; the native usage and its cost are %s:usage", Source)
	}
	if err := c.messageMembers(e, l, nil); err != nil {
		return nil, err
	}
	return e, nil
}

// toolResult maps a tool result to the output of the call it answers:
// a function_call_output, or a custom_tool_call_output for a custom
// tool call.
func (c *converter) toolResult(l *pi.Line, tail string, st *pathState) (string, error) {
	m := l.Message
	cl, ok := st.calls[m.ToolCallID]
	if !ok {
		return c.custom(l, tail, "pi/orphan-result", "a tool result for a call not on its path is a custom entry")
	}
	if st.answered[cl.id] {
		return c.custom(l, tail, "pi/duplicate-result", "a second result for a call answered on its path is a custom entry")
	}
	st.answer(cl.id)
	var data openresponses.FunctionCallOutputData
	if m.Content != nil {
		switch {
		case m.Content.IsText:
			data.Text = m.Content.Text
		case len(m.Content.Blocks) == 1 && !m.Content.Blocks[0].Invalid() && m.Content.Blocks[0].Type == pi.BlockText:
			data.Text = m.Content.Blocks[0].Text
		case len(m.Content.Blocks) > 0:
			data.Parts = c.contents(l, m.Content)
		}
	}
	var e *agentsession.ItemEntry
	if cl.custom {
		raw, err := json.Marshal(struct {
			Type   string                               `json:"type"`
			CallID string                               `json:"call_id"`
			Output openresponses.FunctionCallOutputData `json:"output"`
		}{"openai.custom_tool_call_output", cl.id, data})
		if err != nil {
			return "", err
		}
		e = itemEntry(&openresponses.UnknownItem{Type: "openai.custom_tool_call_output", Raw: raw})
		c.addf(agentimport.Kept, "pi/custom-tool-call", l,
			"the output of a Responses custom tool call is the extension item openai.custom_tool_call_output")
	} else {
		e = itemEntry(&openresponses.FunctionCallOutput{CallID: cl.id, Output: data})
	}
	if m.ToolName != "" && m.ToolName != cl.name {
		if err := c.b.Member(e, "tool_name", m.ToolName); err != nil {
			return "", err
		}
		c.addf(agentimport.Kept, "pi/tool-name", l, "the result names tool %q and its call %q; the result's name is %s:tool_name", m.ToolName, cl.name, Source)
	}
	if m.IsError {
		if err := c.b.Member(e, "is_error", true); err != nil {
			return "", err
		}
		c.addf(agentimport.Gap, "agentimport/tool-error", l, "isError is the member %s:is_error; function_call_output has no error flag", Source)
	}
	if len(m.Details) > 0 && string(m.Details) != "null" {
		if c.opts.Slim {
			c.addf(agentimport.Dropped, "pi/details", l, "a tool's details are left out")
		} else {
			if err := c.b.Member(e, "details", m.Details); err != nil {
				return "", err
			}
			c.addf(agentimport.Kept, "pi/details", l, "a tool's details, which pi does not send the model, are %s:details", Source)
		}
	}
	if m.Usage != nil {
		if err := c.b.Member(e, "usage", m.Usage); err != nil {
			return "", err
		}
		c.addf(agentimport.Kept, "pi/tool-usage", l, "usage of model work the tool did is %s:usage; the session format counts no paid call for it", Source)
	}
	return c.contextItems(l, tail, e)
}

// sameJSON reports whether a and b are the same JSON value.
func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
