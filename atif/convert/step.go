package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// steps converts the document's steps in order: a document is a line,
// never a tree, so each step's entries follow the previous step's.
func (c *docConv) steps() error {
	steps := c.t.Steps
	for i := 0; i < len(steps); i++ {
		s := steps[i]
		if c.leading[i] || c.moved[i] {
			continue
		}
		if s == nil || s.Err != nil {
			c.mark(i, agentimport.LineMalformed)
			what := "null"
			if s != nil {
				what = "not an object"
			}
			c.addf(agentimport.Repaired, "atif/step-malformed", i+1, "the step is %s and is skipped", what)
			continue
		}
		c.checkStepID(i, s)
		if c.exporter {
			if n, ok, err := c.exporterStep(i); ok || err != nil {
				i += n
				if err != nil {
					return err
				}
				continue
			}
		}
		var (
			n   int
			err error
		)
		switch s.Source {
		case atif.SourceUser:
			err = c.userStep(i, s)
		case atif.SourceSystem:
			n, err = c.systemStep(i, s)
		case atif.SourceAgent:
			err = c.agentStep(i, s)
		default:
			_, err = c.customStep(i, s, agentimport.Kept, "atif/source", fmt.Sprintf("a step of source %q, which ATIF does not define, is a custom entry", s.Source))
		}
		if err != nil {
			return err
		}
		i += n
	}
	return nil
}

// checkStepID notes a step_id that is not the step's position.
func (c *docConv) checkStepID(i int, s *atif.Step) {
	if n, _, ok := atif.Int(s.StepID); !ok || n != int64(c.line(i+1)) {
		c.count("atif/step-id", i+1)
	}
}

// stepTime is a step's time, zero when it has none or it does not
// parse, which the entry before it then lends. A step it repairs is
// counted once, however often its time is asked for.
func (c *docConv) stepTime(i int, s *atif.Step) time.Time {
	if s.Timestamp == "" {
		return time.Time{}
	}
	t, naive, ok := parseTime(s.Timestamp)
	if (!ok || naive) && !c.timed[i] {
		if c.timed == nil {
			c.timed = map[int]bool{}
		}
		c.timed[i] = true
		c.count("atif/timestamp", i+1)
	}
	return t
}

// commit appends a step's entries in order. Each carries the step's
// time when it has none of its own, the step's step_id, and the copied
// mark; the last carries the step's extra and the members the
// conversion does not map. It returns the entries' IDs.
func (c *docConv) commit(i int, s *atif.Step, entries []agentsession.Entry, rest map[string]json.RawMessage) ([]string, error) {
	return c.commitEntries(i, s, entries, rest, true)
}

