package convert

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// converter converts one transcript into one session.
type converter struct {
	cv     *conversion
	opts   Options
	report *agentimport.Report
	file   string
	tr     *claudecode.Transcript
	tree   *claudecode.Tree
	b      *agentimport.Builder
	t      target

	disp map[*claudecode.Line]agentimport.Disposition
	// calls maps a native tool_use ID to the call ID recorded for it,
	// the latest call holding it.
	calls map[string]string
	// agents maps a native tool_use ID to the agent the call spawned.
	agents map[string]*claudecode.Agent
	// spawned lists the agents this transcript's calls linked, in the
	// order the links were written.
	spawned []spawn
	// side holds, per side record type, the value last written, so an
	// unchanged repeat is not written again.
	state map[string]string
	// echoWire says the CLI replays a call's arguments as the model
	// wrote them, so those are what the context held.
	echoWire bool
	// consumed marks units a compaction folded into its entry.
	consumed map[*claudecode.Unit]bool
}

// spawn is an agent a call of this transcript spawned.
type spawn struct {
	agent  *claudecode.Agent
	id     string
	callID string
}

// pathState is what is in force at a point of the tree, along the path
// from its root.
type pathState struct {
	model  string
	cwd    string
	branch string
	// tools are the tool definitions a config entry put in force, by
	// name, as encoded; replaced, never changed in place, so a branch
	// shares its trunk's map.
	tools map[string]string
}

func newConverter(cv *conversion, tr *claudecode.Transcript, file string, t target, b *agentimport.Builder) *converter {
	c := &converter{
		cv: cv, opts: cv.opts, report: cv.report, file: file, tr: tr, b: b, t: t,
		disp:     map[*claudecode.Line]agentimport.Disposition{},
		calls:    map[string]string{},
		agents:   map[string]*claudecode.Agent{},
		state:    map[string]string{},
		consumed: map[*claudecode.Unit]bool{},
	}
	c.tree = claudecode.BuildTree(tr)
	for _, l := range tr.Lines {
		if snap, ok := l.PromptSnapshot(); ok {
			c.echoWire = snap.EchoWireToolInputs
			break
		}
	}
	c.linkAgents()
	return c
}

func (c *converter) addf(kind agentimport.Kind, code string, l *claudecode.Line, format string, args ...any) {
	line := 0
	if l != nil {
		line = l.No
	}
	c.report.Addf(kind, code, c.file, line, format, args...)
}

// mark records where a line ended up; the first mark stands.
func (c *converter) mark(l *claudecode.Line, d agentimport.Disposition) {
	if _, ok := c.disp[l]; !ok {
		c.disp[l] = d
	}
}

// run walks the tree from each root, then names the leaf and tallies
// every line.
func (c *converter) run() error {
	t := c.tree
	for _, l := range t.Dangling {
		c.addf(agentimport.Repaired, "claudecode/dangling-parent", l, "parent %s is not in the transcript; the line starts a root", l.ParentUUID)
	}
	for _, l := range t.Cycles {
		c.addf(agentimport.Repaired, "claudecode/parent-cycle", l, "the parent links from this line form a cycle; it starts a root")
	}
	head := t.Head
	for _, root := range t.Roots {
		if err := c.walk(root, "", pathState{}, head); err != nil {
			return err
		}
		head = nil
	}
	if head != nil {
		// A transcript of side records alone.
		if _, err := c.side(head, ""); err != nil {
			return err
		}
	}
	if err := c.leaf(); err != nil {
		return err
	}
	c.tally()
	return nil
}

