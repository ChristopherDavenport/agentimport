package convert

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/cline"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// written records an entry API element k put on the path, and the
// item it put in context, when it put one.
func (c *conv) written(k int, id string, item openresponses.Item) {
	if c.first[k] == "" {
		c.first[k] = id
	}
	if item != nil {
		c.items[k] = append(c.items[k], item)
	}
}

func itemEntry(item openresponses.Item, ts time.Time) *agentsession.ItemEntry {
	e := agentsession.NewItemEntry(item)
	e.Timestamp = ts
	return e
}

// messageRest is a message's members beside those the conversion maps
// (role, content, ts and the named ones), as an object; nil when there
// are none.
func messageRest(m *cline.Message, mapped ...string) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return nil, nil
	}
	for _, k := range append([]string{"role", "content", "ts"}, mapped...) {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

// blockRest is a block's members beside those its mapping takes.
func blockRest(b *cline.Block, mapped ...string) map[string]json.RawMessage {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	for _, k := range append([]string{"type"}, mapped...) {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

// keepMembers sets the members a conversion does not map as member
// name of e, or reports them left out with Slim.
func (c *conv) keepMembers(e agentsession.Entry, k int, name string, rest map[string]json.RawMessage, what string) error {
	if len(rest) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rest))
	for key := range rest {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	kind, _ := c.keeping()
	if c.fam.opts.Slim {
		c.apiAt(kind, "cline/members", k, "%s members %s are left out", what, strings.Join(keys, ", "))
		return nil
	}
	raw, err := json.Marshal(rest)
	if err != nil {
		return err
	}
	if raw, err = c.clean(c.t.API.Name, k+1, raw); err != nil {
		return err
	}
	if err := c.b.Member(e, name, json.RawMessage(raw)); err != nil {
		return err
	}
	c.apiAt(kind, "cline/members", k, "%s members %s are %s:%s", what, strings.Join(keys, ", "), Source, name)
	return nil
}

// user converts a user message: an output for each tool result that
// answers a call of the last response, each after the decision a
// refusal records and the link to a sub-task the call waited for; then
// what else it holds as one user message. Tool results come first, as
// the Anthropic API requires and Cline sends them.
func (c *conv) user(k int, m *cline.Message) error {
	ts, _ := m.Time()
	u := unit{uAPI, k}
	if m.Content.Err != nil {
		_, err := c.customAPI(k, m, "cline/content-malformed", "a message whose content is neither text nor blocks is a custom entry")
		return err
	}
	var results, rest []*cline.Block
	var restIdx []int
	textFirst := false
	if m.Content.IsText {
		rest = append(rest, &cline.Block{Type: cline.BlockText, Text: m.Content.Text})
		restIdx = append(restIdx, -1)
	}
	for i, b := range m.Content.Blocks {
		if b.Err == nil && b.Type == cline.BlockToolResult {
			if len(rest) > 0 {
				textFirst = true
			}
			results = append(results, b)
			continue
		}
		rest = append(rest, b)
		restIdx = append(restIdx, i)
	}
	if textFirst {
		c.apiAt(agentimport.Lossy, "cline/results-first", k, "the message holds content before its tool results; the outputs are written first, as the request sends them")
	}
	// The calls a request answers here: those of the response just
	// before, when this message follows it.
	follows := c.pendingFrom == k-1
	memberSet := false
	setMembers := func(e agentsession.Entry) error {
		if memberSet {
			return nil
		}
		memberSet = true
		rest, err := messageRest(m)
		if err != nil {
			return err
		}
		return c.keepMembers(e, k, "message", rest, "the message's")
	}
	for _, b := range results {
		p := c.pending[b.ToolUseID]
		if p == nil || p.output {
			if err := c.orphan(k, b, ts, setMembers); err != nil {
				return err
			}
			continue
		}
		if err := c.result(k, m, b, p, ts, setMembers); err != nil {
			return err
		}
	}
	if !c.roo && follows {
		if err := c.missingResults(k, ts, setMembers); err != nil {
			return err
		}
	}
	if len(rest) > 0 {
		parts, members := c.parts(k, rest, restIdx)
		if len(parts) > 0 || len(members) > 0 {
			e := itemEntry(openresponses.UserMessage(parts...), ts)
			if len(members) > 0 {
				if err := c.keepMembers(e, k, "blocks", members, "the blocks'"); err != nil {
					return err
				}
			}
			if err := setMembers(e); err != nil {
				return err
			}
			id, err := c.appendEntry(e)
			if err != nil {
				return err
			}
			c.written(k, id, e.Item)
		}
		c.notes(k, rest)
	}
	if c.first[k] == "" {
		c.apiAt(agentimport.Dropped, "cline/empty-message", k, "a user message with nothing to convert is left out")
		c.mark(u, agentimport.LineDropped)
		return nil
	}
	if !c.roo && k == c.cut && !c.cutDone && len(c.outputs[k]) > 0 {
		// The range in force leaves this first message after it without
		// its tool results, whose calls it deleted.
		for _, id := range c.outputs[k] {
			c.omitted[id] = k
		}
		cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: slices.Clone(c.outputs[k])}}
		cfg.Timestamp = ts
		if _, err := c.appendEntry(cfg); err != nil {
			return err
		}
		c.cutDone = true
	}
	c.mark(u, agentimport.LineMapped)
	return nil
}

