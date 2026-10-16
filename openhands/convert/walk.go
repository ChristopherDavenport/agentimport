package convert

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// conv converts one conversation.
type conv struct {
	fam    *family
	c      *openhands.Conversation
	tgt    target
	prefix string
	depth  int
	report *agentimport.Report
	b      *agentimport.Builder
	// out is the session as read back, once converted.
	out *agentsession.Session

	slim  bool
	agent openhands.AgentInfo
	acp   bool

	// Per event: what it is, its effective parent (-1 for a root), where
	// it ended up, the entry its children attach after, and the item
	// entries it put in context.
	info   []evInfo
	parent []int
	disp   []agentimport.Disposition
	tail   []string
	items  [][]string
	// done marks the actions a batch wrote with its first action.
	done []bool
	// eventOf maps an entry to the event that wrote it.
	eventOf map[string]int
	// leaf is the event the conversation resumes from, -1 for none.
	leaf int
	// copied is how many events, from the first, a fork copied from its
	// source; copyKnown says the count is known.
	copied    int
	copyKnown bool

	created, last time.Time
	naive         bool
	// tree says an event carries a parent_id, as SDK 1.31 and later
	// write.
	tree bool
	// byFileID maps the event IDs the file names hold to the first event
	// holding each.
	byFileID map[string]int
	// era is how the SDK that wrote the conversation built its view,
	// and eraWhy what tells it. reloadWhy holds why a reload left
	// events out, for the next reconcile; guessed the events a reload
	// guess was reported for.
	era       era
	eraWhy    string
	reloadWhy map[int]string
	guessed   map[int]bool

	// calls holds the calls written, by native ID, on every branch;
	// callAt the call each action wrote.
	calls   map[string][]*call
	callAt  map[int]*call
	respIDs map[string]bool
	usage   *usageIndex

	// vs is the view OpenHands builds, as of event vsKey.
	vs    *viewState
	vsKey int
	// pinnedOf holds, per compaction entry, the entries whose items it
	// pinned.
	pinnedOf map[string]map[string]bool

	kids     *children
	children []*conv
}

// evInfo is what the view and the walk read of an event.
type evInfo struct {
	kind       string
	toolCallID string
	respID     string
	thinking   bool
	hasAction  bool
	ts         time.Time
	tsOK       bool
	// mistyped lists members of the wrong JSON type, kept as written.
	mistyped []string
	// bad is set when the event does not decode as its kind.
	bad error
}

// call is a function call written as an item entry.
type call struct {
	native string
	id     string
	name   string
	entry  string
	event  int
	// hasAction is false for a call OpenHands could not run: an unknown
	// tool or invalid arguments.
	hasAction bool
	// answers are the events that answered the call, on any branch.
	answers []int
	// origin is the API family the call came from.
	origin string
}

// ancestor reports whether event a is event b or one before it on b's
// path.
func (c *conv) ancestor(a, b int) bool {
	for j := b; j >= a; j = c.parent[j] {
		if j == a {
			return true
		}
	}
	return false
}

// callFor returns the call native names that an output at event i
// answers: the latest so named on i's path.
func (c *conv) callFor(native string, i int) *call {
	list := c.calls[native]
	for k := len(list) - 1; k >= 0; k-- {
		if c.ancestor(list[k].event, i) {
			return list[k]
		}
	}
	return nil
}

// answeredOn reports whether an event on i's path answered cl.
func (c *conv) answeredOn(cl *call, i int) bool {
	for _, a := range cl.answers {
		if c.ancestor(a, i) {
			return true
		}
	}
	return false
}

func newConv(fam *family, c *openhands.Conversation, tgt target, prefix string, depth int, report *agentimport.Report) *conv {
	n := len(c.Events)
	return &conv{
		fam: fam, c: c, tgt: tgt, prefix: prefix, depth: depth, report: report,
		slim:     fam.opts.Slim,
		info:     make([]evInfo, n),
		parent:   make([]int, n),
		disp:     make([]agentimport.Disposition, n),
		tail:     make([]string, n),
		items:    make([][]string, n),
		done:     make([]bool, n),
		eventOf:  map[string]int{},
		leaf:     -1,
		calls:    map[string][]*call{},
		callAt:   map[int]*call{},
		respIDs:  map[string]bool{},
		vsKey:    -1,
		vs:       newViewState(),
		pinnedOf: map[string]map[string]bool{},
		guessed:  map[int]bool{},
	}
}