// walk converts a unit and everything below it. It recurses: a path is
// as deep as its transcript is long, which Go's stacks hold.
func (c *converter) walk(u *claudecode.Unit, parent string, st pathState, head []*claudecode.Line) error {
	start := u.Start()
	tail := parent
	var err error
	if tail == "" {
		if tail, err = c.rootConfig(u, &st); err != nil {
			return err
		}
	}
	if tail, err = c.env(start, tail, &st); err != nil {
		return err
	}
	if len(head) > 0 {
		if tail, err = c.side(head, tail); err != nil {
			return err
		}
	}
	var summary *claudecode.Unit
	switch {
	case u.Response != nil:
		tail, err = c.response(u.Response, tail, &st)
	case start.Type == claudecode.TypeSystem && start.Subtype == claudecode.SubtypeCompactBoundary:
		tail, summary, err = c.compaction(u, tail)
	default:
		tail, err = c.line(start, tail, &st)
	}
	if err != nil {
		return err
	}
	if tail, err = c.side(u.Side, tail); err != nil {
		return err
	}
	children := u.Children
	if summary != nil {
		// The summary is folded into the compaction: its side records
		// and its children follow it.
		if tail, err = c.side(summary.Side, tail); err != nil {
			return err
		}
		children = append(append([]*claudecode.Unit(nil), summary.Children...), without(children, summary)...)
	}
	for _, ch := range children {
		if err := c.walk(ch, tail, st, nil); err != nil {
			return err
		}
	}
	return nil
}

func without(us []*claudecode.Unit, x *claudecode.Unit) []*claudecode.Unit {
	var out []*claudecode.Unit
	for _, u := range us {
		if u != x {
			out = append(out, u)
		}
	}
	return out
}

// append adds an entry made from line l, which may be nil: its
// timestamp, and its uuid as the native identity.
func (c *converter) append(parent string, e agentsession.Entry, l *claudecode.Line) (string, error) {
	if l != nil {
		if e.Base().Timestamp.IsZero() {
			e.Base().Timestamp = l.Time()
		}
		if l.UUID != "" {
			if err := c.b.Member(e, "uuid", l.UUID); err != nil {
				return "", err
			}
		}
	}
	return c.b.Append(parent, e)
}

// rootConfig writes the config a root starts with: the model of the
// first response below it.
func (c *converter) rootConfig(u *claudecode.Unit, st *pathState) (string, error) {
	model := firstModel(u)
	if model == "" {
		return "", nil
	}
	e := &agentsession.ConfigEntry{Model: model}
	e.Timestamp = u.Start().Time()
	st.model = model
	return c.append("", e, nil)
}

// firstModel finds the model of the first response at or below u, in
// walk order.
func firstModel(u *claudecode.Unit) string {
	stack := []*claudecode.Unit{u}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x.Response != nil {
			if m := requestModel(x.Response); m != "" {
				return m
			}
		}
		for i := len(x.Children) - 1; i >= 0; i-- {
			stack = append(stack, x.Children[i])
		}
	}
	return ""
}

// requestModel is the model a response was asked of: requestedModel
// when the line has it, the model that answered otherwise.
func requestModel(r *claudecode.Response) string {
	for _, l := range r.Lines {
		if l.IsSynthetic() {
			continue
		}
		if l.RequestedModel != "" {
			return l.RequestedModel
		}
		if l.Message != nil && l.Message.Model != "" {
			return l.Message.Model
		}
	}
	return ""
}

// env writes an env entry when a line's working directory or branch
// differs from what is in force.
func (c *converter) env(l *claudecode.Line, tail string, st *pathState) (string, error) {
	if l.CWD == "" || l.CWD == st.cwd && l.GitBranch == st.branch {
		return tail, nil
	}
	e := agentsession.NewEnvEntry(l.CWD)
	if l.GitBranch != "" {
		e.VCS = &agentsession.VCS{System: "git"}
		if err := e.VCS.SetMember(Source+":branch", l.GitBranch); err != nil {
			return "", err
		}
		c.addf(agentimport.Gap, "agentimport/vcs-branch", l, "the git branch is the vcs member %s:branch; the format's vcs has no branch", Source)
	}
	e.Timestamp = l.Time()
	st.cwd, st.branch = l.CWD, l.GitBranch
	return c.append(tail, e, nil)
}

