package convert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// From 4.1 Cline keeps the canonical history whole and writes the
// latest compaction's context to the sidecar: every request after it
// sends the sidecar's messages, a summary and the messages it kept,
// then the history after the messages it replaced. Before 4.1 a
// compaction rewrote the history itself, its summary a message of it.

// compPlan is where and how the sidecar's compaction is written.
type compPlan struct {
	c     *clinesdk.Compaction
	apply bool
	// how says what tied the sidecar to the history; why why it did
	// not apply.
	how, why string
	// at is the history message the compaction is written before,
	// len(history) for after the last.
	at    int
	count int
	// tail is the history message the sidecar's kept messages start
	// at, -1 when it kept none; tailN how many it kept; pinned the
	// sidecar's messages between the summary and those.
	tail   int
	tailN  int
	pinned []*clinesdk.Message
}

// planCompaction decides whether the sidecar applies, as Cline decides
// on resume, and where its compaction goes.
func (sc *sconv) planCompaction(c *clinesdk.Compaction) *compPlan {
	msgs := sc.h.f.Messages
	pr := c.Project(msgs)
	plan := &compPlan{c: c, count: pr.Count, tail: -1}
	switch {
	case pr.Applies:
		plan.apply, plan.how = true, pr.By
	case pr.Unhashed != nil:
		plan.how = "structure"
	default:
		plan.why = pr.Why
		return plan
	}
	if len(c.Messages) == 0 {
		plan.apply, plan.why = false, "it holds no messages"
		return plan
	}
	var source []int
	for k, m := range msgs {
		if m.Err == nil && m.DisplayOnly() {
			continue
		}
		source = append(source, k)
	}
	if plan.count > len(source) {
		plan.apply, plan.why = false, fmt.Sprintf("it replaces %d messages, and the history holds %d", plan.count, len(source))
		return plan
	}
	plan.at = len(msgs)
	if plan.count < len(source) {
		plan.at = source[plan.count]
	}
	sm := c.Messages
	plan.pinned = sm[1:]
	for j := 1; j < len(sm); j++ {
		n := len(sm) - j
		if n > plan.count {
			continue
		}
		same := true
		for i := 0; i < n; i++ {
			if !sameMessage(sm[j+i], msgs[source[plan.count-n+i]]) {
				same = false
				break
			}
		}
		if same {
			plan.tail, plan.tailN, plan.pinned = source[plan.count-n], n, sm[1:j]
			break
		}
	}
	if plan.how == "structure" {
		if plan.tailN == 0 {
			plan.apply, plan.why = false, "the history holds a message the hash here cannot reproduce ("+agentimport.ErrorText(pr.Unhashed)+"), and the sidecar's messages copy none of the history"
			return plan
		}
		plan.apply = true
	}
	return plan
}

// sameMessage reports whether two messages have one role and one
// content, as JSON values.
func sameMessage(a, b *clinesdk.Message) bool {
	if a.Err != nil || b.Err != nil {
		return false
	}
	va, err := jsjson.Parse(a.Elem)
	if err != nil {
		return false
	}
	vb, err := jsjson.Parse(b.Elem)
	if err != nil {
		return false
	}
	for _, k := range []string{"role", "content"} {
		x, y := va.Get(k), vb.Get(k)
		if x == nil || y == nil || string(x.Stringify()) != string(y.Stringify()) {
			return false
		}
	}
	return true
}

