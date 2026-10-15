package convert

import (
	"encoding/json"
	"fmt"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// response converts one API response: a config delta when the model
// changed, an item per content block in block order, the response
// entry, then each call's subsession link and output in call order, so
// the results of parallel calls are on the path.
func (c *converter) response(r *claudecode.Response, tail string, st *pathState) (string, error) {
	if allSynthetic(r) {
		return c.syntheticResponse(r, tail)
	}
	var err error
	if model := requestModel(r); model != "" && model != st.model {
		e := &agentsession.ConfigEntry{Model: model}
		e.Timestamp = r.Lines[0].Time()
		if tail, err = c.append(tail, e, nil); err != nil {
			return "", err
		}
		st.model = model
	}
	for _, l := range r.Lines {
		if l.IsSynthetic() {
			// A client-written line inside a model response: its text
			// was never model output.
			if tail, err = c.custom(l, tail, "claudecode/synthetic-line", "a line the CLI wrote inside a response is a custom entry"); err != nil {
				return "", err
			}
			continue
		}
		first := true
		for _, b := range l.Message.Content.Blocks {
			e, err := c.blockEntry(l, b)
			if err != nil {
				return "", err
			}
			e.ResponseID = r.MessageID
			if first {
				if err := c.lineMembers(e, l); err != nil {
					return "", err
				}
				first = false
			}
			if tail, err = c.append(tail, e, l); err != nil {
				return "", err
			}
		}
		if first {
			c.addf(agentimport.Dropped, "claudecode/empty-line", l, "an assistant line with no content blocks")
			c.mark(l, agentimport.LineDropped)
		} else {
			c.mark(l, agentimport.LineMapped)
		}
		c.b.Bind(l.UUID, tail)
	}
	re, err := c.responseEntry(r)
	if err != nil {
		return "", err
	}
	if tail, err = c.append(tail, re, nil); err != nil {
		return "", err
	}
	for _, l := range r.Lines {
		if _, ok := c.b.ID(l.UUID); !ok {
			c.b.Bind(l.UUID, tail)
		}
	}
	// The lines' uuids name their items; the last line's names the
	// response too, since a child of it continues after the outputs.
	done := map[*claudecode.Line]bool{}
	for _, call := range r.Calls {
		if a := c.agents[call.Use.ID]; a != nil && !c.cv.done[a.ID] && !c.linked(a) {
			callID := c.calls[call.Use.ID]
			child := agentsession.SubsessionID(c.t.id, callID)
			link := agentsession.NewSubsessionLink(child, callID)
			link.Timestamp = r.Last().Time()
			if call.Result != nil {
				link.Timestamp = call.Result.Time()
			}
			if tail, err = c.append(tail, link, nil); err != nil {
				return "", err
			}
			c.spawned = append(c.spawned, spawn{agent: a, id: child, callID: callID})
			if status := launchStatus(call.Result); status != "" {
				c.addf(agentimport.Gap, "agentimport/async-subsession", call.Result,
					"the call's output says the agent was started (%s); its result reaches the parent later, as a message, not as the output", status)
			}
		}
		if call.Result == nil {
			continue
		}
		e, err := c.output(call.Result, call.ResultBlock, !done[call.Result])
		if err != nil {
			return "", err
		}
		if tail, err = c.append(tail, e, call.Result); err != nil {
			return "", err
		}
		done[call.Result] = true
		c.mark(call.Result, agentimport.LineMapped)
		c.b.Bind(call.Result.UUID, tail)
	}
	for _, l := range r.Extra {
		if tail, err = c.resultExtra(l, tail, done[l]); err != nil {
			return "", err
		}
		done[l] = true
		c.mark(l, agentimport.LineMapped)
		c.b.Bind(l.UUID, tail)
	}
	return tail, nil
}

func (c *converter) linked(a *claudecode.Agent) bool {
	for _, s := range c.spawned {
		if s.agent == a {
			return true
		}
	}
	return false
}

func allSynthetic(r *claudecode.Response) bool {
	for _, l := range r.Lines {
		if !l.IsSynthetic() {
			return false
		}
	}
	return true
}

// syntheticResponse converts the lines the CLI wrote for a failed call:
// a response entry that failed, with the line's text as the error.
func (c *converter) syntheticResponse(r *claudecode.Response, tail string) (string, error) {
	last := r.Last()
	code := "api_error"
	var s string
	if json.Unmarshal(last.Error, &s) == nil && s != "" {
		code = s
	}
	var text string
	for _, l := range r.Lines {
		for _, b := range l.Message.Content.Blocks {
			if b.Type == claudecode.BlockText {
				text += b.Text
			}
		}
		if l.Message.Content.IsText {
			text += l.Message.Content.Text
		}
	}
	e := &agentsession.ResponseEntry{
		ResponseID: r.MessageID,
		Status:     openresponses.ResponseStatusFailed,
		Error:      &openresponses.ErrorPayload{Code: code, Message: text},
	}
	e.Timestamp = last.Time()
	if err := c.lineMembers(e, last); err != nil {
		return "", err
	}
	if last.APIErrorStatus != 0 {
		if err := c.b.Member(e, "api_error_status", last.APIErrorStatus); err != nil {
			return "", err
		}
	}
	id, err := c.append(tail, e, last)
	if err != nil {
		return "", err
	}
	for _, l := range r.Lines {
		c.mark(l, agentimport.LineMapped)
		c.b.Bind(l.UUID, id)
	}
	return id, nil
}

// blockEntry maps one assistant content block to an item entry, as
// openresponses' Anthropic provider maps a response's blocks.
func (c *converter) blockEntry(l *claudecode.Line, b *claudecode.ContentBlock) (*agentsession.ItemEntry, error) {
	switch b.Type {
	case claudecode.BlockText:
		return itemEntry(&openresponses.Message{
			Role:    openresponses.RoleAssistant,
			Status:  openresponses.StatusCompleted,
			Content: openresponses.Contents{&openresponses.OutputText{Text: b.Text, Annotations: openresponses.Annotations{}}},
		}), nil
	case claudecode.BlockThinking:
		r := &openresponses.ReasoningItem{Status: openresponses.StatusCompleted, EncryptedContent: b.Signature}
		if b.Thinking != "" {
			r.Summary = openresponses.Contents{&openresponses.SummaryText{Text: b.Thinking}}
		}
		e := itemEntry(r)
		switch {
		case b.Signature != "":
			if err := c.b.Member(e, "provider", "anthropic"); err != nil {
				return nil, err
			}
			c.addf(agentimport.Gap, "agentimport/reasoning-provider", l, "an Anthropic thinking signature in encrypted_content, tagged %s:provider", Source)
		case b.Thinking == "":
			c.addf(agentimport.Lossy, "claudecode/thinking-empty", l, "a thinking block with neither text nor signature is an empty reasoning item")
		}
		return e, nil
	case claudecode.BlockToolUse:
		return c.callEntry(l, b)
	}
	// redacted_thinking, server tools and anything newer: kept whole as
	// the provider keeps them.
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	item, err := anthropicItem(b.Type, raw)
	if err != nil {
		return nil, err
	}
	c.addf(agentimport.Kept, "claudecode/anthropic-block", l, "a %s block is an %s item", b.Type, item.Type)
	return itemEntry(item), nil
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

// callEntry maps a tool_use block. The arguments are the ones the
// requests replayed: what the model wrote when the CLI echoes those,
// the CLI's normalised input otherwise; the other is kept beside.
func (c *converter) callEntry(l *claudecode.Line, b *claudecode.ContentBlock) (*agentsession.ItemEntry, error) {
	input := compactJSON(b.Input)
	if len(b.Input) == 0 {
		input = "{}"
	}
	args, other, otherName := input, "", ""
	if wire, ok := l.WireToolInputs[b.ID]; ok {
		if w := compactJSON(wire); w != input {
			if c.echoWire {
				args, other, otherName = w, input, "input"
			} else {
				other, otherName = w, "wire_input"
			}
		}
	}
	id, renamed := c.b.Calls.Assign(b.ID)
	c.calls[b.ID] = id
	e := itemEntry(&openresponses.FunctionCall{
		Status: openresponses.StatusCompleted, CallID: id, Name: b.Name, Arguments: args,
	})
	if renamed {
		if err := c.b.Member(e, "call_id", b.ID); err != nil {
			return nil, err
		}
		c.addf(agentimport.Repaired, "claudecode/call-id-renamed", l, "call %q is recorded as %q, unique in the session", b.ID, id)
	}
	if other != "" {
		if err := c.b.Member(e, otherName, json.RawMessage(other)); err != nil {
			return nil, err
		}
		if otherName == "input" {
			c.addf(agentimport.Kept, "claudecode/normalised-input", l, "arguments are the model's (the CLI echoes them); the CLI's normalised input is %s:input", Source)
		} else {
			c.addf(agentimport.Kept, "claudecode/wire-input", l, "arguments are the CLI's normalised input; the model's are %s:wire_input", Source)
		}
	}
	return e, nil
}

// responseEntry is the envelope of the response: the provider's id, the
// model that answered, the status from the stop reason and the usage
// of the last line that has one.
func (c *converter) responseEntry(r *claudecode.Response) (*agentsession.ResponseEntry, error) {
	last := r.Last()
	e := &agentsession.ResponseEntry{ResponseID: r.MessageID, Model: last.Message.Model}
	e.Timestamp = last.Time()
	stop := ""
	var usage json.RawMessage
	usages := map[string]bool{}
	for _, l := range r.Lines {
		if l.Message.StopReason != "" {
			stop = l.Message.StopReason
		}
		if len(l.Message.Usage) > 0 && string(l.Message.Usage) != "null" {
			usage = l.Message.Usage
			usages[compactJSON(l.Message.Usage)] = true
		}
	}
	switch stop {
	case "end_turn", "tool_use", "stop_sequence", "pause_turn", "refusal":
		e.Status = openresponses.ResponseStatusCompleted
	case "max_tokens", "model_context_window_exceeded":
		e.Status = openresponses.ResponseStatusIncomplete
		e.Incomplete = &openresponses.IncompleteDetails{Reason: openresponses.IncompleteReasonMaxOutputTokens}
	case "":
		c.addf(agentimport.Lossy, "claudecode/no-stop-reason", last, "no line of response %s has a stop reason; the response has no status", r.MessageID)
	default:
		c.addf(agentimport.Lossy, "claudecode/stop-reason", last, "stop reason %q has no response status; the response has none", stop)
	}
	if stop != "" {
		if err := c.b.Member(e, "stop_reason", stop); err != nil {
			return nil, err
		}
	}
	if usage != nil {
		var u claudecode.Usage
		if err := json.Unmarshal(usage, &u); err == nil {
			var thinking int64
			if u.OutputTokensDetails != nil {
				thinking = u.OutputTokensDetails.ThinkingTokens
			}
			ou := openresponses.Usage{
				InputTokens:  int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens),
				OutputTokens: int(u.OutputTokens),
			}
			ou.InputTokensDetails.CachedTokens = int(u.CacheReadInputTokens)
			ou.OutputTokensDetails.ReasoningTokens = int(thinking)
			ou.TotalTokens = ou.InputTokens + ou.OutputTokens
			e.Usage = &ou
		}
		if err := c.b.Member(e, "usage", usage); err != nil {
			return nil, err
		}
		c.addf(agentimport.Gap, "agentimport/usage-detail", last,
			"cache writes are folded into input_tokens; the native usage is %s:usage", Source)
		if len(usages) > 1 {
			c.addf(agentimport.Lossy, "claudecode/usage-snapshots", last,
				"the %d lines of response %s carry %d different usage snapshots; the last is used", len(r.Lines), r.MessageID, len(usages))
		}
	}
	if r.RequestID != "" {
		if err := c.b.Member(e, "request_id", r.RequestID); err != nil {
			return nil, err
		}
	}
	if extra := nonNull(last.Message.Extra); len(extra) > 0 {
		if c.opts.Slim {
			c.addf(agentimport.Dropped, "claudecode/message-members", last, "left out: %s", memberNames(extra))
		} else {
			if err := c.b.Member(e, "message", extra); err != nil {
				return nil, err
			}
			c.addf(agentimport.Kept, "claudecode/message-members", last, "kept in %s:message: %s", Source, memberNames(extra))
		}
	}
	return e, nil
}

// output maps a tool_result block to a function_call_output entry. The
// line's sidecar and members go on the first output made from it.
func (c *converter) output(l *claudecode.Line, b *claudecode.ContentBlock, firstOfLine bool) (*agentsession.ItemEntry, error) {
	callID, ok := c.calls[b.ToolUseID]
	if !ok {
		return nil, fmt.Errorf("line %d: tool result for unknown call %s", l.No, b.ToolUseID)
	}
	out := &openresponses.FunctionCallOutput{CallID: callID}
	if b.Content != nil {
		if b.Content.IsText {
			out.Output.Text = b.Content.Text
		} else {
			out.Output.Parts = c.contents(l, b.Content.Blocks, false)
		}
	}
	e := itemEntry(out)
	if b.IsError {
		if err := c.b.Member(e, "is_error", true); err != nil {
			return nil, err
		}
		c.addf(agentimport.Gap, "agentimport/tool-error", l, "is_error is the member %s:is_error; function_call_output has no error flag", Source)
	}
	if !firstOfLine {
		return e, nil
	}
	if len(l.ToolUseResult) > 0 {
		if c.opts.Slim {
			c.addf(agentimport.Dropped, "claudecode/tool-use-result", l, "the toolUseResult sidecar is left out")
		} else {
			if err := c.b.Member(e, "tool_use_result", l.ToolUseResult); err != nil {
				return nil, err
			}
			c.addf(agentimport.Kept, "claudecode/tool-use-result", l, "the toolUseResult sidecar is %s:tool_use_result", Source)
		}
	}
	return e, c.lineMembers(e, l)
}

// resultExtra converts what a result line holds beyond the results its
// response already wrote: another result for a call already answered,
// kept aside, and other blocks, a user message after the outputs.
func (c *converter) resultExtra(l *claudecode.Line, tail string, wrote bool) (string, error) {
	var rest []*claudecode.ContentBlock
	for _, b := range l.Message.Content.Blocks {
		if b.Type != claudecode.BlockToolResult {
			rest = append(rest, b)
		}
	}
	if !wrote {
		// Every result on the line answered a call another line
		// answered first.
		return c.custom(l, tail, "claudecode/duplicate-result", "a second result for a call that has one")
	}
	if len(rest) == 0 {
		return tail, nil
	}
	e := itemEntry(openresponses.UserMessage(c.contents(l, rest, true)...))
	return c.append(tail, e, l)
}

// launchStatus is the status an Agent call's result reports when the
// call only started the agent: async_launched or teammate_spawned.
func launchStatus(l *claudecode.Line) string {
	if l == nil || len(l.ToolUseResult) == 0 || l.ToolUseResult[0] != '{' {
		return ""
	}
	var r struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(l.ToolUseResult, &r) != nil {
		return ""
	}
	switch r.Status {
	case "async_launched", "teammate_spawned":
		return r.Status
	}
	return ""
}
