package convert

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

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
	pi.RoleSystem:        {"role", "content", "timestamp", "sections", "toolsAdded", "toolsRemoved", "replace"},
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
		typ := Source + "." + b.Type
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

// signatureAPIs are the APIs whose thinking signature openresponses'
// providers carry in encrypted_content: Anthropic's signature, and
// Gemini's thought signature.
var signatureAPIs = map[string]bool{
	"anthropic-messages": true, "bedrock-converse-stream": true,
	"google-generative-ai": true, "google-vertex": true,
}

// anthropicAPIs are the APIs whose redacted thinking is Anthropic's
// redacted_thinking block.
var anthropicAPIs = map[string]bool{"anthropic-messages": true, "bedrock-converse-stream": true}

// assistant converts an assistant message: a config delta when it was
// asked of another model, an item per content block in order, then the
// response entry.
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
	for _, b := range blocks {
		e, err := c.blockEntry(l, b, st)
		if err != nil {
			return "", err
		}
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
		c.items[nodeKeyLine(l)] = append(c.items[nodeKeyLine(l)], tail)
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
	c.mark(l, agentimport.LineMapped)
	return tail, nil
}

// blockEntry maps one assistant content block to an item entry.
func (c *converter) blockEntry(l *pi.Line, b *pi.Block, st *pathState) (*agentsession.ItemEntry, error) {
	m := l.Message
	switch b.Type {
	case pi.BlockText:
		msg := &openresponses.Message{
			Role:    openresponses.RoleAssistant,
			Status:  openresponses.StatusCompleted,
			Content: openresponses.Contents{&openresponses.OutputText{Text: b.Text, Annotations: openresponses.Annotations{}}},
		}
		e := itemEntry(msg)
		if b.TextSignature != "" {
			if id, phase, ok := textSignature(b.TextSignature); ok && responsesAPIs[m.API] {
				msg.ID, msg.Phase = id, phase
			} else {
				if err := c.b.Member(e, "text_signature", b.TextSignature); err != nil {
					return nil, err
				}
				c.addf(agentimport.Kept, "pi/text-signature", l, "a text signature from %s has no place on a message; it is %s:text_signature", m.API, Source)
			}
		}
		return e, nil
	case pi.BlockThinking:
		return c.thinking(l, b)
	case pi.BlockToolCall:
		return c.callEntry(l, b, st)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	typ := Source + "." + b.Type
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
	return itemEntry(item), nil
}

// textSignature decodes the signature pi's Responses provider gives a
// text block: {"v":1,"id":…,"phase":…}, or the bare item id of older
// files.
func textSignature(sig string) (id string, phase openresponses.Phase, ok bool) {
	if !strings.HasPrefix(sig, "{") {
		return sig, "", true
	}
	var s struct {
		V     int    `json:"v"`
		ID    string `json:"id"`
		Phase string `json:"phase"`
	}
	if json.Unmarshal([]byte(sig), &s) != nil || s.V != 1 || s.ID == "" {
		return "", "", false
	}
	switch p := openresponses.Phase(s.Phase); p {
	case openresponses.PhaseCommentary, openresponses.PhaseFinalAnswer:
		phase = p
	}
	return s.ID, phase, true
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
	case sig != "" && signatureAPIs[m.API]:
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
// and item id as "<call_id>|<item_id>"; they are split back.
func (c *converter) callEntry(l *pi.Line, b *pi.Block, st *pathState) (*agentsession.ItemEntry, error) {
	native := b.ID
	callID, itemID, _ := strings.Cut(native, "|")
	id, renamed := c.b.Calls.Assign(callID)
	st.addCall(native, id)
	args := compactJSON(b.Arguments)
	if len(b.Arguments) == 0 || args == "null" {
		args = "{}"
	}
	e := itemEntry(&openresponses.FunctionCall{
		ID: itemID, Status: openresponses.StatusCompleted, CallID: id, Name: b.Name, Arguments: args,
	})
	if renamed {
		if err := c.b.Member(e, "call_id", native); err != nil {
			return nil, err
		}
		c.addf(agentimport.Repaired, "pi/call-id-renamed", l, "call %q is recorded as %q, unique in the session", native, id)
	}
	if b.Namespace != "" {
		if err := c.b.Member(e, "namespace", b.Namespace); err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/tool-namespace", l, "the call's Responses tool namespace is %s:namespace", Source)
	}
	if b.ThoughtSignature != "" {
		if err := c.b.Member(e, "thought_signature", b.ThoughtSignature); err != nil {
			return nil, err
		}
		c.addf(agentimport.Gap, "agentimport/call-signature", l, "a thought signature on a tool call is %s:thought_signature; function_call has no member for it", Source)
	}
	if extra := nonNull(b.Extra); len(extra) > 0 {
		if err := c.b.Member(e, "block", extra); err != nil {
			return nil, err
		}
		c.addf(agentimport.Kept, "pi/block-members", l, "kept in %s:block: %s", Source, memberNames(extra))
	}
	return e, nil
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

// toolResult maps a tool result to the output of the call it answers.
func (c *converter) toolResult(l *pi.Line, tail string, st *pathState) (string, error) {
	m := l.Message
	callID, ok := st.calls[m.ToolCallID]
	if !ok {
		return c.custom(l, tail, "pi/orphan-result", "a tool result for a call not on its path is a custom entry")
	}
	if st.answered[callID] {
		return c.custom(l, tail, "pi/duplicate-result", "a second result for a call answered on its path is a custom entry")
	}
	st.answer(callID)
	out := &openresponses.FunctionCallOutput{CallID: callID}
	if m.Content != nil {
		if m.Content.IsText {
			out.Output.Text = m.Content.Text
		} else if len(m.Content.Blocks) == 1 && m.Content.Blocks[0].Type == pi.BlockText {
			out.Output.Text = m.Content.Blocks[0].Text
		} else if len(m.Content.Blocks) > 0 {
			out.Output.Parts = c.contents(l, m.Content)
		}
	}
	e := itemEntry(out)
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
