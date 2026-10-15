// Package convert converts pi sessions into the Agent Session Format.
// docs/formats/pi.md is the mapping and lists every finding code it
// reports.
package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/pi"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "pi"

// Harness is the harness name converted sessions record.
const Harness = "pi"

// Options tune a conversion. The zero value keeps everything and
// resolves no parent session.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: tool
	// and extension details, members of a line or message the converter
	// does not map, and a bash execution's record beside its rendering.
	// Each drop is still reported. Custom entries, the format's own
	// place for extension state, are kept.
	Slim bool
	// ResolveParent reads the session file a forked session's header
	// names by path. Nil resolves nothing; ConvertFile reads the file.
	ResolveParent func(path string) (*pi.File, bool)
}

// ConvertFile reads the session file at path and converts it. A forked
// session's parent is resolved by reading the file it names, when that
// exists.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	f, err := pi.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if opts.ResolveParent == nil {
		dir := filepath.Dir(path)
		opts.ResolveParent = func(p string) (*pi.File, bool) {
			if !filepath.IsAbs(p) {
				p = filepath.Join(dir, p)
			}
			f, err := pi.ReadFile(p)
			if err != nil || f.Header() == nil || f.Header().ID == "" {
				return nil, false
			}
			return f, true
		}
	}
	return Convert(f, opts)
}

// Convert converts a session file into one session. The file is
// migrated to the current version first, as pi migrates it on load.
func Convert(f *pi.File, opts Options) (*agentimport.Result, error) {
	c := &converter{
		opts:   opts,
		report: agentimport.NewReport(Source),
		f:      f,
		disp:   map[*pi.Line]agentimport.Disposition{},
		first:  map[string]string{},
		items:  map[string][]string{},

		replacements: map[string][]string{},
		nodes:        map[*pi.Line]*pi.Node{},
	}
	c.migrate()
	c.tree = pi.BuildTree(f)
	for _, n := range c.tree.Nodes {
		c.nodes[n.Line] = n
	}
	h, err := c.header()
	if err != nil {
		return nil, err
	}
	c.b = agentimport.NewBuilder(h, Source, c.report)
	if err := c.run(); err != nil {
		return nil, err
	}
	if err := agentimport.Verify(c.b.S); err != nil {
		return nil, fmt.Errorf("converted session does not verify: %w", err)
	}
	return &agentimport.Result{Report: c.report, Sessions: []*agentsession.Session{c.b.S}}, nil
}

// converter converts one file into one session.
type converter struct {
	opts   Options
	report *agentimport.Report
	f      *pi.File
	tree   *pi.Tree
	b      *agentimport.Builder

	disp map[*pi.Line]agentimport.Disposition
	// first maps a pi entry id to the first session entry made from it;
	// the builder's own map holds the last, which its children hang
	// from.
	first map[string]string
	// items maps a pi entry id to the item entries it contributed to
	// context, and those its context edits added, which a later edit of
	// it omits.
	items map[string][]string
	// root is the entry every root hangs from: a fork's fork_of link,
	// or empty.
	root string
	// replacements maps the item a context edit wrote in place of a
	// message to the items it replaced.
	replacements map[string][]string
	// nodes maps each entry line to its node in the tree.
	nodes map[*pi.Line]*pi.Node
}

func (c *converter) addf(kind agentimport.Kind, code string, l *pi.Line, format string, args ...any) {
	line := 0
	if l != nil {
		line = l.No
	}
	c.report.Addf(kind, code, "", line, format, args...)
}

// mark records where a line ended up; the first mark stands.
func (c *converter) mark(l *pi.Line, d agentimport.Disposition) {
	if _, ok := c.disp[l]; !ok {
		c.disp[l] = d
	}
}