// writeCompaction writes the sidecar's compaction where its source
// ends: its first message the summary, the messages it kept from the
// history from first_kept, the ones between pinned.
func (sc *sconv) writeCompaction() error {
	plan := sc.comp
	c := plan.c
	sm := c.Messages
	ts, ok := clinesdk.ISOTime(c.UpdatedAt)
	if !ok {
		ts = sc.last
	}
	items, err := sc.renderItems(-1, sm[0])
	if err != nil {
		return err
	}
	var summary openresponses.Item
	var pinned openresponses.Items
	for _, it := range items {
		if m, ok := it.(*openresponses.Message); ok && summary == nil {
			summary = m
			continue
		}
		pinned = append(pinned, it)
	}
	if summary == nil {
		summary = userMessage(openresponses.Contents{&openresponses.InputText{Text: ""}})
		sc.addf(agentimport.Lossy, "clinesdk/compaction-summary", c.Name, 1, "the sidecar's first message holds no text to summarise with; the summary is empty and its items are pinned")
	}
	if !sm[0].IsCompactionSummary() {
		sc.addf(agentimport.Lossy, "clinesdk/compaction-summary", c.Name, 1, "the sidecar's first message is not a compaction summary, as Cline's basic compaction writes; it is the summary, and the messages after it are pinned")
	}
	for _, m := range plan.pinned {
		its, err := sc.renderItems(-1, m)
		if err != nil {
			return err
		}
		pinned = append(pinned, its...)
	}
	first := ""
	if plan.tail >= 0 {
		for k := plan.tail; k < plan.at && first == ""; k++ {
			first = sc.first[k]
		}
	}
	if first == "" {
		data, _ := json.Marshal(map[string]string{"kind": "compaction_marker"})
		marker := &agentsession.CustomEntry{NS: Source, Data: data}
		marker.Timestamp = ts
		if first, err = sc.appendEntry(marker); err != nil {
			return err
		}
		sc.addf(agentimport.Kept, "clinesdk/compaction-marker", c.Name, 0, "the compaction keeps no message of the history after its summary; a custom marker is its first_kept")
	}
	cfg := agentsession.Settings{Model: sc.model}
	if sp := c.SystemPrompt; sp != nil {
		cfg.Instructions = *sp
	} else if sp := sc.h.f.SystemPrompt; sp != nil {
		cfg.Instructions = *sp
	}
	e := &agentsession.CompactionEntry{FirstKept: first, Summary: summary, Pinned: pinned, Config: cfg}
	e.Timestamp = ts
	if tb, ok := tokensBefore(sm[0]); ok {
		e.TokensBefore = tb
	}
	if err := sc.sidecarMembers(e, plan); err != nil {
		return err
	}
	if _, err := sc.appendEntry(e); err != nil {
		return err
	}
	sc.mark(unit{uComp, 0, 0}, agentimport.LineMapped)
	sc.mark(unit{uCompMsg, 0, 0}, agentimport.LineMapped)
	for k := 1; k <= len(plan.pinned); k++ {
		sc.mark(unit{uCompMsg, 0, k}, agentimport.LineMapped)
	}
	for k := len(plan.pinned) + 1; k < len(sm); k++ {
		sc.mark(unit{uCompMsg, 0, k}, agentimport.LineDropped)
		sc.addf(agentimport.Dropped, "clinesdk/compaction-copy", c.Name, k+1, "the sidecar's copy of history message %d, which the compaction keeps from first_kept", plan.tail+k-len(plan.pinned))
	}
	how := map[string]string{
		"hash":      "its source_prefix_hash matches the history",
		"boundary":  "its source_last_message_key matches the history",
		"structure": "its kept messages copy the history's; the hash cannot be reproduced here",
	}[plan.how]
	sc.addf(agentimport.Lossy, "clinesdk/compaction", c.Name, 0, "the sidecar's compaction is written before history message %d (%s): every request after it sends its summary, %d rewritten messages pinned, and the history from first_kept", plan.at+1, how, len(plan.pinned))
	if plan.how == "structure" {
		sc.addf(agentimport.Lossy, "clinesdk/compaction-unverified", c.Name, 0, "the sidecar is tied to the history by its copies alone: the history holds a message the hash here cannot reproduce")
	}
	sc.addf(agentimport.Lossy, "clinesdk/compaction-latest-only", c.Name, 0, "Cline keeps only its latest compaction: the requests before it are written with the whole history, though an earlier compaction, overwritten, may have cut it")
	if id := c.ConversationID; id != "" && id != sc.fam.rootID() && (sc.h.row == nil || sc.h.row.ConversationID != id) {
		sc.addf(agentimport.Lossy, "clinesdk/compaction-conversation", c.Name, 0, "the sidecar names conversation %s, not the session; Cline applies it when that is the agent's conversation, which the files do not record; it is applied", id)
	}
	if plan.tail >= 0 {
		k := plan.tail
		for _, m := range sm[1+len(plan.pinned):] {
			for k < plan.at && sc.h.f.Messages[k].Err == nil && sc.h.f.Messages[k].DisplayOnly() {
				k++
			}
			if k < plan.at {
				sc.rd.copySent(sc.h.f.Messages[k], m)
			}
			k++
		}
	}
	sc.project(c.Messages)
	return nil
}