// sessions returns the conversation's session, then its sub-agents',
// each after its parent.
func (c *conv) sessions() []*agentsession.Session {
	out := []*agentsession.Session{c.out}
	for _, ch := range c.children {
		out = append(out, ch.sessions()...)
	}
	return out
}

// addf records a finding against a file of the conversation, at a
// 1-based line, or 0.
func (c *conv) addf(kind agentimport.Kind, code, file string, line int, format string, args ...any) {
	if file != "" {
		file = c.prefix + file
	} else if c.prefix != "" {
		file = strings.TrimSuffix(c.prefix, "/")
	}
	c.report.Addf(kind, code, file, line, format, args...)
}

// at records a finding against event i.
func (c *conv) at(kind agentimport.Kind, code string, i int, format string, args ...any) {
	c.addf(kind, code, openhands.DirEvents, i+1, format, args...)
}

// keeping says what Slim makes of something kept: the kind to report
// and the disposition.
func (c *conv) keeping() (agentimport.Kind, agentimport.Disposition) {
	if c.slim {
		return agentimport.Dropped, agentimport.LineDropped
	}
	return agentimport.Kept, agentimport.LineKept
}

// mark records where event i ended up; the first mark stands.
func (c *conv) mark(i int, d agentimport.Disposition) {
	if c.disp[i] == "" {
		c.disp[i] = d
	}
}

// clean makes raw I-JSON, reporting what it changed against event i, or
// the conversation when i is negative.
func (c *conv) clean(i int, raw json.RawMessage) (json.RawMessage, error) {
	file, line := "", 0
	if i >= 0 {
		file, line = openhands.DirEvents, i+1
	}
	if agentimport.RepeatedMembers(raw) {
		c.addf(agentimport.Repaired, "agentimport/repeated-member", file, line, "an object named a member twice, which I-JSON forbids; the last is kept")
	}
	if agentimport.InvalidText(raw) {
		c.addf(agentimport.Repaired, "agentimport/invalid-text", file, line, "text that was not valid Unicode was replaced with U+FFFD")
	}
	out, _, err := agentimport.CleanJSON(raw)
	return out, err
}

// run converts the conversation and accounts for every unit.
func (c *conv) run() error {
	if c.c.State != nil {
		c.agent = c.c.State.AgentInfo()
		c.acp = c.agent.Kind == openhands.AgentKindACP
	}
	c.plan()
	h, err := c.header()
	if err != nil {
		return err
	}
	c.b = agentimport.NewBuilder(h, Source, c.report)
	c.b.File = strings.TrimSuffix(c.prefix, "/")
	c.files()
	c.indexUsage()
	c.matchChildren()
	if len(c.c.Events) == 0 {
		if _, err := c.rootConfig(-1); err != nil {
			return err
		}
	}
	for i := range c.c.Events {
		if err := c.event(i); err != nil {
			return err
		}
	}
	if err := c.unlinkedChildren(); err != nil {
		return err
	}
	if err := c.finish(); err != nil {
		return err
	}
	c.tally()
	return nil
}

// plan reads what the walk needs before it starts: each event's kind,
// timestamp and call, its effective parent as OpenHands resolves it,
// the leaf, and a fork's copied events.
func (c *conv) plan() {
	evs := c.c.Events
	// OpenHands indexes events by the IDs their file names hold, the
	// first index of an ID winning, and resolves parents through it.
	firstIdx := map[string]int{}
	for i, e := range evs {
		if e.Err == nil && e.FileID != "" && e.ID != e.FileID {
			c.at(agentimport.Repaired, "openhands/id-mismatch", i, "the event's id %q is not the one its file name holds; parents resolve by the file name's, as OpenHands resolves them", e.ID)
		}
		if j, ok := firstIdx[e.FileID]; ok {
			c.at(agentimport.Repaired, "openhands/duplicate-event-id", i, "the event ID is event %d's too; a parent naming it names event %d, as OpenHands takes the first", j+1, j+1)
			continue
		}
		firstIdx[e.FileID] = i
	}
	c.byFileID = firstIdx
	for i, e := range evs {
		c.parent[i] = i - 1
		if i > 0 {
			// The legacy parent is the event the previous file's ID
			// names.
			c.parent[i] = firstIdx[evs[i-1].FileID]
		}
		if e.Err != nil {
			continue
		}
		c.info[i] = c.readInfo(i, e)
		if e.HasParent {
			c.tree = true
		}
		switch {
		case e.HasParent && e.ParentID == openhands.RootParent:
			c.parent[i] = -1
		case e.HasParent:
			j, ok := firstIdx[e.ParentID]
			switch {
			case !ok:
				c.at(agentimport.Repaired, "openhands/parent-missing", i, "the parent_id names no event of the conversation; the event before it is taken as its parent, as for an event with none")
			case j >= i:
				c.at(agentimport.Repaired, "openhands/parent-after", i, "the parent_id names event %d, which is not before it; the event before it is taken as its parent", j+1)
			default:
				c.parent[i] = j
			}
		}
	}
	c.leaf = c.pickLeaf()
	c.forkCopies()
	c.pickEra()
}

