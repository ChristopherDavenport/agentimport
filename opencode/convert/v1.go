package convert

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// The texts OpenCode's V1 toModelMessages sends for parts that carry
// none of their own, and for a call that never finished.
const (
	compactionText  = "What did we do so far?"
	subtaskText     = "The following tool was executed by the user"
	interruptedText = "[Tool execution was interrupted]"
	prunedText      = "[Old tool result content cleared]"
)

// The texts of a V1 tool error that says the user refused the call:
// PermissionV1's RejectedError, CorrectedError and DeniedError.
const (
	rejectedText  = "The user rejected permission to use this specific tool call."
	correctedText = "The user rejected permission to use this specific tool call with the following feedback:"
	deniedText    = "The user has specified a rule which prevents you from using this specific tool call."
)

// forkTitle matches the title V1's Session.fork gives a fork.
var forkTitle = regexp.MustCompile(` \(fork #\d+\)$`)

// v1walk converts a session's V1 messages: G1 storage files or G2 rows.
type v1walk struct {
	sc   *sconv
	msgs []*opencode.MessageRecord
	idx  map[string]int
	// ver is the release that created the session, verOK whether it
	// reads; the rules of a request are those of the release that made
	// it, which ruleAt guesses from ver and the request's time. guessed
	// marks the rules a guess was reported for.
	ver     version
	verOK   bool
	guessed map[string]bool
	// items lists, per message, the item entries it wrote; omitted the
	// item entries no request sends: a failed turn's, reasoning the
	// provider does not take back. ctx is the item entries the path's
	// context holds, in order, as this walk wrote it.
	items   [][]string
	omitted map[string]bool
	ctx     []string
	// forked says the session is an OpenCode 1 fork, told by its title,
	// whose messages created before it are copies.
	forked bool
	// hidden is the first message a staged revert hides, len(msgs) when
	// none does; hiddenPart the part of the message before it from which
	// the revert hides the rest of that message.
	hidden     int
	hiddenPart string
	// prunes are the outputs OpenCode pruned, by when.
	prunes []prune
	// callOf holds the call each tool part wrote; modelOf the model in
	// force when each reasoning item was written.
	callOf  map[*opencode.PartRecord]callCtx
	modelOf map[string]string
}

// prune is a tool output OpenCode replaced with a placeholder at a
// time, in every request after it.
type prune struct {
	at     int64
	callID string
	unit   string
}

// walkV1 converts the session's V1 messages.
func (sc *sconv) walkV1() error {
	v := &v1walk{sc: sc, msgs: sc.msgs, idx: map[string]int{}, items: make([][]string, len(sc.msgs)), omitted: map[string]bool{}, hidden: len(sc.msgs),
		callOf: map[*opencode.PartRecord]callCtx{}, modelOf: map[string]string{}, guessed: map[string]bool{}}
	for i, m := range sc.msgs {
		if _, ok := v.idx[m.ID]; !ok {
			v.idx[m.ID] = i
		}
	}
	v.rules()
	v.revert()
	for i, m := range v.msgs {
		if i == v.hidden {
			if err := v.sync(i - 1); err != nil {
				return err
			}
			sc.leaf = sc.at
		}
		if err := v.prunesDue(m.Created, m.Unit); err != nil {
			return err
		}
		if m.Info.Role == opencode.RoleAssistant && !m.Info.IsSummary() && !v.harnessRun(i) {
			if err := v.sync(i - 1); err != nil {
				return err
			}
		}
		sc.mistyped(m.Unit, m.Info)
		for _, p := range m.Parts {
			sc.mistyped(p.Unit, p.Part)
			if p.Part.State != nil {
				sc.mistyped(p.Unit, p.Part.State)
			}
		}
		var err error
		if m.Info.Role == opencode.RoleUser {
			err = v.user(i)
		} else {
			err = v.assistant(i)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", m.Unit, err)
		}
	}
	if err := v.prunesDue(math.MaxInt64, ""); err != nil {
		return err
	}
	if v.hidden == len(v.msgs) {
		if err := v.sync(len(v.msgs) - 1); err != nil {
			return err
		}
	}
	v.notes()
	return nil
}

// rules reads the release that created the session, the only version
// OpenCode records, and the evidence that a later one resumed it.
func (v *v1walk) rules() {
	sc := v.sc
	v.ver, v.verOK = parseVersion(sc.rec.v1.Version)
	v.forked = forkTitle.MatchString(sc.rec.v1.Title)
	if !v.verOK || v.ver.atLeast(v11419) {
		return
	}
	for _, m := range v.msgs {
		for _, p := range m.Parts {
			if p.Part.Type == opencode.PartCompaction && p.Part.TailStartID != "" {
				sc.addf(agentimport.Lossy, "opencode/version-resumed", p.Unit, "the session was created by %s, which writes no compaction tail, but a compaction names one, so a later release resumed it; each request is read by the rules of the release its time allows", v.ver)
				return
			}
		}
	}
}

// ruleAt reports whether the request made at t, in milliseconds,
// follows the rule release r introduced: certainly when the session was
// created by r or later, certainly not when the request predates r's
// release, and otherwise by guess, yes, as OpenCode updates itself; a
// guess is reported once per rule. what names the rule.
func (v *v1walk) ruleAt(r release, t int64, unit, what string) bool {
	if v.verOK && v.ver.atLeast(r.v) {
		return true
	}
	if t > 0 && t < r.date {
		return false
	}
	if v.guessed[what] {
		return true
	}
	v.guessed[what] = true
	if !v.verOK {
		v.sc.addf(agentimport.Lossy, "opencode/version-unknown", unit, "the session records no release that can be read (%q), and a request was made after %s's release; it is read by %s's rule (%s)", v.sc.rec.v1.Version, r.v, r.v, what)
		return true
	}
	v.sc.addf(agentimport.Lossy, "opencode/version-guess", unit, "the session was created by %s, and a request was made after %s's release: it is read by %s's rule (%s), as OpenCode updates itself; an older release would not have", v.ver, r.v, r.v, what)
	return true
}

