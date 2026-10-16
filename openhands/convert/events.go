package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// Part IDs of the instructions a system prompt event composes.
const (
	partSystem  = "system"
	partDynamic = "dynamic_context"
)

// systemPrompt writes a system prompt event as a config: the first on
// a root carries full settings, a later one the delta from the
// settings in force.
func (c *conv) systemPrompt(i int) error {
	sp, _ := c.c.Events[i].SystemPrompt()
	var parts []agentsession.InstructionPart
	members := map[string]json.RawMessage{}
	text := ""
	if sp.SystemPrompt != nil && sp.SystemPrompt.Err == nil {
		text = sp.SystemPrompt.Text
		if r := partRest(sp.SystemPrompt); r != nil {
			raw, _ := json.Marshal(r)
			members["system_prompt"] = raw
		}
	} else {
		c.at(agentimport.Lossy, "openhands/bad-system-prompt", i, "the system prompt is missing or not a text content; the instructions are empty")
	}
	parts = append(parts, agentsession.InstructionPart{ID: partSystem, Text: text})
	if dc := sp.DynamicContext; dc != nil && dc.Err == nil {
		parts = append(parts, agentsession.InstructionPart{ID: partDynamic, Text: dc.Text})
		if r := partRest(dc); r != nil {
			raw, _ := json.Marshal(r)
			members["dynamic_context"] = raw
		}
		c.at(agentimport.Lossy, "openhands/instructions-join", i, "OpenHands sends the system prompt and the dynamic context as two blocks of one system message, joined with a newline for the Responses API; the config carries them as two parts, which the session joins with a blank line")
	}
	tools, legacy := c.tools(i, sp.Tools)
	if legacy > 0 {
		c.at(agentimport.Lossy, "openhands/version-guess", i, "the tools are chat tool params, as SDK 1.0 to 1.6 wrote them, with the schema the model saw")
	}
	if len(sp.Tools) > 0 && !c.slim {
		raw, err := json.Marshal(sp.Tools)
		if err != nil {
			return err
		}
		members["tools"] = raw
	}
	r, err := rest(sp, "system_prompt", "tools", "dynamic_context")
	if err != nil {
		return err
	}
	for k, v := range r {
		members[k] = v
	}
	var cfg *agentsession.ConfigEntry
	at := ""
	var s agentsession.Settings
	if p := c.parent[i]; p >= 0 {
		at = c.tail[p]
		ctx, err := c.b.S.ContextAt(at)
		if err != nil {
			return err
		}
		s = ctx.Settings
	}
	if len(s.InstructionsParts) > 0 || s.Instructions != "" || len(s.Tools) > 0 {
		cfg = s.InstructionsDelta(parts)
		if cfg == nil {
			cfg = &agentsession.ConfigEntry{}
		}
		have := map[string]string{}
		for _, t := range s.Tools {
			raw, _ := json.Marshal(t)
			have[agentsession.ToolName(t)] = string(raw)
		}
		want := map[string]bool{}
		for _, t := range tools {
			name := agentsession.ToolName(t)
			want[name] = true
			raw, _ := json.Marshal(t)
			if have[name] != string(raw) {
				cfg.ToolsAdded = append(cfg.ToolsAdded, t)
			}
		}
		for _, t := range s.Tools {
			if name := agentsession.ToolName(t); !want[name] {
				cfg.ToolsRemoved = append(cfg.ToolsRemoved, name)
			}
		}
		c.at(agentimport.Lossy, "openhands/system-prompt-again", i, "a later system prompt event, as an agent switch or a resume with other tools writes; it is a config delta from the settings in force")
	} else {
		joined := agentsession.JoinInstructions(parts)
		cfg = &agentsession.ConfigEntry{InstructionsParts: parts, Instructions: &joined, ToolsAdded: tools}
		if s.Model == "" {
			cfg.Model = c.agent.Model
			if c.agent.ReasoningEffort != "" {
				cfg.Reasoning = &openresponses.ReasoningConfig{Effort: openresponses.ReasoningEffort(c.agent.ReasoningEffort)}
			}
		}
		if c.agent.Model != "" {
			c.at(agentimport.Lossy, "openhands/model-from-state", i, "the model is the one base_state names, as last saved; a later switch of model is not recorded per event")
		}
	}
	cfg.Timestamp = c.ts(i)
	if err := c.keepAll(i, cfg, members); err != nil {
		return err
	}
	id, err := c.add(i, at, cfg)
	if err != nil {
		return err
	}
	c.setTail(i, id)
	c.mark(i, agentimport.LineMapped)
	return nil
}