// migrate applies pi's migrations and reports what they changed.
func (c *converter) migrate() {
	m := pi.Migrate(c.f)
	if m.IDs > 0 {
		c.addf(agentimport.Repaired, "pi/v1-linear", nil,
			"a version %d file has no entry ids: its %d entries are chained in file order, as pi migrates them, and carry no %s:id", m.From, m.IDs, Source)
	}
	if m.FirstKept > 0 {
		c.addf(agentimport.Repaired, "pi/v1-first-kept-index", nil,
			"%d version 1 compactions name the first kept entry by index; the index became the entry", m.FirstKept)
	}
	if m.KeptIDs {
		c.addf(agentimport.Repaired, "pi/version-ids-kept", nil,
			"the file reads as version %d, by its header or for want of one, but its entries carry ids; they are kept, so its branches stand", m.From)
	}
	if m.HookMessages > 0 {
		c.addf(agentimport.Repaired, "pi/hook-message", nil,
			"%d messages of version 2's role hookMessage are read as role custom, as pi migrates them", m.HookMessages)
	}
}

// header builds the session header from the session line: the session
// ID, when it was created, where, and the session it was forked from.
func (c *converter) header() (agentsession.Header, error) {
	hl := c.f.Header()
	var h agentsession.Header
	if hl != nil {
		h.ID, h.CWD = hl.ID, hl.CWD
		h.CreatedAt = hl.Time()
	}
	if h.ID == "" {
		h.ID = pi.IDFromFileName(c.f.Path)
	}
	if hl == nil {
		c.addf(agentimport.Repaired, "pi/no-header", nil, "the file has no session line; the session ID is taken from the file name")
	}
	if h.ID == "" {
		h.ID = agentsession.NewSessionID()
		c.addf(agentimport.Repaired, "pi/no-session-id", nil, "neither the header nor the file name gives a session ID; one was made")
	}
	if h.CreatedAt.IsZero() {
		for _, l := range c.f.Entries() {
			if ts := c.lineTime(l); !ts.IsZero() && (h.CreatedAt.IsZero() || ts.Before(h.CreatedAt)) {
				h.CreatedAt = ts
			}
		}
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Unix(0, 0).UTC()
		c.addf(agentimport.Lossy, "pi/no-timestamp", nil, "the file has no timestamps; created_at is the epoch")
	}
	h.Harness = &agentsession.Harness{Name: Harness}
	if err := agentimport.SetSource(&h, agentimport.Source{Format: Source}); err != nil {
		return h, err
	}
	c.addf(agentimport.Gap, "agentimport/provenance", nil,
		"the import is recorded in the header member %s", agentimport.SourceMember)
	if hl == nil {
		return h, nil
	}
	c.mark(hl, agentimport.LineMapped)
	h.Extra[Source+":version"] = json.RawMessage(fmt.Sprint(c.f.Version()))
	if extra := nonNull(hl.Extra); len(extra) > 0 {
		if c.opts.Slim {
			c.addf(agentimport.Dropped, "pi/header-members", hl, "left out: %s", memberNames(extra))
		} else {
			raw, err := json.Marshal(extra)
			if err != nil {
				return h, err
			}
			if raw, err = c.clean(hl, raw); err != nil {
				return h, err
			}
			h.Extra[Source+":header"] = raw
			c.addf(agentimport.Kept, "pi/header-members", hl, "kept in the header member %s:header: %s", Source, memberNames(extra))
		}
	}
	if hl.ParentSession != "" {
		raw, err := json.Marshal(hl.ParentSession)
		if err != nil {
			return h, err
		}
		h.Extra[Source+":parent_session"] = raw
		var parent *pi.File
		ok := false
		if c.opts.ResolveParent != nil {
			parent, ok = c.opts.ResolveParent(hl.ParentSession)
		}
		if ok {
			h.ParentSession = parent.Header().ID
			ids := map[string]bool{}
			for _, l := range parent.Entries() {
				if l.ID != "" {
					ids[l.ID] = true
				}
			}
			copied := 0
			for _, l := range c.f.Entries() {
				if l.ID != "" && ids[l.ID] {
					copied++
				}
			}
			if copied > 0 {
				c.addf(agentimport.Lossy, "pi/fork-prefix", hl,
					"the fork copied %d entries of its parent's path into its own file; they are converted as this session's own entries, not as the prefix of a base in the parent", copied)
			}
		} else {
			c.addf(agentimport.Lossy, "pi/parent-unresolved", hl,
				"the parent session is named by a path that could not be read; it is kept in the header member %s:parent_session and parent_session is unset", Source)
		}
	}
	return h, nil
}

