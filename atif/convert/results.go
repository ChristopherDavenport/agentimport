package convert

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// results converts a step's observation results, in order: a result
// naming a call is its output, preceded by the link to a sub-agent it
// references; a result naming no call is a user message, after the
// outputs, with links to the sub-agents it references. local holds the
// step's calls by native id and order in the order they were made.
func (c *docConv) results(i int, s *atif.Step, local map[string][]*call, order []*call) ([]agentsession.Entry, error) {
	o := s.Observation
	if o == nil {
		return nil, nil
	}
	line := i + 1
	if o.Err != nil {
		// stepRest keeps it.
		return nil, nil
	}
	if len(o.Members.Extra) > 0 {
		c.reportMembers(line, "observation", o.Members.Extra, o.Mistyped())
	}
	// mini-swe-agent's chat shape: no result of a step names a call, the
	// tool messages first, one per call, then the environment's user
	// messages (mini_swe_agent.py, _add_observation_to_last_agent_step).
	paired := len(order) > 0 && len(o.Results) >= len(order)
	for _, r := range o.Results {
		if r == nil || r.Err != nil || r.SourceCallID != "" {
			paired = false
		}
	}
	switch {
	case paired && len(o.Results) == len(order):
		c.addf(agentimport.Repaired, "atif/result-paired-by-order", line,
			"none of the step's %d results names its call; they are its %d calls' outputs, in order", len(o.Results), len(order))
	case paired:
		c.addf(agentimport.Repaired, "atif/result-paired-by-order", line,
			"none of the step's %d results names its call; the first %d are its %d calls' outputs, in order, and the rest user messages after them, as mini-swe-agent's environment messages are", len(o.Results), len(order), len(order))
	}
	var outputs, after []agentsession.Entry
	for j, r := range o.Results {
		if r == nil || r.Err != nil {
			c.addf(agentimport.Dropped, "atif/result-malformed", line, "result %d is not an object and is left out", j)
			continue
		}
		var cl *call
		switch {
		case paired && j < len(order):
			cl = order[j]
		case r.SourceCallID != "":
			cl = c.findCall(r.SourceCallID, i, local)
		}
		switch {
		case cl != nil && !cl.answered:
			ents, err := c.output(i, s, r, cl)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, ents...)
		case cl != nil:
			e, err := c.resultCustom(i, r, cl.id)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, e)
			c.addf(agentimport.Kept, "atif/duplicate-result", line,
				"a second result for tool call %s, which an output already answers, is a custom entry", r.SourceCallID)
		default:
			ents, err := c.calllessResult(i, s, r)
			if err != nil {
				return nil, err
			}
			after = append(after, ents...)
		}
	}
	return append(outputs, after...), nil
}

// findCall finds the call a result names: one of its own step's, the
// first not yet answered, or, as Harbor's validator would refuse, one
// of an earlier step's, the latest with that id.
func (c *docConv) findCall(native string, i int, local map[string][]*call) *call {
	if q := local[native]; len(q) > 0 {
		for _, cl := range q {
			if !cl.answered {
				return cl
			}
		}
		return q[len(q)-1]
	}
	var found *call
	for _, cl := range c.calls {
		if cl.native == native && cl.step < i && (found == nil || !cl.answered || found.answered) {
			found = cl
		}
	}
	if found != nil && !found.answered {
		c.addf(agentimport.Repaired, "atif/result-foreign-call", i+1,
			"a result names tool call %s of step %d rather than one of its own step's, which Harbor's validator refuses; it is that call's output", native, found.step+1)
	}
	return found
}