// keepAll keeps members as the event member, dropping them under Slim.
func (c *conv) keepAll(i int, e agentsession.Entry, members map[string]json.RawMessage) error {
	return c.keep(i, e, "event", members)
}

// itemEntry builds an item entry at event i's time.
func (c *conv) itemEntry(i int, item openresponses.Item, resp string) *agentsession.ItemEntry {
	e := agentsession.NewItemEntry(item)
	e.ResponseID = resp
	e.Timestamp = c.ts(i)
	return e
}

// message writes a MessageEvent: a person's or the environment's
// message as a user message, the model's as reasoning and an assistant
// message under a response.
func (c *conv) message(i int) error {
	m, _ := c.c.Events[i].Message()
	lm := m.LLMMessage
	if lm == nil {
		return c.undecodable(i, fmt.Errorf("the message event has no llm_message"))
	}
	at, err := c.attach(i)
	if err != nil {
		return err
	}
	src := c.c.Events[i].Source
	members, err := rest(m, "llm_message", "llm_response_id", "extended_content", "critic_result")
	if err != nil {
		return err
	}
	if members == nil {
		members = map[string]json.RawMessage{}
	}
	var ids []string
	add := func(e agentsession.Entry) error {
		id, err := c.add(i, at, e)
		if err != nil {
			return err
		}
		at = id
		return nil
	}
	if src == openhands.SourceAgent || lm.Role == "assistant" {
		resp := ""
		if src == openhands.SourceAgent && m.LLMResponseID != "" {
			if at, err = c.reconcile(i, at, nil); err != nil {
				return err
			}
			resp = c.responseID(i, m.LLMResponseID)
		} else {
			c.at(agentimport.Kept, "openhands/assistant-input", i, "an assistant message no response produced, as OpenHands writes for a refusal it sends in the model's place; it is an input item with no response")
		}
		reasons, provs, extra := c.reasoning(i, lm.ThinkingBlocks, lm.ResponsesReasoningItem, lm.ReasoningContent)
		for k, r := range reasons {
			e := c.itemEntry(i, r, resp)
			if err := c.reasoningMembers(i, e, provs[k], extra, k); err != nil {
				return err
			}
			if err := add(e); err != nil {
				return err
			}
			ids = append(ids, at)
		}
		parts, pm := c.outputParts(i, lm.Content)
		if len(parts) > 0 || len(reasons) == 0 {
			if parts == nil {
				parts = openresponses.Contents{}
			}
			e := c.itemEntry(i, &openresponses.Message{Role: openresponses.RoleAssistant, Content: parts, Status: openresponses.StatusCompleted}, resp)
			if err := c.messageMembers(i, e, lm, pm, members, true); err != nil {
				return err
			}
			if err := add(e); err != nil {
				return err
			}
			ids = append(ids, at)
		} else if err := c.messageMembersOnly(i, lm, pm, members); err != nil {
			return err
		}
		if len(m.ExtendedContent.Items) > 0 {
			c.at(agentimport.Dropped, "openhands/bad-content", i, "extended content on an assistant message, which OpenHands appends to it, is left out")
		}
		if resp != "" {
			if at, err = c.response(i, at, m.LLMResponseID, resp); err != nil {
				return err
			}
		}
	} else {
		c.mergeNote(i)
		parts, pm := c.inputParts(i, lm.Content, "message")
		ext, xm := c.inputParts(i, m.ExtendedContent, "extended content")
		parts = append(parts, ext...)
		if xm != nil {
			raw, _ := json.Marshal(xm)
			members["extended_content"] = raw
		}
		if parts == nil {
			parts = openresponses.Contents{}
		}
		e := c.itemEntry(i, openresponses.UserMessage(parts...), "")
		if src != openhands.SourceUser {
			if err := c.b.Member(e, "source", src); err != nil {
				return err
			}
			c.at(agentimport.Kept, "openhands/harness-user-message", i, "a user message the %s wrote, such as a nudge after an empty response or a hook's feedback; it carries %s:source", src, Source)
		}
		if err := c.messageMembers(i, e, lm, pm, members, false); err != nil {
			return err
		}
		if err := add(e); err != nil {
			return err
		}
		ids = append(ids, at)
	}
	c.items[i] = ids
	if at, err = c.critic(i, at, m.CriticResult, lastOf(ids)); err != nil {
		return err
	}
	c.setTail(i, at)
	c.mark(i, agentimport.LineMapped)
	return nil
}