// commitEntries is commit; with final false the entries are the first
// of the step's, and carry neither its extra nor its other members.
func (c *docConv) commitEntries(i int, s *atif.Step, entries []agentsession.Entry, rest map[string]json.RawMessage, final bool) ([]string, error) {
	if len(entries) == 0 {
		c.mark(i, agentimport.LineDropped)
		c.addf(agentimport.Dropped, "atif/empty-step", i+1, "the step holds nothing to convert: no message, no calls, no results")
		return nil, nil
	}
	ts := c.stepTime(i, s)
	for j, e := range entries {
		base := e.Base()
		if base.Timestamp.IsZero() {
			base.Timestamp = ts
		}
		if !atif.IsNull(s.StepID) {
			if err := c.b.Member(e, "step_id", s.StepID); err != nil {
				return nil, err
			}
		}
		if s.Copied() && !c.exporter {
			if err := c.b.Member(e, "copied", true); err != nil {
				return nil, err
			}
		}
		if !final || j < len(entries)-1 {
			continue
		}
		if err := c.keepExtra(e, i, s.Ext); err != nil {
			return nil, err
		}
		if len(rest) > 0 && !c.fam.opts.Slim {
			if err := c.b.Member(e, "step", rest); err != nil {
				return nil, err
			}
		}
	}
	if final && s.Copied() {
		c.count("atif/copied-context", i+1)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		id, err := c.appendEntry(e)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// keepExtra keeps an extra object in the entry's member atif:extra, or
// with Slim drops it.
func (c *docConv) keepExtra(e agentsession.Entry, i int, raw json.RawMessage) error {
	if atif.IsNull(raw) {
		return nil
	}
	if c.fam.opts.Slim {
		c.addf(agentimport.Dropped, "atif/extra", i+1, "an extra object is left out")
		return nil
	}
	c.addf(agentimport.Kept, "atif/extra", i+1, "an extra object is the entry member %s:extra", Source)
	return c.b.Member(e, "extra", raw)
}

// agentOnly are the step members Harbor allows on agent steps alone.
var agentOnly = []string{"model_name", "reasoning_effort", "reasoning_content", "tool_calls", "metrics", "llm_call_count"}

// stepRest gathers the step members the conversion does not map: those
// Harbor's models do not declare, those of another JSON type, a
// message that is neither a string nor a list, and, on a step that is
// not an agent's, the members of agent steps alone. It reports them.
func (c *docConv) stepRest(i int, s *atif.Step) map[string]json.RawMessage {
	rest := map[string]json.RawMessage{}
	mistyped := s.Mistyped()
	for k, v := range s.Members.Extra {
		rest[k] = v
	}
	if s.Message.Raw != nil {
		rest["message"] = s.Message.Raw
		mistyped = append(mistyped, "message")
	}
	if o := s.Observation; o != nil && o.Err != nil {
		rest["observation"] = o.Raw
		mistyped = append(mistyped, "observation")
	}
	c.reportMembers(i+1, "step", rest, mistyped)
	if s.Source != atif.SourceAgent {
		raw, err := json.Marshal(s)
		var all map[string]json.RawMessage
		if err == nil && json.Unmarshal(raw, &all) == nil {
			var found []string
			for _, k := range agentOnly {
				if v, ok := all[k]; ok && !atif.IsNull(v) {
					rest[k] = v
					found = append(found, k)
				}
			}
			if len(found) > 0 {
				kind, verb := c.keeping("step")
				c.addf(kind, "atif/agent-only-field", i+1, "a %s step carries members Harbor allows on agent steps alone, which are %s: %s", s.Source, verb, strings.Join(found, ", "))
			}
		}
	}
	if len(rest) == 0 {
		return nil
	}
	return rest
}

// userStep converts a user step: a user message, then any observation
// it carries.
func (c *docConv) userStep(i int, s *atif.Step) error {
	rest := c.stepRest(i, s)
	entries := []agentsession.Entry{agentsession.NewItemEntry(openresponses.UserMessage(c.inputParts(i, s.Message)...))}
	more, err := c.results(i, s, nil, nil)
	if err != nil {
		return err
	}
	if _, err := c.commit(i, s, append(entries, more...), rest); err != nil {
		return err
	}
	c.mark(i, agentimport.LineMapped)
	return nil
}

// contextManagement is a system step's extra.context_management.
type contextManagement struct {
	present       bool
	typ, boundary string
}

func stepContextManagement(s *atif.Step) contextManagement {
	raw, ok := object(s.Ext)["context_management"]
	if !ok {
		return contextManagement{}
	}
	m := object(raw)
	return contextManagement{present: true, typ: str(m["type"]), boundary: str(m["boundary"])}
}

// systemStep converts a system step after the first user or agent step.
// It returns how many steps after it the conversion took with it.
func (c *docConv) systemStep(i int, s *atif.Step) (int, error) {
	cm := stepContextManagement(s)
	switch {
	case cm.boundary == "replace":
		return c.replace(i, s)
	case cm.boundary == "append" || cm.boundary == "" && cm.typ == "injection":
		return 0, c.injection(i, s)
	case cm.boundary == "truncate" || cm.boundary == "" && cm.typ == "pruning":
		_, err := c.customStep(i, s, agentimport.Gap, "atif/context-pruning",
			"the step pruned the context without naming what it removed; RFC 0001 can leave out named entries alone, so the step is a custom entry and the context keeps everything")
		return 0, err
	case cm.present:
		_, err := c.customStep(i, s, agentimport.Kept, "atif/context-management",
			fmt.Sprintf("a context-management step of type %q and boundary %q, which RFC 0001 of ATIF does not define, is a custom entry", cm.typ, cm.boundary))
		return 0, err
	}
	rest := c.stepRest(i, s)
	var entries []agentsession.Entry
	if hasContent(s.Message) {
		m := &openresponses.Message{Role: openresponses.RoleSystem, Content: c.inputParts(i, s.Message)}
		entries = append(entries, agentsession.NewItemEntry(m))
		c.addf(agentimport.Lossy, "atif/system-message", i+1,
			"a system step after the conversation started is a system message; Harbor's own readers send it as a user or developer message, or drop it")
	}
	more, err := c.results(i, s, nil, nil)
	if err != nil {
		return 0, err
	}
	if _, err := c.commit(i, s, append(entries, more...), rest); err != nil {
		return 0, err
	}
	c.mark(i, agentimport.LineMapped)
	return 0, nil
}

// hasContent reports whether content holds anything: text, or a part.
func hasContent(m atif.Content) bool {
	return m.Text != "" || len(m.Parts) > 0
}

// customStep keeps a whole step as a custom entry.
func (c *docConv) customStep(i int, s *atif.Step, kind agentimport.Kind, code, detail string) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	if raw, err = c.clean(i+1, raw); err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"step": raw})
	if err != nil {
		return "", err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	e.Timestamp = c.stepTime(i, s)
	if !atif.IsNull(s.StepID) {
		if err := c.b.Member(e, "step_id", s.StepID); err != nil {
			return "", err
		}
	}
	id, err := c.appendEntry(e)
	if err != nil {
		return "", err
	}
	c.mark(i, agentimport.LineKept)
	c.addf(kind, code, i+1, "%s", detail)
	return id, nil
}

