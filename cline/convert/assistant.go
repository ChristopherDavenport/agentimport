package convert

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/cline"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// Texts classic Cline stores in an assistant message the model did not
// finish.
const (
	interruptedUser = "[Response interrupted by user]"
	interruptedAPI  = "[Response interrupted by API Error]"
	noResponse      = "Failure: I did not provide a response."
)

// assistant converts an assistant message: a config delta when the
// model changed, an item per content block in block order, the
// response entry, and a config delta leaving out the reasoning the
// requests do not send back.
func (c *conv) assistant(k int, m *cline.Message) error {
	ts, _ := m.Time()
	if m.Content.Err != nil {
		_, err := c.customAPI(k, m, "cline/content-malformed", "a message whose content is neither text nor blocks is a custom entry")
		return err
	}
	for _, p := range c.calls {
		if !p.output {
			c.apiAt(agentimport.Lossy, "cline/call-without-result", k, "call %s (%s) of the response before has no result", p.id, p.name)
		}
	}
	c.pending, c.calls, c.pendingFrom = map[string]*call{}, nil, k
	if mi := m.ModelInfo; mi != nil && mi.ModelID != "" && mi.ModelID != c.model {
		cfg := &agentsession.ConfigEntry{Model: mi.ModelID}
		cfg.Timestamp = ts
		id, err := c.appendEntry(cfg)
		if err != nil {
			return err
		}
		c.written(k, id, nil)
		c.model = mi.ModelID
	}
	respID := c.responseID(k, m)
	blocks := m.Content.Blocks
	if m.Content.IsText {
		blocks = []*cline.Block{{Type: cline.BlockText, Text: m.Content.Text}}
	}
	// Roo's trailing thoughtSignature signs the first call.
	sigAt := map[int]bool{}
	firstUse := slices.IndexFunc(blocks, func(b *cline.Block) bool { return b.Err == nil && b.Type == cline.BlockToolUse })
	for i, b := range blocks {
		if b.Err == nil && b.Type == cline.BlockThoughtSignature && firstUse >= 0 && firstUse < i {
			sigAt[firstUse] = true
		}
	}
	var omit []string
	add := func(item openresponses.Item, b *cline.Block, mapped []string, provider string) (string, error) {
		e := itemEntry(item, ts)
		e.ResponseID = respID
		if provider != "" {
			if err := c.b.Member(e, "provider", provider); err != nil {
				return "", err
			}
			c.apiAt(agentimport.Gap, "agentimport/reasoning-provider", k, "a reasoning signature from %s in encrypted_content, tagged %s:provider", provider, Source)
		}
		if b != nil && !m.Content.IsText {
			if err := c.keepMembers(e, k, "block", blockRest(b, mapped...), "the "+b.Type+" block's"); err != nil {
				return "", err
			}
		}
		id, err := c.appendEntry(e)
		if err != nil {
			return "", err
		}
		c.written(k, id, item)
		return id, nil
	}
	signature := func(sig, provider string) error {
		r := &openresponses.ReasoningItem{Status: openresponses.StatusCompleted, Summary: openresponses.Contents{}, EncryptedContent: sig}
		_, err := add(r, nil, nil, provider)
		return err
	}
	var xml []cline.XMLToolCall
	for i, b := range blocks {
		if sigAt[i] {
			for _, s := range blocks[i:] {
				if s.Err == nil && s.Type == cline.BlockThoughtSignature {
					if err := signature(s.ThoughtSignature, "gemini"); err != nil {
						return err
					}
					c.apiAt(agentimport.Lossy, "cline/roo-thought-signature", k, "Roo's trailing thoughtSignature block is a reasoning item before the first call, which it signs")
					break
				}
			}
		}
		if b.Err != nil {
			c.apiAt(agentimport.Dropped, "cline/null-block", k, "a content block that is not an object is left out")
			continue
		}
		if len(b.Mistyped()) > 0 {
			c.apiAt(agentimport.Lossy, "cline/mistyped-member", k, "a %s block's members %s are not of their type; they are kept in %s:block and read as empty", b.Type, strings.Join(b.Mistyped(), ", "), Source)
		}
		switch b.Type {
		case cline.BlockText:
			if b.Signature != "" {
				// A Gemini thought signature on a text part.
				if err := signature(b.Signature, "gemini"); err != nil {
					return err
				}
			}
			item := &openresponses.Message{Role: openresponses.RoleAssistant, Status: openresponses.StatusCompleted,
				Content: openresponses.Contents{&openresponses.OutputText{Text: b.Text, Annotations: openresponses.Annotations{}}}}
			e := itemEntry(item, ts)
			e.ResponseID = respID
			if calls := cline.ParseXMLTools(b.Text); len(calls) > 0 {
				xml = append(xml, calls...)
				if err := c.b.Member(e, "xml_tools", calls); err != nil {
					return err
				}
			}
			if !m.Content.IsText {
				if err := c.keepMembers(e, k, "block", blockRest(b, "text", "signature"), "the text block's"); err != nil {
					return err
				}
			}
			id, err := c.appendEntry(e)
			if err != nil {
				return err
			}
			c.written(k, id, item)
		case cline.BlockThinking:
			r := &openresponses.ReasoningItem{Status: openresponses.StatusCompleted, Summary: openresponses.Contents{}, EncryptedContent: b.Signature}
			if b.Thinking != "" {
				r.Summary = openresponses.Contents{&openresponses.SummaryText{Text: b.Thinking}}
			}
			provider := ""
			if b.Signature != "" {
				provider = "anthropic"
			}
			id, err := add(r, b, []string{"thinking", "signature"}, provider)
			if err != nil {
				return err
			}
			if b.Signature == "" {
				omit = append(omit, id)
				c.apiAt(agentimport.Lossy, "cline/unsigned-thinking", k, "a thinking block with no signature, which Cline leaves out of every request, is a reasoning item the next config entry omits")
			}
		case cline.BlockReasoning:
			r := &openresponses.ReasoningItem{ID: b.ID, Status: openresponses.StatusCompleted, EncryptedContent: b.EncryptedContent, Summary: summaryParts(b.Members.Extra["summary"])}
			mapped := []string{"id", "encrypted_content"}
			if summaryMapped(b.Members.Extra["summary"]) {
				mapped = append(mapped, "summary")
			}
			if b.EncryptedContent != "" {
				if _, err := add(r, b, mapped, "openai"); err != nil {
					return err
				}
				continue
			}
			if b.Text != "" {
				r.Summary = append(openresponses.Contents{&openresponses.SummaryText{Text: b.Text}}, r.Summary...)
			}
			id, err := add(r, b, append(mapped, "text"), "")
			if err != nil {
				return err
			}
			omit = append(omit, id)
			c.apiAt(agentimport.Lossy, "cline/roo-plain-reasoning", k, "Roo's plain-text reasoning, which requests leave out unless the model preserves reasoning, is a reasoning item the next config entry omits")
		case cline.BlockThoughtSignature:
			if sigAt[firstUse] && firstUse >= 0 && firstUse < i {
				continue
			}
			if err := signature(b.ThoughtSignature, "gemini"); err != nil {
				return err
			}
		case cline.BlockToolUse:
			if b.Signature != "" {
				if err := signature(b.Signature, "gemini"); err != nil {
					return err
				}
			}
			if err := c.toolUse(k, m, b, respID, ts); err != nil {
				return err
			}
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				return err
			}
			if raw, err = c.clean(c.t.API.Name, k+1, raw); err != nil {
				return err
			}
			item, err := anthropicItem(extType(b.Type), raw)
			if err != nil {
				return err
			}
			if _, err := add(item, nil, nil, ""); err != nil {
				return err
			}
			c.apiAt(agentimport.Kept, "cline/anthropic-block", k, "a %s block is an %s item", extType(b.Type), item.Type)
		}
	}
	if len(xml) > 0 {
		c.apiAt(agentimport.Lossy, "cline/xml-tool-calls", k, "the response calls %s as XML in its text, as the model wrote and saw it; no function_call is written, and the calls are %s:xml_tools", strings.Join(cline.Names(xml), ", "), Source)
		for _, x := range xml {
			c.toolNotes(k, x.Name)
		}
	}
	re, err := c.response(k, m, respID, ts)
	if err != nil {
		return err
	}
	id, err := c.appendEntry(re)
	if err != nil {
		return err
	}
	if c.first[k] == "" {
		c.first[k] = id
	}
	if len(c.items[k]) == 0 {
		c.apiAt(agentimport.Lossy, "cline/empty-message", k, "an assistant message with no content is a response with no items")
	}
	if len(omit) > 0 {
		for _, id := range omit {
			c.omitted[id] = k
		}
		cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: omit}}
		cfg.Timestamp = ts
		if _, err := c.appendEntry(cfg); err != nil {
			return err
		}
	}
	c.mark(unit{uAPI, k}, agentimport.LineMapped)
	return nil
}

