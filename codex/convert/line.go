package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/codex"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// line converts one line in file order.
func (c *converter) line(l *codex.Line) error {
	if l.Err != nil {
		return c.custom(l, "codex/undecodable-line", "a line that is not a rollout line (%s) is a custom entry", agentimport.ErrorText(l.Err))
	}
	if l.PayloadErr != nil {
		return c.custom(l, "codex/undecodable-payload", "a %s line whose payload does not decode (%s) is a custom entry", l.Kind, agentimport.ErrorText(l.PayloadErr))
	}
	if ms := l.MistypedMembers(); len(ms) > 0 {
		c.addf(agentimport.Kept, "codex/mistyped-member", l,
			"members whose value is not of the type Codex defines are kept as written beside the object's other members: %s", strings.Join(ms, ", "))
	}
	switch l.Kind {
	case codex.TypeSessionMeta:
		if err := c.closePending(l); err != nil {
			return err
		}
		return c.custom(l, "codex/copied-session-meta", "a later session meta, a forked thread's copy of an ancestor's, is a custom entry")
	case codex.TypeResponseItem:
		return c.item(l)
	case codex.TypeEventMsg:
		return c.event(l)
	case codex.TypeTurnContext:
		return c.turnContext(l)
	case codex.TypeCompacted:
		return c.compacted(l)
	case codex.TypeInterAgentCommunication:
		return c.interAgent(l)
	case codex.TypeTokenUsageRecord:
		return c.usageRecord(l)
	case codex.TypeWorldState:
		c.addOnce(agentimport.Lossy, "codex/world-state", l,
			"Codex renders its world state into each request; the rollout records the state, not the text the model saw, so it is not in the session's context")
		return c.custom(l, "codex/record", "a world_state record is a custom entry")
	case codex.TypeInterAgentCommunicationMetadata, codex.TypeRetainedContext, codex.TypeSecurityRiskScore,
		codex.TypeRealtimeItem, codex.TypeState:
		return c.custom(l, "codex/record", "a %s record, which the model does not see, is a custom entry", l.Kind)
	}
	return c.custom(l, "codex/unknown-line-type", "a line of unknown type %q is a custom entry", l.Type)
}

// harnessMeta is what a response item's harness metadata says that
// bears on the conversion.
type harnessMeta struct {
	HarnessAuthoredConfiguration bool    `json:"harness_authored_configuration"`
	DeliveredAssistantMessage    *string `json:"delivered_assistant_message"`
	CompactionOutput             bool    `json:"compaction_output"`
	InheritedUserMessage         bool    `json:"inherited_user_message"`
	TokenLimitOverride           *int64  `json:"fallback_token_limit_override"`
}

// outputTypes are the item types a model's response holds.
var outputTypes = map[string]bool{
	codex.ItemReasoning: true, codex.ItemFunctionCall: true, codex.ItemCustomToolCall: true,
	codex.ItemLocalShellCall: true, codex.ItemWebSearchCall: true, codex.ItemImageGenerationCall: true,
	codex.ItemToolSearchCall: true, codex.ItemCompaction: true, codex.ItemCompactionSummary: true,
	codex.ItemContextCompaction: true,
}

// isOutput reports whether an item is model output: part of a response,
// rather than input the harness, a tool or a person gave.
func isOutput(it *codex.Item, hm harnessMeta) bool {
	switch it.Type {
	case codex.ItemMessage:
		// An assistant message carrying another agent's instruction as
		// JSON is input Codex wrote, and starts a turn.
		return it.Role == "assistant" && hm.DeliveredAssistantMessage == nil && !hm.CompactionOutput &&
			!hm.InheritedUserMessage && !interAgentText(it.Parts())
	case codex.ItemToolSearchOutput:
		// A hosted tool search is answered in the same response.
		return it.Execution == "server"
	}
	return outputTypes[it.Type]
}

// callTypes are the item types that make a call; outputTypes the ones
// that answer one.
var (
	callTypes = map[string]bool{
		codex.ItemFunctionCall: true, codex.ItemCustomToolCall: true,
		codex.ItemLocalShellCall: true, codex.ItemToolSearchCall: true,
	}
	answerTypes = map[string]bool{
		codex.ItemFunctionCallOutput: true, codex.ItemCustomToolCallOutput: true, codex.ItemToolSearchOutput: true,
	}
)