// injection converts an append boundary: each result's content is a
// user message, as an injection adds it to the context.
func (c *docConv) injection(i int, s *atif.Step) error {
	rest := c.stepRest(i, s)
	if hasContent(s.Message) {
		if rest == nil {
			rest = map[string]json.RawMessage{}
		}
		raw, err := json.Marshal(s.Message)
		if err != nil {
			return err
		}
		rest["message"] = raw
	}
	more, err := c.results(i, s, nil, nil)
	if err != nil {
		return err
	}
	if _, err := c.commit(i, s, more, rest); err != nil {
		return err
	}
	c.mark(i, agentimport.LineMapped)
	_, verb := c.keeping("step")
	c.addf(agentimport.Lossy, "atif/context-injection", i+1,
		"an append boundary's results are user messages; the step's own message, which describes it, is %s", verb)
	return nil
}

// replace converts a replace boundary, RFC 0001 of ATIF's one normative
// statement of context: the boundary's results are the context for
// every later step, so it is a compaction whose summary they are and
// that keeps nothing before it. A boundary with no results' content
// takes the user step after it as its summary, as Vibe and Terminus 2
// write it; with none, the step is a custom entry. It returns 1 when it
// took the next step.
func (c *docConv) replace(i int, s *atif.Step) (int, error) {
	rest := c.stepRest(i, s)
	var parts openresponses.Contents
	var links []agentsession.Entry
	if o := s.Observation; o != nil && o.Err == nil {
		for _, r := range o.Results {
			if r == nil || r.Err != nil {
				c.addf(agentimport.Dropped, "atif/result-malformed", i+1, "a result that is not an object is left out")
				continue
			}
			if hasContent(r.Content) {
				parts = append(parts, c.inputParts(i, r.Content)...)
			}
			more, err := c.calllessRefs(i, s, r)
			if err != nil {
				return 0, err
			}
			links = append(links, more...)
		}
	}
	taken := 0
	var pinned openresponses.Items
	switch {
	case len(parts) > 0:
	case c.agentName() == "terminus-2":
		parts, pinned = c.terminusHandoff(i, s)
	case i+1 < len(c.t.Steps):
		// Vibe writes the summary as the user step after the boundary.
		// A step that carries results is not a summary alone.
		next := c.t.Steps[i+1]
		if next == nil || next.Err != nil || next.Source != atif.SourceUser || c.leading[i+1] || next.Observation != nil {
			break
		}
		parts = c.inputParts(i+1, next.Message)
		taken = 1
		c.mark(i+1, agentimport.LineMapped)
		c.addf(agentimport.Lossy, "atif/compaction-summary-next", i+1,
			"the replace boundary's results hold no content; its summary is the user step after it, as Vibe writes it, which RFC 0001 of ATIF does not say")
		if r := c.stepRest(i+1, next); len(r) > 0 {
			if rest == nil {
				rest = map[string]json.RawMessage{}
			}
			raw, err := json.Marshal(r)
			if err != nil {
				return 0, err
			}
			rest["summary_step"] = raw
		}
		for k, v := range map[string]json.RawMessage{"summary_step_extra": next.Ext, "summary_step_id": next.StepID, "summary_step_timestamp": jsonString(next.Timestamp)} {
			if atif.IsNull(v) {
				continue
			}
			if rest == nil {
				rest = map[string]json.RawMessage{}
			}
			rest[k] = v
		}
	}
	if len(parts) == 0 {
		if len(links) > 0 {
			if _, err := c.commitEntries(i, s, links, nil, false); err != nil {
				return 0, err
			}
		}
		_, err := c.customStep(i, s, agentimport.Gap, "atif/compaction-unrecorded",
			"a replace boundary records no summary, in its results or a user step after it; a compaction needs one, so the step is a custom entry and the context keeps what came before")
		return 0, err
	}
	if len(links) > 0 {
		// The helpers ran before the fold they wrote.
		if _, err := c.commitEntries(i, s, links, nil, false); err != nil {
			return 0, err
		}
	}
	firstKept, err := c.keepNone(i, s)
	if err != nil {
		return 0, err
	}
	settings := c.settings
	if c.tail != "" {
		ctx, err := c.b.S.ContextAt(c.tail)
		if err != nil {
			return 0, err
		}
		settings = ctx.Settings
	}
	e := &agentsession.CompactionEntry{FirstKept: firstKept, Summary: openresponses.UserMessage(parts...), Pinned: pinned, Config: settings}
	if hasContent(s.Message) {
		if rest == nil {
			rest = map[string]json.RawMessage{}
		}
		raw, err := json.Marshal(s.Message)
		if err != nil {
			return 0, err
		}
		rest["message"] = raw
	}
	if _, err := c.commit(i, s, []agentsession.Entry{e}, rest); err != nil {
		return 0, err
	}
	c.mark(i, agentimport.LineMapped)
	return taken, nil
}