// readInfo reads the members of event i the walk and the view use.
func (c *conv) readInfo(i int, e *openhands.Event) evInfo {
	in := evInfo{kind: e.Kind}
	in.ts, in.tsOK = c.parseTime(i, e.Timestamp)
	switch e.Kind {
	case openhands.KindAction:
		a, err := e.Action()
		if err != nil {
			in.bad = err
			break
		}
		in.toolCallID, in.respID = a.ToolCallID, a.LLMResponseID
		in.thinking = len(a.ThinkingBlocks) > 0
		in.hasAction = !openhands.IsNull(a.Action)
		in.mistyped = a.Mistyped()
	case openhands.KindObservation:
		o, err := e.Observation()
		if err != nil {
			in.bad = err
			break
		}
		in.toolCallID, in.mistyped = o.ToolCallID, o.Mistyped()
	case openhands.KindUserReject:
		o, err := e.UserReject()
		if err != nil {
			in.bad = err
			break
		}
		in.toolCallID, in.mistyped = o.ToolCallID, o.Mistyped()
	case openhands.KindAgentError:
		o, err := e.AgentError()
		if err != nil {
			in.bad = err
			break
		}
		in.toolCallID, in.mistyped = o.ToolCallID, o.Mistyped()
	case openhands.KindMessage:
		m, err := e.Message()
		if err != nil {
			in.bad = err
			break
		}
		in.respID, in.mistyped = m.LLMResponseID, m.Mistyped()
	case openhands.KindSystemPrompt:
		s, err := e.SystemPrompt()
		if err != nil {
			in.bad = err
			break
		}
		in.mistyped = s.Mistyped()
	case openhands.KindCondensation:
		s, err := e.Condensation()
		if err != nil {
			in.bad = err
			break
		}
		in.mistyped = s.Mistyped()
	}
	return in
}

// parseTime reads an event's timestamp, noting a naive one.
func (c *conv) parseTime(i int, s string) (time.Time, bool) {
	t, naive, ok := openhands.ParseTime(s, c.fam.opts.Location)
	if !ok {
		if i >= 0 {
			c.at(agentimport.Repaired, "openhands/bad-timestamp", i, "the timestamp %q does not read; the entries take the time before them", s)
		}
		return time.Time{}, false
	}
	if naive {
		c.naive = true
	}
	return t.UTC(), true
}

// pickLeaf returns the event the conversation resumes from:
// base_state's leaf_event_id, else the last event that is not a state
// update or a conversation error, as OpenHands resolves its head.
func (c *conv) pickLeaf() int {
	evs := c.c.Events
	if s := c.c.State; s != nil && s.LeafEventID != "" {
		for i, e := range evs {
			if e.Err == nil && e.FileID == s.LeafEventID {
				return i
			}
		}
		c.addf(agentimport.Repaired, "openhands/leaf-missing", openhands.FileBaseState, 0, "leaf_event_id names no event OpenHands reads; the last event is the leaf")
	} else if s != nil && s.HeadIsEmpty {
		c.addf(agentimport.Lossy, "openhands/empty-head", openhands.FileBaseState, 0, "the conversation's head is deliberately empty, which a session cannot say; the last event is the leaf")
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Err == nil && !openhands.IsArtifact(evs[i].Kind) {
			return i
		}
	}
	return len(evs) - 1
}