// revert finds what a staged revert hides: the message it names and
// every message after it, or, when it names a part, the message's parts
// from that part on and every message after it. OpenCode deletes them
// before its next request; until then they are on disk but no longer in
// the conversation.
func (v *v1walk) revert() {
	sc := v.sc
	r, ok := sc.rec.v1.StagedRevert()
	if !ok {
		return
	}
	i, found := v.idx[r.MessageID]
	if !found {
		sc.addf(agentimport.Lossy, "opencode/revert-staged", "", "the session stages a revert to message %s, which it does not hold; nothing is hidden", r.MessageID)
		return
	}
	if r.PartID != "" {
		v.hiddenPart = r.PartID
		v.hidden = i + 1
		sc.addf(agentimport.Lossy, "opencode/revert-staged", sc.msgs[i].Unit, "the session stages a revert from part %s of this message: the parts from it on are left out, and the %d messages after it are on a branch the session does not resume", r.PartID, len(v.msgs)-i-1)
		return
	}
	v.hidden = i
	sc.addf(agentimport.Lossy, "opencode/revert-staged", sc.msgs[i].Unit, "the session stages a revert from this message: it and the %d messages after it are on a branch the session does not resume, as OpenCode deletes them before its next request", len(v.msgs)-i-1)
}

// parts are a message's parts, without those a revert hides.
func (v *v1walk) parts(i int) []*opencode.PartRecord {
	ps := v.msgs[i].Parts
	if v.hiddenPart == "" || i != v.hidden-1 {
		return ps
	}
	for k, p := range ps {
		if p.ID == v.hiddenPart {
			for _, h := range ps[k:] {
				v.sc.mark(h.Unit, agentimport.LineDropped)
			}
			return ps[:k]
		}
	}
	return ps
}

// prunesDue writes, before a message created at t, the prunes OpenCode
// made before it: from then on every request sent a placeholder for the
// output.
func (v *v1walk) prunesDue(t int64, unit string) error {
	var due []prune
	rest := v.prunes[:0]
	for _, p := range v.prunes {
		if p.at <= t {
			due = append(due, p)
		} else {
			rest = append(rest, p)
		}
	}
	v.prunes = rest
	if len(due) == 0 {
		return nil
	}
	sc := v.sc
	ids := make([]string, len(due))
	for k, p := range due {
		ids[k] = p.callID
		sc.addf(agentimport.Lossy, "opencode/tool-output-pruned", p.unit, "OpenCode pruned the output of call %s: requests before the prune sent it, and every request after it sent %q; the output stays in context as it was sent first", p.callID, prunedText)
	}
	sc.addf(agentimport.Gap, "agentimport/context-replace", due[0].unit, "%d tool outputs were replaced in place by OpenCode's prune; the outputs stay in context and the replacement is a custom entry", len(due))
	if sc.slim {
		return nil
	}
	data := map[string]any{"pruned": ids, "replacement": prunedText}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: raw}
	e.Timestamp = ms(due[0].at)
	_, err = sc.append(e)
	return err
}

// notes reports what the session's messages say of context OpenCode
// added and did not store.
func (v *v1walk) notes() {
	sc := v.sc
	plan, system := false, false
	for _, m := range v.msgs {
		if m.Info.Role == opencode.RoleUser && m.Info.Agent == "plan" {
			plan = true
		}
		if m.Info.Role == opencode.RoleUser && m.Info.System != "" {
			system = true
		}
	}
	if plan {
		sc.addf(agentimport.Lossy, "opencode/reminder-unrecorded", "", "the session ran the plan agent: OpenCode adds its plan-mode reminders to a request's last user message in memory and does not store them, so those requests are missing them")
	}
	if system {
		sc.addf(agentimport.Lossy, "opencode/user-system", "", "a user message carries system text OpenCode added to the system prompt of its requests, which the session does not hold; it is kept in the message's %s:message member", Source)
	}
	if v.forked {
		sc.addf(agentimport.Lossy, "opencode/fork-unlinked", "", "the session's title is a fork's; OpenCode 1 records no link from a fork to its origin, so parent_session is empty, and the messages it copied are converted as its own, those created before the fork their usage kept off the responses")
	}
}

// itemEntry is an item entry for a native record.
func (v *v1walk) itemEntry(item openresponses.Item, ts time.Time) *agentsession.ItemEntry {
	e := agentsession.NewItemEntry(item)
	e.Timestamp = ts
	return e
}

// partRest is a part's members other than those mapped.
func partRest(p *opencode.PartRecord, mapped ...string) map[string]json.RawMessage {
	return rest(p.Raw, append([]string{"id", "sessionID", "messageID", "type"}, mapped...)...)
}