// agentName is the document's agent.name.
func (c *docConv) agentName() string {
	if a := c.t.Agent; a != nil && a.Err == nil {
		return a.Name
	}
	return ""
}

// terminusHandoff rebuilds the context Terminus 2 continues from after
// it summarises (terminus_2.py, _summarize): its first message, then
// the question prompt and the questions of its questions helper, then
// the handoff, which is the user step after the boundary and converts
// as one. The first message is the compaction's summary, the question
// prompt and the questions its pinned items. The helper's file is read
// for them; without it the pinned items are missing, and reported.
func (c *docConv) terminusHandoff(i int, s *atif.Step) (openresponses.Contents, openresponses.Items) {
	line := i + 1
	var first *atif.Step
	for k, st := range c.t.Steps[:i] {
		if st != nil && st.Err == nil && st.Source == atif.SourceUser && !c.leading[k] {
			first = st
			break
		}
	}
	if first == nil {
		return nil, nil
	}
	parts := c.inputParts(i, first.Message)
	var prompt, questions *atif.Step
	if o := s.Observation; o != nil && o.Err == nil {
		for _, r := range o.Results {
			if r == nil || r.Err != nil {
				continue
			}
			for _, ref := range r.Refs.List {
				if ref == nil || ref.Err != nil || !strings.HasSuffix(ref.TrajectoryPath, "-questions.json") {
					continue
				}
				real, err := c.fam.resolve(c.in.dir, ref.TrajectoryPath)
				if err != nil {
					continue
				}
				f, err := c.fam.readDoc(real)
				if err != nil {
					continue
				}
				for _, st := range f.Doc.Steps {
					switch {
					case st == nil || st.Err != nil || st.Copied():
					case st.Source == atif.SourceUser:
						prompt = st
					case st.Source == atif.SourceAgent:
						questions = st
					}
				}
			}
		}
	}
	if prompt == nil || questions == nil {
		c.addf(agentimport.Lossy, "atif/terminus-handoff", line,
			"Terminus 2 continues from its first message, its questions helper's prompt and questions, and the handoff; the helper's file cannot be read, so the context is the first message and the handoff alone")
		return parts, nil
	}
	pinned := openresponses.Items{
		openresponses.UserMessage(c.inputParts(i, prompt.Message)...),
		&openresponses.Message{Role: openresponses.RoleAssistant, Content: c.outputParts(i, questions.Message)},
	}
	c.addf(agentimport.Lossy, "atif/terminus-handoff", line,
		"Terminus 2 continues from its first message, its questions helper's prompt and questions, and the handoff: the compaction's summary is the first message and its pinned items the prompt and the questions, copied from the helper's file")
	return parts, pinned
}