// lineTime is a line's timestamp, or its message's when the line has
// none.
func (c *converter) lineTime(l *pi.Line) time.Time {
	if ts := l.Time(); !ts.IsZero() {
		return ts
	}
	if l.Message != nil && l.Message.Timestamp > 0 {
		ms := int64(l.Message.Timestamp)
		return time.UnixMilli(ms).UTC()
	}
	return time.Time{}
}

// run writes the fork link, walks the tree from each root, names the
// leaf and tallies every line.
func (c *converter) run() error {
	h := c.b.S.Header()
	if h.ParentSession != "" {
		link := agentsession.NewLinkEntry(agentsession.RelForkOf, h.ParentSession)
		link.Timestamp = h.CreatedAt
		id, err := c.b.Append("", link)
		if err != nil {
			return err
		}
		c.root = id
	}
	t := c.tree
	for _, l := range t.Dangling {
		c.addf(agentimport.Repaired, "pi/dangling-parent", l, "parent %s is not in the file; the entry starts a root", l.ParentID)
	}
	for _, l := range t.Cycles {
		c.addf(agentimport.Repaired, "pi/parent-cycle", l, "the parent links from this entry form a cycle; it starts a root")
	}
	for _, l := range t.Duplicates {
		c.addf(agentimport.Repaired, "pi/duplicate-id", l, "an earlier entry has id %s; a parentId naming it names this one, as pi resolves it", l.ID)
	}
	// Entries are converted in file order, which pi writes parents
	// and the entries an entry names before it, so a branch summary or
	// label naming another branch finds it converted. An entry whose
	// parent comes later in the file waits for it.
	states := map[*pi.Node]pathState{}
	done := map[*pi.Node]bool{}
	waiting := map[*pi.Node][]*pi.Node{}
	var visit func(n *pi.Node) error
	visit = func(n *pi.Node) error {
		parent, st := c.root, pathState{}
		if n.Parent != nil {
			parent, _ = c.b.ID(nodeKey(n.Parent))
			st = states[n.Parent]
		}
		tail, err := c.entry(n.Line, parent, &st)
		if err != nil {
			return err
		}
		c.b.Bind(nodeKey(n), tail)
		states[n] = st
		done[n] = true
		for _, w := range waiting[n] {
			if err := visit(w); err != nil {
				return err
			}
		}
		delete(waiting, n)
		return nil
	}
	for _, n := range t.Nodes {
		if n.Parent != nil && !done[n.Parent] {
			waiting[n.Parent] = append(waiting[n.Parent], n)
			continue
		}
		if err := visit(n); err != nil {
			return err
		}
	}
	if t.Leaf != nil {
		if id, ok := c.b.ID(nodeKey(t.Leaf)); ok && id != "" {
			if err := c.b.Leaf(id); err != nil {
				return err
			}
		}
	}
	c.tally()
	return nil
}

// tally accounts for every line of the file.
func (c *converter) tally() {
	for _, l := range c.f.Lines {
		d, ok := c.disp[l]
		if !ok {
			if l.Type == pi.TypeSession {
				d = agentimport.LineDropped
				c.addf(agentimport.Dropped, "pi/extra-header", l, "a second session line; the first is the header")
			} else {
				d = agentimport.LineDropped
				c.addf(agentimport.Dropped, "pi/unreached", l, "the converter did not reach this %s line", l.Type)
			}
		}
		c.report.Line(d)
	}
	for _, e := range c.f.Errors {
		c.report.Line(agentimport.LineMalformed)
		c.report.Addf(agentimport.Repaired, "pi/malformed-line", "", e.No, "skipped, as pi skips it: %v", e.Err)
	}
	for _, no := range c.f.Surrogates {
		c.report.Addf(agentimport.Repaired, "pi/lone-surrogate", "", no,
			"a lone UTF-16 surrogate escape, half of a character cut short, was removed, as pi removes it from what it sends")
	}
}