// toolUse writes a tool_use block as a function call.
func (c *conv) toolUse(k int, m *cline.Message, b *cline.Block, respID string, ts time.Time) error {
	args := compactJSON(b.Input)
	switch {
	case args == "":
		args = "{}"
		if !cline.IsNull(b.Input) {
			c.apiAt(agentimport.Lossy, "cline/invalid-input", k, "a tool_use input that is not JSON is recorded as {}")
		}
	case args[0] != '{':
		c.apiAt(agentimport.Lossy, "cline/invalid-input", k, "a tool_use input that is not a JSON object is recorded as its JSON text")
	}
	id, renamed := c.b.Calls.Assign(b.ID)
	item := &openresponses.FunctionCall{Status: openresponses.StatusCompleted, CallID: id, Name: b.Name, Arguments: args}
	e := itemEntry(item, ts)
	e.ResponseID = respID
	if renamed {
		if err := c.b.Member(e, "call_id", b.ID); err != nil {
			return err
		}
		c.apiAt(agentimport.Repaired, "cline/call-id-renamed", k, "call %q is recorded as %q, unique in the session", b.ID, id)
	}
	if err := c.keepMembers(e, k, "block", blockRest(b, "id", "name", "input", "signature"), "the tool_use block's"); err != nil {
		return err
	}
	eid, err := c.appendEntry(e)
	if err != nil {
		return err
	}
	c.written(k, eid, item)
	p := &call{native: b.ID, id: id, name: b.Name, entry: eid}
	if _, dup := c.pending[b.ID]; !dup {
		c.pending[b.ID] = p
	}
	c.calls = append(c.calls, p)
	c.toolNotes(k, b.Name)
	return nil
}