// header builds the session header: the first timestamp, the working
// directory, the state and the import's provenance.
func (c *conv) header() (agentsession.Header, error) {
	h := agentsession.Header{ID: c.tgt.id, ParentSession: c.tgt.parent, SpawnedBy: c.tgt.spawnedBy}
	if c.tgt.parent == "" && c.tgt.alone != "" {
		if storeName(c.tgt.alone) {
			h.ParentSession = c.tgt.alone
		}
		c.addf(agentimport.Lossy, "openhands/subagent-alone", "", 0, "the conversation is a sub-agent of %s, converted on its own: parent_session names the parent, and no link in the parent's session names this one", c.tgt.alone)
	}
	if m := c.c.Meta; m != nil && m.ForkedFromConversationID != "" && h.ParentSession == "" {
		src := dashed(openhands.HexID(m.ForkedFromConversationID))
		if storeName(src) {
			h.ParentSession = src
		}
	}
	for i := range c.c.Events {
		if c.info[i].tsOK {
			c.created = c.info[i].ts
			break
		}
	}
	if c.created.IsZero() && c.c.Meta != nil {
		if t, ok := c.parseTime(-1, c.c.Meta.CreatedAt); ok {
			c.created = t
		}
	}
	if c.created.IsZero() {
		c.created = time.Unix(0, 0).UTC()
		if !c.fam.opts.CreatedAt.IsZero() {
			c.created = c.fam.opts.CreatedAt.UTC()
		}
		c.addf(agentimport.Lossy, "openhands/no-timestamp", "", 0, "the conversation holds no timestamp; created_at is %s", c.created.Format(time.RFC3339))
	}
	h.CreatedAt = c.created
	c.last = c.created
	if c.c.State != nil {
		h.CWD = c.c.State.WorkingDir()
	}
	h.Harness = &agentsession.Harness{Name: Harness}
	c.addf(agentimport.Lossy, "openhands/no-version", "", 0, "OpenHands records no version of itself or of its format; the harness names none, and shapes are told apart by structure")
	src := agentimport.Source{Format: Source}
	if c.tgt.id != c.tgt.native {
		src.NativeID = c.tgt.native
	}
	if err := agentimport.SetSource(&h, src); err != nil {
		return h, err
	}
	c.addf(agentimport.Gap, "agentimport/provenance", "", 0, "the import is recorded in the header member %s", agentimport.SourceMember)
	if c.tgt.derived != "" {
		c.addf(agentimport.Lossy, "openhands/session-id", "", 0, "%s", c.tgt.derived)
	}
	if c.naive {
		c.addf(agentimport.Lossy, "openhands/timestamp-zone", "", 0, "OpenHands writes local time with no zone; the timestamps are read in %s", c.fam.opts.Location)
	}
	if s := c.c.State; s != nil {
		var policy struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(s.ConfirmationPolicy, &policy) == nil && policy.Kind != "" && policy.Kind != "NeverConfirm" {
			c.addf(agentimport.Lossy, "openhands/confirmation-policy", openhands.FileBaseState, 0, "the conversation's confirmation policy is %s, its last value: OpenHands records no approval, so a call that ran may have been approved by the user or not asked about", policy.Kind)
		}
	}
	if c.acp {
		c.addf(agentimport.Lossy, "openhands/acp-agent", openhands.FileBaseState, 0, "the agent is an ACPAgent, which drives an external agent and records its tool calls rather than the model's context; the session holds what OpenHands recorded")
	}
	if err := c.stateMembers(&h); err != nil {
		return h, err
	}
	return h, nil
}

// stateMembers keeps base_state.json and meta.json in header members:
// the state without its per-call metrics, which the response entries
// carry, and the metrics' totals and costs apart.
func (c *conv) stateMembers(h *agentsession.Header) error {
	set := func(name string, raw json.RawMessage) error {
		if h.Extra == nil {
			h.Extra = map[string]json.RawMessage{}
		}
		raw, err := c.clean(-1, raw)
		if err != nil {
			return err
		}
		h.Extra[Source+":"+name] = raw
		return nil
	}
	kind, _ := c.keeping()
	if s := c.c.State; s != nil {
		var all map[string]json.RawMessage
		if err := json.Unmarshal(c.c.StateRaw, &all); err != nil {
			return err
		}
		stats := all["stats"]
		delete(all, "stats")
		if !c.slim {
			raw, err := json.Marshal(all)
			if err != nil {
				return err
			}
			if err := set("state", raw); err != nil {
				return err
			}
		}
		c.addf(kind, "openhands/state", openhands.FileBaseState, 0, "the conversation's state but its metrics, as OpenHands stored it: the agent's configuration, policies, skills, tags and secret registry")
		if !openhands.IsNull(stats) {
			raw, err := c.statsMember(s)
			if err != nil {
				return err
			}
			if raw != nil && !c.slim {
				if err := set("stats", raw); err != nil {
					return err
				}
			}
			if raw != nil {
				c.addf(kind, "openhands/cost-unplaced", openhands.FileBaseState, 0, "per-call costs name no response, so they cannot be placed on one; they are in the header member %s:stats with the metrics' totals", Source)
			}
		}
	}
	if m := c.c.Meta; m != nil {
		if !c.slim {
			if err := set("meta", c.c.MetaRaw); err != nil {
				return err
			}
		}
		c.addf(kind, "openhands/meta", openhands.FileMeta, 0, "the agent server's record of the conversation: title, metrics snapshot, fork origin and launch profile")
	}
	return nil
}