// leaf names the entry the transcript was last at: the leafUuid of the
// last last-prompt record, or the last tree line.
func (c *converter) leaf() error {
	var target *claudecode.Line
	for _, l := range c.tr.Lines {
		if l.Err != nil {
			continue
		}
		if l.Type == claudecode.TypeLastPrompt && l.LeafUUID != "" {
			if x := c.tree.Line(l.LeafUUID); x != nil {
				target = x
			}
		}
	}
	if target == nil {
		for i := len(c.tr.Lines) - 1; i >= 0; i-- {
			if l := c.tr.Lines[i]; l.Err == nil && l.InTree() {
				target = l
				break
			}
		}
	}
	if target == nil {
		return nil
	}
	id, ok := c.b.ID(target.UUID)
	if !ok {
		return nil
	}
	return c.b.Leaf(id)
}

// tally accounts for every line of the transcript.
func (c *converter) tally() {
	for _, l := range c.tr.Lines {
		d, ok := c.disp[l]
		if !ok {
			d = agentimport.LineDropped
			c.addf(agentimport.Dropped, "claudecode/unreached", l, "the converter did not reach this %s line", l.Type)
		}
		c.report.Line(d)
	}
	for _, e := range c.tr.Errors {
		c.report.Line(agentimport.LineMalformed)
		c.report.Addf(agentimport.Repaired, "claudecode/malformed-line", c.file, e.No, "skipped: %v", e.Err)
	}
	for _, no := range c.tr.NULLines {
		c.report.Addf(agentimport.Repaired, "claudecode/nul-bytes", c.file, no, "NUL bytes removed from the line")
	}
}

// lineMembers keeps the members of a line the converter does not map
// in the entry's member claudecode:line.
func (c *converter) lineMembers(e agentsession.Entry, l *claudecode.Line) error {
	m := map[string]json.RawMessage{}
	for k, v := range nonNull(l.Extra) {
		if !envelopeExtra[k] {
			m[k] = v
		}
	}
	if l.PermissionMode != "" {
		m["permissionMode"], _ = json.Marshal(l.PermissionMode)
	}
	if len(m) == 0 {
		return nil
	}
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "claudecode/line-members", l, "left out: %s", memberNames(m))
		return nil
	}
	c.addf(agentimport.Kept, "claudecode/line-members", l, "kept in %s:line: %s", Source, memberNames(m))
	return c.b.Member(e, "line", m)
}

// envelopeExtra are undeclared members every line repeats, which the
// header and the env entries already say.
var envelopeExtra = map[string]bool{"session_id": true, "slug": true}

// nonNull returns the members whose value is not null: a null member
// says nothing an absent one does not.
func nonNull(m map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range m {
		if string(bytes.TrimSpace(v)) != "null" {
			out[k] = v
		}
	}
	return out
}

func memberNames(m map[string]json.RawMessage) string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sortStrings(names)
	return strings.Join(names, ", ")
}

// custom keeps a whole native line as a custom entry.
func (c *converter) custom(l *claudecode.Line, tail, code string, format string, args ...any) (string, error) {
	return c.customAs(agentimport.Kept, l, tail, code, format, args...)
}

// customAs is custom with the finding's kind given.
func (c *converter) customAs(kind agentimport.Kind, l *claudecode.Line, tail, code string, format string, args ...any) (string, error) {
	e := &agentsession.CustomEntry{NS: Source, Data: l.Raw}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.mark(l, agentimport.LineKept)
	if l.UUID != "" {
		c.b.Bind(l.UUID, id)
	}
	c.addf(kind, code, l, format, args...)
	return id, nil
}

// compactJSON is raw with insignificant space removed; raw itself when
// it is not JSON.
func compactJSON(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return string(raw)
	}
	return b.String()
}

// itemEntry wraps an item.
func itemEntry(item openresponses.Item) *agentsession.ItemEntry {
	return agentsession.NewItemEntry(item)
}