// toolNotes reports the tools whose work the task directory does not
// hold.
func (c *conv) toolNotes(k int, name string) {
	switch {
	case name == "use_subagents" && !c.roo:
		c.apiAt(agentimport.Lossy, "cline/subagents-in-memory", k, "use_subagents ran its sub-agents in memory and wrote no transcript; the UI records hold their prompts, results and usage")
	case name == "new_task" && !c.roo:
		c.apiAt(agentimport.Lossy, "cline/new-task-unlinked", k, "classic Cline starts a new_task's task in a new chat and records no link; the new task, if it ran, is a session of its own")
	}
}

// compactJSON is raw re-encoded without insignificant space; "" when
// raw is absent, null or not JSON.
func compactJSON(raw json.RawMessage) string {
	if cline.IsNull(raw) || !json.Valid(raw) {
		return ""
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	out, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(out)
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

// responseID is the response's ID: the provider's request ID the
// message records, or one made from its position when it records none
// or one another response has.
func (c *conv) responseID(k int, m *cline.Message) string {
	if m.ID != "" && !c.respIDs[m.ID] {
		c.respIDs[m.ID] = true
		return m.ID
	}
	id := fmt.Sprintf("resp_%d", k+1)
	for n := 2; c.respIDs[id]; n++ {
		id = fmt.Sprintf("resp_%d_%d", k+1, n)
	}
	c.respIDs[id] = true
	if m.ID == "" {
		c.apiAt(agentimport.Lossy, "cline/no-response-id", k, "the message records no response ID; the response is %s", id)
	} else {
		c.apiAt(agentimport.Repaired, "cline/response-id-repeated", k, "response ID %q is another response's; this one is %s, and the native ID is %s:message", m.ID, id, Source)
	}
	return id
}

// response is the envelope of an assistant message: the model, the
// usage of the request it answered, and the status its text or the
// request's record says.
func (c *conv) response(k int, m *cline.Message, respID string, ts time.Time) (*agentsession.ResponseEntry, error) {
	e := &agentsession.ResponseEntry{ResponseID: respID, Status: openresponses.ResponseStatusCompleted}
	e.Timestamp = ts
	if m.ModelInfo != nil {
		e.Model = m.ModelInfo.ModelID
	}
	switch tail := lastText(m); {
	case strings.HasSuffix(tail, interruptedUser):
		e.Status = openresponses.ResponseStatusIncomplete
		c.apiAt(agentimport.Lossy, "cline/interrupted-response", k, "the user interrupted the response; Cline stored what had streamed with %q appended, which later requests send", interruptedUser)
	case strings.HasSuffix(tail, interruptedAPI):
		e.Status = openresponses.ResponseStatusFailed
		e.Error = &openresponses.ErrorPayload{Code: "api_error", Message: "Response interrupted by API Error"}
		c.apiAt(agentimport.Lossy, "cline/interrupted-response", k, "an API error cut the response; Cline stored what had streamed with %q appended, which later requests send", interruptedAPI)
	case tail == noResponse:
		e.Status = openresponses.ResponseStatusFailed
		e.Error = &openresponses.ErrorPayload{Code: "empty_response", Message: noResponse}
		c.apiAt(agentimport.Lossy, "cline/empty-response", k, "the model returned nothing; Cline stored %q as its message, which later requests send", noResponse)
	}
	if err := c.usage(k, m, e); err != nil {
		return nil, err
	}
	var mapped []string
	if m.ID == respID {
		mapped = append(mapped, "id")
	}
	rest, err := messageRest(m, mapped...)
	if err != nil {
		return nil, err
	}
	return e, c.keepMembers(e, k, "message", rest, "the message's")
}

// lastText is the text a message ends with: its string content, or its
// last text block's.
func lastText(m *cline.Message) string {
	if m.Content.IsText {
		return strings.TrimSpace(m.Content.Text)
	}
	for i := len(m.Content.Blocks) - 1; i >= 0; i-- {
		if b := m.Content.Blocks[i]; b.Err == nil && b.Type == cline.BlockText {
			return strings.TrimSpace(b.Text)
		}
	}
	return ""
}

// uiReq is the UI record of the request an assistant message answered.
type uiReq struct {
	ui       int
	info     *cline.ReqInfo
	attempts int
}

// matchRequests pairs each api_req_started UI record with the
// assistant message its request produced: by the API index the record
// carries (classic), else by time, the first response written after the
// record and before the next. Retries reuse one record, so each request
// is counted once; the api_req_retried records after it count its
// attempts.
func (c *conv) matchRequests() {
	ui := c.t.UI.Elems
	n := len(c.t.API.Elems)
	isResponse := func(k int) bool {
		m := c.t.API.Elems[k]
		return m.Err == nil && m.Role == cline.RoleAssistant && !m.IsSummary
	}
	var starts []int
	for i, m := range ui {
		if m.Err == nil && !m.Partial && m.Type == "say" && m.Say == "api_req_started" {
			starts = append(starts, i)
		}
	}
	// With no index and no time on either side, records and responses
	// pair in order.
	byOrder := true
	for _, i := range starts {
		if _, ok := ui[i].HistoryIndex(); ok && !c.roo {
			byOrder = false
		}
	}
	for _, m := range c.t.API.Elems {
		if _, ok := m.Time(); ok && m.Err == nil {
			byOrder = false
		}
	}
	// A request whose retries were exhausted keeps its record, and the
	// request a resume makes after it names the same API index: the
	// last record naming an index answers it, and earlier ones were
	// attempts of it that produced no response.
	superseded := map[int]int{}
	if !c.roo {
		lastAt := map[int]int{}
		for si, i := range starts {
			if hi, ok := ui[i].HistoryIndex(); ok {
				if prev, seen := lastAt[hi]; seen {
					superseded[prev] = si
				}
				lastAt[hi] = si
			}
		}
	}
	extra := map[int]int{}
	taken := map[int]bool{}
	lastK := -1
	for si, i := range starts {
		m := ui[i]
		end := len(ui)
		if si+1 < len(starts) {
			end = starts[si+1]
		}
		var retried []int
		for j := i + 1; j < end; j++ {
			if r := ui[j]; r.Err == nil && r.Type == "say" && r.Say == "api_req_retried" {
				retried = append(retried, j)
			}
		}
		if by, ok := superseded[si]; ok {
			// Its attempts count toward the request that answered the
			// index; its record, with the usage it paid for, is kept.
			for later := by; ; {
				next, more := superseded[later]
				if !more {
					extra[later] += 1 + len(retried)
					break
				}
				later = next
			}
			c.failed[i] = true
			continue
		}
		k := -1
		if hi, ok := m.HistoryIndex(); ok && !c.roo {
			if cand := hi + 2; cand >= 0 && cand < n && isResponse(cand) && !taken[cand] {
				k = cand
			}
		}
		if k < 0 {
			if ts, ok := m.Time(); ok {
				var next time.Time
				hasNext := false
				if si+1 < len(starts) {
					next, hasNext = ui[starts[si+1]].Time()
				}
				for cand := lastK + 1; cand < n; cand++ {
					if !isResponse(cand) || taken[cand] {
						continue
					}
					ct, ok := c.t.API.Elems[cand].Time()
					if !ok || ct.Before(ts) {
						continue
					}
					if hasNext && !ct.Before(next) {
						break
					}
					k = cand
					break
				}
			}
		}
		if k < 0 && byOrder {
			for cand := lastK + 1; cand < n; cand++ {
				if isResponse(cand) && !taken[cand] {
					k = cand
					break
				}
			}
			if k >= 0 {
				c.uiAt(agentimport.Lossy, "cline/usage-by-order", i, "neither the request records nor the messages carry an index or a time; the record is matched to the next response in order")
			}
		}
		info := m.ReqInfo()
		if k < 0 {
			if info == nil || (!info.HasUsage() && info.CancelReason == "" && info.StreamingFailedMessage == "") {
				c.uiAt(agentimport.Dropped, "cline/request-placeholder", i, "a request record with no usage and no response is left out")
				c.mark(unit{uUI, i}, agentimport.LineDropped)
			}
			c.failed[i] = true
			continue
		}
		taken[k] = true
		lastK = max(lastK, k)
		c.reqs[k] = &uiReq{ui: i, info: info, attempts: 1 + len(retried) + extra[si]}
		c.mark(unit{uUI, i}, agentimport.LineMapped)
		for _, j := range retried {
			c.mark(unit{uUI, j}, agentimport.LineMapped)
		}
	}
}

// exclusive lists the classic providers whose input count leaves the
// cache reads and writes out, as Anthropic's does; the others' include
// them, as OpenAI's prompt_tokens does.
var exclusive = map[string]bool{
	"anthropic": true, "aihubmix": true, "bedrock": true, "claude-code": true,
	"cline": true, "deepseek": true, "gemini": true, "groq": true, "minimax": true,
	"moonshot": true, "openai-native": true, "openrouter": true,
	"vercel-ai-gateway": true, "vertex": true,
}

// inclusive says whether a usage record's input count already holds
// the cache reads and writes, for a record written at ts; why says what
// was assumed, empty when nothing was. provider is the classic
// provider that answered, when the message names it.
func (c *conv) inclusive(provider string, ts time.Time, hasTS bool, info *cline.ReqInfo) (bool, string) {
	if c.roo {
		in, cache := count(info.TokensIn), count(info.CacheReads)+count(info.CacheWrites)
		switch info.APIProtocol {
		case "anthropic":
			// Roo 3.30 and later write the total; before, the raw count,
			// which leaves the cache out.
			if in < cache {
				return false, "the record's input count is smaller than its cache, as Roo before 3.30 wrote it for the Anthropic protocol; the cache is added"
			}
			return true, ""
		case "":
			if in < cache {
				return false, "the record names no API protocol, as Roo before 3.25 wrote it, and its input count is smaller than its cache; the cache is added"
			}
			return true, "the record names no API protocol, as Roo before 3.25 wrote it; its input count is read as holding the cache"
		}
		return true, ""
	}
	if provider == "" {
		provider = c.providerAt(ts, hasTS)
	}
	if provider == "" {
		return false, "nothing names the provider; the input count is read as Anthropic's, which leaves the cache out"
	}
	return !exclusive[provider], ""
}

// providerAt is the provider the metadata says was in use at ts.
func (c *conv) providerAt(ts time.Time, ok bool) string {
	md := c.t.Metadata
	if md == nil {
		return ""
	}
	provider := ""
	for _, u := range md.ModelUsage {
		if u == nil || u.ModelProviderID == "" {
			continue
		}
		if ut, uok := cline.Millis(u.TS); ok && uok && ut.After(ts) {
			break
		}
		provider = u.ModelProviderID
	}
	return provider
}

// maxCount bounds a token count: a larger one, or a negative one, is
// no count.
const maxCount = 1 << 40

// count reads a token count, 0 when it is absent or out of range.
func count(raw json.RawMessage) int64 {
	n, _, ok := cline.Int(raw)
	if !ok || n < 0 || n >= maxCount {
		return 0
	}
	return n
}

// inRange reports whether every count a record holds is a count.
func inRange(raws ...json.RawMessage) bool {
	for _, raw := range raws {
		if n, _, ok := cline.Int(raw); ok && (n < 0 || n >= maxCount) {
			return false
		}
	}
	return true
}

// recordUsage converts a usage record: input_tokens holds the cache,
// as openresponses' Anthropic provider has it, and cached_tokens is
// the cache reads.
func recordUsage(info *cline.ReqInfo, inclusive bool) *openresponses.Usage {
	in, out, cr, cw := count(info.TokensIn), count(info.TokensOut), count(info.CacheReads), count(info.CacheWrites)
	if !inclusive {
		in += cr + cw
	}
	u := openresponses.Usage{InputTokens: int(in), OutputTokens: int(out)}
	u.InputTokensDetails.CachedTokens = int(cr)
	u.TotalTokens = u.InputTokens + u.OutputTokens
	return &u
}

// usage sets a response's usage: the request's record when one matched,
// else the message's metrics, folding the cache into the input tokens
// as openresponses' Anthropic provider does.
func (c *conv) usage(k int, m *cline.Message, e *agentsession.ResponseEntry) error {
	req := c.reqs[k]
	if req != nil && req.attempts > 1 {
		e.Attempts = req.attempts
	}
	if req != nil && req.info != nil {
		switch req.info.CancelReason {
		case "user_cancelled":
			e.Status = openresponses.ResponseStatusIncomplete
		case "streaming_failed", "retries_exhausted":
			e.Status = openresponses.ResponseStatusFailed
			if e.Error == nil {
				e.Error = &openresponses.ErrorPayload{Code: req.info.CancelReason, Message: req.info.StreamingFailedMessage}
			}
		}
	}
	if req != nil && req.info != nil && req.info.HasUsage() {
		info := req.info
		provider := ""
		if m.ModelInfo != nil {
			provider = m.ModelInfo.ProviderID
		}
		ts, ok := m.Time()
		incl, why := c.inclusive(provider, ts, ok, info)
		if why != "" {
			c.uiAt(agentimport.Lossy, "cline/usage-semantics", req.ui, "%s", why)
		}
		if !inRange(info.TokensIn, info.TokensOut, info.CacheReads, info.CacheWrites) {
			c.uiAt(agentimport.Lossy, "cline/usage-out-of-range", req.ui, "a count of the record is negative or above 2^40; it is read as 0")
		}
		e.Usage = recordUsage(info, incl)
		raw, err := json.Marshal(info)
		if err != nil {
			return err
		}
		if info.Members.Has("request") {
			var all map[string]json.RawMessage
			if json.Unmarshal(raw, &all) == nil {
				delete(all, "request")
				if raw, err = json.Marshal(all); err != nil {
					return err
				}
			}
			c.uiAt(agentimport.Dropped, "cline/request-text", req.ui, "the request record's request text, the user message rendered as Markdown, is left out")
		}
		if raw, err = c.clean(c.t.UI.Name, req.ui+1, raw); err != nil {
			return err
		}
		if err := c.b.Member(e, "usage", json.RawMessage(raw)); err != nil {
			return err
		}
		c.uiAt(agentimport.Gap, "agentimport/usage-detail", req.ui, "cache writes are folded into input_tokens; the request record's usage and cost are %s:usage", Source)
		return nil
	}
	if m.Metrics != nil && m.Metrics.Tokens != nil {
		t := m.Metrics.Tokens
		if _, _, pok := cline.Int(t.Prompt); pok {
			provider := ""
			if m.ModelInfo != nil {
				provider = m.ModelInfo.ProviderID
			}
			ts, ok := m.Time()
			info := &cline.ReqInfo{TokensIn: t.Prompt, TokensOut: t.Completion, CacheReads: t.Cached}
			incl, why := c.inclusive(provider, ts, ok, info)
			if why != "" {
				c.apiAt(agentimport.Lossy, "cline/usage-semantics", k, "%s", why)
			}
			e.Usage = recordUsage(info, incl)
			e.Usage.InputTokensDetails.CachedTokens = 0
			c.apiAt(agentimport.Lossy, "cline/usage-from-metrics", k, "no request record matched the response; its usage is the message's metrics, whose cached count adds cache reads and writes together, so cached_tokens is not set")
		}
	}
	return nil
}