// output converts a call's result into its output, preceded by the link
// to the sub-agent its first resolvable reference names, whose leaf the
// output then names.
func (c *docConv) output(i int, s *atif.Step, r *atif.Result, cl *call) ([]agentsession.Entry, error) {
	line := i + 1
	cl.answered = true
	out := &openresponses.FunctionCallOutput{CallID: cl.id}
	switch {
	case c.rawToolResult(i, r, &out.Output):
	case r.Content.Parts != nil:
		out.Output.Parts = c.inputParts(i, r.Content)
	default:
		out.Output.Text = r.Content.Text
	}
	e := agentsession.NewItemEntry(out)
	var entries, after []agentsession.Entry
	linked := false
	if rec, ok := c.spawnedBy[cl.native]; ok && !c.sidechains[rec.agent].done {
		ts := c.stepTime(i, s)
		if ts.IsZero() {
			ts = c.last
		}
		child, leaf, ok := c.sidechainChild(c.sidechains[rec.agent], cl.id, !rec.waited, ts)
		switch {
		case ok && rec.waited:
			linked = true
			entries = append(entries, agentsession.NewSubsessionLink(child, cl.id))
			if leaf != "" {
				e.Parents = []agentsession.EntryRef{{Session: child, Entry: leaf}}
			}
		case ok:
			// The output is the CLI's acknowledgement that the agent
			// started; the peer link follows it (RFC 0001, link).
			link := agentsession.NewLinkEntry(agentsession.RelPeer, child)
			link.CallID = cl.id
			after = append(after, link)
		}
	}
	for _, ref := range r.Refs.List {
		if !linked {
			child, leaf, ok, err := c.spawn(i, s, ref, cl.id)
			if err != nil {
				return nil, err
			}
			if ok {
				linked = true
				link := agentsession.NewSubsessionLink(child, cl.id)
				if err := c.refMembers(link, i, ref); err != nil {
					return nil, err
				}
				entries = append(entries, link)
				if leaf != "" {
					e.Parents = []agentsession.EntryRef{{Session: child, Entry: leaf}}
				}
				continue
			}
			if err := c.keepRef(e, i, ref); err != nil {
				return nil, err
			}
			continue
		}
		ents, err := c.calllessRef(i, s, ref)
		if err != nil {
			return nil, err
		}
		entries = append(entries, ents...)
	}
	if err := c.resultMembers(e, i, r); err != nil {
		return nil, err
	}
	if r.Refs.Object {
		c.addf(agentimport.Repaired, "atif/ref-object", line, "subagent_trajectory_ref is one object, as RFC 0001 v1.0 wrote it, not a list; it is read as a list of one")
	}
	return append(append(entries, e), after...), nil
}

// rawToolResult takes the block a Claude Code run recorded as what the
// model saw, which Harbor's own resume prefers: its adapter appends the
// tool's stdout and metadata to the result's content.
func (c *docConv) rawToolResult(i int, r *atif.Result, out *openresponses.FunctionCallOutputData) bool {
	if a := c.t.Agent; a == nil || a.Err != nil || a.Name != "claude-code" {
		return false
	}
	raw := object(object(object(r.Ext)["tool_result_metadata"])["raw_tool_result"])
	content, ok := raw["content"]
	if !ok {
		return false
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		out.Text = text
	} else {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(content, &blocks) != nil {
			return false
		}
		parts := openresponses.Contents{}
		for _, b := range blocks {
			if b.Type != "text" {
				return false
			}
			parts = append(parts, &openresponses.InputText{Text: b.Text})
		}
		out.Parts = parts
	}
	c.addf(agentimport.Repaired, "atif/raw-tool-result", i+1,
		"the result's content is Claude Code's block with the tool's stdout and metadata appended; the output is the block the model saw, from extra.tool_result_metadata.raw_tool_result")
	return true
}

// resultMembers keeps a result's extra and the members the conversion
// does not map on the entry made from it.
func (c *docConv) resultMembers(e agentsession.Entry, i int, r *atif.Result) error {
	if err := c.keepExtra(e, i, r.Ext); err != nil {
		return err
	}
	rest := map[string]json.RawMessage{}
	for k, v := range r.Members.Extra {
		rest[k] = v
	}
	mistyped := r.Mistyped()
	if r.Content.Raw != nil {
		rest["content"] = r.Content.Raw
		mistyped = append(mistyped, "content")
	}
	if r.Refs.Raw != nil {
		rest["subagent_trajectory_ref"] = r.Refs.Raw
		mistyped = append(mistyped, "subagent_trajectory_ref")
	}
	if len(rest) == 0 {
		return nil
	}
	c.reportMembers(i+1, "result", rest, mistyped)
	if c.fam.opts.Slim {
		return nil
	}
	return c.b.Member(e, "result", rest)
}

// resultCustom keeps a result as a custom entry.
func (c *docConv) resultCustom(i int, r *atif.Result, callID string) (agentsession.Entry, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	if raw, err = c.clean(i+1, raw); err != nil {
		return nil, err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"result": raw})
	if err != nil {
		return nil, err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data, CallID: callID}
	return e, nil
}

