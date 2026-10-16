package convert

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
	"github.com/ChristopherDavenport/agentsession"
)

// family converts one session directory: the session's history and the
// histories of the children it ran, each its own session.
type family struct {
	s      *clinesdk.Session
	opts   Options
	report *agentimport.Report
	// ids are the session IDs given out.
	ids map[string]bool
	// hists are the histories: the session's own first, then each
	// child's, by file name.
	hists []*hist
	// rows are the index's rows of the session and its children, by
	// session ID; indexed says an index was read.
	rows    map[string]*clinesdk.Row
	indexed bool
	// sub maps a call to the child it spawned and waited for; peers a
	// call to the children that outlive it.
	sub   map[callKey]int
	peers map[callKey][]int
	// disp records where each unit ended up.
	disp map[unit]agentimport.Disposition
}

// hist is one messages file and what the family knows of it.
type hist struct {
	i    int
	f    *clinesdk.MessagesFile
	kind string
	// native is the history's session ID; agent its agent's ID for a
	// child, and team its team task when it is one.
	native string
	agent  string
	team   clinesdk.TeamTask
	row    *clinesdk.Row
	// tasks are the texts of its user messages, as each request sends
	// them, by message index; first is the first one's index, -1 when
	// it has none.
	tasks  map[int]string
	taskTS map[int]time.Time
	first  int
	// firstTS is its first message's time.
	firstTS time.Time
	// calls are the spawn_agent and team_run_task calls it made.
	calls []*spawnCall
	// own is, for a team task, the index of the user message that
	// started it; the messages before it are a copy of an earlier
	// task's conversation.
	own int
	// linked says a call claimed it; sc is its conversion, failed says
	// it could not be converted; why says why it is unlinked.
	linked bool
	sc     *sconv
	failed bool
	active bool
	done   bool
	why    string
}

// spawnCall is a call that started a child.
type spawnCall struct {
	hist   int
	msg    int
	native string
	name   string
	task   string
	agent  string
	system string
	ts     time.Time
	outTS  time.Time
	// joined says a team_run_task call joined another call's run, as
	// Cline collapses a repeated sync call of one batch.
	joined bool
}

type callKey struct {
	hist   int
	native string
}

// Unit kinds.
const (
	uManifest = "manifest"
	uFile     = "file"
	uMsg      = "message"
	uComp     = "compaction"
	uCompMsg  = "compaction-message"
	uOther    = "other"
)

// unit is one unit of the session directory: a file (i is its history,
// or 0), or an element of an array (k is its index).
type unit struct {
	kind string
	i, k int
}

func newFamily(s *clinesdk.Session, opts Options, r *agentimport.Report) *family {
	return &family{s: s, opts: opts, report: r, ids: map[string]bool{}, rows: map[string]*clinesdk.Row{},
		sub: map[callKey]int{}, peers: map[callKey][]int{}, disp: map[unit]agentimport.Disposition{}}
}

// mark records where a unit ended up; the first mark stands.
func (fam *family) mark(u unit, d agentimport.Disposition) {
	if _, ok := fam.disp[u]; !ok {
		fam.disp[u] = d
	}
}

// remark records where a unit ended up, over an earlier mark.
func (fam *family) remark(u unit, d agentimport.Disposition) { fam.disp[u] = d }

// rootID is the session's native ID: its manifest's, else its
// directory's name.
func (fam *family) rootID() string {
	if m := fam.s.Manifest; m != nil && m.SessionID != "" {
		return m.SessionID
	}
	return fam.s.ID
}

// index builds the histories and reads the index's rows.
func (fam *family) index() error {
	root := fam.s.Messages
	if root == nil {
		root = &clinesdk.MessagesFile{Name: fam.s.ID + clinesdk.SuffixMessages, HasMessages: true}
	}
	fam.hists = append(fam.hists, &hist{i: 0, f: root, kind: clinesdk.AgentLead, native: fam.rootID()})
	for _, a := range fam.s.Agents {
		fam.hists = append(fam.hists, fam.child(len(fam.hists), a))
	}
	if ix := fam.opts.Index; ix != nil {
		rows, err := ix.Family(fam.rootID())
		if err != nil {
			fam.report.Addf(agentimport.Lossy, "clinesdk/index-unread", ix.Name(), 0, "the index's rows cannot be read: %s", agentimport.ErrorText(err))
		} else {
			fam.indexed = true
			for _, r := range rows {
				fam.rows[r.SessionID] = r
			}
		}
	}
	for _, h := range fam.hists {
		h.row = fam.rows[h.native]
		fam.scan(h)
	}
	if !fam.indexed && len(fam.hists) > 1 {
		fam.report.Addf(agentimport.Lossy, "clinesdk/no-index", "", 0, "no index of Cline's sessions is read: the session's %d children are matched to their calls by their task and time alone, without the spawning agent and the task the index records for each", len(fam.hists)-1)
	}
	fam.match()
	return nil
}