func lastOf(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

// messageMembers keeps on e the members of the event and of its
// llm_message the conversion has no place for.
func (c *conv) messageMembers(i int, e agentsession.Entry, lm *openhands.Message, parts map[string]json.RawMessage, members map[string]json.RawMessage, assistant bool) error {
	if err := c.llmMessageRest(lm, parts, members, assistant); err != nil {
		return err
	}
	return c.keepAll(i, e, members)
}

// messageMembersOnly reports the members of a message that wrote no
// entry to keep them on.
func (c *conv) messageMembersOnly(i int, lm *openhands.Message, parts map[string]json.RawMessage, members map[string]json.RawMessage) error {
	if err := c.llmMessageRest(lm, parts, members, true); err != nil {
		return err
	}
	if len(members) > 0 {
		c.at(agentimport.Dropped, "openhands/event-members", i, "the message wrote only reasoning, which carries none of its members; they are left out")
	}
	return nil
}

func (c *conv) llmMessageRest(lm *openhands.Message, parts map[string]json.RawMessage, members map[string]json.RawMessage, assistant bool) error {
	mapped := []string{"role", "content"}
	if assistant {
		mapped = append(mapped, "reasoning_content", "thinking_blocks", "responses_reasoning_item")
	}
	r, err := rest(lm, mapped...)
	if err != nil {
		return err
	}
	if parts != nil {
		if r == nil {
			r = map[string]json.RawMessage{}
		}
		raw, _ := json.Marshal(parts)
		r["content"] = raw
	}
	if len(r) > 0 {
		raw, err := json.Marshal(r)
		if err != nil {
			return err
		}
		members["llm_message"] = raw
	}
	return nil
}

// reasoningMembers tags a reasoning item's provider, the first one's
// copy of reasoning_content that differs from it, and reports a
// signature.
func (c *conv) reasoningMembers(i int, e agentsession.Entry, provider, extra string, k int) error {
	if provider != "" {
		if err := c.b.Member(e, "provider", provider); err != nil {
			return err
		}
		c.at(agentimport.Gap, "agentimport/reasoning-provider", i, "a reasoning signature from %s in encrypted_content, tagged %s:provider", provider, Source)
	}
	if k == 0 && extra != "" {
		if err := c.b.Member(e, "reasoning_content", extra); err != nil {
			return err
		}
		c.at(agentimport.Kept, "openhands/reasoning-content", i, "reasoning_content differs from the reasoning items' text; it is kept in %s:reasoning_content", Source)
	}
	return nil
}

// mergeNote reports a user message OpenHands merges with the one before
// it in the view, which the session keeps apart.
func (c *conv) mergeNote(i int) {
	v := c.vs.view
	if len(v) == 0 {
		return
	}
	last := v[len(v)-1]
	if last.idx < 0 || c.plainUser(last.idx) {
		c.at(agentimport.Lossy, "openhands/merged-user-messages", i, "OpenHands sends this message merged into the user message before it, as one turn; the session keeps the two items")
	}
}

// plainUser reports whether event j is a plain user message, which
// OpenHands merges with an adjacent one.
func (c *conv) plainUser(j int) bool {
	e := c.c.Events[j]
	switch e.Kind {
	case openhands.KindCondensationSummary:
		return true
	case openhands.KindMessage:
		m, err := e.Message()
		if err != nil || m.LLMMessage == nil {
			return false
		}
		lm := m.LLMMessage
		return lm.Role == "user" && lm.ToolCalls == nil && lm.ToolCallID == "" && lm.Name == ""
	}
	return false
}

// responseID is the response's ID: the event's llm_response_id, or one
// made from it when another response has it.
func (c *conv) responseID(i int, native string) string {
	if !c.respIDs[native] {
		c.respIDs[native] = true
		return native
	}
	id := native + "_2"
	for n := 3; c.respIDs[id]; n++ {
		id = fmt.Sprintf("%s_%d", native, n)
	}
	c.respIDs[id] = true
	c.at(agentimport.Repaired, "openhands/response-id-repeated", i, "response ID %q is another response's; this one is %s", native, id)
	return id
}

// response writes the response entry of event i's model call, with
// its usage, model and latency joined from base_state by the native
// response ID. A response a fork copied keeps its usage in a member.
func (c *conv) response(i int, at, native, id string) (string, error) {
	e := &agentsession.ResponseEntry{ResponseID: id, Status: openresponses.ResponseStatusCompleted}
	e.Timestamp = c.ts(i)
	if err := c.joinUsage(i, e, native); err != nil {
		return "", err
	}
	return c.add(i, at, e)
}

// critic writes an event's critic_result as an eval outcome on target.
func (c *conv) critic(i int, at string, raw json.RawMessage, target string) (string, error) {
	if openhands.IsNull(raw) {
		return at, nil
	}
	if target == "" {
		c.at(agentimport.Dropped, "openhands/critic", i, "a critic_result on an event that wrote no entry to judge is left out")
		return at, nil
	}
	var cr struct {
		Score    json.RawMessage `json:"score"`
		Message  json.RawMessage `json:"message"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if json.Unmarshal(raw, &cr) != nil {
		c.at(agentimport.Dropped, "openhands/critic", i, "a critic_result that is not an object is left out")
		return at, nil
	}
	o := agentsession.NewOutcomeEntry(agentsession.OutcomeEval, target)
	if f, ok := openhands.Float(cr.Score); ok {
		o.WithScore(f)
	}
	clean, err := c.clean(i, raw)
	if err != nil {
		return "", err
	}
	o.Details = clean
	o.Timestamp = c.ts(i)
	id, err := c.add(i, at, o)
	if err != nil {
		return "", err
	}
	c.at(agentimport.Lossy, "openhands/critic", i, "the critic's result is an eval outcome on the entry it judged, its score the critic's own and the result in details")
	return id, nil
}

// batchOf returns the actions of the response event i starts: it and
// the actions of the same llm_response_id that follow it on its chain,
// past the outputs written between them. split says an output came
// between two of them.
func (c *conv) batchOf(i int) (batch []int, split bool) {
	resp := c.info[i].respID
	batch = []int{i}
	calls := map[string]bool{c.info[i].toolCallID: true}
	between := false
	for j := i + 1; j < len(c.c.Events); j++ {
		if c.parent[j] != j-1 || c.c.Events[j].Err != nil || c.info[j].bad != nil {
			break
		}
		in := c.info[j]
		if in.kind == openhands.KindAction && in.respID == resp && resp != "" {
			batch = append(batch, j)
			calls[in.toolCallID] = true
			if between {
				split = true
			}
			continue
		}
		if openhands.IsObservationLike(in.kind) && calls[in.toolCallID] {
			between = true
			continue
		}
		break
	}
	return batch, split
}

// action writes a response of tool calls: the first action's reasoning
// and thought, every call of the batch, and the response entry.
func (c *conv) action(i int) error {
	at, err := c.attach(i)
	if err != nil {
		return err
	}
	if at, err = c.reconcile(i, at, nil); err != nil {
		return err
	}
	batch, split := c.batchOf(i)
	if split {
		c.at(agentimport.Lossy, "openhands/split-batch", i, "an output was written between the calls of this response, as for an unknown tool; OpenHands sends the response as several assistant messages around it, the session as one response before the outputs")
	}
	a0, _ := c.c.Events[i].Action()
	native := a0.LLMResponseID
	if native == "" {
		native = "resp_" + c.c.Events[i].ID
		c.at(agentimport.Repaired, "openhands/no-response-id", i, "the action names no llm_response_id; its response is %s", native)
	}
	resp := c.responseID(i, native)
	add := func(j int, e agentsession.Entry) (string, error) {
		id, err := c.add(j, at, e)
		if err != nil {
			return "", err
		}
		at = id
		return id, nil
	}
	reasons, provs, extra := c.reasoning(i, a0.ThinkingBlocks, a0.ResponsesReasoningItem, a0.ReasoningContent)
	for k, r := range reasons {
		e := c.itemEntry(i, r, resp)
		if err := c.reasoningMembers(i, e, provs[k], extra, k); err != nil {
			return err
		}
		id, err := add(i, e)
		if err != nil {
			return err
		}
		c.items[i] = append(c.items[i], id)
	}
	parts, pm := c.outputParts(i, a0.Thought)
	if len(parts) > 0 {
		e := c.itemEntry(i, &openresponses.Message{Role: openresponses.RoleAssistant, Content: parts, Status: openresponses.StatusCompleted}, resp)
		if pm != nil {
			raw, _ := json.Marshal(pm)
			if err := c.keep(i, e, "thought", map[string]json.RawMessage{"content": raw}); err != nil {
				return err
			}
		}
		id, err := add(i, e)
		if err != nil {
			return err
		}
		c.items[i] = append(c.items[i], id)
	}
	type judged struct {
		j      int
		raw    json.RawMessage
		target string
	}
	var critics []judged
	for _, j := range batch {
		a, _ := c.c.Events[j].Action()
		if j != i && (len(a.Thought.Items) > 0 || a.ReasoningContent != "" || len(a.ThinkingBlocks) > 0 || a.ResponsesReasoningItem != nil) {
			c.at(agentimport.Dropped, "openhands/batch-member-content", j, "thought or reasoning on an action after the first of its response, which OpenHands does not send, is left out")
		}
		fc, members, err := c.functionCall(j, a)
		if err != nil {
			return err
		}
		e := c.itemEntry(j, fc, resp)
		if err := c.keepAll(j, e, members); err != nil {
			return err
		}
		id, err := add(j, e)
		if err != nil {
			return err
		}
		c.recordCall(j, a, fc, id)
		c.items[j] = append(c.items[j], id)
		if j != i {
			c.done[j] = true
		}
		if !openhands.IsNull(a.CriticResult) {
			critics = append(critics, judged{j, a.CriticResult, id})
		}
	}
	if at, err = c.response(i, at, native, resp); err != nil {
		return err
	}
	for _, cr := range critics {
		if at, err = c.critic(cr.j, at, cr.raw, cr.target); err != nil {
			return err
		}
	}
	c.setTail(i, at)
	c.mark(i, agentimport.LineMapped)
	return nil
}

// functionCall maps an action's tool call to a function_call, with the
// members of the action the item has no place for.
func (c *conv) functionCall(j int, a *openhands.ActionEvent) (*openresponses.FunctionCall, map[string]json.RawMessage, error) {
	tc := a.ToolCall
	native, name, args, itemID := a.ToolCallID, a.ToolName, "", ""
	mapped := []string{"thought", "reasoning_content", "thinking_blocks", "responses_reasoning_item", "tool_call", "llm_response_id", "critic_result"}
	var tcRest map[string]json.RawMessage
	if tc != nil {
		if tc.ID != "" {
			native = tc.ID
		}
		if tc.Name != "" {
			name = tc.Name
		}
		args, itemID = tc.Arguments, tc.ResponsesItemID
		var err error
		if tcRest, err = rest(tc, "id", "name", "arguments", "responses_item_id"); err != nil {
			return nil, nil, err
		}
		if a.ToolCallID != "" && a.ToolCallID != tc.ID {
			c.at(agentimport.Repaired, "openhands/call-id-mismatch", j, "tool_call_id %q differs from the tool call's id %q; the call is the tool call's, and outputs naming either answer it", a.ToolCallID, tc.ID)
		}
	} else {
		args = "{}"
		c.at(agentimport.Lossy, "openhands/no-tool-call", j, "the action has no tool_call; the call is made from its tool_name and tool_call_id with empty arguments")
	}
	if a.ToolName == name || a.ToolName == "" {
		mapped = append(mapped, "tool_name")
	}
	if a.ToolCallID == native {
		mapped = append(mapped, "tool_call_id")
	}
	id, renamed := c.b.Calls.Assign(native)
	fc := &openresponses.FunctionCall{CallID: id, Name: name, Arguments: args, ID: itemID, Status: openresponses.StatusCompleted}
	members, err := rest(a, mapped...)
	if err != nil {
		return nil, nil, err
	}
	if len(tcRest) > 0 {
		if members == nil {
			members = map[string]json.RawMessage{}
		}
		raw, _ := json.Marshal(tcRest)
		members["tool_call"] = raw
	}
	if renamed {
		if members == nil {
			members = map[string]json.RawMessage{}
		}
		raw, _ := json.Marshal(native)
		members["call_id"] = raw
		c.at(agentimport.Repaired, "openhands/call-id", j, "call ID %q is repeated or not one every provider takes; the call is %s, and the native ID is in %s:event", native, id, Source)
	}
	return fc, members, nil
}

// recordCall remembers a call written as entry, by both of its native
// IDs.
func (c *conv) recordCall(j int, a *openhands.ActionEvent, fc *openresponses.FunctionCall, entry string) {
	cl := &call{id: fc.CallID, name: fc.Name, entry: entry, event: j, hasAction: c.info[j].hasAction}
	if a.ToolCall != nil && a.ToolCall.ID != "" {
		cl.native = a.ToolCall.ID
		c.calls[a.ToolCall.ID] = cl
	}
	if a.ToolCallID != "" {
		if cl.native == "" {
			cl.native = a.ToolCallID
		}
		c.calls[a.ToolCallID] = cl
	}
	if !cl.hasAction {
		c.at(agentimport.Lossy, "openhands/invalid-call", j, "OpenHands could not run this call: its tool is unknown or its arguments did not validate, and the stored arguments are as OpenHands repaired or replaced them")
	}
}

// output writes an observation, a rejection or an agent error: the
// decision that ended the call when it did not run, the sub-agents the
// call ran, and the function_call_output with the text the model was
// sent.
func (c *conv) output(i int) error {
	ev := c.c.Events[i]
	native := c.info[i].toolCallID
	cl := c.calls[native]
	at, err := c.attach(i)
	if err != nil {
		return err
	}
	switch {
	case cl == nil:
		c.at(agentimport.Kept, "openhands/orphan-output", i, "the %s answers call %q, which no action before it on its path made; OpenHands leaves it out of the context, and it is a custom entry", ev.Kind, native)
		return c.customEvent(i, at, "")
	case cl.answered:
		c.at(agentimport.Kept, "openhands/duplicate-output", i, "a second answer to call %s; OpenHands sends the first alone, and this one is a custom entry", cl.id)
		return c.customEvent(i, at, cl.id)
	}
	var parts []outPart
	var members map[string]json.RawMessage
	var decision *agentsession.DecisionEntry
	isError := false
	switch ev.Kind {
	case openhands.KindObservation:
		o, _ := ev.Observation()
		if o.Observation == nil {
			c.at(agentimport.Lossy, "openhands/no-observation", i, "the observation event has no observation; the output is empty")
		} else {
			parts = c.render(i, o.Observation)
			isError = o.Observation.IsError
			kind, _ := c.keeping()
			if c.slim {
				c.at(kind, "openhands/observation", i, "the structured %s beside the output rendered from it is left out", o.Observation.Kind)
			} else {
				c.at(kind, "openhands/observation", i, "the structured %s is kept in %s:observation beside the output rendered from it", o.Observation.Kind, Source)
			}
		}
		ext, xm := c.inputParts(i, o.ExtendedContent, "extended content")
		for _, p := range ext {
			if t, ok := p.(*openresponses.InputText); ok {
				parts = append(parts, outPart{text: t.Text, isText: true})
			}
		}
		if members, err = rest(o, "tool_name", "tool_call_id", "observation", "action_id", "extended_content"); err != nil {
			return err
		}
		if xm != nil {
			if members == nil {
				members = map[string]json.RawMessage{}
			}
			raw, _ := json.Marshal(xm)
			members["extended_content"] = raw
		}
		if !c.slim && o.Observation != nil {
			if members == nil {
				members = map[string]json.RawMessage{}
			}
			raw, err := json.Marshal(o.Observation)
			if err != nil {
				return err
			}
			members["observation"] = raw
		}
	case openhands.KindUserReject:
		o, _ := ev.UserReject()
		reason := o.RejectionReason
		if !o.Has("rejection_reason") {
			reason = openhands.DefaultRejectionReason
		}
		by := agentsession.ByHuman
		if o.RejectionSource == "hook" {
			by = agentsession.ByPolicy
		}
		decision = agentsession.NewDecision(cl.id, cl.entry, agentsession.VerdictReject, by).WithReason(reason)
		parts = []outPart{{text: "Action rejected: " + reason, isText: true}}
		if members, err = rest(o, "tool_name", "tool_call_id", "action_id", "rejection_reason", "rejection_source"); err != nil {
			return err
		}
		c.at(agentimport.Lossy, "openhands/rejected", i, "rejected by the %s: a reject decision by %s before the output OpenHands sent", map[bool]string{true: "user", false: "hook"}[by == agentsession.ByHuman], by)
	case openhands.KindAgentError:
		o, _ := ev.AgentError()
		parts = []outPart{{text: o.Error, isText: true}}
		switch {
		case !cl.hasAction:
			decision = agentsession.NewDecision(cl.id, cl.entry, agentsession.VerdictReject, agentsession.ByPolicy).WithReason(nonEmpty(o.Error, "the call could not be run"))
			c.at(agentimport.Lossy, "openhands/rejected", i, "OpenHands refused a call it could not run: a reject decision by policy before the error it sent")
		case strings.HasPrefix(o.Error, openhands.InterruptedPrefix):
			decision = agentsession.NewDecision(cl.id, cl.entry, agentsession.VerdictAnswer, agentsession.ByPolicy).WithReason(o.Error)
			c.at(agentimport.Lossy, "openhands/interrupted", i, "the call was cut by a pause or a crash: an answer decision by policy before the output OpenHands wrote for it")
		default:
			isError = true
		}
		if members, err = rest(o, "tool_name", "tool_call_id", "error"); err != nil {
			return err
		}
	}
	if decision != nil {
		decision.Timestamp = c.ts(i)
		if at, err = c.add(i, at, decision); err != nil {
			return err
		}
	}
	refs, at, err := c.linkChildren(i, at, cl)
	if err != nil {
		return err
	}
	out := c.outputData(i, parts)
	e := c.itemEntry(i, &openresponses.FunctionCallOutput{CallID: cl.id, Output: out, Status: openresponses.StatusCompleted}, "")
	e.Parents = refs
	if isError {
		if err := c.b.Member(e, "is_error", true); err != nil {
			return err
		}
		c.at(agentimport.Gap, "agentimport/tool-error", i, "the call failed; the output carries %s:is_error", Source)
	}
	if err := c.keepAll(i, e, members); err != nil {
		return err
	}
	id, err := c.add(i, at, e)
	if err != nil {
		return err
	}
	at = id
	cl.answered = true
	c.items[i] = []string{id}
	if at, err = c.peerChildren(i, at, cl); err != nil {
		return err
	}
	c.setTail(i, at)
	c.mark(i, agentimport.LineMapped)
	return nil
}

func nonEmpty(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

// customEvent writes event i whole as a custom entry after at.
func (c *conv) customEvent(i int, at, callID string) error {
	raw, err := c.clean(i, c.c.Events[i].Raw)
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: raw, CallID: callID}
	e.Timestamp = c.ts(i)
	id, err := c.add(i, at, e)
	if err != nil {
		return err
	}
	c.setTail(i, id)
	c.mark(i, agentimport.LineKept)
	return nil
}

// undecodable keeps an event whose members do not fit its kind as a
// custom entry.
func (c *conv) undecodable(i int, err error) error {
	c.at(agentimport.Kept, "openhands/undecodable-event", i, "the %s does not decode as its kind (%s), which OpenHands would refuse to load; it is a custom entry", c.c.Events[i].Kind, agentimport.ErrorText(err))
	at, aerr := c.attach(i)
	if aerr != nil {
		return aerr
	}
	return c.customEvent(i, at, "")
}

// summaryEvent writes a CondensationSummaryEvent, which OpenHands
// synthesizes and never writes, as the user message it would send.
func (c *conv) summaryEvent(i int) error {
	at, err := c.attach(i)
	if err != nil {
		return err
	}
	var s struct {
		Summary string `json:"summary"`
	}
	_ = json.Unmarshal(c.c.Events[i].Raw, &s)
	e := c.itemEntry(i, openresponses.UserText(s.Summary), "")
	c.mergeNote(i)
	c.at(agentimport.Lossy, "openhands/summary-event", i, "a condensation summary written as an event, which OpenHands only synthesizes; it is the user message OpenHands would send")
	id, err := c.add(i, at, e)
	if err != nil {
		return err
	}
	c.items[i] = []string{id}
	c.setTail(i, id)
	c.mark(i, agentimport.LineMapped)
	return nil
}

// slimDropped are the record kinds Slim drops: repeats of the state,
// token IDs, raw completion logs and stream fragments.
var slimDropped = map[string]bool{
	openhands.KindStateUpdate:    true,
	openhands.KindToken:          true,
	openhands.KindCompletionLog:  true,
	openhands.KindStreamingDelta: true,
}

// record writes an event that never enters the context as a custom
// entry, or drops it under Slim when it repeats or logs what the
// session holds.
func (c *conv) record(i int) error {
	ev := c.c.Events[i]
	at, err := c.attach(i)
	if err != nil {
		return err
	}
	if !openhands.IsKnownKind(ev.Kind) {
		c.at(agentimport.Kept, "openhands/unknown-kind", i, "an event of kind %q, which this converter does not know, is a custom entry", ev.Kind)
		return c.customEvent(i, at, "")
	}
	if c.slim && slimDropped[ev.Kind] {
		c.at(agentimport.Dropped, "openhands/record", i, "the %s is left out", ev.Kind)
		c.setTail(i, at)
		c.mark(i, agentimport.LineDropped)
		return nil
	}
	callID := ""
	if ev.Kind == openhands.KindHookExecution {
		var h struct {
			ActionID string `json:"action_id"`
		}
		if json.Unmarshal(ev.Raw, &h) == nil && h.ActionID != "" {
			for _, cl := range c.calls {
				if c.c.Events[cl.event].ID == h.ActionID {
					callID = cl.id
					break
				}
			}
		}
	}
	c.at(agentimport.Kept, "openhands/record", i, "the %s, which never enters the context, is a custom entry", ev.Kind)
	return c.customEvent(i, at, callID)
}