// calllessResult converts a result that names no call of the document:
// links to the sub-agents it references, then its content as a user
// message, which is what Harbor's own export sends the model.
func (c *docConv) calllessResult(i int, s *atif.Step, r *atif.Result) ([]agentsession.Entry, error) {
	line := i + 1
	entries, err := c.calllessRefs(i, s, r)
	if err != nil {
		return nil, err
	}
	if hasContent(r.Content) {
		e := agentsession.NewItemEntry(openresponses.UserMessage(c.inputParts(i, r.Content)...))
		if err := c.resultMembers(e, i, r); err != nil {
			return nil, err
		}
		entries = append(entries, e)
		if r.SourceCallID != "" {
			if err := c.b.Member(e, "source_call_id", r.SourceCallID); err != nil {
				return nil, err
			}
			c.addf(agentimport.Lossy, "atif/result-unknown-call", line,
				"a result names tool call %s, which no step of the document made; it is a user message, the id in %s:source_call_id", r.SourceCallID, Source)
		} else {
			c.addf(agentimport.Lossy, "atif/result-without-call", line,
				"a result that names no tool call is a user message after the step's outputs, as Harbor's own export sends it")
		}
	} else if len(entries) == 0 {
		c.addf(agentimport.Dropped, "atif/result-empty", line, "a result with no content, no call and no sub-agent is left out")
	} else if len(r.Members.Extra) > 0 || !atif.IsNull(r.Ext) {
		if err := c.resultMembers(entries[len(entries)-1], i, r); err != nil {
			return nil, err
		}
	}
	return entries, nil
}

// calllessRefs links the sub-agents a result references with no call.
func (c *docConv) calllessRefs(i int, s *atif.Step, r *atif.Result) ([]agentsession.Entry, error) {
	var out []agentsession.Entry
	for _, ref := range r.Refs.List {
		ents, err := c.calllessRef(i, s, ref)
		if err != nil {
			return nil, err
		}
		out = append(out, ents...)
	}
	if r.Refs.Object && len(r.Refs.List) > 0 {
		c.addf(agentimport.Repaired, "atif/ref-object", i+1, "subagent_trajectory_ref is one object, as RFC 0001 v1.0 wrote it, not a list; it is read as a list of one")
	}
	return out, nil
}

// calllessRef converts the sub-agent a reference with no call names,
// and returns the link to it: a subsession link with no call_id, which
// RFC 0001 does not define. A reference spawn could not convert is a
// custom entry.
func (c *docConv) calllessRef(i int, s *atif.Step, ref *atif.Ref) ([]agentsession.Entry, error) {
	child, _, ok, err := c.spawn(i, s, ref, "")
	if err != nil {
		return nil, err
	}
	if !ok {
		raw, err := json.Marshal(ref)
		if err != nil {
			return nil, err
		}
		if raw, err = c.clean(i+1, raw); err != nil {
			return nil, err
		}
		data, err := json.Marshal(map[string]json.RawMessage{"subagent_trajectory_ref": raw})
		if err != nil {
			return nil, err
		}
		return []agentsession.Entry{&agentsession.CustomEntry{NS: Source, Data: data}}, nil
	}
	link := agentsession.NewLinkEntry(agentsession.RelSubsession, child)
	if err := c.refMembers(link, i, ref); err != nil {
		return nil, err
	}
	c.addf(agentimport.Gap, "atif/callless-subagent", i+1,
		"a sub-agent no tool call spawned (a harness's helper, such as a summariser) is session %s with parent_session and no spawned_by, linked by a subsession link with no call_id, which RFC 0001 does not define", child)
	return []agentsession.Entry{link}, nil
}

// refMembers keeps what a reference holds beside the link made from it.
func (c *docConv) refMembers(link *agentsession.LinkEntry, i int, ref *atif.Ref) error {
	if err := c.keepExtra(link, i, ref.Ext); err != nil {
		return err
	}
	if ref.SessionID != "" {
		if err := c.b.Member(link, "session_id", ref.SessionID); err != nil {
			return err
		}
	}
	if len(ref.Members.Extra) > 0 {
		c.reportMembers(i+1, "ref", ref.Members.Extra, ref.Mistyped())
		if !c.fam.opts.Slim {
			return c.b.Member(link, "ref", ref.Members.Extra)
		}
	}
	return nil
}

// keepRef keeps an unresolved reference on the output it was on.
func (c *docConv) keepRef(e agentsession.Entry, i int, ref *atif.Ref) error {
	raw, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	base := e.Base()
	var refs []json.RawMessage
	if prev, ok := base.Unknown[Source+":refs"]; ok {
		_ = json.Unmarshal(prev, &refs)
	}
	return c.b.Member(e, "refs", append(refs, raw))
}