// child makes the hist of a child's messages file: its kind and IDs
// from its envelope, else from its name.
func (fam *family) child(i int, f *clinesdk.MessagesFile) *hist {
	h := &hist{i: i, f: f, kind: f.Agent, native: f.SessionID}
	stem := strings.TrimSuffix(f.Name, clinesdk.SuffixMessages)
	root := fam.rootID()
	if h.native == "" {
		if strings.Contains(stem, "__") {
			h.native = root + "__teamtask__" + stem
		} else {
			h.native = clinesdk.MakeSubSessionID(root, stem)
		}
	}
	t, team := clinesdk.ParseTeamTaskSubSessionID(h.native)
	switch {
	case h.kind == clinesdk.AgentTeammate || f.TaskType == "team" || h.kind == "" && team:
		h.kind = clinesdk.AgentTeammate
		h.team = t
		h.agent = t.Agent
		if h.agent == "" {
			h.agent, _, _ = strings.Cut(stem, "__")
		}
	default:
		h.kind = clinesdk.AgentSubagent
		_, h.agent, _ = clinesdk.ParseSubSessionID(h.native)
		if f.Origin != nil && f.Origin.Subagent != "" {
			h.agent = f.Origin.Subagent
		}
		if h.agent == "" {
			h.agent = stem
		}
	}
	return h
}

// scan reads what matching needs from a history: its user messages'
// texts and its calls that start children.
func (fam *family) scan(h *hist) {
	h.tasks, h.taskTS = map[int]string{}, map[int]time.Time{}
	h.first = -1
	h.own = -1
	answered := map[string]time.Time{}
	joined := map[string]bool{}
	msgs := h.f.Messages
	for k, m := range msgs {
		if m.Err != nil || m.DisplayOnly() {
			continue
		}
		ts, _ := m.Time()
		if h.firstTS.IsZero() {
			h.firstTS = ts
		}
		for _, b := range m.Blocks() {
			if b.Err == nil && b.Type == clinesdk.BlockToolResult {
				if _, ok := answered[b.ToolUseID]; !ok {
					answered[b.ToolUseID] = ts
				}
				if b.Name == "team_run_task" {
					joined[b.ToolUseID] = joinedRun(b.Content)
				}
			}
		}
		if m.Role == clinesdk.RoleUser {
			if text := userText(m); text != "" {
				h.tasks[k] = text
				h.taskTS[k] = ts
				if h.first < 0 {
					h.first = k
				}
			}
			continue
		}
		if m.Role != clinesdk.RoleAssistant {
			continue
		}
		for _, b := range m.Blocks() {
			if b.Err != nil || b.Type != clinesdk.BlockToolUse || b.Name != "spawn_agent" && b.Name != "team_run_task" {
				continue
			}
			in, err := jsjson.Parse(b.Input)
			if err != nil {
				continue
			}
			task, _ := in.Get("task").Text()
			c := &spawnCall{hist: h.i, msg: k, native: b.ID, name: b.Name, task: clinesdk.NormalizeUserInput(task), ts: ts}
			c.system, _ = in.Get("systemPrompt").Text()
			c.agent, _ = in.Get("agentId").Text()
			h.calls = append(h.calls, c)
		}
	}
	for _, c := range h.calls {
		c.outTS = answered[c.native]
		c.joined = joined[c.native]
	}
}

// joinedRun reports whether a team_run_task result says the call joined
// another's run.
func joinedRun(content json.RawMessage) bool {
	v, err := jsjson.Parse(content)
	if err != nil {
		return false
	}
	if s, ok := v.Text(); ok {
		if v, err = jsjson.Parse([]byte(s)); err != nil {
			return false
		}
	}
	status, _ := v.Get("status").Text()
	return status == "joined"
}

// placeholder reports whether an index row's prompt is one Cline makes
// when it has none: a sub-agent's or a team task's.
func placeholder(prompt string) bool {
	return strings.HasPrefix(prompt, "Subagent run by ") || strings.HasPrefix(prompt, "Team task for ")
}

