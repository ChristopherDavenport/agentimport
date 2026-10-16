package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// responseID is the response ID of an assistant row: Hermes keeps no
// provider's, so the row's.
func responseID(m *hermes.Message) string { return fmt.Sprintf("row-%d", m.ID) }

// assistant converts an assistant row: its reasoning, its text and its
// calls, the output of one model call, then the response. Every column
// the row maps is known before its first entry is written, which
// carries the rest.
func (w *walk) assistant(i int) error {
	sc := w.sc
	m := w.rows[i]
	if w.skipReply {
		w.skipReply = false
		return sc.custom(m.Time, m.Unit, "hermes/review-harness", "the curator's reply to a background-review harness prompt (a resume strips it with the prompt)", rowData(m))
	}
	summaryRow := isSummary(m)
	copied := w.copied[i]
	respID := ""
	if !summaryRow {
		respID = responseID(m)
	}
	used := []string{"tool_calls"}
	if len(m.BadCalls) > 0 {
		sc.addf(agentimport.Repaired, "hermes/call-malformed", m.Unit, "%d entries of the row's tool_calls are not objects; Hermes cannot send them, and they are kept in the row's columns", len(m.BadCalls))
		used = nil
	}

	r := reasoningOf(m)
	if len(r.items) > 0 && r.from != r.keptFrom {
		used = append(used, r.from)
	}
	if r.bad > 0 {
		sc.addf(agentimport.Repaired, "hermes/reasoning-malformed", m.Unit, "%d entries of the row's %s are not objects; they are kept as written", r.bad, r.keptFrom)
	}
	if len(r.kept) > 0 {
		sc.addf(agentimport.Kept, "hermes/reasoning-carrier", m.Unit, "%d entries of %s no item holds, such as a provider's native carrier, are kept in the row's columns", len(r.kept), r.keptFrom)
	}
	switch r.from {
	case "reasoning_content", "reasoning":
		if len(r.items) > 0 {
			sc.addf(agentimport.Lossy, "hermes/reasoning-text", m.Unit, "the reasoning is the row's %s text, a reasoning item's content, which a chat-completions route replays only into the field its provider reads", r.from)
		}
	}
	encrypted := false
	for _, it := range r.items {
		if ri, ok := it.(*openresponses.ReasoningItem); ok && ri.EncryptedContent != "" || it.ItemType() == "anthropic.redacted_thinking" || it.ItemType() == openresponses.ItemTypeCompaction {
			encrypted = true
		}
	}

	// The text.
	var msg *openresponses.Message
	if m.Content.Structured && !m.HasAPIContent {
		cs, kept := parts(m.Content.Parts, true)
		if kept > 0 {
			sc.addf(agentimport.Kept, "hermes/content-part", m.Unit, "%d content parts Open Responses does not define are carried as extension parts", kept)
		}
		if len(cs) > 0 {
			msg = &openresponses.Message{Role: openresponses.RoleAssistant, Content: cs}
		}
	} else {
		text, sidecar := sentText(m)
		if sidecar {
			used = append(used, "api_content")
			if m.Content.Text != m.APIContent {
				sc.addf(agentimport.Lossy, "hermes/api-content", m.Unit, "the row's api_content, what Hermes sent, is the item; the content it shows is kept in the row's columns")
			}
		}
		if len(m.ToolCalls) > 0 && !sidecar && staleMarkerRE.MatchString(text) {
			sc.addf(agentimport.Repaired, "hermes/stale-marker", m.Unit, "the assistant's content is a bare tool marker an older Hermes stored, which a resume blanks; the turn has no text")
			text = ""
		}
		switch {
		case text != "":
			msg = openresponses.AssistantText(text)
		case len(m.ToolCalls) == 0 && len(r.items) == 0:
			// Hermes sends the empty turn as it is.
			msg = openresponses.AssistantText("")
		}
	}
	if msg != nil {
		if id, phase := messagePhase(m, encrypted); phase != "" || id != "" {
			msg.Phase, msg.ID = phase, id
			used = append(used, "codex_message_items")
		}
	}
	if summaryRow {
		used = append(used, "_compressed_summary")
	}
	if m.FinishReason != "" && respID != "" {
		used = append(used, "finish_reason")
	}
	w.used = used

	for k, it := range r.items {
		k, it := k, it
		_, _, err := w.item(it, m, respID, nil, func(e *agentsession.ItemEntry) error {
			if ri, ok := it.(*openresponses.ReasoningItem); ok && ri.EncryptedContent != "" || it.ItemType() == "anthropic.redacted_thinking" || it.ItemType() == openresponses.ItemTypeCompaction {
				if err := sc.b.Member(e, "provider", r.providers[k]); err != nil {
					return err
				}
			}
			if k < len(r.issuer) && !isEmpty(r.issuer[k]) {
				return sc.member(e, "item", r.issuer[k], m.Unit)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if it.ItemType() == openresponses.ItemTypeCompaction {
			sc.addf(agentimport.Lossy, "hermes/native-compaction", m.Unit, "a provider's compaction item Hermes keeps among the reasoning items; it replays only to a request that carries context management, and it is in context here")
		}
	}
	if encrypted {
		sc.addf(agentimport.Gap, "agentimport/reasoning-provider", m.Unit, "the reasoning's encrypted content is opaque and names no provider; the item entry carries %s:provider", Source)
	}
	if msg != nil {
		if _, _, err := w.item(msg, m, respID, visible(m), nil); err != nil {
			return err
		}
	}

	// The calls.
	for _, c := range m.ToolCalls {
		if sig := thoughtSignature(c); sig != "" {
			_, _, err := w.item(&openresponses.ReasoningItem{EncryptedContent: sig}, m, respID, nil, func(e *agentsession.ItemEntry) error {
				return sc.b.Member(e, "provider", "gemini")
			})
			if err != nil {
				return err
			}
			sc.addf(agentimport.Gap, "agentimport/reasoning-provider", m.Unit, "the call's Gemini thought signature is a reasoning item holding it alone, before the call, as providers/gemini reads one; the item entry carries %s:provider", Source)
		}
		native := c.PairingID()
		callID, renamed := sc.b.Calls.Assign(native)
		fc := &openresponses.FunctionCall{CallID: callID, Name: c.Name(), Arguments: c.ArgumentsText()}
		id, _, err := w.item(fc, m, respID, nil, func(e *agentsession.ItemEntry) error {
			if renamed {
				if err := sc.b.Member(e, "call_id", native); err != nil {
					return err
				}
			}
			if rest := callRest(c); len(rest) > 0 {
				return sc.member(e, "call", rest, m.Unit)
			}
			return nil
		})
		if err != nil {
			return err
		}
		if renamed {
			sc.addf(agentimport.Repaired, "hermes/call-id", m.Unit, "call ID %q is empty, malformed or used before in the session; the call is %s, the native ID kept in %s:call_id", native, callID, Source)
		}
		sc.pairs[native] = callID
		sc.callEntry[callID] = id
		if c.Name() == "delegate_task" {
			sc.kids.called(native, callID, m)
		}
	}
	sc.mark(m.Unit, agentimport.LineMapped)

	if respID == "" {
		sc.addf(agentimport.Lossy, "hermes/summary-row", m.Unit, "the compaction's summary, which Hermes wrote as an assistant message; it is no model's output and has no response")
		return nil
	}
	resp := &agentsession.ResponseEntry{ResponseID: respID, Status: openresponses.ResponseStatusCompleted}
	resp.Timestamp = m.Time
	if copied {
		if err := sc.b.Member(resp, "copied", true); err != nil {
			return err
		}
		sc.addf(agentimport.Lossy, "hermes/copied-row", m.Unit, "the row is a copy Hermes wrote of an earlier assistant turn, kept by a compaction, a continuation or a branch: a response entry marked %s:copied, the model call the turn it copies made; Hermes records no usage per response, so none moves off the entry", Source)
	}
	switch m.FinishReason {
	case "length":
		resp.Status = openresponses.ResponseStatusIncomplete
		resp.Incomplete = &openresponses.IncompleteDetails{Reason: openresponses.IncompleteReasonMaxOutputTokens}
	case "content_filter":
		resp.Status = openresponses.ResponseStatusIncomplete
		resp.Incomplete = &openresponses.IncompleteDetails{Reason: openresponses.IncompleteReasonContentFilter}
	}
	if err := sc.b.Member(resp, "finish_reason", m.FinishReason); err != nil {
		return err
	}
	if !w.rowKept {
		if err := w.rowMembers(resp, m, used...); err != nil {
			return err
		}
		w.rowKept = true
	}
	_, err := sc.append(resp)
	return err
}

// callRest is a tool call without the members a function call maps.
func callRest(c *hermes.ToolCall) json.RawMessage {
	var all map[string]json.RawMessage
	raw, err := json.Marshal(c)
	if err != nil || json.Unmarshal(raw, &all) != nil {
		return nil
	}
	delete(all, "id")
	delete(all, "call_id")
	if t, ok := all["type"]; ok && string(t) == `"function"` {
		delete(all, "type")
	}
	if f, ok := all["function"]; ok {
		var fn map[string]json.RawMessage
		if json.Unmarshal(f, &fn) == nil {
			delete(fn, "name")
			delete(fn, "arguments")
			if len(fn) == 0 {
				delete(all, "function")
			} else {
				all["function"], _ = json.Marshal(fn)
			}
		}
	}
	if len(all) == 0 {
		return nil
	}
	out, _ := json.Marshal(all)
	return out
}

// tool converts a tool row: the output of the call it names, after the
// decision a refusal is and the sub-agents the call ran.
func (w *walk) tool(i int) error {
	sc := w.sc
	m := w.rows[i]
	native := m.ToolCallID
	callID, paired := sc.pairs[native]
	if !paired {
		callID = native
		if !wellFormedCall(callID) {
			callID = fmt.Sprintf("orphan_%d", m.ID)
		}
	}
	var data openresponses.FunctionCallOutputData
	if m.Content.Structured {
		cs, kept := parts(m.Content.Parts, false)
		if kept > 0 {
			sc.addf(agentimport.Kept, "hermes/content-part", m.Unit, "%d content parts Open Responses does not define are carried as extension parts", kept)
		}
		data = openresponses.FunctionCallOutputData{Parts: cs}
		if cs == nil {
			data.Parts = openresponses.Contents{}
		}
	} else {
		text, _ := sentText(m)
		data = openresponses.FunctionCallOutputData{Text: text}
	}
	if paired && strings.Contains(data.String(), notConsented) {
		by := "policy"
		if strings.Contains(data.String(), userDenied) {
			by = "user"
		}
		d := agentsession.NewDecision(callID, sc.callEntry[callID], agentsession.VerdictReject, by).WithReason(data.String())
		d.Timestamp = m.Time
		if _, err := sc.append(d); err != nil {
			return err
		}
		sc.addf(agentimport.Lossy, "hermes/denial-text", m.Unit, "the output is the text Hermes' approval gate gives a refusal; it is a reject decision by the %s, told by the text alone", by)
	}
	var refs []agentsession.EntryRef
	if paired {
		var err error
		if refs, err = sc.linkBefore(native, callID, m); err != nil {
			return err
		}
	}
	w.used = []string{"tool_call_id"}
	_, _, err := w.item(&openresponses.FunctionCallOutput{CallID: callID, Output: data}, m, "", nil, func(e *agentsession.ItemEntry) error {
		e.Parents = refs
		return nil
	})
	if err != nil {
		return err
	}
	sc.mark(m.Unit, agentimport.LineMapped)
	if paired {
		if err := sc.linkAfter(native, callID, m, data.String()); err != nil {
			return err
		}
	}
	return nil
}

// wellFormedCall reports a call ID a provider takes as it is.
func wellFormedCall(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// repair leaves out of the context Hermes resumes with what its
// repair_message_sequence drops before it sends: outputs of no call
// before them in that context, and calls no output answers.
// Consecutive user turns, which it merges into one, are reported, not
// merged.
func (w *walk) repair() error {
	sc := w.sc
	ctx, err := sc.b.S.ContextAt(sc.at)
	if err != nil {
		return err
	}
	answered := map[string]bool{}
	made := map[string]bool{}
	var omit []string
	for k, it := range ctx.Items {
		e, ok := ctx.ItemEntries[k].(*agentsession.ItemEntry)
		switch it := it.(type) {
		case *openresponses.FunctionCall:
			made[it.CallID] = true
		case *openresponses.FunctionCallOutput:
			if made[it.CallID] {
				answered[it.CallID] = true
				continue
			}
			if ok {
				omit = append(omit, e.ID)
				sc.addf(agentimport.Repaired, "hermes/orphan-output", w.unitOf[e.ID], "the output names call %s, which no call before it in the context Hermes resumes with made; a resume drops it (repair_message_sequence), and it is left out of that context", it.CallID)
			}
		}
	}
	prevUser := false
	merged := 0
	for k, it := range ctx.Items {
		e, ok := ctx.ItemEntries[k].(*agentsession.ItemEntry)
		if fc, isCall := it.(*openresponses.FunctionCall); isCall && ok && !answered[fc.CallID] {
			omit = append(omit, e.ID)
			sc.addf(agentimport.Repaired, "hermes/unanswered-call", w.unitOf[e.ID], "call %s has no output in the context Hermes resumes with; a resume prunes it (repair_message_sequence), and it is left out of that context", fc.CallID)
		}
		user := false
		if msg, isMsg := it.(*openresponses.Message); isMsg && msg.Role == openresponses.RoleUser {
			user = true
			if prevUser {
				merged++
			}
		}
		prevUser = user
	}
	if merged > 0 {
		sc.addf(agentimport.Lossy, "hermes/alternation", sc.rec.s.Unit, "%d user messages follow another in the context Hermes resumes with; a resume merges each into the one before (repair_message_sequence), which is not reproduced", merged)
	}
	if len(omit) == 0 {
		return nil
	}
	cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: omit}}
	if e, ok := sc.b.S.Entry(sc.at); ok {
		cfg.Timestamp = e.Base().Timestamp
	}
	_, err = sc.append(cfg)
	return err
}