// item converts a response item: model output joins the response being
// gathered; input closes it and is written.
func (c *converter) item(l *codex.Line) error {
	it := l.Item
	var hm harnessMeta
	_ = json.Unmarshal(l.Metadata, &hm)
	switch {
	case !codex.ItemTypes[it.Type]:
		return c.custom(l, "codex/unknown-item", "an item of type %q, which Codex reads as Other and neither sends nor keeps, is a custom entry", it.Type)
	case it.Type == codex.ItemGhostSnapshot:
		return c.custom(l, "codex/ghost-snapshot", "a ghost snapshot, the git state Codex kept for undo and never sent the model, is a custom entry")
	case it.Type == codex.ItemCompactionTrigger:
		return c.custom(l, "codex/not-sent", "a compaction trigger, a request control and not history, is a custom entry")
	case it.Type == codex.ItemMessage && it.Role == "system":
		return c.custom(l, "codex/system-message", "a system message, which Codex never keeps in the history it sends, is a custom entry")
	case it.Type == codex.ItemConfigurationUpdate && !hm.HarnessAuthoredConfiguration:
		return c.custom(l, "codex/not-sent", "a configuration update not written by the harness, which Codex does not send, is a custom entry")
	}
	output := isOutput(it, hm)
	if output {
		if p := c.pending; p != nil && p.closesOnOutput() {
			// The response that was gathered is over: this output is the
			// next one's.
			if err := c.closePending(l); err != nil {
				return err
			}
		}
	} else if !answerTypes[it.Type] {
		// Input other than a tool's output starts a step or a turn.
		if err := c.closePending(l); err != nil {
			return err
		}
		if boundaryItem(it) {
			c.reportUnanswered(l)
		}
	}
	if answerTypes[it.Type] && it.CallID != "" && !output {
		if _, ok := c.st.calls[it.CallID]; !ok {
			return c.custom(l, "codex/orphan-output", "an output whose call is not on its path, which Codex drops from what it sends, is a custom entry")
		}
	}
	item, extra, err := c.convertItem(l, l.PayloadRaw(), it)
	if err != nil {
		return c.custom(l, "codex/undecodable-item", "a %s item that does not decode as one (%s) is a custom entry", it.Type, agentimport.ErrorText(err))
	}
	if answerTypes[it.Type] && c.pending != nil && !output {
		c.pending.inputSeen = true
	}
	if r, ok := item.(*openresponses.ReasoningItem); ok && len(r.Content) > 0 {
		c.rawReasoning = true
	}
	e := agentsession.NewItemEntry(item)
	if !output && it.Type == codex.ItemMessage && it.Role == "assistant" {
		if err := c.b.Member(e, "input", true); err != nil {
			return err
		}
		c.addf(agentimport.Kept, "codex/assistant-input", l,
			"an assistant message the harness wrote (a delivered or inherited message, compaction output, another agent's instruction) is input, not a response's output; it is marked %s:input", Source)
	}
	if err := c.customMembers(e, l, it); err != nil {
		return err
	}
	if err := c.itemMembers(e, l, item, extra, hm); err != nil {
		return err
	}
	if output {
		if c.pending == nil {
			c.pending = &pending{first: l}
		}
		if len(c.pending.outputs) == 0 {
			// A response is named, and its findings placed, by its
			// first item, not by the count that opened it.
			c.pending.first = l
		}
		c.pending.outputs = append(c.pending.outputs, e)
	}
	if err := c.emit(e, l); err != nil {
		return err
	}
	c.mark(l, agentimport.LineMapped)
	return nil
}

// itemMembers reports what an item became and sets the members that
// keep what it could not hold.
func (c *converter) itemMembers(e *agentsession.ItemEntry, l *codex.Line, item openresponses.Item, extra map[string]json.RawMessage, hm harnessMeta) error {
	if err := c.itemGaps(e, l, item); err != nil {
		return err
	}
	if fo, ok := item.(*openresponses.FunctionCallOutput); ok {
		c.checkTruncation(l, fo.Output.String(), hm)
	}
	if err := c.extraMembers(e, l, extra); err != nil {
		return err
	}
	if err := c.member(e, l, "metadata", "codex/metadata", l.Metadata, "the item's harness metadata, which the model never saw, is"); err != nil {
		return err
	}
	return c.lineMembers(e, l)
}

// extraMembers keeps the members of a native item its Open Responses
// item has no place for in codex:item.
func (c *converter) extraMembers(e agentsession.Entry, l *codex.Line, extra map[string]json.RawMessage) error {
	if native, ok := extra["call_id"]; ok {
		// A renamed call keeps its native id where every converter
		// keeps one.
		if err := c.b.Member(e, "call_id", native); err != nil {
			return err
		}
		delete(extra, "call_id")
	}
	if len(extra) == 0 {
		return nil
	}
	raw, err := json.Marshal(extra)
	if err != nil {
		return err
	}
	return c.member(e, l, "item", "codex/item-members", raw, "item members "+memberNames(extra)+", which the item has no place for and Codex sent beside it, are")
}