// timely reports whether a child that started at started can have been
// started by call c: after the call, the call's turn stamped when it
// ended, and before its result.
func timely(c *spawnCall, started time.Time) bool {
	if started.IsZero() {
		return true
	}
	return (c.ts.IsZero() || !started.Before(c.ts.Add(-slack))) && (c.outTS.IsZero() || !started.After(c.outTS))
}

// userText is the text a user message sends, its text blocks joined;
// "" when it has none, or holds a tool result.
func userText(m *clinesdk.Message) string {
	var parts []string
	for _, b := range m.Blocks() {
		if b.Err != nil {
			continue
		}
		switch b.Type {
		case clinesdk.BlockToolResult:
			return ""
		case clinesdk.BlockText:
			if t := clinesdk.NormalizeUserInput(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// slack is how far before a call a child's first message may be and
// still be taken as the call's: the call's message is stamped when its
// turn ends.
const slack = 2 * time.Second

// match links each child to the calls that ran it. Cline records no
// call ID for a child: a sub-agent is matched to the spawn_agent call
// whose task is its first message, among the calls of the agent the
// index names as its parent when there is an index, made before it
// started and answered after; a team task to the team_run_task call
// for its agent whose task is the message that started it, made before
// that message and after any earlier such call. A child no one call
// answers to is left unlinked; a call links one child, and a call
// that joined another's run the child that run was.
func (fam *family) match() {
	order := make([]*hist, 0, len(fam.hists))
	order = append(order, fam.hists[1:]...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].firstTS.Before(order[j].firstTS) })
	claimed := map[callKey]bool{}
	var teams []*hist
	for _, h := range order {
		if h.kind == clinesdk.AgentTeammate {
			teams = append(teams, h)
			continue
		}
		fam.matchSub(h, claimed)
	}
	fam.matchTeams(teams, claimed)
}

// matchSub links a sub-agent to its spawn_agent call.
func (fam *family) matchSub(h *hist, claimed map[callKey]bool) {
	task := ""
	if h.first >= 0 {
		task = h.tasks[h.first]
	}
	if prompt := fam.prompt(h); prompt != "" {
		switch {
		case task == "":
			task = prompt
		case prompt != task:
			fam.report.Addf(agentimport.Lossy, "clinesdk/index-prompt", h.f.Name, 0, "the index's prompt for the sub-agent is not its first message, which Cline runs it with; the first message is matched")
		}
	}
	if task == "" {
		h.why = "it has no user message to match a call's task with"
		return
	}
	var cands, early []*spawnCall
	for _, p := range fam.parentsOf(h) {
		for _, c := range p.calls {
			if c.name != "spawn_agent" || c.task != task || claimed[callKey{c.hist, c.native}] {
				continue
			}
			if !timely(c, h.firstTS) {
				early = append(early, c)
				continue
			}
			cands = append(cands, c)
		}
	}
	switch len(cands) {
	case 0:
		h.why = "no spawn_agent call of its parent has its first message as its task"
		if len(early) > 0 {
			h.why = fmt.Sprintf("%d spawn_agent calls have its first message as their task, and it started outside the time each of them ran", len(early))
		}
	case 1:
		c := cands[0]
		k := callKey{c.hist, c.native}
		claimed[k] = true
		fam.sub[k] = h.i
		h.linked = true
	default:
		h.why = fmt.Sprintf("%d spawn_agent calls have its first message as their task, and their times do not tell them apart", len(cands))
	}
}

// prompt is the index's prompt for a child, "" when it has none or one
// Cline made up.
func (fam *family) prompt(h *hist) string {
	if h.row == nil || h.row.Prompt == "" || placeholder(h.row.Prompt) {
		return ""
	}
	return clinesdk.NormalizeUserInput(h.row.Prompt)
}

// parentsOf lists the histories whose calls may have spawned h: those
// of the sub-agent the index names as h's parent; when it names an
// agent no sub-agent's history is, the lead's or a teammate's, whose
// runtime agent IDs no file records, the root's and the team tasks';
// without an index, every other history.
func (fam *family) parentsOf(h *hist) []*hist {
	var out []*hist
	if h.row != nil && h.row.ParentAgentID != "" {
		for _, p := range fam.hists[1:] {
			if p != h && p.kind == clinesdk.AgentSubagent && p.agent == h.row.ParentAgentID {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out
		}
		for _, p := range fam.hists {
			if p != h && (p.i == 0 || p.kind == clinesdk.AgentTeammate) {
				out = append(out, p)
			}
		}
		return out
	}
	for _, p := range fam.hists {
		if p != h {
			out = append(out, p)
		}
	}
	return out
}

// own finds the message that started a team task: the index's prompt
// for it, else the latest of its user messages that is the task of a
// team_run_task call for its agent.
func (fam *family) own(h *hist, calls []*spawnCall) {
	keys := make([]int, 0, len(h.tasks))
	for k := range h.tasks {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	prompt := fam.prompt(h)
	for i := len(keys) - 1; i >= 0; i-- {
		text := h.tasks[keys[i]]
		if prompt != "" {
			if text == prompt {
				h.own = keys[i]
				return
			}
			continue
		}
		for _, c := range calls {
			if c.task == text {
				h.own = keys[i]
				return
			}
		}
	}
}

// matchTeams links each team task to the team_run_task call that
// started it, in the order the tasks started: a call for its agent
// whose task is the task's message, made before the message and not
// followed by another such call before it, which no task claimed.
func (fam *family) matchTeams(teams []*hist, claimed map[callKey]bool) {
	var calls []*spawnCall
	for _, p := range fam.hists {
		for _, c := range p.calls {
			if c.name == "team_run_task" && c.agent != "" {
				calls = append(calls, c)
			}
		}
	}
	sort.SliceStable(calls, func(i, j int) bool { return calls[i].ts.Before(calls[j].ts) })
	for _, h := range teams {
		var mine []*spawnCall
		for _, c := range calls {
			if c.agent == h.agent && c.hist != h.i {
				mine = append(mine, c)
			}
		}
		fam.own(h, mine)
	}
	sort.SliceStable(teams, func(i, j int) bool { return teams[i].taskTS[teams[i].own].Before(teams[j].taskTS[teams[j].own]) })
	for _, h := range teams {
		if h.own < 0 {
			h.why = "no team_run_task call for its agent has one of its messages as its task"
			continue
		}
		text, started := h.tasks[h.own], h.taskTS[h.own]
		var cands []*spawnCall
		for i, c := range calls {
			if c.agent != h.agent || c.hist == h.i || c.task != text || c.joined || claimed[callKey{c.hist, c.native}] {
				continue
			}
			if !started.IsZero() && !c.ts.IsZero() {
				if started.Before(c.ts.Add(-slack)) {
					continue
				}
				// A later call of the same task to the agent started
				// before this task did: this task is that call's.
				later := false
				for _, d := range calls[i+1:] {
					if d.agent == c.agent && d.task == c.task && !d.joined && !d.ts.IsZero() && d.ts.After(c.ts) && !started.Before(d.ts.Add(-slack)) {
						later = true
						break
					}
				}
				if later {
					continue
				}
			}
			cands = append(cands, c)
		}
		switch len(cands) {
		case 0:
			h.why = "no team_run_task call for its agent, made before the message that started it, has that message as its task"
		case 1:
			c := cands[0]
			k := callKey{c.hist, c.native}
			claimed[k] = true
			fam.peers[k] = append(fam.peers[k], h.i)
			h.linked = true
			// A call that joined this call's run, in the same
			// response, ran the same task.
			for _, d := range calls {
				if d.joined && d.hist == c.hist && d.msg == c.msg && d.agent == c.agent {
					dk := callKey{d.hist, d.native}
					claimed[dk] = true
					fam.peers[dk] = append(fam.peers[dk], h.i)
				}
			}
		default:
			h.why = fmt.Sprintf("%d team_run_task calls for its agent have the message that started it as their task, and their times do not tell them apart", len(cands))
		}
	}
}

// run converts the session, then each child no call of a converted
// history reached, and accounts for every unit.
func (fam *family) run() ([]*agentsession.Session, error) {
	root := fam.hists[0]
	var out []*agentsession.Session
	sc, err := fam.convert(root, fam.rootTarget(), 0)
	if err != nil {
		return nil, err
	}
	if sc != nil {
		out = append(out, sc.sessions()...)
	}
	for _, h := range fam.hists[1:] {
		if h.done {
			continue
		}
		if fam.opts.Alone {
			fam.dropHist(h, "clinesdk/children-alone", "the child is not converted: Alone converts the session without its sub-agents and team tasks")
			continue
		}
		t := fam.ownID(h.native)
		t.parent = fam.parentSID(h)
		why := h.why
		if why == "" {
			why = "the call that ran it is on no path converted"
		}
		sc, err := fam.convert(h, t, 1)
		if err != nil || sc == nil {
			continue
		}
		code := "clinesdk/subagent-unlinked"
		if h.kind == clinesdk.AgentTeammate {
			code = "clinesdk/teammate-unlinked"
		}
		fam.report.Addf(agentimport.Lossy, code, h.f.Name, 0, "the %s is linked to no call (%s); its session names %s in parent_session alone", kindName(h), why, t.parent)
		out = append(out, sc.sessions()...)
	}
	fam.tally()
	return out, nil
}

func kindName(h *hist) string {
	if h.kind == clinesdk.AgentTeammate {
		return "team task"
	}
	return "sub-agent"
}

// parentSID is the session a child converted on its own names as its
// parent: the history of the agent that spawned it when the index
// names one and it converted, else the root's.
func (fam *family) parentSID(h *hist) string {
	if h.row != nil && h.row.ParentAgentID != "" {
		var found *hist
		n := 0
		for _, p := range fam.hists[1:] {
			if p != h && p.kind == clinesdk.AgentSubagent && p.agent == h.row.ParentAgentID && p.sc != nil {
				found = p
				n++
			}
		}
		if n == 1 {
			return found.sc.tgt.id
		}
	}
	if root := fam.hists[0]; root.sc != nil {
		return root.sc.tgt.id
	}
	return fam.rootID()
}

// convert converts one history into tgt; nil when it could not be,
// which is reported, or was skipped. Only the root's failure is an
// error.
func (fam *family) convert(h *hist, tgt target, depth int) (*sconv, error) {
	h.done, h.active = true, true
	defer func() { h.active = false }()
	sc := newSconv(fam, h, tgt, depth)
	err := sc.run()
	if err == errSkip {
		sc.skipped()
		return nil, nil
	}
	var read *agentsession.Session
	if err == nil {
		read, err = agentimport.Finish(sc.b.S)
		if err != nil {
			err = fmt.Errorf("converted session does not verify: %w", err)
		}
	}
	if err != nil {
		if h.i == 0 {
			return nil, fmt.Errorf("%s: %w", h.f.Name, err)
		}
		h.failed = true
		fam.dropHist(h, "clinesdk/child-failed", "the child could not be converted: "+agentimport.ErrorText(err))
		return nil, nil
	}
	sc.out = read
	h.sc = sc
	fam.report.Merge(sc.report)
	for u, d := range sc.disp {
		fam.mark(u, d)
	}
	return sc, nil
}

// dropHist reports a history not converted, and drops its units.
func (fam *family) dropHist(h *hist, code, detail string) {
	h.done = true
	fam.report.Addf(agentimport.Dropped, code, h.f.Name, 0, "%s", detail)
	fam.remark(unit{uFile, h.i, 0}, agentimport.LineDropped)
	for k := range h.f.Messages {
		fam.remark(unit{uMsg, h.i, k}, agentimport.LineDropped)
	}
}

// tally counts every unit of the session directory by where it ended
// up. A unit no conversion reached is dropped and reported.
func (fam *family) tally() {
	count := func(u unit, file string, line int) {
		d, ok := fam.disp[u]
		if !ok {
			d = agentimport.LineDropped
			fam.report.Addf(agentimport.Dropped, "clinesdk/unreached", file, line, "the converter did not reach this unit; a bug if it occurs")
		}
		fam.report.Line(d)
	}
	s := fam.s
	if s.HasManifest {
		count(unit{uManifest, 0, 0}, s.ManifestName(), 0)
	}
	for _, h := range fam.hists {
		if h.i == 0 && s.Messages == nil {
			continue
		}
		count(unit{uFile, h.i, 0}, h.f.Name, 0)
		for k := range h.f.Messages {
			count(unit{uMsg, h.i, k}, h.f.Name, k+1)
		}
	}
	if c := s.Compaction; c != nil {
		count(unit{uComp, 0, 0}, c.Name, 0)
		for k := range c.Messages {
			count(unit{uCompMsg, 0, k}, c.Name, k+1)
		}
	}
	for i, name := range s.Others {
		fam.report.Addf(agentimport.Dropped, "clinesdk/file-unread", name, 0, "a file of the session directory the converter does not read")
		fam.mark(unit{uOther, i, 0}, agentimport.LineDropped)
		count(unit{uOther, i, 0}, name, 0)
	}
}

// rawJSON is a string as a JSON value.
func rawJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