// append adds an entry made from line l, which may be nil: its
// timestamp, and its id as the native identity.
func (c *converter) append(parent string, e agentsession.Entry, l *pi.Line) (string, error) {
	if l != nil {
		if e.Base().Timestamp.IsZero() {
			e.Base().Timestamp = c.lineTime(l)
		}
		if l.ID != "" && !l.Synthetic {
			if err := c.b.Member(e, "id", l.ID); err != nil {
				return "", err
			}
		}
	}
	id, err := c.b.Append(parent, e)
	if err != nil {
		return "", err
	}
	if l != nil {
		if _, ok := c.first[nodeKeyLine(l)]; !ok {
			c.first[nodeKeyLine(l)] = id
		}
	}
	return id, nil
}

// nodeKey is the key a node's entries are bound under: its id, or its
// line number when two entries share the id.
func nodeKey(n *pi.Node) string { return nodeKeyLine(n.Line) }

func nodeKeyLine(l *pi.Line) string { return fmt.Sprintf("%d", l.No) }

// lineMembers keeps the members of a line the converter does not map
// in the entry's member pi:line.
func (c *converter) lineMembers(e agentsession.Entry, l *pi.Line) error {
	m := nonNull(l.Extra)
	if len(m) == 0 {
		return nil
	}
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "pi/line-members", l, "left out: %s", memberNames(m))
		return nil
	}
	c.addf(agentimport.Kept, "pi/line-members", l, "kept in %s:line: %s", Source, memberNames(m))
	return c.b.Member(e, "line", m)
}

// custom keeps a whole native line as a custom entry, under data's
// member line: a line's own top-level usage, in pi's shape, would read
// as the format's record of a paid call.
func (c *converter) custom(l *pi.Line, tail, code string, format string, args ...any) (string, error) {
	return c.customAs(agentimport.Kept, l, tail, code, format, args...)
}

// customAs is custom with the finding's kind given.
func (c *converter) customAs(kind agentimport.Kind, l *pi.Line, tail, code string, format string, args ...any) (string, error) {
	line, err := c.clean(l, l.Raw)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"line": line})
	if err != nil {
		return "", err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.mark(l, agentimport.LineKept)
	c.addf(kind, code, l, format, args...)
	return id, nil
}

// clean makes native JSON from line l I-JSON, as the session format
// holds it, and reports what it changed.
func (c *converter) clean(l *pi.Line, raw json.RawMessage) (json.RawMessage, error) {
	if agentimport.RepeatedMembers(raw) {
		c.addf(agentimport.Repaired, "agentimport/repeated-member", l, "an object in the line named a member twice, which I-JSON forbids; the last is kept, as JSON.parse keeps it")
	}
	if agentimport.InvalidText(raw) {
		c.addf(agentimport.Repaired, "agentimport/invalid-text", l, "text in the line that was not valid Unicode was replaced with U+FFFD")
	}
	out, _, err := agentimport.CleanJSON(raw)
	return out, err
}

// drop accounts for a line that is not written.
func (c *converter) drop(l *pi.Line, tail, code, format string, args ...any) (string, error) {
	c.mark(l, agentimport.LineDropped)
	c.addf(agentimport.Dropped, code, l, format, args...)
	return tail, nil
}

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
	return joinComma(names)
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

// usage converts pi's usage to the Responses shape: input counts the
// cache reads and writes pi counts apart, cached tokens are the reads,
// and reasoning tokens are already in the output.
func usage(u *pi.Usage) *openresponses.Usage {
	if u == nil {
		return nil
	}
	ou := &openresponses.Usage{
		InputTokens:  int(u.Input + u.CacheRead + u.CacheWrite),
		OutputTokens: int(u.Output),
	}
	ou.InputTokensDetails.CachedTokens = int(u.CacheRead)
	if u.Reasoning != nil {
		ou.OutputTokensDetails.ReasoningTokens = int(*u.Reasoning)
	}
	ou.TotalTokens = ou.InputTokens + ou.OutputTokens
	return ou
}