// itemGaps reports what the session format and openresponses have no
// place for in an item: a type openresponses does not type, encrypted
// reasoning untagged by provider. e, when not nil, is the entry that
// carries the item and takes codex:provider.
func (c *converter) itemGaps(e agentsession.Entry, l *codex.Line, item openresponses.Item) error {
	switch v := item.(type) {
	case *openresponses.UnknownItem:
		if strings.HasPrefix(v.Type, "openai.") {
			c.addf(agentimport.Kept, "codex/openai-item", l,
				"an OpenAI item type openresponses does not type is the extension item %s, its body verbatim", v.Type)
		} else {
			c.addf(agentimport.Kept, "codex/codex-item", l,
				"an item of Codex's own is the extension item %s, its body verbatim", v.Type)
		}
	case *openresponses.ReasoningItem:
		if v.EncryptedContent == "" {
			return nil
		}
		if e != nil {
			provider := "openai"
			if m := c.meta; m != nil && m.Meta.ModelProvider != "" {
				provider = m.Meta.ModelProvider
			}
			if err := c.b.Member(e, "provider", provider); err != nil {
				return err
			}
		}
		c.addf(agentimport.Gap, "agentimport/reasoning-provider", l,
			"the encrypted reasoning is in encrypted_content, which says nothing of the provider that can replay it; the entry carries %s:provider", Source)
	}
	return nil
}

// checkTruncation reports a tool output longer than the budget Codex
// cut tool output to before it sent it.
func (c *converter) checkTruncation(l *codex.Line, text string, hm harnessMeta) {
	limit := c.st.truncation
	if hm.TokenLimitOverride != nil {
		limit = *hm.TokenLimitOverride * 4
	}
	if limit > 0 && int64(len(text)) > limit {
		c.addf(agentimport.Lossy, "codex/output-truncation", l,
			"the output is %d bytes; Codex cut what it sent to about %d bytes by the model's truncation policy, and the output is written whole, as recorded", len(text), limit)
	}
}

// convertItem decodes a Codex item as an Open Responses item, and
// returns the members the item could not hold. A call or output gets
// the call id recorded for it; a type openresponses does not type is an
// extension item, carried verbatim. An item that does not decode
// changes nothing: no call id is taken, no call recorded.
func (c *converter) convertItem(l *codex.Line, raw json.RawMessage, it *codex.Item) (openresponses.Item, map[string]json.RawMessage, error) {
	raw, err := c.clean(l, raw)
	if err != nil {
		return nil, nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, nil, err
	}
	if it.Type == codex.ItemCompactionSummary {
		obj["type"] = json.RawMessage(`"compaction"`)
	}
	if _, _, err := buildItem(obj, it, ""); err != nil {
		return nil, nil, err
	}
	if it.Type == codex.ItemCompactionSummary {
		c.addf(agentimport.Repaired, "codex/compaction-alias", l, "an item of type compaction_summary, which Codex reads as compaction, is a compaction item")
	}
	// The call id recorded: a native id another call holds, or that a
	// provider would refuse, is renamed; an output takes its call's. A
	// function call, and a custom tool call written as one, needs an id
	// the format accepts even when Codex recorded an empty one; the
	// other calls are extension items, carried as they are. An output
	// with no call id stands alone, as Codex keeps it.
	id := ""
	switch {
	case callTypes[it.Type] && (it.CallID != "" || it.Type == codex.ItemFunctionCall || it.Type == codex.ItemCustomToolCall):
		var renamed bool
		id, renamed = c.b.Calls.Assign(it.CallID)
		if renamed {
			c.addf(agentimport.Repaired, "codex/call-id-renamed", l,
				"call id %q is held by another call of the session, is empty, or has characters providers refuse; it is %q, and the native one is %s:call_id", it.CallID, id, Source)
		}
		c.st.addCall(it.CallID, id)
	case answerTypes[it.Type] && it.CallID != "":
		if rec, ok := c.st.calls[it.CallID]; ok {
			id = rec
			c.st.answer(rec)
		}
	}
	return buildItem(obj, it, id)
}

// buildItem decodes a native item, with call id id in place of its own
// when id is set, and returns the members the item could not hold.
func buildItem(native map[string]json.RawMessage, it *codex.Item, id string) (openresponses.Item, map[string]json.RawMessage, error) {
	obj := make(map[string]json.RawMessage, len(native))
	for k, v := range native {
		obj[k] = v
	}
	renamed := id != "" && id != it.CallID
	if renamed {
		obj["call_id"], _ = json.Marshal(id)
	}
	nativeID := func(extra map[string]json.RawMessage) map[string]json.RawMessage {
		if renamed {
			if extra == nil {
				extra = map[string]json.RawMessage{}
			}
			extra["call_id"], _ = json.Marshal(it.CallID)
		}
		return extra
	}
	fobj, consumed, err := asFunction(obj, it)
	if err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(fobj)
	if err != nil {
		return nil, nil, err
	}
	item, err := openresponses.UnmarshalItem(data)
	if err != nil {
		return nil, nil, err
	}
	if consumed != nil {
		// A custom tool call or its output, written as a function call
		// or its output: the members it did not use are kept.
		extra := map[string]json.RawMessage{}
		for k, v := range nonNull(native) {
			if !consumed[k] {
				extra[k] = v
			}
		}
		return item, nativeID(extra), nil
	}
	if u, ok := item.(*openresponses.UnknownItem); ok {
		if data, err = extension(fobj); err != nil {
			return nil, nil, err
		}
		u.Type, u.Raw = extensionType(it.Type), data
		return u, nativeID(nil), nil
	}
	out, err := json.Marshal(item)
	if err != nil {
		return nil, nil, err
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		return nil, nil, err
	}
	extra := map[string]json.RawMessage{}
	for k, v := range fobj {
		if !sameJSON(v, back[k]) {
			extra[k] = v
		}
	}
	return item, nativeID(extra), nil
}