// user converts a user message: the parts toModelMessages sends become
// one user message, the rest are kept on it.
func (v *v1walk) user(i int) error {
	sc := v.sc
	m := v.msgs[i]
	ts := ms(m.Created)
	var content openresponses.Contents
	var unsent []json.RawMessage
	// sent holds each part sent: its ID and the members the content has
	// no place for.
	var sent []map[string]json.RawMessage
	authored, synthetic := false, false
	result := ""
	for _, p := range v.parts(i) {
		pt := p.Part
		switch pt.Type {
		case opencode.PartText:
			switch {
			case pt.Ignored:
				unsent = append(unsent, p.Raw)
				sc.mark(p.Unit, sc.keepDisp())
				sc.keepNote("opencode/ignored-text", p.Unit, "unsent", "an ignored text part, which OpenCode does not send")
				continue
			case pt.Text == "" && v.ruleAt(r11442, m.Created, p.Unit, "an empty user text is not sent"):
				sc.mark(p.Unit, agentimport.LineDropped)
				sc.addf(agentimport.Dropped, "opencode/empty-text", p.Unit, "an empty text part, which OpenCode does not send from 1.14.42, is left out")
				continue
			}
			content = append(content, &openresponses.InputText{Text: pt.Text})
			if pt.Synthetic {
				sc.addf(agentimport.Kept, "opencode/synthetic-text", p.Unit, "text OpenCode wrote into the user's message, which it sends with it; the item entry carries %s:synthetic", Source)
				synthetic = true
				if id := taskResult(pt.Text); id != "" {
					result = id
				}
			} else {
				authored = true
			}
		case opencode.PartFile:
			if pt.Mime == "text/plain" || pt.Mime == "application/x-directory" {
				unsent = append(unsent, p.Raw)
				sc.mark(p.Unit, sc.keepDisp())
				sc.keepNote("opencode/file-expanded", p.Unit, "unsent", "a %s file part, which OpenCode expands into synthetic text when the prompt is made and does not send itself", pt.Mime)
				continue
			}
			if pt.URL == "" {
				unsent = append(unsent, p.Raw)
				sc.mark(p.Unit, sc.keepDisp())
				sc.keepNote("opencode/unsent-part", p.Unit, "unsent", "a file part with no URL, which OpenCode cannot send")
				continue
			}
			if !strings.HasPrefix(pt.URL, "data:") {
				sc.addf(agentimport.Lossy, "opencode/file-url", p.Unit, "a file part's URL is not a data URL; the AI SDK sends it by reference for the provider to fetch, and the session carries the reference")
			}
			content = append(content, fileContent(pt.Mime, pt.Filename, pt.URL))
			authored = true
		case opencode.PartCompaction:
			content = append(content, &openresponses.InputText{Text: compactionText})
		case opencode.PartSubtask:
			content = append(content, &openresponses.InputText{Text: subtaskText})
			sc.addf(agentimport.Lossy, "opencode/subtask", p.Unit, "a subtask part: OpenCode sends %q in its place and runs the task tool itself, in the assistant message that follows", subtaskText)
		default:
			unsent = append(unsent, p.Raw)
			sc.mark(p.Unit, sc.keepDisp())
			if !opencode.IsKnownPart(pt.Type) {
				sc.keepNote("opencode/unknown-part", p.Unit, "unsent", "a part of type %q, which this converter does not know", pt.Type)
			} else {
				sc.keepNote("opencode/unsent-part", p.Unit, "unsent", "a part of type %s, which OpenCode does not send in a user message", pt.Type)
			}
			continue
		}
		sc.mark(p.Unit, agentimport.LineMapped)
		r := partRest(p, "text", "mime", "filename", "url")
		if r == nil {
			r = map[string]json.RawMessage{}
		}
		r["id"] = rawJSON(p.ID)
		sent = append(sent, r)
	}
	info := rest(m.Raw, "id", "sessionID", "role")
	if len(content) == 0 {
		sc.addf(sc.keepKind(), "opencode/message-empty", m.Unit, "a user message with no part OpenCode sends is not in context")
		return sc.custom(ts, m.Unit, "a user message with nothing sent", map[string]any{"id": m.ID, "message": json.RawMessage(m.Raw), "unsent": unsent})
	}
	e := v.itemEntry(openresponses.UserMessage(content...), ts)
	if !authored {
		e.Visible = visible(false)
	}
	if err := sc.b.Member(e, "id", m.ID); err != nil {
		return err
	}
	if synthetic {
		if err := sc.b.Member(e, "synthetic", true); err != nil {
			return err
		}
	}
	if result != "" {
		if err := sc.b.Member(e, "subagent_result", result); err != nil {
			return err
		}
	}
	if err := sc.member(e, "message", info, m.Unit); err != nil {
		return err
	}
	if err := sc.member(e, "parts", sent, m.Unit); err != nil {
		return err
	}
	if len(unsent) > 0 && !sc.slim {
		if err := sc.b.Member(e, "unsent", unsent); err != nil {
			return err
		}
	}
	id, err := sc.append(e)
	if err != nil {
		return err
	}
	v.add(i, id)
	sc.mark(m.Unit, agentimport.LineMapped)
	return nil
}

// add records that message i wrote item entry id, which the context
// now holds.
func (v *v1walk) add(i int, id string) {
	v.items[i] = append(v.items[i], id)
	v.ctx = append(v.ctx, id)
}

// leaveOut records that no request sends the item entries ids.
func (v *v1walk) leaveOut(ids []string) {
	gone := map[string]bool{}
	for _, id := range ids {
		v.omitted[id] = true
		gone[id] = true
	}
	v.ctx = slices.DeleteFunc(v.ctx, func(id string) bool { return gone[id] })
}

// taskResult returns the child session a background task's result
// names: the synthetic text OpenCode adds to the parent when a
// background task ends, <task id="ses_…" state="…">.
func taskResult(text string) string {
	const open = `<task id="`
	if !strings.HasPrefix(text, open) {
		return ""
	}
	id, _, ok := strings.Cut(text[len(open):], `"`)
	if !ok {
		return ""
	}
	return id
}