// sidecarMembers keeps what the compaction entry has no place for: the
// sidecar's envelope, and the members of the summary and the pinned
// messages beside their role and content, as clinesdk:sidecar.
func (sc *sconv) sidecarMembers(e *agentsession.CompactionEntry, plan *compPlan) error {
	c := plan.c
	if sc.fam.opts.Slim {
		sc.addf(agentimport.Dropped, "clinesdk/members", c.Name, 0, "the sidecar's envelope and its messages' members the compaction has no place for are left out")
		return nil
	}
	g := *c
	g.HasMessages, g.Messages = false, nil
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return err
	}
	var rests []map[string]json.RawMessage
	for _, m := range c.Messages[:1+len(plan.pinned)] {
		rest, err := messageRest(m)
		if err != nil {
			return err
		}
		if rest == nil {
			rest = map[string]json.RawMessage{}
		}
		rests = append(rests, rest)
	}
	if raw, err = json.Marshal(rests); err != nil {
		return err
	}
	env["messages"] = raw
	if raw, err = json.Marshal(env); err != nil {
		return err
	}
	if raw, err = sc.clean(0, raw); err != nil {
		return err
	}
	if err := sc.b.Member(e, "sidecar", json.RawMessage(raw)); err != nil {
		return err
	}
	sc.addf(agentimport.Kept, "clinesdk/members", c.Name, 0, "the sidecar's envelope, and its summary's and pinned messages' members, are %s:sidecar", Source)
	return nil
}

// tokensBefore is a summary's metadata.tokensBefore.
func tokensBefore(m *clinesdk.Message) (int, bool) {
	n, exact, ok := clinesdk.Int(m.Meta()["tokensBefore"])
	if !ok || !exact || n <= 0 || n > 1<<40 {
		return 0, false
	}
	return int(n), true
}

// staleCompaction writes a sidecar Cline would not apply as a custom
// entry.
func (sc *sconv) staleCompaction() error {
	c := sc.comp.c
	fkind, d := sc.keeping()
	sc.addf(fkind, "clinesdk/stale-compaction", c.Name, 0, "Cline does not apply the sidecar to the history (%s); %s", sc.comp.why, map[bool]string{true: "it is left out", false: "it is a custom entry after the last message"}[sc.fam.opts.Slim])
	mark := func() {
		if c.Err != nil {
			sc.mark(unit{uComp, 0, 0}, agentimport.LineMalformed)
			sc.addf(agentimport.Repaired, "clinesdk/malformed-file", c.Name, 0, "%s", agentimport.ErrorText(c.Err))
		} else {
			sc.mark(unit{uComp, 0, 0}, d)
		}
		for k := range c.Messages {
			sc.mark(unit{uCompMsg, 0, k}, d)
		}
	}
	if sc.fam.opts.Slim {
		mark()
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if raw, err = sc.clean(0, raw); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"kind": rawJSON("stale_compaction"), "why": rawJSON(sc.comp.why), "compaction": raw})
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	if ts, ok := clinesdk.ISOTime(c.UpdatedAt); ok && ts.After(sc.last) {
		e.Timestamp = ts
	}
	if _, err := sc.appendEntry(e); err != nil {
		return err
	}
	mark()
	return nil
}