// spawn resolves a reference to an embedded trajectory or a file and
// converts it into a session of its own, after this one: a subsession
// of callID, or, with none, a sub-agent no call spawned. It returns the
// child's session ID and leaf, or ok false with the reason reported.
func (c *docConv) spawn(i int, s *atif.Step, ref *atif.Ref, callID string) (child, leaf string, ok bool, err error) {
	line := i + 1
	if ref == nil || ref.Err != nil {
		c.addf(agentimport.Dropped, "atif/ref-unresolved", line, "a sub-agent reference that is not an object resolves nothing")
		return "", "", false, nil
	}
	var (
		in  docInput
		key string
		emb *atif.Trajectory
	)
	switch {
	case ref.TrajectoryID != "" && c.byTID[ref.TrajectoryID] != nil:
		emb = c.byTID[ref.TrajectoryID]
		if c.fam.embedded[emb] {
			c.repeated(line, "embedded trajectory "+ref.TrajectoryID)
			return "", "", false, nil
		}
		in = docInput{t: emb, name: c.in.name + "#" + ref.TrajectoryID, dir: c.in.dir}
		key = "trajectory/" + ref.TrajectoryID
	case ref.TrajectoryPath != "":
		real, rerr := c.fam.resolve(c.in.dir, ref.TrajectoryPath)
		if rerr == nil && c.fam.done[real] {
			c.repeated(line, c.fam.display(real))
			return "", "", false, nil
		}
		var f *atif.File
		if rerr == nil {
			f, rerr = c.fam.readDoc(real)
		}
		if rerr != nil {
			c.addf(agentimport.Dropped, "atif/ref-unresolved", line,
				"sub-agent file %s cannot be read (%s); the reference is kept and no session is made for it", ref.TrajectoryPath, agentimport.ErrorText(rerr))
			return "", "", false, nil
		}
		c.fam.done[real] = true
		in = docInput{t: f.Doc, file: f, name: c.fam.display(real), dir: filepath.Dir(real), real: real}
		key = "file/" + c.fam.display(real)
	default:
		what := "names neither an embedded trajectory of this document nor a file"
		if ref.TrajectoryID != "" {
			what = fmt.Sprintf("names trajectory %s, which this document does not embed", ref.TrajectoryID)
		}
		c.addf(agentimport.Dropped, "atif/ref-unresolved", line, "a sub-agent reference %s; it is kept and no session is made for it", what)
		return "", "", false, nil
	}
	if emb != nil {
		c.fam.embedded[emb] = true
	}
	t := target{parent: c.tgt.id, spawnedBy: callID, native: in.t.SessionID, created: c.stepTime(i, s)}
	if t.created.IsZero() {
		t.created = c.last
	}
	if callID != "" {
		t.id = agentsession.SubsessionID(c.tgt.id, callID)
		c.fam.ids[t.id] = true
	} else {
		t.id, t.derived = c.fam.claim(in.t.SessionID, c.tgt.id, key)
	}
	sessions, leaf, cerr := c.fam.child(in, t, c.report, c.in.name)
	if cerr != nil {
		return "", "", false, nil
	}
	c.children = append(c.children, sessions...)
	return t.id, leaf, true, nil
}

// repeated reports a reference to a document the conversion already
// converted: itself, an ancestor, or another reference's.
func (c *docConv) repeated(line int, what string) {
	c.addf(agentimport.Lossy, "atif/ref-repeated", line,
		"a sub-agent reference names %s, which this conversion already converted (another reference's, an ancestor, or itself); it is not converted again or linked, and the reference is kept", what)
}

// child converts a sub-agent's or continuation's document into its
// sessions, its findings and units in report. A document that fails to
// convert is a finding, and its units are dropped.
func (fam *family) child(in docInput, t target, report *agentimport.Report, from string) ([]*agentsession.Session, string, error) {
	units := in.t.Units()
	if in.file != nil && in.file.CutStep {
		units++
	}
	if in.synthetic {
		// Its root is the other document's.
		units--
	}
	drop := func() {
		for range units {
			report.Line(agentimport.LineDropped)
		}
	}
	// Each file and embedded trajectory converts once, so neither can
	// nest without end; this bound holds whatever names a document.
	key := in.key()
	if fam.depth >= maxDepth || fam.active[key] {
		drop()
		why := fmt.Sprintf("nests %d documents deep, past the %d the conversion follows", fam.depth+1, maxDepth)
		if fam.active[key] {
			why = "is one the conversion is converting already, which names itself through it"
		}
		report.Addf(agentimport.Dropped, "atif/subagent-nesting", in.name, 0,
			"the document named from %s %s; its %d units are left out", displayName(from), why, units)
		return nil, "", errNesting
	}
	fam.active[key] = true
	fam.depth++
	defer func() {
		delete(fam.active, key)
		fam.depth--
	}()
	sub := agentimport.NewReport(Source)
	sub.Unit = Unit
	sessions, leaf, err := fam.one(in, t, sub)
	if err == nil && in.real != "" {
		fam.held = append(fam.held, in.real)
	}
	if err != nil {
		drop()
		report.Addf(agentimport.Dropped, "atif/subagent-failed", in.name, 0,
			"the document named from %s could not be converted (%s); its %d units are left out", displayName(from), agentimport.ErrorText(err), units)
		return nil, "", err
	}
	report.Merge(sub)
	return sessions, leaf, nil
}