// keepNone returns the entry a compaction that keeps nothing names as
// its first kept: the format's first_kept keeps the entry it names, so
// it must be one that puts nothing in context. The tail serves when it
// is such an entry; otherwise a custom marker is written after it.
func (c *docConv) keepNone(i int, s *atif.Step) (string, error) {
	if e, ok := c.b.S.Entry(c.tail); ok {
		switch e.(type) {
		case *agentsession.ItemEntry, *agentsession.CompactionEntry, *agentsession.BranchSummaryEntry:
		default:
			return c.tail, nil
		}
	}
	data, err := json.Marshal(map[string]int{"replace": i + 1})
	if err != nil {
		return "", err
	}
	marker := &agentsession.CustomEntry{NS: Source, Data: data}
	marker.Timestamp = c.stepTime(i, s)
	id, err := c.appendEntry(marker)
	if err != nil {
		return "", err
	}
	c.addf(agentimport.Kept, "atif/compaction-marker", i+1,
		"a replace boundary keeps nothing before it, so a custom marker entry written before the compaction is its first_kept, which puts nothing in context")
	return id, nil
}

// agentStep converts an agent step: a config delta when its model or
// effort is not the one in force, its reasoning, message and calls, the
// response, then its results.
func (c *docConv) agentStep(i int, s *atif.Step) error {
	line := i + 1
	rest := c.stepRest(i, s)
	var entries []agentsession.Entry
	if delta := c.stepConfig(i, s); delta != nil {
		entries = append(entries, delta)
	}
	calls := int64(-1)
	if !atif.IsNull(s.LLMCallCount) {
		n, _, ok := atif.Int(s.LLMCallCount)
		if ok && n >= 0 {
			calls = n
		} else {
			if rest == nil {
				rest = map[string]json.RawMessage{}
			}
			rest["llm_call_count"] = s.LLMCallCount
			kind, verb := c.keeping("step")
			c.addf(kind, "atif/mistyped-member", line, "llm_call_count is not a count; it is %s", verb)
		}
	}
	dispatch := calls == 0
	respID := ""
	if !dispatch {
		respID = fmt.Sprintf("atif-step-%d", c.line(line))
	}
	item := func(it openresponses.Item) *agentsession.ItemEntry {
		e := agentsession.NewItemEntry(it)
		e.ResponseID = respID
		return e
	}
	if s.ReasoningContent != "" {
		entries = append(entries, item(&openresponses.ReasoningItem{Summary: openresponses.Contents{&openresponses.SummaryText{Text: s.ReasoningContent}}}))
		c.count("atif/reasoning-text", line)
	}
	if hasContent(s.Message) {
		entries = append(entries, item(&openresponses.Message{Role: openresponses.RoleAssistant, Content: c.outputParts(i, s.Message)}))
	}
	local := map[string][]*call{}
	var order []*call
	for j, tc := range s.ToolCalls {
		if tc == nil || tc.Err != nil {
			c.addf(agentimport.Dropped, "atif/tool-call-malformed", line, "tool call %d is not an object and is left out", j)
			continue
		}
		id, renamed := c.b.Calls.Assign(tc.ToolCallID)
		fc := &openresponses.FunctionCall{CallID: id, Name: tc.FunctionName, Arguments: c.arguments(line, tc)}
		e := item(fc)
		if renamed {
			if err := c.b.Member(e, "call_id", tc.ToolCallID); err != nil {
				return err
			}
			c.addf(agentimport.Repaired, "atif/call-id", line,
				"tool call id %q is empty, repeats another call's in the session, or holds characters providers refuse; it is %s, the native id in %s:call_id", tc.ToolCallID, id, Source)
		}
		if tc.FunctionName == "" {
			c.addf(agentimport.Repaired, "atif/tool-call-name", line, "tool call %s has no function name, which ATIF requires; the call's name is empty", tc.ToolCallID)
		}
		if dispatch {
			if err := c.b.Member(e, "llm_call_count", 0); err != nil {
				return err
			}
		}
		if err := c.keepExtra(e, i, tc.Ext); err != nil {
			return err
		}
		if len(tc.Members.Extra) > 0 {
			c.reportMembers(line, "tool_call", tc.Members.Extra, tc.Mistyped())
			if !c.fam.opts.Slim {
				if err := c.b.Member(e, "tool_call", tc.Members.Extra); err != nil {
					return err
				}
			}
		}
		entries = append(entries, e)
		cl := &call{id: id, native: tc.ToolCallID, step: i}
		c.calls = append(c.calls, cl)
		local[tc.ToolCallID] = append(local[tc.ToolCallID], cl)
		order = append(order, cl)
	}
	if dispatch {
		c.addf(agentimport.Gap, "atif/deterministic-dispatch", line,
			"the step has llm_call_count 0: its calls were dispatched by the harness, not produced by a model, and RFC 0001 has no such call; they are function calls marked %s:llm_call_count with no response", Source)
		if s.Metrics != nil {
			if err := c.keepMetrics(entries, i, s); err != nil {
				return err
			}
		}
	} else {
		resp, err := c.response(i, s, respID, calls)
		if err != nil {
			return err
		}
		entries = append(entries, resp)
	}
	more, err := c.results(i, s, local, order)
	if err != nil {
		return err
	}
	if _, err := c.commit(i, s, append(entries, more...), rest); err != nil {
		return err
	}
	c.mark(i, agentimport.LineMapped)
	return nil
}