// renderItems are the items a message sends, outside the walk: a
// compaction's summary and pinned messages. A call or output the walk
// wrote keeps the call ID it was given.
func (sc *sconv) renderItems(k int, m *clinesdk.Message) (openresponses.Items, error) {
	var out openresponses.Items
	var parts openresponses.Contents
	flush := func() {
		if len(parts) > 0 {
			out = append(out, userMessage(parts))
			parts = nil
		}
	}
	callID := func(native string) string {
		if p := sc.calls[native]; p != nil {
			return p.id
		}
		return native
	}
	for _, b := range m.Blocks() {
		if b.Err != nil {
			continue
		}
		switch {
		case b.Type == clinesdk.BlockToolResult:
			flush()
			content, err := jsjson.Parse(b.Content)
			if err != nil {
				content = jsjson.NewString("")
			}
			out = append(out, &openresponses.FunctionCallOutput{CallID: callID(b.ToolUseID), Output: render(truncateResult(content), b.IsError)})
		case m.Role == clinesdk.RoleUser && b.Type == clinesdk.BlockText:
			parts = append(parts, &openresponses.InputText{Text: clinesdk.NormalizeUserInput(b.Text)})
		case m.Role == clinesdk.RoleUser && b.Type == clinesdk.BlockImage:
			parts = append(parts, &openresponses.InputImage{ImageURL: dataURL(b.MediaType, b.Data), Detail: openresponses.ImageDetailAuto})
		case m.Role == clinesdk.RoleUser && b.Type == clinesdk.BlockFile:
			content, _ := b.FileText()
			parts = append(parts, &openresponses.InputText{Text: fileContent(b.Path, truncateMiddle(content, maxFileContentChars, truncatedMarker))})
		case b.Type == clinesdk.BlockText:
			out = append(out, &openresponses.Message{Role: openresponses.RoleAssistant, Status: openresponses.StatusCompleted,
				Content: openresponses.Contents{&openresponses.OutputText{Text: b.Text, Annotations: openresponses.Annotations{}}}})
		case b.Type == clinesdk.BlockThinking:
			r := &openresponses.ReasoningItem{Status: openresponses.StatusCompleted, Summary: openresponses.Contents{}, EncryptedContent: b.Signature}
			if b.Thinking != "" {
				r.Summary = openresponses.Contents{&openresponses.SummaryText{Text: b.Thinking}}
			}
			out = append(out, r)
		case b.Type == clinesdk.BlockToolUse:
			args := "{}"
			if in, err := jsjson.Parse(b.Input); err == nil {
				args = string(in.Stringify())
			}
			out = append(out, &openresponses.FunctionCall{Status: openresponses.StatusCompleted, CallID: callID(b.ID), Name: b.Name, Arguments: args})
		default:
			raw, err := json.Marshal(b)
			if err != nil {
				return nil, err
			}
			if raw, err = sc.clean(k+1, raw); err != nil {
				return nil, err
			}
			typ := extType(b.Type)
			body, err := json.Marshal(struct {
				Type   string               `json:"type"`
				Status openresponses.Status `json:"status"`
				Block  json.RawMessage      `json:"block"`
			}{typ, openresponses.StatusCompleted, raw})
			if err != nil {
				return nil, err
			}
			out = append(out, &openresponses.UnknownItem{Type: typ, Status: openresponses.StatusCompleted, Raw: body})
		}
	}
	flush()
	if len(m.Blocks()) == 0 && m.Role == clinesdk.RoleUser {
		out = append(out, userMessage(openresponses.Contents{&openresponses.InputText{Text: emptyContent}}))
	}
	return out, nil
}

// forkCopy is how many messages a fork copied from its source: the
// prefix the source's history shares with it when the source is beside
// it, else the messages written before the fork was made.
func (sc *sconv) forkCopy(fk *clinesdk.Fork) int {
	msgs := sc.h.f.Messages
	if dir := sessionsDir(sc.fam.s); dir != "" && storeName(fk.From) {
		if _, err := os.Stat(filepath.Join(dir, fk.From)); err == nil {
			src, err := clinesdk.ReadSession(filepath.Join(dir, fk.From))
			if err == nil && src.Messages != nil {
				n := 0
				for n < len(msgs) && n < len(src.Messages.Messages) && sameMessage(msgs[n], src.Messages.Messages[n]) {
					n++
				}
				sc.addf(agentimport.Lossy, "clinesdk/fork-copied", "", 0, "the session is a fork of %s, named in parent_session and by a fork_of link; its first %d messages are the source's, as its history beside it shows, converted as its own, their usage kept off the responses", fk.From, n)
				return n
			}
		}
	}
	at, ok := fk.When()
	n := 0
	if ok {
		for n < len(msgs) {
			ts, has := msgs[n].Time()
			if msgs[n].Err != nil || !has || ts.After(at) {
				break
			}
			n++
		}
	}
	sc.addf(agentimport.Lossy, "clinesdk/fork-copied", "", 0, "the session is a fork of %s, named in parent_session and by a fork_of link; the source's history is not beside it, so its first %d messages, written before the fork, are taken as the copy, a guess; they are converted as its own, their usage kept off the responses", fk.From, n)
	return n
}