// orphan keeps a tool result that answers no call of the response
// before it as a custom entry.
func (c *conv) orphan(k int, b *cline.Block, ts time.Time, setMembers func(agentsession.Entry) error) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if raw, err = c.clean(c.t.API.Name, k+1, raw); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"orphan_result": raw})
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	e.Timestamp = ts
	if err := setMembers(e); err != nil {
		return err
	}
	id, err := c.appendEntry(e)
	if err != nil {
		return err
	}
	c.written(k, id, nil)
	c.apiAt(agentimport.Kept, "cline/orphan-result", k, "a tool result for %q, which no call of the response before it made, is a custom entry", b.ToolUseID)
	return nil
}

// result writes a tool result: a reject decision when it refuses the
// call, the link to the sub-task a new_task call waited for, and the
// output.
func (c *conv) result(k int, m *cline.Message, b *cline.Block, p *call, ts time.Time, setMembers func(agentsession.Entry) error) error {
	text := resultText(b)
	if by, reason, ok := refusal(text, b.IsError); ok {
		d := agentsession.NewDecision(p.id, p.entry, agentsession.VerdictReject, by).WithReason(reason)
		d.Timestamp = ts
		id, err := c.appendEntry(d)
		if err != nil {
			return err
		}
		c.written(k, id, nil)
		c.apiAt(agentimport.Lossy, "cline/rejection", k, "the tool result refuses call %s (%s); a reject decision by %s precedes the output", p.id, p.name, by)
	}
	var parents []agentsession.EntryRef
	if p.name == "new_task" {
		var err error
		if parents, err = c.subtask(k, p, text, ts); err != nil {
			return err
		}
	}
	out := &openresponses.FunctionCallOutput{CallID: p.id}
	if b.Content != nil {
		switch {
		case b.Content.IsText:
			out.Output.Text = b.Content.Text
		case b.Content.Err == nil:
			out.Output.Parts, _ = c.parts(k, b.Content.Blocks, nil)
		default:
			c.apiAt(agentimport.Lossy, "cline/result-malformed", k, "a tool result whose content is neither text nor blocks is an empty output; the content is %s:block", Source)
		}
	}
	e := itemEntry(out, ts)
	e.Parents = parents
	if b.IsError {
		if err := c.b.Member(e, "is_error", true); err != nil {
			return err
		}
		c.apiAt(agentimport.Gap, "agentimport/tool-error", k, "is_error is the member %s:is_error; function_call_output has no error flag", Source)
	}
	mapped := []string{"tool_use_id", "content", "is_error"}
	if b.Content != nil && b.Content.Err != nil {
		mapped = mapped[:1]
	}
	if err := c.keepMembers(e, k, "block", blockRest(b, mapped...), "the tool result's"); err != nil {
		return err
	}
	if err := setMembers(e); err != nil {
		return err
	}
	id, err := c.appendEntry(e)
	if err != nil {
		return err
	}
	p.output = true
	c.written(k, id, out)
	c.outputs[k] = append(c.outputs[k], id)
	return nil
}