// stepConfig returns a config delta when an agent step's model or
// effort is not the one in force, and puts it in force.
func (c *docConv) stepConfig(i int, s *atif.Step) *agentsession.ConfigEntry {
	cfg := &agentsession.ConfigEntry{}
	if s.ModelName != "" && s.ModelName != c.settings.Model {
		cfg.Model = s.ModelName
	}
	if !atif.IsNull(s.ReasoningEffort) {
		var effort string
		if json.Unmarshal(s.ReasoningEffort, &effort) == nil && effort != "" {
			if openresponses.ReasoningEffort(effort) != c.settings.Reasoning.Effort {
				r := c.settings.Reasoning
				r.Effort = openresponses.ReasoningEffort(effort)
				cfg.Reasoning = &r
			}
			switch openresponses.ReasoningEffort(effort) {
			case openresponses.ReasoningEffortNone, openresponses.ReasoningEffortMinimal, openresponses.ReasoningEffortLow,
				openresponses.ReasoningEffortMedium, openresponses.ReasoningEffortHigh, openresponses.ReasoningEffortXHigh:
			default:
				c.addf(agentimport.Lossy, "atif/reasoning-effort", i+1, "reasoning effort %q is not one Responses defines; it is written as it is", effort)
			}
		} else {
			c.addf(agentimport.Lossy, "atif/reasoning-effort", i+1, "reasoning effort %s is a number, which Responses has no place for; the effort in force stands", strings.TrimSpace(string(s.ReasoningEffort)))
		}
	}
	if cfg.Model == "" && cfg.Reasoning == nil {
		return nil
	}
	c.settings = c.settings.Apply(cfg)
	return cfg
}

// arguments renders a tool call's arguments as the JSON string a
// function call carries: an object as written. Anything else is
// reported: a string is taken as the arguments' text, which an adapter
// that could not parse it leaves, and another value as its JSON.
func (c *docConv) arguments(line int, tc *atif.ToolCall) string {
	raw := bytes.TrimSpace(tc.Arguments)
	switch {
	case atif.IsNull(raw):
		c.addf(agentimport.Repaired, "atif/arguments", line, "tool call %s has no arguments, which ATIF requires; they are {}", tc.ToolCallID)
		return "{}"
	case raw[0] == '{':
		clean, err := c.clean(line, raw)
		if err != nil {
			return string(raw)
		}
		var b bytes.Buffer
		if json.Compact(&b, clean) != nil {
			return string(clean)
		}
		return b.String()
	case raw[0] == '"':
		c.addf(agentimport.Repaired, "atif/arguments", line, "tool call %s has a string for arguments, which ATIF requires be an object; the string is the arguments' text", tc.ToolCallID)
		return str(raw)
	}
	c.addf(agentimport.Repaired, "atif/arguments", line, "tool call %s has arguments that are not an object, which ATIF requires; their JSON is the arguments' text", tc.ToolCallID)
	return string(raw)
}