// files accounts for the units that are not events in the sequence: a
// state or metadata file that does not read, event files OpenHands does
// not read, and other files.
func (c *conv) files() {
	cv := c.c
	if cv.HasState && cv.StateErr != nil {
		c.addf(agentimport.Repaired, "openhands/malformed-file", openhands.FileBaseState, 0, "%s; it is skipped, and the session has no working directory, leaf or usage", agentimport.ErrorText(cv.StateErr))
	}
	if !cv.HasState {
		c.addf(agentimport.Lossy, "openhands/no-state", "", 0, "the conversation has no %s; the session has no working directory, leaf or usage", openhands.FileBaseState)
	}
	if cv.HasMeta && cv.MetaErr != nil {
		c.addf(agentimport.Repaired, "openhands/malformed-file", openhands.FileMeta, 0, "%s; it is skipped", agentimport.ErrorText(cv.MetaErr))
	}
	for _, f := range cv.Files {
		file := openhands.DirEvents + "/" + f.Name
		switch f.Status {
		case openhands.StatusGap:
			c.addf(agentimport.Dropped, "openhands/index-gap", file, 0, "an event file after a missing index; OpenHands stops reading at the gap, and so does the converter")
		case openhands.StatusDuplicate:
			c.addf(agentimport.Repaired, "openhands/duplicate-index", file, 0, "another file has index %d; the one whose name sorts last is read", f.Index)
		case openhands.StatusBadName:
			c.addf(agentimport.Dropped, "openhands/bad-file-name", file, 0, "a file of the events directory whose name is not an event file's; OpenHands ignores it")
		}
	}
	for _, name := range cv.Others {
		c.addf(agentimport.Dropped, "openhands/file-unread", name, 0, "a file of the conversation directory the converter does not read")
	}
	for _, d := range cv.Dirs {
		switch d {
		case openhands.DirSubagents:
		case openhands.DirObservations:
			c.addf(agentimport.Dropped, "openhands/observations-dir", d, 0, "the directory where tools saved the full output of results they cut is not read; the outputs are as the model saw them")
		default:
			c.addf(agentimport.Dropped, "openhands/dir-unread", d, 0, "a directory of the conversation the converter does not read")
		}
	}
}

// tally counts every unit by where it ended up. An event the
// conversion did not reach is dropped and reported.
func (c *conv) tally() {
	cv := c.c
	if cv.HasState {
		if cv.StateErr != nil {
			c.report.Line(agentimport.LineMalformed)
		} else {
			c.report.Line(agentimport.LineMapped)
		}
	}
	if cv.HasMeta {
		switch {
		case cv.MetaErr != nil:
			c.report.Line(agentimport.LineMalformed)
		case c.slim:
			c.report.Line(agentimport.LineDropped)
		default:
			c.report.Line(agentimport.LineKept)
		}
	}
	for _, f := range cv.Files {
		switch f.Status {
		case openhands.StatusEvent, openhands.StatusMalformed:
			i := f.Index
			d := c.disp[i]
			if d == "" {
				d = agentimport.LineDropped
				c.at(agentimport.Dropped, "openhands/unreached", i, "the converter did not reach this event; a bug if it occurs")
			}
			c.report.Line(d)
		default:
			c.report.Line(agentimport.LineDropped)
		}
	}
	for range cv.Others {
		c.report.Line(agentimport.LineDropped)
	}
}

// add appends e to the chain of event i after the entry at, records it,
// and returns its ID.
func (c *conv) add(i int, at string, e agentsession.Entry) (string, error) {
	base := e.Base()
	if base.Timestamp.IsZero() {
		base.Timestamp = c.last
	}
	if i >= 0 {
		if err := c.b.Member(e, "id", c.c.Events[i].ID); err != nil {
			return "", err
		}
	}
	id, err := c.b.Append(at, e)
	if err != nil {
		return "", err
	}
	if base.Timestamp.After(c.last) {
		c.last = base.Timestamp
	}
	if i >= 0 {
		c.eventOf[id] = i
	}
	return id, nil
}