// maxDepth is how deep the conversion follows sub-agents and
// continuations below the main document.
const maxDepth = 32

// errNesting is why a document past maxDepth, or one already being
// converted, is not.
var errNesting = errors.New("nested too deep, or a cycle")

// key identifies a document while it converts: its trajectory, and for
// a sub-agent's interleaved steps, the first of them and the agent.
func (in docInput) key() string {
	if in.synthetic && len(in.t.Steps) > 0 {
		return fmt.Sprintf("steps:%p:%s", in.t.Steps[0], in.name)
	}
	return fmt.Sprintf("doc:%p", in.t)
}

// unlinked converts the trajectories this one embeds that no reference
// named, as sessions with parent_session alone.
func (c *docConv) unlinked() error {
	for n, sub := range c.t.Subagents {
		if sub == nil || sub.Err != nil {
			c.report.Line(agentimport.LineMalformed)
			c.addf(agentimport.Repaired, "atif/subagent-malformed", 0, "embedded trajectory %d is not an object and is skipped", n)
			continue
		}
		if c.fam.embedded[sub] {
			continue
		}
		c.fam.embedded[sub] = true
		name := sub.TrajectoryID
		if name == "" || c.byTID[name] != sub {
			name = fmt.Sprint(n)
		}
		t := target{parent: c.tgt.id, native: sub.SessionID, created: c.last}
		t.id, t.derived = c.fam.claim(sub.SessionID, c.tgt.id, "trajectory/"+name)
		in := docInput{t: sub, name: c.in.name + "#" + name, dir: c.in.dir}
		c.addf(agentimport.Lossy, "atif/subagent-unlinked", 0,
			"embedded trajectory %s is referenced by no step; it is session %s with parent_session alone", name, t.id)
		sessions, _, err := c.fam.child(in, t, c.report, c.in.name)
		if err != nil {
			continue
		}
		c.children = append(c.children, sessions...)
	}
	return nil
}

// continuation converts the document this one continues in, ATIF's
// continued_trajectory_ref, as the session this one is continued in:
// this one ends with a continued_in link, and the successor names it in
// parent_session.
func (c *docConv) continuation() error {
	ref := c.t.ContinuedRef
	if ref == "" {
		return nil
	}
	real, err := c.fam.resolve(c.in.dir, ref)
	if err == nil && c.fam.done[real] {
		c.addf(agentimport.Repaired, "atif/continuation-repeated", 0,
			"the continuation %s is a document this conversion already converted (itself or one before it); the chain ends here", ref)
		return nil
	}
	var f *atif.File
	if err == nil {
		f, err = c.fam.readDoc(real)
	}
	if err != nil {
		c.addf(agentimport.Dropped, "atif/continuation-missing", 0,
			"the continuation %s cannot be read (%s); it is the header member %s:continued_trajectory_ref", ref, agentimport.ErrorText(err), Source)
		return nil
	}
	c.fam.done[real] = true
	name := c.fam.display(real)
	t := target{parent: c.tgt.id, native: f.Doc.SessionID, created: c.last}
	t.id, t.derived = c.fam.claim(f.Doc.SessionID, c.tgt.id, "continuation/"+name)
	sessions, _, err := c.fam.child(docInput{t: f.Doc, file: f, name: name, dir: filepath.Dir(real), real: real}, t, c.report, c.in.name)
	if err != nil {
		return nil
	}
	if _, err := c.appendEntry(agentsession.NewLinkEntry(agentsession.RelContinuedIn, t.id)); err != nil {
		return err
	}
	c.after = append(c.after, sessions...)
	return nil
}
