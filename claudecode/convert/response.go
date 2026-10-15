package convert

import (
	"encoding/json"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// response converts one API response: a config delta when the model
// changed, an item per content block in block order, the response
// entry, then each call's link, decision and output in call order, so
// the results of parallel calls are on the path, then what was sent
// with them.
func (c *converter) response(r *claudecode.Response, tail string, st *pathState) (string, error) {
	var err error
	if allSynthetic(r) {
		tail, err = c.syntheticResponse(r, tail)
	} else {
		tail, err = c.modelResponse(r, tail, st)
	}
	if err != nil {
		return "", err
	}
	written := map[*claudecode.Line]bool{}
	for _, call := range r.Calls {
		callID, ok := c.calls[call.Use.ID]
		if !ok || call.Line.IsSynthetic() {
			// A call on a line the CLI wrote itself was never recorded
			// as a call; its result is kept aside.
			if call.Result != nil && !written[call.Result] && c.disp[call.Result] == "" {
				if tail, err = c.custom(call.Result, tail, "claudecode/orphan-result", "a result for a call the CLI wrote, not the model, is a custom entry"); err != nil {
					return "", err
				}
			}
			continue
		}
		var parents []agentsession.EntryRef
		var peer *claudecode.Agent
		if a := c.agents[call.Use.ID]; a != nil && !c.cv.done[a.ID] && !c.linked(a) {
			if waitedFor(a, call.Result) {
				if tail, parents, err = c.link(a, call, callID, tail); err != nil {
					return "", err
				}
			} else {
				peer = a
			}
		}
		if call.Result == nil {
			if peer != nil {
				if tail, err = c.peerLink(peer, call, callID, tail); err != nil {
					return "", err
				}
			}
			continue
		}
		if tail, err = c.decision(call, callID, tail); err != nil {
			return "", err
		}
		e, err := c.output(call.Result, call.ResultBlock, callID, !written[call.Result])
		if err != nil {
			return "", err
		}
		e.Parents = parents
		id, err := c.append(tail, e, call.Result)
		if err != nil {
			return "", err
		}
		tail = id
		if peer != nil {
			// The output is the CLI's acknowledgement that the agent
			// started, not the agent's answer: its peer link follows
			// it, so a reader does not take the agent to have answered
			// the call (RFC 0001, link).
			if tail, err = c.peerLink(peer, call, callID, tail); err != nil {
				return "", err
			}
		}
		written[call.Result] = true
		c.mark(call.Result, agentimport.LineMapped)
		c.bind(call.Result, tail)
	}
	for _, l := range r.Extra {
		if tail, err = c.resultExtra(l, tail, written[l]); err != nil {
			return "", err
		}
		written[l] = true
	}
	for _, l := range r.Between {
		if tail, err = c.line(l, tail, st); err != nil {
			return "", err
		}
	}
	return tail, nil
}

// modelResponse writes the items and the response entry of a response
// the model wrote.
func (c *converter) modelResponse(r *claudecode.Response, tail string, st *pathState) (string, error) {
	var err error
	if model := requestModel(r); model != "" && model != st.model {
		e := &agentsession.ConfigEntry{Model: model}
		e.Timestamp = r.Lines[0].Time()
		if tail, err = c.append(tail, e, nil); err != nil {
			return "", err
		}
		st.model = model
	}
	stop, refusal := c.stopReason(r)
	// The provider puts a refusal's explanation in the message still
	// open: the last text block's, when the response ends with one.
	var lastBlock *claudecode.ContentBlock
	for _, l := range r.Lines {
		if !l.IsSynthetic() && len(l.Message.Content.Blocks) > 0 {
			lastBlock = l.Message.Content.Blocks[len(l.Message.Content.Blocks)-1]
		}
	}
	refusalPlaced := false
	for _, l := range r.Lines {
		if l.IsSynthetic() {
			// A client-written line inside a model response: its text
			// was never model output.
			if tail, err = c.custom(l, tail, "claudecode/synthetic-line", "a line the CLI wrote inside a response is a custom entry"); err != nil {
				return "", err
			}
			continue
		}
		wrote := false
		for _, b := range l.Message.Content.Blocks {
			if b.IsRaw() && b.Type == "" {
				c.addf(agentimport.Dropped, "claudecode/null-block", l, "a content block that is not an object is left out")
				continue
			}
			e, err := c.blockEntry(l, b, st)
			if err != nil {
				return "", err
			}
			if stop == "refusal" && b == lastBlock && b.Type == claudecode.BlockText {
				if m, ok := e.Item.(*openresponses.Message); ok {
					m.Content = append(m.Content, &openresponses.Refusal{Refusal: refusal})
					refusalPlaced = true
				}
			}
			e.ResponseID = r.MessageID
			if err := c.blockMembers(e, l, b); err != nil {
				return "", err
			}
			if !wrote {
				if err := c.lineMembers(e, l, "line"); err != nil {
					return "", err
				}
			}
			id, err := c.append(tail, e, l)
			if err != nil {
				return "", err
			}
			if b.Type == claudecode.BlockToolUse && !b.IsRaw() {
				c.callEntries[b.ID] = id
			}
			c.bind(l, id)
			tail, wrote = id, true
		}
		if wrote {
			c.mark(l, agentimport.LineMapped)
		} else {
			c.addf(agentimport.Dropped, "claudecode/empty-line", l, "an assistant line with no content block")
			c.mark(l, agentimport.LineDropped)
			c.bind(l, tail)
		}
	}
	if stop == "refusal" && !refusalPlaced {
		m := &openresponses.Message{Role: openresponses.RoleAssistant, Status: openresponses.StatusCompleted,
			Content: openresponses.Contents{&openresponses.Refusal{Refusal: refusal}}}
		e := itemEntry(m)
		e.ResponseID = r.MessageID
		e.Timestamp = r.Last().Time()
		if tail, err = c.append(tail, e, nil); err != nil {
			return "", err
		}
	}
	re, err := c.responseEntry(r, stop)
	if err != nil {
		return "", err
	}
	return c.append(tail, re, nil)
}

// stopReason returns the response's stop reason, from the last line
// that has one, and for a refusal the explanation the provider gives:
// stop_details.explanation, or "The request was refused" with the
// category.
func (c *converter) stopReason(r *claudecode.Response) (string, string) {
	stop := ""
	var details json.RawMessage
	for _, l := range r.Lines {
		if l.IsSynthetic() {
			continue
		}
		if l.Message.StopReason != "" {
			stop = l.Message.StopReason
			details = l.Message.Extra["stop_details"]
		}
	}
	if stop != "refusal" {
		return stop, ""
	}
	var d struct {
		Category    string `json:"category"`
		Explanation string `json:"explanation"`
	}
	_ = json.Unmarshal(details, &d)
	if d.Explanation != "" {
		return stop, d.Explanation
	}
	text := "The request was refused"
	if d.Category != "" {
		text += " (" + d.Category + ")"
	}
	return stop, text + "."
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
	var text strings.Builder
	for _, l := range r.Lines {
		if l.Message.Content.IsText {
			text.WriteString(l.Message.Content.Text)
		}
		for _, b := range l.Message.Content.Blocks {
			if b.Type == claudecode.BlockText && !b.IsRaw() {
				text.WriteString(b.Text)
			}
		}
	}
	e := &agentsession.ResponseEntry{
		ResponseID: r.MessageID,
		Status:     openresponses.ResponseStatusFailed,
		Error:      &openresponses.ErrorPayload{Code: code, Message: text.String()},
	}
	e.Timestamp = last.Time()
	if err := c.lineMembers(e, last, "line"); err != nil {
		return "", err
	}
	if last.APIErrorStatus != 0 {
		if err := c.b.Member(e, "api_error_status", last.APIErrorStatus); err != nil {
			return "", err
		}
	}
	if err := c.messageMembers(e, last, last.Message); err != nil {
		return "", err
	}
	id, err := c.append(tail, e, last)
	if err != nil {
		return "", err
	}
	for _, l := range r.Lines {
		c.mark(l, agentimport.LineMapped)
		c.bind(l, id)
	}
	return id, nil
}

// blockEntry maps one assistant content block to an item entry, as
// openresponses' Anthropic provider maps a response's blocks.
func (c *converter) blockEntry(l *claudecode.Line, b *claudecode.ContentBlock, st *pathState) (*agentsession.ItemEntry, error) {
	if !b.IsRaw() {
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
			return c.callEntry(l, b, st)
		}
	}
	// redacted_thinking, server tools, anything newer and any block of
	// a shape this package does not type: kept whole as the provider
	// keeps them.
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	typ := b.Type
	if typ == "" {
		typ = "unknown"
	}
	item, err := anthropicItem(typ, c.clean(raw, l))
	if err != nil {
		return nil, err
	}
	c.addf(agentimport.Kept, "claudecode/anthropic-block", l, "a %s block is an %s item", typ, item.Type)
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

// echo says whether the CLI replays the model's arguments at this
// point of the path: as the last prompt snapshot on it says, or before
// any, as the transcript's first does.
func (c *converter) echo(st *pathState) bool {
	if st.echo != nil {
		return *st.echo
	}
	return c.echoWire
}

// callEntry maps a tool_use block. The arguments are the ones the
// requests replayed: what the model wrote when the CLI echoes those,
// the CLI's normalised input otherwise; the other is kept beside.
func (c *converter) callEntry(l *claudecode.Line, b *claudecode.ContentBlock, st *pathState) (*agentsession.ItemEntry, error) {
	input := compactJSON(b.Input)
	if len(b.Input) == 0 || !json.Valid(b.Input) {
		input = "{}"
	}
	args, other, otherName := input, "", ""
	if wire, ok := l.WireToolInputs[b.ID]; ok && json.Valid(wire) {
		if w := compactJSON(wire); w != input {
			if c.echo(st) {
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
	if len(b.Input) > 0 && !json.Valid(b.Input) {
		c.addf(agentimport.Lossy, "claudecode/invalid-input", l, "a tool_use input that is not JSON is recorded as {}")
	}
	return e, nil
}

// responseEntry is the envelope of the response: the provider's id, the
// model that answered, the status from the stop reason and the usage
// of the last line that has one.
func (c *converter) responseEntry(r *claudecode.Response, stop string) (*agentsession.ResponseEntry, error) {
	var last *claudecode.Line
	for _, l := range r.Lines {
		if !l.IsSynthetic() {
			last = l
		}
	}
	e := &agentsession.ResponseEntry{ResponseID: r.MessageID, Model: last.Message.Model}
	e.Timestamp = last.Time()
	var usage json.RawMessage
	usages := map[string]bool{}
	dropped := 0
	for _, l := range r.Lines {
		if l.IsSynthetic() {
			continue
		}
		if len(l.Message.Usage) > 0 && string(l.Message.Usage) != "null" {
			usage = l.Message.Usage
			usages[compactJSON(l.Message.Usage)] = true
		}
		var ts []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(l.Message.Extra["input_transformations"], &ts) == nil {
			for _, t := range ts {
				if t.Type == "thinking_dropped" {
					dropped++
				}
			}
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
	if dropped > 0 {
		c.addf(agentimport.Lossy, "claudecode/thinking-dropped", last,
			"the CLI left %d earlier thinking blocks out of this request; the path holds them, and the request it names by index is not rebuilt to say which", dropped)
	}
	copied := c.copied[r.MessageID]
	if usage != nil {
		var u claudecode.Usage
		if err := json.Unmarshal(usage, &u); err == nil && !copied {
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
		if !copied {
			c.addf(agentimport.Gap, "agentimport/usage-detail", last,
				"cache writes are folded into input_tokens; the native usage is %s:usage", Source)
		}
		if len(usages) > 1 {
			c.addf(agentimport.Lossy, "claudecode/usage-snapshots", last,
				"the %d lines of response %s carry %d different usage snapshots; the last is used", len(r.Lines), r.MessageID, len(usages))
		}
	}
	if copied {
		if err := c.b.Member(e, "copied", true); err != nil {
			return nil, err
		}
		c.addf(agentimport.Lossy, "claudecode/fork-copied-response", last,
			"response %s is the parent's, copied into the fork; its usage is left off the response (kept in %s:usage) so it counts once", r.MessageID, Source)
	}
	if r.RequestID != "" {
		if err := c.b.Member(e, "request_id", r.RequestID); err != nil {
			return nil, err
		}
	}
	var skip []string
	if stop == "refusal" {
		// The explanation is the refusal part.
		skip = append(skip, "stop_details")
	}
	return e, c.messageMembers(e, last, last.Message, skip...)
}

// output maps a tool_result block to a function_call_output entry. The
// line's sidecar and members go on the first output made from it.
func (c *converter) output(l *claudecode.Line, b *claudecode.ContentBlock, callID string, firstOfLine bool) (*agentsession.ItemEntry, error) {
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
	if err := c.blockMembers(e, l, b); err != nil {
		return nil, err
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
	if err := c.messageMembers(e, l, l.Message); err != nil {
		return nil, err
	}
	return e, c.lineMembers(e, l, "line")
}

// resultText is what a tool result said, as text: the reason a reject
// carries.
func resultText(b *claudecode.ContentBlock) string {
	if b.Content == nil {
		return ""
	}
	if b.Content.IsText {
		return b.Content.Text
	}
	var parts []string
	for _, x := range b.Content.Blocks {
		if x.Type == claudecode.BlockText && !x.IsRaw() {
			parts = append(parts, x.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// decision writes the reject a refused call's result line records: a
// toolDenialKind, or a permissionDecision of reject. An approval is a
// proceed the RFC asks a writer not to write without a hold or
// rewritten arguments, so it stays in the line's members.
func (c *converter) decision(call *claudecode.Call, callID, tail string) (string, error) {
	l := call.Result
	var denial string
	_ = json.Unmarshal(l.Extra["toolDenialKind"], &denial)
	var pd struct {
		Decision   string `json:"decision"`
		Source     string `json:"source"`
		ReasonType string `json:"reasonType"`
	}
	_ = json.Unmarshal(l.Extra["permissionDecision"], &pd)
	if !(denial != "" && denial != "interrupted" || pd.Decision == "reject") {
		return tail, nil
	}
	target, ok := c.callEntries[call.Use.ID]
	if !ok {
		return tail, nil
	}
	by := agentsession.ByPolicy
	switch {
	case denial == "user-rejected" || strings.HasPrefix(pd.Source, "user"):
		by = agentsession.ByHuman
	case denial == "automode-blocked" || pd.ReasonType == "classifier":
		by = agentsession.ByAgent
	}
	reason := resultText(call.ResultBlock)
	if reason == "" {
		reason = "rejected"
		if denial != "" {
			reason += " (" + denial + ")"
		}
	}
	d := agentsession.NewDecision(callID, target, agentsession.VerdictReject, by).WithReason(reason)
	return c.append(tail, d, l)
}

// resultExtra converts what a result line holds beyond the results its
// response wrote: a line that wrote no result is kept aside; other
// blocks become a user message after the outputs.
func (c *converter) resultExtra(l *claudecode.Line, tail string, wrote bool) (string, error) {
	if !wrote {
		return c.custom(l, tail, "claudecode/duplicate-result", "a result line none of whose results the response could take (a second result, or another response's) is a custom entry")
	}
	var rest []*claudecode.ContentBlock
	for _, b := range l.Message.Content.Blocks {
		if b.Type != claudecode.BlockToolResult || b.IsRaw() {
			rest = append(rest, b)
		}
	}
	if len(rest) == 0 {
		return tail, nil
	}
	e := itemEntry(openresponses.UserMessage(c.contents(l, rest, true)...))
	if err := c.blockMembers(e, l, rest...); err != nil {
		return "", err
	}
	return c.append(tail, e, l)
}

// link writes the link to an agent the call waited for, whose result is
// the call's output: a subsession, linked before the output and
// converted here, so the output can name its leaf.
func (c *converter) link(a *claudecode.Agent, call *claudecode.Call, callID, tail string) (string, []agentsession.EntryRef, error) {
	child := agentsession.SubsessionID(c.t.id, callID)
	ts := call.Line.Time()
	if call.Result != nil {
		ts = call.Result.Time()
	}
	link := agentsession.NewSubsessionLink(child, callID)
	link.Timestamp = ts
	id, err := c.append(tail, link, nil)
	if err != nil {
		return "", nil, err
	}
	sessions, leaf, err := c.cv.transcript(a.Transcript, agentFile(a.ID), target{
		id: child, parent: c.t.id, spawnedBy: callID, agent: a, parentTr: c.tr,
	})
	if err != nil {
		return "", nil, err
	}
	c.spawned = append(c.spawned, spawn{agent: a, id: child, callID: callID, sessions: sessions})
	var parents []agentsession.EntryRef
	if leaf != "" && call.Result != nil {
		parents = []agentsession.EntryRef{{Session: child, Entry: leaf}}
	}
	return id, parents, nil
}

// peerLink writes the link to an agent the call only started, in the
// background or as a team member, whose work outlives the call: a peer,
// converted after this transcript. It follows the call's output, which
// is the CLI's acknowledgement, not the agent's answer.
func (c *converter) peerLink(a *claudecode.Agent, call *claudecode.Call, callID, tail string) (string, error) {
	child := agentsession.SubsessionID(c.t.id, callID)
	ts := call.Line.Time()
	if call.Result != nil {
		ts = call.Result.Time()
	}
	link := agentsession.NewLinkEntry(agentsession.RelPeer, child)
	link.CallID = callID
	link.Timestamp = ts
	id, err := c.append(tail, link, nil)
	if err != nil {
		return "", err
	}
	c.spawned = append(c.spawned, spawn{agent: a, id: child, callID: callID})
	return id, nil
}

// waitedFor reports whether the call that spawned an agent waited for
// it: its result says the agent completed, or, with no status, the
// agent ran in the foreground.
func waitedFor(a *claudecode.Agent, result *claudecode.Line) bool {
	if result != nil && len(result.ToolUseResult) > 0 && result.ToolUseResult[0] == '{' {
		var r struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(result.ToolUseResult, &r) == nil && r.Status != "" {
			return r.Status == "completed"
		}
	}
	return a.Meta != nil && a.Meta.RequestShape == "foreground"
}