// sameJSON reports whether two JSON values are the same once members
// that say nothing (null, false, "", [], {}) are left out of both, the
// difference between a member written empty and one left out.
func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if len(a) > 0 && json.Unmarshal(a, &x) != nil {
		return false
	}
	if len(b) > 0 && json.Unmarshal(b, &y) != nil {
		return false
	}
	return equalPruned(prune(x), prune(y))
}

func prune(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			if p := prune(x); p != nil {
				out[k] = p
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, x := range v {
			p := prune(x)
			if p == nil {
				p = map[string]any{}
			}
			out = append(out, p)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case string:
		if v == "" {
			return nil
		}
	case bool:
		if !v {
			return nil
		}
	case nil:
		return nil
	}
	return v
}

func equalPruned(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// pending is a response being gathered: its output items, the entries
// written while it was, and the usage and response id that complete it.
type pending struct {
	first   *codex.Line
	outputs []*agentsession.ItemEntry
	queue   []queued
	// inputSeen says a tool's output was queued after the outputs began:
	// the next output is the next response's, unless fromCount.
	inputSeen bool
	// fromCount says the response was opened by its token count, as
	// Codex wrote them up to 0.65: the count first, then the response's
	// items in one batch, each call followed by its output.
	fromCount bool
	// usage is the response's usage, from a token usage record or a
	// token_count event; usageLine is the line it came from.
	usage     *codex.TokenUsage
	usageLine *codex.Line
	// count is the token_count line the response took its running total
	// and rate limits from.
	count      *codex.Line
	tokenCount json.RawMessage
	rateLimits json.RawMessage
	responseID string
	record     *codex.Line
}

// closesOnOutput reports whether an output item arriving now starts
// the next response: the response completed (its usage arrived) or a
// tool's output followed it, unless Codex wrote the response as a batch
// opened by its count.
func (p *pending) closesOnOutput() bool {
	return !p.fromCount && (p.inputSeen || p.usage != nil)
}

// queued is an entry written while a response was gathered, with what
// was in force when it was.
type queued struct {
	e  agentsession.Entry
	l  *codex.Line
	st state
}

// closePending writes the response being gathered: the entries queued
// while it was, in order, with its response entry right after its last
// output item, where the format's request context of a response looks
// for its items. by is the line that closed it, nil at the end.
func (c *converter) closePending(by *codex.Line) error {
	p := c.pending
	if p == nil {
		return nil
	}
	c.pending = nil
	respID := p.responseID
	if respID == "" {
		respID = fmt.Sprintf("%s-line-%d", Source, p.first.No)
		c.addOnce(agentimport.Lossy, "codex/response-id", p.first,
			"the rollout names no response ids (only the token usage records of newer rollouts do); a response is named %s-line-<n> after its first item's line", Source)
	}
	outputs := map[agentsession.Entry]bool{}
	for _, e := range p.outputs {
		e.ResponseID = respID
		outputs[e] = true
	}
	// last is the index of the last output item; an item between the
	// first and the last that is not output (a tool's output, as Codex
	// recorded responses up to 0.65) interleaves them.
	last, first := -1, -1
	for i, q := range p.queue {
		if outputs[q.e] {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	interleaved := false
	for i := first + 1; first >= 0 && i < last; i++ {
		if _, ok := p.queue[i].e.(*agentsession.ItemEntry); ok && !outputs[p.queue[i].e] {
			interleaved = true
		}
	}
	if interleaved {
		c.addf(agentimport.Lossy, "codex/interleaved-outputs", p.first,
			"Codex recorded the response's calls each followed by its output; the response entry follows its last item, so the format's request context of the response holds only the items after the last output")
	}
	e, src, err := c.responseEntry(p, respID)
	if err != nil {
		return err
	}
	st := c.st
	written := false
	for i, q := range p.queue {
		c.st = q.st
		if err := c.appendNow(q.e, q.l); err != nil {
			return err
		}
		if i == last {
			if err := c.appendNow(e, src); err != nil {
				return err
			}
			written = true
		}
	}
	c.st = st
	if !written {
		return c.appendNow(e, src)
	}
	return nil
}

// responseEntry builds the response entry of a gathered response, and
// returns the line it is made from.
func (c *converter) responseEntry(p *pending, respID string) (*agentsession.ResponseEntry, *codex.Line, error) {
	e := &agentsession.ResponseEntry{ResponseID: respID, Model: c.st.model}
	src := p.first
	if p.usageLine != nil {
		src = p.usageLine
	}
	if p.usage != nil {
		e.Status = openresponses.ResponseStatusCompleted
		switch {
		case c.copiedLine(src):
			// The parent's response, copied into a fork or a sub-agent
			// that inherited its context: the parent paid for it.
			if err := c.b.Member(e, "usage", p.usage); err != nil {
				return nil, nil, err
			}
			if err := c.b.Member(e, "copied", true); err != nil {
				return nil, nil, err
			}
			c.addf(agentimport.Lossy, "codex/fork-copied-response", src,
				"the response was copied from the parent thread; its usage, which the parent's session counts, is %s:usage and not the entry's", Source)
		default:
			e.Usage = usage(p.usage)
			if p.usage.CacheWriteInputTokens != 0 || len(nonNull(p.usage.Extra)) > 0 {
				if err := c.b.Member(e, "usage", p.usage); err != nil {
					return nil, nil, err
				}
				c.addf(agentimport.Gap, "agentimport/usage-detail", src,
					"usage has members Responses usage lacks (cache writes); the native usage is %s:usage", Source)
			}
		}
	} else {
		c.addf(agentimport.Lossy, "codex/no-status", p.first,
			"no token count or usage record went with the response, which Codex writes when one completes; it has no status and no usage")
	}
	if c.st.model == "" {
		c.addOnce(agentimport.Lossy, "codex/no-model", p.first,
			"no turn context named the model before this response (older rollouts record none); the response has no model")
	}
	countLine := src
	if p.count != nil {
		countLine = p.count
	}
	if err := c.member(e, countLine, "token_count", "codex/token-count", p.tokenCount, "the thread's running total and context window are"); err != nil {
		return nil, nil, err
	}
	if err := c.member(e, countLine, "rate_limits", "codex/rate-limits", p.rateLimits, "the rate limits reported with the usage are"); err != nil {
		return nil, nil, err
	}
	if p.record != nil {
		if err := c.recordMembers(e, p.record); err != nil {
			return nil, nil, err
		}
	}
	return e, src, nil
}

// usage converts Codex's usage to the Responses shape, which it already
// is but for its names: input counts the cached input, as OpenAI counts
// it, and reasoning tokens are among the output.
func usage(u *codex.TokenUsage) *openresponses.Usage {
	ou := &openresponses.Usage{
		InputTokens:  int(u.InputTokens),
		OutputTokens: int(u.OutputTokens),
		TotalTokens:  int(u.TotalTokens),
	}
	ou.InputTokensDetails.CachedTokens = int(u.CachedInputTokens)
	ou.OutputTokensDetails.ReasoningTokens = int(u.ReasoningOutputTokens)
	if ou.TotalTokens == 0 {
		ou.TotalTokens = ou.InputTokens + ou.OutputTokens
	}
	return ou
}

// sameUsage reports whether two usages count the same tokens.
func sameUsage(a, b *codex.TokenUsage) bool {
	return a.InputTokens == b.InputTokens && a.CachedInputTokens == b.CachedInputTokens &&
		a.CacheWriteInputTokens == b.CacheWriteInputTokens && a.OutputTokens == b.OutputTokens &&
		a.ReasoningOutputTokens == b.ReasoningOutputTokens && a.TotalTokens == b.TotalTokens
}

// tokenCount gives a response its usage: the event's last response
// usage, with the running total and the rate limits kept beside it.
// Which response depends on the order Codex wrote: up to 0.65 the count
// opens the response whose items follow; later it completes the one
// being gathered, before or after its tool outputs.
func (c *converter) tokenCount(l *codex.Line) error {
	ev := l.Event
	if ev.Info == nil || ev.Info.LastTokenUsage == nil {
		return c.custom(l, "codex/rate-limits-only", "a token count with no usage, a rate limit report, is a custom entry")
	}
	if c.batch {
		if err := c.closePending(l); err != nil {
			return err
		}
		c.pending = &pending{first: l, fromCount: true}
	}
	p := c.pending
	if p == nil {
		return c.custom(l, "codex/unplaced-usage", "a token count with no response before it to complete is a custom entry")
	}
	if p.count != nil {
		if bytes.Equal(bytes.TrimSpace(p.count.Payload), bytes.TrimSpace(l.Payload)) {
			c.drop(l, "codex/token-count-repeat", "a token count identical to the one before it")
			return nil
		}
		return c.custom(l, "codex/token-count-extra", "a second token count for one response, with other numbers, is a custom entry")
	}
	switch {
	case p.usage == nil:
		p.usage, p.usageLine = ev.Info.LastTokenUsage, l
	case !sameUsage(p.usage, ev.Info.LastTokenUsage):
		c.addf(agentimport.Lossy, "codex/usage-snapshots", l,
			"the token count's usage differs from the usage record's for the same response; the record's, which names the response, is used")
	}
	p.count = l
	info := map[string]any{}
	if ev.Info.TotalTokenUsage != nil {
		info["total_token_usage"] = ev.Info.TotalTokenUsage
	}
	if ev.Info.ModelContextWindow != nil {
		info["model_context_window"] = *ev.Info.ModelContextWindow
	}
	for k, v := range nonNull(ev.Info.Extra) {
		info[k] = v
	}
	for k, v := range nonNull(ev.Extra) {
		info[k] = v
	}
	if len(info) > 0 {
		raw, err := json.Marshal(info)
		if err != nil {
			return err
		}
		p.tokenCount = raw
	}
	p.rateLimits = ev.RateLimits
	c.mark(l, agentimport.LineMapped)
	return nil
}

// usageRecord names the response being gathered and gives its usage:
// Codex writes it when the response completes, so it is the response's
// own usage, before any token count.
func (c *converter) usageRecord(l *codex.Line) error {
	r := l.UsageRecord
	p := c.pending
	if p == nil || p.responseID != "" || r.ResponseID == "" {
		return c.custom(l, "codex/record", "a token usage record with no response before it to name is a custom entry")
	}
	p.responseID, p.record = r.ResponseID, l
	if r.Usage != nil {
		if p.usage != nil && !sameUsage(p.usage, r.Usage) {
			c.addf(agentimport.Lossy, "codex/usage-snapshots", l,
				"the usage record's usage differs from the token count's for the same response; the record's, which names the response, is used")
		}
		p.usage, p.usageLine = r.Usage, l
	}
	c.mark(l, agentimport.LineMapped)
	return nil
}

// recordMembers keeps a token usage record's turn and thread totals.
func (c *converter) recordMembers(e agentsession.Entry, l *codex.Line) error {
	raw, err := json.Marshal(l.UsageRecord)
	if err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return err
	}
	delete(all, "response_id")
	delete(all, "usage")
	all = nonNull(all)
	if len(all) == 0 {
		return nil
	}
	raw, err = json.Marshal(all)
	if err != nil {
		return err
	}
	return c.member(e, l, "usage_record", "codex/usage-record", raw, "the usage record's turn and thread totals are")
}

// interAgent writes a message from another agent as the agent_message
// item Codex puts in the recipient's context.
func (c *converter) interAgent(l *codex.Line) error {
	if err := c.closePending(l); err != nil {
		return err
	}
	c.reportUnanswered(l)
	raw, err := json.Marshal(l.InterAgent.ModelInput())
	if err != nil {
		return err
	}
	if raw, err = c.clean(l, raw); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	if raw, err = extension(obj); err != nil {
		return err
	}
	item := &openresponses.UnknownItem{Type: extensionType(codex.ItemAgentMessage), ID: l.InterAgent.ID, Raw: raw}
	e := agentsession.NewItemEntry(item)
	c.addf(agentimport.Kept, "codex/codex-item", l,
		"a message from another agent is the agent_message item Codex sends for it, the extension item %s", item.Type)
	if err := c.member(e, l, "inter_agent", "codex/inter-agent", l.Payload, "the message as recorded (recipients, whether it started a turn) is"); err != nil {
		return err
	}
	if err := c.lineMembers(e, l); err != nil {
		return err
	}
	if err := c.emit(e, l); err != nil {
		return err
	}
	c.mark(l, agentimport.LineMapped)
	return nil
}

// event converts an event: a token count completes a response, a
// rollback branches, turn boundaries close the response being gathered,
// and the events that echo an item are dropped.
func (c *converter) event(l *codex.Line) error {
	ev := l.Event
	switch ev.Type {
	case codex.EventAgentReasoningRawContent:
		if !c.rawReasoning {
			// Codex writes a reasoning item's content only when it is
			// reasoning_text (models.rs, should_serialize_reasoning_content),
			// so raw reasoning of another kind is here alone. Codex does
			// not send it.
			return c.custom(l, "codex/reasoning-raw", "raw reasoning text the turn's reasoning items do not carry is a custom entry")
		}
		c.drop(l, "codex/event-echo", "a %s event, which repeats an item or record the rollout holds, for Codex's own display", ev.Type)
		return nil
	case codex.EventUserMessage, codex.EventAgentMessage, codex.EventAgentReasoning, codex.EventContextCompacted:
		c.drop(l, "codex/event-echo", "a %s event, which repeats an item or record the rollout holds, for Codex's own display", ev.Type)
		return nil
	case codex.EventTokenCount:
		return c.tokenCount(l)
	case codex.EventThreadRolledBack:
		return c.rollback(l)
	case codex.EventTurnStarted, codex.EventTurnComplete, codex.EventTurnAborted:
		if err := c.closePending(l); err != nil {
			return err
		}
		c.rawReasoning = false
		if err := c.custom(l, "codex/turn-event", "a %s event is a custom entry", ev.Type); err != nil {
			return err
		}
		if ev.Type == codex.EventTurnStarted {
			c.turnStarts[c.tail] = true
		}
		return nil
	}
	return c.custom(l, "codex/event", "a %s event, which the model does not see, is a custom entry", ev.Type)
}

// efforts maps Codex's reasoning efforts to Responses efforts.
var efforts = map[string]openresponses.ReasoningEffort{
	"none":    openresponses.ReasoningEffortNone,
	"minimal": openresponses.ReasoningEffortMinimal,
	"low":     openresponses.ReasoningEffortLow,
	"medium":  openresponses.ReasoningEffortMedium,
	"high":    openresponses.ReasoningEffortHigh,
	"xhigh":   openresponses.ReasoningEffortXHigh,
}

// summaries maps Codex's reasoning summary settings to Responses ones;
// "none" asks for no summary, which is the setting left unset.
var summaries = map[string]openresponses.ReasoningSummary{
	"auto":     openresponses.ReasoningSummaryAuto,
	"concise":  openresponses.ReasoningSummaryConcise,
	"detailed": openresponses.ReasoningSummaryDetailed,
	"none":     "",
}

// turnContext is a config delta of what the turn's settings changed: the
// model, the reasoning, the other settings in codex:turn_context; and an
// env entry when the working directory moved.
func (c *converter) turnContext(l *codex.Line) error {
	if err := c.closePending(l); err != nil {
		return err
	}
	tc := l.TurnContext
	if mode, limit, ok := tc.Truncation(); ok {
		if mode == "tokens" {
			limit *= 4
		}
		c.st.truncation = limit
	}
	cfg := &agentsession.ConfigEntry{}
	changed := false
	if tc.Model != "" && tc.Model != c.st.model {
		cfg.Model, c.st.model, changed = tc.Model, tc.Model, true
	}
	summary := tc.SummaryMode()
	if tc.Effort != c.st.effort || summary != c.st.summary {
		r := openresponses.ReasoningConfig{}
		if tc.Effort != "" {
			eff, ok := efforts[tc.Effort]
			if !ok {
				c.addf(agentimport.Lossy, "codex/effort", l, "reasoning effort %q has no Responses effort; it is in %s:turn_context alone", tc.Effort, Source)
			}
			r.Effort = eff
		}
		if summary != "" {
			s, ok := summaries[summary]
			if !ok {
				c.addf(agentimport.Lossy, "codex/reasoning-summary", l, "reasoning summary %q has no Responses setting; it is in %s:turn_context alone", summary, Source)
			}
			r.Summary = s
		}
		cfg.Reasoning = &r
		c.st.effort, c.st.summary = tc.Effort, summary
		changed = true
	}
	rest, err := turnContextMembers(tc)
	if err != nil {
		return err
	}
	if rest != c.st.turnContext {
		c.st.turnContext = rest
		changed = true
		if rest != "" {
			if err := c.member(cfg, l, "turn_context", "codex/turn-context", json.RawMessage(rest), "the turn's other settings (approval and sandbox policy and the like), which reach the model as messages the rollout records, are"); err != nil {
				return err
			}
		}
	}
	cwdMoved := tc.CWD != "" && tc.CWD != c.st.cwd
	if !changed && !cwdMoved {
		c.drop(l, "codex/turn-context-repeat", "a turn context with the settings of the one before it, the turn id aside")
		if m := nonNull(l.Extra); len(m) > 0 && !l.Legacy {
			c.addf(agentimport.Dropped, "codex/line-members", l, "the dropped turn context's line members %s are left out", memberNames(m))
		}
		return nil
	}
	if changed {
		if err := c.lineMembers(cfg, l); err != nil {
			return err
		}
		if err := c.emit(cfg, l); err != nil {
			return err
		}
	}
	if cwdMoved {
		c.st.cwd = tc.CWD
		env := agentsession.NewEnvEntry(tc.CWD)
		if !changed {
			if err := c.lineMembers(env, l); err != nil {
				return err
			}
		}
		if err := c.emit(env, l); err != nil {
			return err
		}
	}
	c.mark(l, agentimport.LineMapped)
	return nil
}

// turnContextMembers returns, as compact JSON, the turn context's
// members the config entry does not map: all but the turn ids, the
// model, the reasoning settings and the working directory.
func turnContextMembers(tc *codex.TurnContext) (string, error) {
	raw, err := json.Marshal(tc)
	if err != nil {
		return "", err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return "", err
	}
	for _, k := range []string{"turn_id", "root_turn_id", "model", "effort", "summary", "cwd"} {
		delete(all, k)
	}
	all = nonNull(all)
	if len(all) == 0 {
		return "", nil
	}
	raw, err = json.Marshal(all)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// reportUnanswered reports the calls on the path that have no output
// when a new user turn starts: Codex sends each such call an output it
// makes up, "aborted", which the rollout does not record.
func (c *converter) reportUnanswered(l *codex.Line) {
	var ids []string
	for _, id := range c.st.calls {
		if !c.st.answered[id] && !c.reported[id] {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		c.reported[id] = true
		c.addf(agentimport.Lossy, "codex/call-without-output", l,
			"call %s has no output; Codex sends later requests an output it makes up for it, which the rollout does not record", id)
	}
}

// openaiTypes are the item types OpenAI's Responses API defines that
// openresponses does not type; Codex sends them as they are.
var openaiTypes = map[string]bool{
	codex.ItemLocalShellCall: true,
	codex.ItemWebSearchCall:  true, codex.ItemImageGenerationCall: true,
	codex.ItemToolSearchCall: true, codex.ItemToolSearchOutput: true,
}

// extensionType is the slug-prefixed type an item openresponses does not
// type is written as: openai.<type> for a type OpenAI's API defines,
// codex.<type> for one of Codex's own.
func extensionType(t string) string {
	if openaiTypes[t] {
		return "openai." + t
	}
	return Source + "." + t
}

// extension writes a native item as an extension item: its type
// slug-prefixed, its body verbatim.
func extension(obj map[string]json.RawMessage) (json.RawMessage, error) {
	var t string
	if err := json.Unmarshal(obj["type"], &t); err != nil {
		return nil, err
	}
	typ, err := json.Marshal(extensionType(t))
	if err != nil {
		return nil, err
	}
	out := make(map[string]json.RawMessage, len(obj))
	for k, v := range obj {
		out[k] = v
	}
	out["type"] = typ
	return json.Marshal(out)
}

// asFunction writes a Responses custom tool call as the function call it
// is, and its output as a function call output, so the format's call
// rules see them: the free-form input becomes the arguments
// {"input": "<text>"}, unless the item records arguments itself. It
// returns the native members it used, nil for any other item, which is
// returned as it was. A ctc_ item id is not a function call's, and is
// kept as codex:item_id.
func asFunction(obj map[string]json.RawMessage, it *codex.Item) (map[string]json.RawMessage, map[string]bool, error) {
	switch it.Type {
	case codex.ItemCustomToolCall:
		out := map[string]json.RawMessage{"type": json.RawMessage(`"function_call"`)}
		consumed := map[string]bool{"type": true, "input": true}
		for _, k := range []string{"call_id", "name", "status"} {
			if v, ok := obj[k]; ok {
				out[k] = v
				consumed[k] = true
			}
		}
		if id, ok := obj["id"]; ok {
			// A ctc_ id is not a function call's; it is codex:item_id.
			var s string
			if json.Unmarshal(id, &s) == nil && !strings.HasPrefix(s, "ctc_") {
				out["id"] = id
			}
			consumed["id"] = true
		}
		args, err := customArguments(obj)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := obj["arguments"]; ok {
			consumed["arguments"] = true
		}
		out["arguments"] = args
		return out, consumed, nil
	case codex.ItemCustomToolCallOutput:
		out := map[string]json.RawMessage{"type": json.RawMessage(`"function_call_output"`)}
		consumed := map[string]bool{"type": true}
		for _, k := range []string{"call_id", "output", "status"} {
			if v, ok := obj[k]; ok {
				out[k] = v
				consumed[k] = true
			}
		}
		return out, consumed, nil
	}
	return obj, nil, nil
}

// customArguments is a custom tool call's arguments as a JSON string: the
// item's own arguments when it records them, else {"input": <input>}.
func customArguments(obj map[string]json.RawMessage) (json.RawMessage, error) {
	if raw, ok := obj["arguments"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return raw, nil
		}
		var v map[string]json.RawMessage
		if json.Unmarshal(raw, &v) == nil && v != nil {
			return json.Marshal(string(raw))
		}
	}
	var input string
	if raw, ok := obj["input"]; ok {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
	}
	args, err := json.Marshal(map[string]string{"input": input})
	if err != nil {
		return nil, err
	}
	return json.Marshal(string(args))
}

// customMembers marks an entry made from a custom tool call or its
// output: codex:tool_kind "custom", and a ctc_ item id as codex:item_id.
func (c *converter) customMembers(e agentsession.Entry, l *codex.Line, it *codex.Item) error {
	if it.Type != codex.ItemCustomToolCall && it.Type != codex.ItemCustomToolCallOutput {
		return nil
	}
	if err := c.b.Member(e, "tool_kind", "custom"); err != nil {
		return err
	}
	if strings.HasPrefix(it.ID, "ctc_") {
		if err := c.b.Member(e, "item_id", it.ID); err != nil {
			return err
		}
	}
	c.addf(agentimport.Kept, "codex/custom-tool-call", l,
		"a Responses %s is a %s, marked %s:tool_kind \"custom\"; its free-form input is the arguments {\"input\": …}", it.Type, map[bool]string{true: "function call", false: "function call output"}[it.Type == codex.ItemCustomToolCall], Source)
	return nil
}