// missingResults writes the output Cline puts in a request for each
// call of the response before that this message does not answer.
func (c *conv) missingResults(k int, ts time.Time, setMembers func(agentsession.Entry) error) error {
	for _, p := range c.calls {
		if p.output {
			continue
		}
		out := &openresponses.FunctionCallOutput{CallID: p.id, Output: openresponses.FunctionCallOutputData{Text: resultMissingText}}
		e := itemEntry(out, ts)
		if err := c.b.Member(e, "synthesized", true); err != nil {
			return err
		}
		if err := setMembers(e); err != nil {
			return err
		}
		id, err := c.appendEntry(e)
		if err != nil {
			return err
		}
		p.output = true
		c.written(k, id, out)
		c.outputs[k] = append(c.outputs[k], id)
		c.apiAt(agentimport.Repaired, "cline/result-missing", k, "no tool result answers call %s (%s); Cline sends %q in its place, which is written, marked %s:synthesized", p.id, p.name, resultMissingText, Source)
	}
	return nil
}

// notes reports what the blocks of a user message show: the XML-era
// results, a sub-task's result as text.
func (c *conv) notes(k int, blocks []*cline.Block) {
	for _, b := range blocks {
		if b.Err != nil || b.Type != cline.BlockText {
			continue
		}
		switch {
		case subtaskDone.MatchString(b.Text), strings.HasPrefix(b.Text, "[new_task completed]"):
			c.apiAt(agentimport.Lossy, "cline/roo-subtask-unlinked", k, "a sub-task's result is user text, which names no call; the sub-task is not linked and converts on its own")
		case xmlResult.MatchString(b.Text):
			c.apiAt(agentimport.Lossy, "cline/xml-tool-calls", k, "an XML-era tool result is user text, as the model saw it")
		}
	}
}

var (
	subtaskDone    = regexp.MustCompile(`^Subtask (\S+) completed\.`)
	subtaskPending = regexp.MustCompile(`^Delegated to child task (\S+)`)
	xmlResult      = regexp.MustCompile(`^\[[a-z_]+(?: for [^\]]*)?\] Result:`)
	feedbackRE     = regexp.MustCompile(`(?s)<feedback>\n?(.*?)\n?</feedback>`)
)