// ts is event i's time, or zero.
func (c *conv) ts(i int) time.Time {
	if i < 0 || !c.info[i].tsOK {
		return time.Time{}
	}
	return c.info[i].ts
}

// rootConfig writes the config a new root starts with, the model the
// state names, and returns it. i is the event the root is for, -1 for a
// conversation with none.
func (c *conv) rootConfig(i int) (string, error) {
	cfg := &agentsession.ConfigEntry{Model: c.agent.Model}
	if c.agent.ReasoningEffort != "" {
		cfg.Reasoning = &openresponses.ReasoningConfig{Effort: openresponses.ReasoningEffort(c.agent.ReasoningEffort)}
	}
	cfg.Timestamp = c.ts(i)
	if cfg.Timestamp.IsZero() {
		cfg.Timestamp = c.created
	}
	id, err := c.b.Append("", cfg)
	if err != nil {
		return "", err
	}
	if i >= 0 {
		c.at(agentimport.Lossy, "openhands/no-system-prompt", i, "the path starts with no system prompt event; a config of the model the state names starts it")
	}
	return id, nil
}

// attach returns the entry event i's entries follow: its parent's tail,
// or a new root config.
func (c *conv) attach(i int) (string, error) {
	if p := c.parent[i]; p >= 0 {
		return c.tail[p], nil
	}
	return c.rootConfig(i)
}

// event converts event i.
func (c *conv) event(i int) error {
	e := c.c.Events[i]
	if e.Err != nil {
		c.at(agentimport.Repaired, "openhands/malformed-event", i, "%s; the event is skipped, as OpenHands skips an empty one", agentimport.ErrorText(e.Err))
		c.mark(i, agentimport.LineMalformed)
		at := ""
		if p := c.parent[i]; p >= 0 {
			at = c.tail[p]
		}
		c.tail[i] = at
		return nil
	}
	in := &c.info[i]
	if len(in.mistyped) > 0 {
		c.at(agentimport.Repaired, "openhands/mistyped-member", i, "members %s are not of the JSON type OpenHands writes; they are kept as written and not mapped", strings.Join(in.mistyped, ", "))
	}
	c.viewFor(i)
	if c.copyKnown && c.copied > 0 && i == c.copied {
		// A fork rebuilds its view from the copied events.
		c.reload("the fork's start")
	}
	if c.done[i] {
		// An action its batch wrote with the first: it writes nothing
		// more.
		if p := c.parent[i]; p >= 0 {
			c.tail[i] = c.tail[p]
		}
		c.mark(i, agentimport.LineMapped)
		c.viewApply(i)
		return nil
	}
	var err error
	applied := false
	switch {
	case in.bad != nil:
		err = c.undecodable(i, in.bad)
	case e.Kind == openhands.KindSystemPrompt:
		err = c.systemPrompt(i)
	case e.Kind == openhands.KindMessage:
		err = c.message(i)
	case e.Kind == openhands.KindAction:
		err = c.action(i)
	case e.Kind == openhands.KindObservation, e.Kind == openhands.KindUserReject, e.Kind == openhands.KindAgentError:
		err = c.output(i)
	case e.Kind == openhands.KindCondensation:
		err = c.condensation(i)
		applied = true
	case e.Kind == openhands.KindCondensationSummary:
		err = c.summaryEvent(i)
	default:
		err = c.record(i)
	}
	if err != nil {
		return fmt.Errorf("event %d: %w", i+1, err)
	}
	if !applied {
		c.viewApply(i)
	}
	return nil
}

// treeArtifact reports whether event i is a state update or a
// conversation error stamped with a parent: not a node of the event
// tree, so its children, which OpenHands never gives it, would be its
// parent's.
func (c *conv) treeArtifact(i int) bool {
	e := c.c.Events[i]
	return e.Err == nil && e.HasParent && openhands.IsArtifact(e.Kind)
}

// setTail records the end of event i's chain. A tree artifact's chain
// becomes its parent's, so the parent's later children follow it and
// it makes no branch of its own.
func (c *conv) setTail(i int, id string) {
	c.tail[i] = id
	if c.treeArtifact(i) {
		if p := c.parent[i]; p >= 0 {
			c.tail[p] = id
		}
	}
}