// anthropicSigned reports provider metadata holding an Anthropic
// signature, metadata.anthropic.signature not null, which is when
// OpenCode sends an empty text beside it as a space.
func anthropicSigned(raw json.RawMessage) bool {
	var m struct {
		Anthropic struct {
			Signature json.RawMessage `json:"signature"`
		} `json:"anthropic"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	return !opencode.IsNull(m.Anthropic.Signature)
}

// block is the parts of one step of an assistant message: the AI SDK's
// convertToModelMessages splits a message at each step-start, and each
// step is one model call.
type block struct {
	start  *opencode.PartRecord
	parts  []*opencode.PartRecord
	finish *opencode.PartRecord
}

// blocks splits an assistant message's parts at each step-start, as
// convertToModelMessages does: parts before the first step-start are a
// block of their own.
func blocks(parts []*opencode.PartRecord) []*block {
	var out []*block
	cur := &block{}
	for _, p := range parts {
		switch p.Part.Type {
		case opencode.PartStepStart:
			if cur.start != nil || len(cur.parts) > 0 || cur.finish != nil {
				out = append(out, cur)
			}
			cur = &block{start: p}
		case opencode.PartStepFinish:
			if cur.finish != nil {
				cur.parts = append(cur.parts, p)
				continue
			}
			cur.finish = p
		default:
			cur.parts = append(cur.parts, p)
		}
	}
	if cur.start != nil || len(cur.parts) > 0 || cur.finish != nil {
		out = append(out, cur)
	}
	return out
}

// sendsOutput reports whether an aborted message has a part other than
// step-start and reasoning, which keeps it in context.
func sendsOutput(parts []*opencode.PartRecord) bool {
	for _, p := range parts {
		if p.Part.Type != opencode.PartStepStart && p.Part.Type != opencode.PartReasoning {
			return true
		}
	}
	return false
}

// harnessRun reports whether an assistant message is one OpenCode wrote
// itself to run a subtask's task tool: its parent user message has a
// subtask part, it has a task tool part, and no step started.
func (v *v1walk) harnessRun(i int) bool {
	m := v.msgs[i]
	j, ok := v.idx[m.Info.ParentID]
	if !ok {
		return false
	}
	sub := false
	for _, p := range v.msgs[j].Parts {
		if p.Part.Type == opencode.PartSubtask {
			sub = true
		}
	}
	if !sub {
		return false
	}
	task := false
	for _, p := range m.Parts {
		switch {
		case p.Part.Type == opencode.PartStepStart:
			return false
		case p.Part.Type == opencode.PartTool && p.Part.Tool == "task":
			task = true
		}
	}
	return task
}

// assistant converts an assistant message: one response per step, its
// calls' outputs after it, and, for a compaction's summary, the
// compaction.
func (v *v1walk) assistant(i int) error {
	sc := v.sc
	m := v.msgs[i]
	info := m.Info
	ts := ms(m.Created)
	parts := v.parts(i)
	harness := v.harnessRun(i)
	errored := info.Error != nil
	aborted := errored && info.Error.Name == opencode.ErrorAborted
	skipped := errored && !(aborted && sendsOutput(parts))
	if !harness {
		if err := sc.settings(ts, m.Unit, info.ModelID, info.ProviderID, info.Agent, info.Variant); err != nil {
			return err
		}
	}
	signed := false
	for _, p := range parts {
		if p.Part.Type == opencode.PartReasoning && anthropicSigned(p.Part.Metadata) {
			signed = true
		}
	}
	bs := blocks(parts)
	if len(bs) == 0 {
		sc.addf(sc.keepKind(), "opencode/message-empty", m.Unit, "an assistant message with no parts is not in context")
		return sc.custom(ts, m.Unit, "an assistant message with no parts", map[string]any{"id": m.ID, "message": json.RawMessage(m.Raw)})
	}
	if harness {
		sc.addf(agentimport.Lossy, "opencode/subtask", m.Unit, "OpenCode ran the subtask's task tool itself and wrote this message; its calls belong to no model response")
	}
	var lastResp *agentsession.ResponseEntry
	for k, blk := range bs {
		last := k == len(bs)-1
		resp, err := v.step(i, k, blk, last, harness, signed)
		if err != nil {
			return err
		}
		if resp != nil {
			lastResp = resp
		}
	}
	_ = lastResp
	sc.mark(m.Unit, agentimport.LineMapped)
	if skipped {
		if err := v.omit(i); err != nil {
			return err
		}
	}
	if info.IsSummary() {
		v.summaryEnded(i)
	}
	return nil
}

// omit leaves an assistant message that ended in an error out of every
// later request, as toModelMessages skips it: an error other than an
// abort, or an abort before any output.
func (v *v1walk) omit(i int) error {
	sc := v.sc
	m := v.msgs[i]
	if len(v.items[i]) == 0 {
		return nil
	}
	ids := slices.DeleteFunc(append([]string(nil), v.items[i]...), func(id string) bool { return v.omitted[id] })
	if len(ids) == 0 {
		return nil
	}
	v.leaveOut(ids)
	cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: ids}}
	if t, ok := m.Info.Completed(); ok {
		cfg.Timestamp = ms(t)
	}
	if _, err := sc.append(cfg); err != nil {
		return err
	}
	name := m.Info.Error.Name
	sc.addf(agentimport.Lossy, "opencode/error-turn", m.Unit, "the message ended in %s; OpenCode leaves it out of every later request, so its %d items are left out by omit", name, len(v.items[i]))
	return nil
}

// step converts one step of an assistant message: its reasoning, text
// and calls as output items, the response, and then each call's
// output. harness says OpenCode, not a model, wrote the calls.
func (v *v1walk) step(i, k int, blk *block, last, harness, signed bool) (*agentsession.ResponseEntry, error) {
	sc := v.sc
	m := v.msgs[i]
	info := m.Info
	respID := ""
	if !harness {
		respID = fmt.Sprintf("%s:%d", m.ID, k+1)
	}
	if blk.start != nil {
		sc.mark(blk.start.Unit, agentimport.LineMapped)
	}
	var (
		items    []*agentsession.ItemEntry
		unsigned []*agentsession.ItemEntry
		calls    []*opencode.PartRecord
		kept     []json.RawMessage
		lastAt   = ms(m.Created)
		pending  *openresponses.ReasoningItem
		pendID   string
	)
	push := func(it openresponses.Item, ts time.Time, unit string) *agentsession.ItemEntry {
		e := v.itemEntry(it, ts)
		e.ResponseID = respID
		items = append(items, e)
		return e
	}
	for _, p := range blk.parts {
		pt := p.Part
		pts := partTime(pt.Time, lastAt)
		if t, ok := toolStart(pt); ok {
			pts = t
		}
		lastAt = pts
		if pt.Type != opencode.PartReasoning {
			pending, pendID = nil, ""
		}
		switch pt.Type {
		case opencode.PartText:
			md := readMetadata(pt.Metadata)
			text := pt.Text
			if text == "" {
				if !signed || !v.ruleAt(r11440, m.Created, p.Unit, "an empty text beside signed reasoning is sent as a space") {
					kept = append(kept, p.Raw)
					sc.mark(p.Unit, sc.keepDisp())
					sc.keepNote("opencode/empty-text", p.Unit, "parts", "an empty text part, which the provider is not sent, is no item")
					continue
				}
				text = " "
				sc.addf(agentimport.Lossy, "opencode/text-space", p.Unit, "an empty text beside signed reasoning, which OpenCode sends as a single space so the reasoning keeps its place, is written as that space")
			}
			if th := thoughtItem(md); th != nil {
				push(th, pts, p.Unit)
			}
			e := push(textItem(text, md), pts, p.Unit)
			if err := v.partMembers(e, p, "text"); err != nil {
				return nil, err
			}
		case opencode.PartReasoning:
			md := readMetadata(pt.Metadata)
			if pending != nil && md.itemID != "" && md.itemID == pendID && md.redacted == "" {
				if pt.Text != "" {
					pending.Summary = append(pending.Summary, &openresponses.SummaryText{Text: pt.Text})
				}
				if md.encrypted != "" {
					pending.EncryptedContent = md.encrypted
				}
				sc.mark(p.Unit, agentimport.LineMapped)
				continue
			}
			it := reasoningItems(pt.Text, md)
			e := push(it, pts, p.Unit)
			switch takes := providerTakes(info.ProviderID, md); {
			case harness:
			case takes == "no":
				unsigned = append(unsigned, e)
				sc.addf(agentimport.Lossy, "opencode/reasoning-unsent", p.Unit, "reasoning with no %s metadata, which the AI SDK's %s provider does not send back; it is left out of the requests after its own by omit", info.ProviderID, info.ProviderID)
			case takes == "unknown" && md.signature == "" && md.encrypted == "" && md.thought == "" && md.redacted == "" && md.itemID == "" && !v.guessed["reasoning-unverified"]:
				v.guessed["reasoning-unverified"] = true
				sc.addf(agentimport.Lossy, "opencode/reasoning-unverified", p.Unit, "reasoning with no provider metadata from provider %s, whose AI SDK package OpenCode does not record and this converter cannot tell; it is taken as sent back, which @ai-sdk/anthropic and @ai-sdk/openai would not do", info.ProviderID)
			}
			if r, ok := it.(*openresponses.ReasoningItem); ok && md.itemID != "" {
				pending, pendID = r, md.itemID
			} else {
				pending, pendID = nil, ""
			}
			if md.ns != "" && md.redacted == "" && (md.signature != "" || md.encrypted != "" || md.thought != "") {
				if err := sc.b.Member(e, "provider", md.ns); err != nil {
					return nil, err
				}
				sc.addf(agentimport.Gap, "agentimport/reasoning-provider", p.Unit, "the reasoning's signature or encrypted content is %s's, which encrypted_content does not say; the item entry carries %s:provider", md.ns, Source)
			}
			if md.redacted != "" {
				sc.addf(agentimport.Kept, "opencode/redacted-thinking", p.Unit, "Anthropic's redacted thinking is an anthropic.redacted_thinking item")
			}
			if err := v.partMembers(e, p, "text"); err != nil {
				return nil, err
			}
		case opencode.PartTool:
			if pt.State == nil || !knownToolStatus(pt.State.Status) {
				kept = append(kept, p.Raw)
				sc.mark(p.Unit, sc.keepDisp())
				sc.keepNote("opencode/tool-unsent", p.Unit, "parts", "a tool part with no state OpenCode knows is not sent")
				continue
			}
			md := readMetadata(pt.Metadata)
			if opencode.MetadataBool(pt.Metadata, "providerExecuted") {
				raw, err := json.Marshal(map[string]any{"type": "opencode.hosted_tool", "part": json.RawMessage(p.Raw)})
				if err != nil {
					return nil, err
				}
				raw, _, err = agentimport.CleanJSON(raw)
				if err != nil {
					return nil, err
				}
				e := push(&openresponses.UnknownItem{Type: "opencode.hosted_tool", Raw: raw}, pts, p.Unit)
				_ = e
				sc.mark(p.Unit, agentimport.LineKept)
				sc.addf(agentimport.Kept, "opencode/hosted-tool", p.Unit, "a tool the provider ran, its call and result in the response, is an opencode.hosted_tool item holding the part")
				continue
			}
			if th := thoughtItem(md); th != nil {
				push(th, pts, p.Unit)
			}
			callID, renamed := sc.b.Calls.Assign(pt.CallID)
			fc := &openresponses.FunctionCall{CallID: callID, Name: pt.Tool, Arguments: arguments(pt.State.Input)}
			if pt.State.Status == opencode.ToolPending && opencode.IsNull(pt.State.Input) && pt.State.Raw != "" {
				fc.Arguments = pt.State.Raw
			}
			e := push(fc, pts, p.Unit)
			if renamed {
				if err := sc.b.Member(e, "call_id", pt.CallID); err != nil {
					return nil, err
				}
				sc.addf(agentimport.Repaired, "opencode/call-id", p.Unit, "call ID %q is empty, malformed or used before; the call is %s", pt.CallID, callID)
			}
			if err := v.partMembers(e, p, "callID", "tool", "state"); err != nil {
				return nil, err
			}
			calls = append(calls, p)
			v.callOf[p] = callCtx{id: callID, entry: e}
		default:
			kept = append(kept, p.Raw)
			sc.mark(p.Unit, sc.keepDisp())
			code, what := "opencode/step-part", "part of type "+pt.Type
			if !opencode.IsKnownPart(pt.Type) {
				code, what = "opencode/unknown-part", fmt.Sprintf("part of type %q, which this converter does not know,", pt.Type)
			}
			sc.keepNote(code, p.Unit, "parts", "a %s is not sent", what)
			continue
		}
		sc.mark(p.Unit, agentimport.LineMapped)
	}
	for _, e := range items {
		id, err := sc.append(e)
		if err != nil {
			return nil, err
		}
		v.add(i, id)
		if _, ok := e.Item.(*openresponses.ReasoningItem); ok {
			v.modelOf[id] = sc.model
		}
	}
	var resp *agentsession.ResponseEntry
	if !harness {
		var err error
		if resp, err = v.response(i, k, blk, last, respID, kept, lastAt); err != nil {
			return nil, err
		}
	} else if len(kept) > 0 {
		if err := sc.custom(lastAt, m.Unit, "the parts of a message OpenCode wrote itself that it does not send", map[string]any{"parts": kept}); err != nil {
			return nil, err
		}
	}
	if len(unsigned) > 0 {
		ids := make([]string, len(unsigned))
		for k, e := range unsigned {
			ids[k] = e.ID
		}
		v.leaveOut(ids)
		cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: ids}}
		cfg.Timestamp = lastAt
		if _, err := sc.append(cfg); err != nil {
			return nil, err
		}
	}
	for _, p := range calls {
		if err := v.output(i, p, info); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

// providerTakes reports whether the AI SDK provider OpenCode sends a
// reasoning part to takes it back: @ai-sdk/anthropic sends a thinking
// block only with its signature, or its redacted data, and drops one
// with neither; @ai-sdk/openai sends a reasoning item only with its
// item ID or its encrypted content. Other providers' packages are not
// read, and their reasoning is taken as sent.
func providerTakes(provider string, md provided) string {
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	switch provider {
	case "anthropic":
		return yes(md.signature != "" || md.redacted != "")
	case "openai":
		return yes(md.itemID != "" || md.encrypted != "")
	}
	return "unknown"
}

// callCtx is a call written: its call ID and its item entry.
type callCtx struct {
	id    string
	entry *agentsession.ItemEntry
}

// knownToolStatus reports a V1 tool status toModelMessages sends.
func knownToolStatus(s string) bool {
	switch s {
	case opencode.ToolPending, opencode.ToolRunning, opencode.ToolCompleted, opencode.ToolError:
		return true
	}
	return false
}

// partTime is when a part started, from its time.start, else def.
func partTime(raw json.RawMessage, def time.Time) time.Time {
	var t struct {
		Start json.RawMessage `json:"start"`
	}
	if json.Unmarshal(raw, &t) != nil {
		return def
	}
	if n, ok := opencode.Millis(t.Start); ok && n > 0 {
		return ms(n)
	}
	return def
}

// toolStart is when a tool part's state says it started.
func toolStart(p *opencode.Part) (time.Time, bool) {
	if p.Type != opencode.PartTool || p.State == nil || p.State.Time == nil {
		return time.Time{}, false
	}
	if n, ok := opencode.Millis(p.State.Time.Start); ok && n > 0 {
		return ms(n), true
	}
	return time.Time{}, false
}

// partMembers keeps on an item entry the part's ID and the members the
// item has no place for.
func (v *v1walk) partMembers(e *agentsession.ItemEntry, p *opencode.PartRecord, mapped ...string) error {
	sc := v.sc
	if err := sc.b.Member(e, "id", p.ID); err != nil {
		return err
	}
	return sc.member(e, "part", partRest(p, mapped...), p.Unit)
}

// response writes the response entry of one step: its usage and finish
// reason from the step-finish part, its status from how the message
// ended.
func (v *v1walk) response(i, k int, blk *block, last bool, respID string, kept []json.RawMessage, ts time.Time) (*agentsession.ResponseEntry, error) {
	sc := v.sc
	m := v.msgs[i]
	info := m.Info
	resp := &agentsession.ResponseEntry{ResponseID: respID, Model: info.ModelID, Status: openresponses.ResponseStatusCompleted}
	if last {
		if t, ok := info.Completed(); ok {
			ts = ms(t)
		}
	}
	resp.Timestamp = ts
	unit := m.Unit
	if blk.finish != nil {
		fin := blk.finish.Part
		unit = blk.finish.Unit
		sc.mark(blk.finish.Unit, agentimport.LineMapped)
		u, extra := usage(fin.Tokens, fin.Cost, v.outputHasReasoning(fin.Tokens, m.Created, blk.finish.Unit))
		if v.copied(i) {
			copied := map[string]any{"usage": u}
			for k, x := range extra {
				copied[k] = x
			}
			extra = nil
			if err := sc.b.Member(resp, "copied_usage", copied); err != nil {
				return nil, err
			}
			sc.addf(agentimport.Kept, "opencode/fork-usage", blk.finish.Unit, "the step is a copy an OpenCode 1 fork made, created before the fork, and its usage, the origin's, is kept in %s:copied_usage", Source)
		} else {
			resp.Usage = u
		}
		switch fin.Reason {
		case "length":
			resp.Status = openresponses.ResponseStatusIncomplete
			resp.Incomplete = &openresponses.IncompleteDetails{Reason: openresponses.IncompleteReasonMaxOutputTokens}
		case "content-filter":
			resp.Status = openresponses.ResponseStatusIncomplete
			resp.Incomplete = &openresponses.IncompleteDetails{Reason: openresponses.IncompleteReasonContentFilter}
		}
		if err := sc.b.Member(resp, "finish", fin.Reason); err != nil {
			return nil, err
		}
		if err := v.usageMembers(resp, extra, blk.finish.Unit); err != nil {
			return nil, err
		}
		if r := partRest(blk.finish, "reason", "tokens", "cost"); len(r) > 0 {
			if err := sc.member(resp, "step_finish", r, blk.finish.Unit); err != nil {
				return nil, err
			}
		}
	} else if !(last && info.Error != nil) {
		resp.Status = openresponses.ResponseStatusIncomplete
		sc.addf(agentimport.Lossy, "opencode/step-unfinished", m.Unit, "step %d of the message has no step-finish part: the call did not finish, and its usage is not recorded", k+1)
	}
	if last && info.Error != nil {
		e := info.Error
		if e.Name == opencode.ErrorAborted {
			resp.Status = openresponses.ResponseStatusIncomplete
		} else {
			resp.Status = openresponses.ResponseStatusFailed
			resp.Error = &openresponses.ErrorPayload{Code: e.Name, Message: e.Message()}
		}
		raw, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		if err := sc.b.Member(resp, "error", json.RawMessage(raw)); err != nil {
			return nil, err
		}
	}
	if blk.start != nil {
		if r := partRest(blk.start); len(r) > 0 {
			if err := sc.member(resp, "step_start", r, blk.start.Unit); err != nil {
				return nil, err
			}
		}
	}
	if len(kept) > 0 && !sc.slim {
		if err := sc.b.Member(resp, "parts", kept); err != nil {
			return nil, err
		}
	}
	if last {
		if err := sc.b.Member(resp, "message_id", m.ID); err != nil {
			return nil, err
		}
		if r := rest(m.Raw, "id", "sessionID", "role", "modelID", "error"); len(r) > 0 {
			if err := sc.member(resp, "message", r, m.Unit); err != nil {
				return nil, err
			}
		}
		if info.IsSummary() {
			sc.addf(agentimport.Lossy, "opencode/compaction-request", m.Unit, "the summary's request sent the compaction agent one user message holding the instructions and the conversation serialized as text, not the context the path rebuilds")
		}
	}
	_ = unit
	if _, err := sc.append(resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// usageMembers keeps on a response the usage Responses usage has no
// place for.
func (v *v1walk) usageMembers(resp *agentsession.ResponseEntry, extra map[string]any, unit string) error {
	return v.sc.usageMembers(resp, extra, unit)
}

// usageMembers keeps on a response the usage Responses usage has no
// place for: the cache writes, which are folded into input, the cost
// and OpenCode's total.
func (sc *sconv) usageMembers(resp *agentsession.ResponseEntry, extra map[string]any, unit string) error {
	if extra == nil {
		return nil
	}
	if err := sc.b.Member(resp, "usage", extra); err != nil {
		return err
	}
	if _, ok := extra["cache_write_tokens"]; ok {
		sc.addf(agentimport.Gap, "agentimport/usage-detail", unit, "cache writes have no place in Responses usage; they are folded into input tokens, and the count, with the cost, is in %s:usage", Source)
		return nil
	}
	sc.addf(agentimport.Kept, "opencode/usage-member", unit, "the cost and the total OpenCode records with the usage are in %s:usage", Source)
	return nil
}

// output writes a call's output, after the decision that ended it when
// it did not run or did not finish, and the sub-agent links around it.
func (v *v1walk) output(i int, p *opencode.PartRecord, info *opencode.Message) error {
	sc := v.sc
	pt := p.Part
	st := pt.State
	cc := v.callOf[p]
	ts := ms(v.msgs[i].Created)
	if st.Time != nil {
		if n, ok := opencode.Millis(st.Time.End); ok && n > 0 {
			ts = ms(n)
		} else if n, ok := opencode.Millis(st.Time.Start); ok && n > 0 {
			ts = ms(n)
		}
	}
	var data openresponses.FunctionCallOutputData
	isError, synthesized := false, false
	switch st.Status {
	case opencode.ToolCompleted:
		var nmedia int
		data, nmedia = toolOutput(st.Output, st.Attachments)
		if nmedia > 0 && !mediaInToolResults[info.ProviderID] {
			sc.addf(agentimport.Lossy, "opencode/media-relocated", p.Unit, "the output carries %d media attachments; for provider %s OpenCode may move them to a user message after the assistant's, which depends on the provider's package and is not reproduced", nmedia, info.ProviderID)
		}
		if at, ok := st.Compacted(); ok {
			v.prunes = append(v.prunes, prune{at: at, callID: cc.id, unit: p.Unit})
		}
	case opencode.ToolError:
		if out, ok := st.Interrupted(); ok && v.ruleAt(r142, v.msgs[i].Created, p.Unit, "an interrupted call sends the output it had") {
			data = openresponses.FunctionCallOutputData{Text: out}
			sc.addf(agentimport.Lossy, "opencode/call-interrupted", p.Unit, "the call was cleaned up after an abort, and OpenCode sends the output it had as the call's output")
			break
		}
		data = openresponses.FunctionCallOutputData{Text: st.Error}
		isError = true
		if verdict, by := permissionText(st.Error); verdict != "" {
			d := agentsession.NewDecision(cc.id, cc.entry.ID, verdict, by).WithReason(st.Error)
			d.Timestamp = ts
			if _, err := sc.append(d); err != nil {
				return err
			}
			sc.addf(agentimport.Lossy, "opencode/permission-text", p.Unit, "the call's error is the text OpenCode gives a refused permission; it is a reject decision by %s, told by the text alone", by)
		}
	case opencode.ToolPending, opencode.ToolRunning:
		data = openresponses.FunctionCallOutputData{Text: interruptedText}
		isError, synthesized = true, true
		verdict := agentsession.VerdictReject
		if st.Status == opencode.ToolRunning {
			verdict = agentsession.VerdictAnswer
		}
		d := agentsession.NewDecision(cc.id, cc.entry.ID, verdict, "policy").WithReason(interruptedText)
		d.Timestamp = ts
		if _, err := sc.append(d); err != nil {
			return err
		}
		sc.addf(agentimport.Lossy, "opencode/call-unfinished", p.Unit, "the call was %s when the message ended; OpenCode sends %q as its output, which follows a decision, %s by policy", st.Status, interruptedText, verdict)
	}
	refs, err := sc.linkBefore(pt.CallID, cc.id, p.Unit, ts)
	if err != nil {
		return err
	}
	out := v.itemEntry(&openresponses.FunctionCallOutput{CallID: cc.id, Output: data}, ts)
	out.Parents = refs
	if isError {
		if err := sc.b.Member(out, "is_error", true); err != nil {
			return err
		}
		if !synthesized {
			sc.addf(agentimport.Gap, "agentimport/tool-error", p.Unit, "a function_call_output has no error flag; the item entry carries %s:is_error", Source)
		}
	}
	if synthesized {
		if err := sc.b.Member(out, "synthesized", true); err != nil {
			return err
		}
	}
	r := rest(stateRaw(st), "status", "input", "output", "error", "attachments")
	if unsent := unsentAttachments(st.Attachments); len(unsent) > 0 {
		if r == nil {
			r = map[string]json.RawMessage{}
		}
		r["attachments"] = rawJSON(unsent)
	}
	if len(r) > 0 {
		if err := sc.member(out, "state", r, p.Unit); err != nil {
			return err
		}
	}
	id, err := sc.append(out)
	if err != nil {
		return err
	}
	v.add(i, id)
	return sc.linkAfter(pt.CallID, cc.id, p.Unit, ts)
}

// stateRaw is a tool state as JSON.
func stateRaw(st *opencode.ToolState) json.RawMessage {
	raw, err := json.Marshal(st)
	if err != nil {
		return nil
	}
	return raw
}

// permissionText tells a refused permission by the error text OpenCode
// gives it: a rejection by the user, with or without feedback, or a
// rule that denies the call.
func permissionText(text string) (verdict, by string) {
	switch {
	case strings.Contains(text, correctedText), strings.Contains(text, rejectedText):
		return agentsession.VerdictReject, "human"
	case strings.Contains(text, deniedText):
		return agentsession.VerdictReject, "policy"
	}
	return "", ""
}

// copied reports whether message i is a copy an OpenCode 1 fork made:
// Session.fork copies each message whole, its creation time with it, so
// in a fork a message created before the fork itself is the origin's.
func (v *v1walk) copied(i int) bool {
	if !v.forked || !v.sc.rec.v1.HasCreated {
		return false
	}
	t, ok := v.msgs[i].Info.Created()
	return ok && t < v.sc.rec.v1.Created
}

// outputHasReasoning tells whether a step's output tokens count its
// reasoning tokens, which OpenCode's did up to 1.3.15. The step's total,
// the AI SDK's, settles it when there is reasoning: input with the
// cache plus output, or plus reasoning too. Without one the rule is the
// release's that made the request.
func (v *v1walk) outputHasReasoning(t *opencode.Tokens, at int64, unit string) bool {
	if t == nil {
		return false
	}
	reasoning := opencode.Count(t.Reasoning)
	byRelease := func() bool {
		return !v.ruleAt(r1316, at, unit, "output tokens leave out reasoning")
	}
	if reasoning == 0 {
		return byRelease()
	}
	total, ok := opencode.Int(t.Total)
	if !ok {
		return byRelease()
	}
	read, write := 0, 0
	if t.Cache != nil {
		read, write = opencode.Count(t.Cache.Read), opencode.Count(t.Cache.Write)
	}
	base := int64(opencode.Count(t.Input) + read + write + opencode.Count(t.Output))
	var has bool
	switch total {
	case base:
		has = true
	case base + int64(reasoning):
		has = false
	default:
		return byRelease()
	}
	if want := !(v.verOK && v.ver.atLeast(r1316.v)); has != want && !v.guessed["usage-total"] {
		v.guessed["usage-total"] = true
		v.sc.addf(agentimport.Lossy, "opencode/usage-version", unit, "a step's total says its output tokens %s reasoning, against the rule of the release that created the session, %s; each step's usage is read by its total where it settles it", map[bool]string{true: "count", false: "leave out"}[has], v.ver)
	}
	return has
}

// unsentAttachments are a completed call's attachments OpenCode does not
// send: those whose URL is not a data URL.
func unsentAttachments(attachments []json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	for _, raw := range attachments {
		a, ok := opencode.Attachment(raw)
		if !ok || !strings.HasPrefix(a.URL, "data:") || !strings.Contains(a.URL, ",") {
			out = append(out, raw)
		}
	}
	return out
}