// resultText is what a tool result says, as text.
func resultText(b *cline.Block) string {
	if b.Content == nil || b.Content.Err != nil {
		return ""
	}
	if b.Content.IsText {
		return b.Content.Text
	}
	var parts []string
	for _, x := range b.Content.Blocks {
		if x.Err == nil && x.Type == cline.BlockText {
			parts = append(parts, x.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// refusal reports whether a tool result says the call was refused, by
// whom and why: the user's denial (Cline's text, Roo's JSON), a
// command permission, or an ignore file.
func refusal(text string, isError bool) (by, reason string, ok bool) {
	t := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(t, "The user denied this operation."):
		reason = "The user denied this operation."
		if f := feedbackRE.FindStringSubmatch(t); f != nil {
			reason = strings.TrimSpace(f[1])
		}
		return agentsession.ByHuman, reason, true
	case strings.HasPrefix(t, "Command execution blocked by CLINE_COMMAND_PERMISSIONS"):
		return agentsession.ByPolicy, t, true
	case strings.Contains(t, "is blocked by the .clineignore file settings"):
		return agentsession.ByPolicy, t, true
	case strings.HasPrefix(t, "{"):
		var j struct {
			Status   string `json:"status"`
			Type     string `json:"type"`
			Message  string `json:"message"`
			Feedback string `json:"feedback"`
		}
		if json.Unmarshal([]byte(t), &j) != nil {
			return "", "", false
		}
		switch {
		case j.Status == "denied":
			reason = j.Feedback
			if reason == "" {
				reason = j.Message
			}
			if reason == "" {
				reason = "The user denied this operation."
			}
			return agentsession.ByHuman, reason, true
		case j.Status == "error" && j.Type == "access_denied":
			reason = j.Message
			if reason == "" {
				reason = "access denied"
			}
			return agentsession.ByPolicy, reason, true
		}
	}
	return "", "", false
}

// parts converts user content blocks to input parts: text, images and
// documents as Open Responses has them, any other block as the
// extension part anthropic.<type>. idx gives each block's index in its
// message, -1 for string content; the blocks' unmapped members are
// returned by index.
func (c *conv) parts(k int, blocks []*cline.Block, idx []int) (openresponses.Contents, map[string]json.RawMessage) {
	out := openresponses.Contents{}
	var members map[string]json.RawMessage
	keep := func(i int, rest map[string]json.RawMessage) {
		if len(rest) == 0 || idx == nil || idx[i] < 0 {
			return
		}
		raw, err := json.Marshal(rest)
		if err != nil {
			return
		}
		if members == nil {
			members = map[string]json.RawMessage{}
		}
		members[fmt.Sprint(idx[i])] = raw
	}
	for i, b := range blocks {
		if b.Err != nil {
			c.apiAt(agentimport.Dropped, "cline/null-block", k, "a content block that is not an object is left out")
			continue
		}
		switch {
		case b.Type == cline.BlockText:
			out = append(out, &openresponses.InputText{Text: b.Text})
			keep(i, blockRest(b, "text"))
			continue
		case b.Type == cline.BlockImage && b.Source != nil && b.Source.Type == "base64":
			out = append(out, &openresponses.InputImage{ImageURL: "data:" + b.Source.MediaType + ";base64," + b.Source.Data})
			keep(i, blockRest(b, "source"))
			continue
		case b.Type == cline.BlockImage && b.Source != nil && b.Source.Type == "url":
			out = append(out, &openresponses.InputImage{ImageURL: b.Source.URL})
			keep(i, blockRest(b, "source"))
			continue
		case b.Type == cline.BlockDocument && b.Source != nil && b.Source.Type == "base64":
			out = append(out, &openresponses.InputFile{FileData: "data:" + b.Source.MediaType + ";base64," + b.Source.Data})
			keep(i, blockRest(b, "source"))
			continue
		}
		raw, err := json.Marshal(b)
		if err != nil {
			raw = []byte("{}")
		}
		raw, _ = c.clean(c.t.API.Name, k+1, raw)
		typ := "anthropic." + extType(b.Type)
		part, _ := json.Marshal(struct {
			Type  string          `json:"type"`
			Block json.RawMessage `json:"block"`
		}{typ, raw})
		out = append(out, &openresponses.UnknownContent{Type: typ, Raw: part})
		c.apiAt(agentimport.Kept, "cline/extension-part", k, "a %s block is the extension part %s", extType(b.Type), typ)
	}
	return out, members
}

// extType is a block type as an extension type's suffix.
func extType(t string) string {
	if t == "" {
		return "unknown"
	}
	return t
}

// subtask links the sub-task a Roo new_task call delegated to, which
// its output names: Roo 3.35 and later run the child while the parent
// waits, and rewrite the call's output in place to the child's result
// when it completes. The child converts here, as a subsession, and the
// output names its leaf.
func (c *conv) subtask(k int, p *call, text string, ts time.Time) ([]agentsession.EntryRef, error) {
	if !c.roo {
		return nil, nil
	}
	childID, done := "", false
	if mm := subtaskDone.FindStringSubmatch(text); mm != nil {
		childID, done = mm[1], true
	} else if mm := subtaskPending.FindStringSubmatch(text); mm != nil {
		childID = mm[1]
	}
	if childID == "" {
		c.apiAt(agentimport.Lossy, "cline/roo-subtask-unlinked", k, "the new_task output names no sub-task, as releases before 3.35 wrote it; the sub-task is not linked and converts on its own")
		return nil, nil
	}
	if done {
		c.apiAt(agentimport.Lossy, "cline/roo-subtask-output-rewritten", k, "Roo wrote this output as \"Delegated to child task %s\" and rewrote it in place to the sub-task's result when the sub-task completed; the model saw only the result", childID)
	} else {
		c.apiAt(agentimport.Lossy, "cline/roo-subtask-pending", k, "the sub-task %s had not completed when the file was written; the output is Roo's placeholder, which the model never saw", childID)
	}
	if c.fam.opts.Alone {
		c.apiAt(agentimport.Dropped, "cline/roo-subtask-missing", k, "sub-task %s is not converted: the conversion reads no other task", childID)
		return nil, nil
	}
	dir, ok := childDir(c.t.Dir, childID)
	if !ok {
		c.apiAt(agentimport.Dropped, "cline/roo-subtask-missing", k, "sub-task %q names no task directory beside this one", childID)
		return nil, nil
	}
	real := realPath(dir)
	if c.fam.active[real] || c.childIDs[childID] {
		c.apiAt(agentimport.Dropped, "cline/roo-subtask-cycle", k, "sub-task %s is this task or one being converted already; it is not converted again", childID)
		return nil, nil
	}
	if c.depth+1 >= maxDepth {
		c.apiAt(agentimport.Dropped, "cline/roo-subtask-nesting", k, "sub-task %s is nested past %d tasks and is not converted", childID, maxDepth)
		return nil, nil
	}
	child, err := readChild(dir)
	if err != nil {
		c.apiAt(agentimport.Dropped, "cline/roo-subtask-missing", k, "sub-task %s cannot be read: %s", childID, agentimport.ErrorText(err))
		return nil, nil
	}
	sid := agentsession.SubsessionID(c.tgt.id, p.id)
	for n := 2; c.fam.ids[sid]; n++ {
		sid = agentsession.SubsessionID(c.tgt.id, fmt.Sprintf("%s/%d", p.id, n))
	}
	c.fam.ids[sid] = true
	c.childIDs[childID] = true
	c.fam.active[real] = true
	// The sub-task reports apart, so a failure costs only itself.
	sub := agentimport.NewReport(Source)
	sessions, leaf, err := c.fam.task(child, target{id: sid, parent: c.tgt.id, spawnedBy: p.id}, "../"+childID+"/", c.depth+1, sub)
	delete(c.fam.active, real)
	if err != nil {
		c.apiAt(agentimport.Dropped, "cline/roo-subtask-failed", k, "sub-task %s could not be converted: %s", childID, agentimport.ErrorText(err))
		return nil, nil
	}
	c.report.Merge(sub)
	c.fam.held = append(c.fam.held, real)
	c.children = append(c.children, sessions)
	link := agentsession.NewSubsessionLink(sid, p.id)
	link.Timestamp = ts
	id, err := c.appendEntry(link)
	if err != nil {
		return nil, err
	}
	c.written(k, id, nil)
	if leaf == "" {
		return nil, nil
	}
	return []agentsession.EntryRef{{Session: sid, Entry: leaf}}, nil
}

// standaloneReasoning converts Roo's reasoning item written on its own:
// a reasoning item, which the request sends as it is.
func (c *conv) standaloneReasoning(k int, m *cline.Message) error {
	ts, _ := m.Time()
	r := &openresponses.ReasoningItem{ID: m.ID, Status: openresponses.StatusCompleted, EncryptedContent: m.EncryptedContent, Summary: summaryParts(m.Summary)}
	e := itemEntry(r, ts)
	if m.EncryptedContent != "" {
		if err := c.b.Member(e, "provider", "openai"); err != nil {
			return err
		}
		c.apiAt(agentimport.Gap, "agentimport/reasoning-provider", k, "an encrypted reasoning item, tagged %s:provider", Source)
	}
	rest, err := messageRest(m, "type", "id", "encrypted_content", "summary")
	if err != nil {
		return err
	}
	if !summaryMapped(m.Summary) {
		if rest == nil {
			rest = map[string]json.RawMessage{}
		}
		rest["summary"] = m.Summary
	}
	if err := c.keepMembers(e, k, "message", rest, "the reasoning item's"); err != nil {
		return err
	}
	id, err := c.appendEntry(e)
	if err != nil {
		return err
	}
	c.written(k, id, r)
	c.mark(unit{uAPI, k}, agentimport.LineMapped)
	return nil
}

// summaryParts reads a Responses reasoning summary: summary_text parts.
func summaryParts(raw json.RawMessage) openresponses.Contents {
	out := openresponses.Contents{}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return out
	}
	for _, p := range parts {
		if p.Type == "summary_text" {
			out = append(out, &openresponses.SummaryText{Text: p.Text})
		}
	}
	return out
}

// summaryMapped reports whether summaryParts took the whole summary.
func summaryMapped(raw json.RawMessage) bool {
	if cline.IsNull(raw) {
		return true
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return false
	}
	for _, p := range parts {
		var typ string
		_ = json.Unmarshal(p["type"], &typ)
		if typ != "summary_text" || len(p) != 2 {
			return false
		}
	}
	return true
}
