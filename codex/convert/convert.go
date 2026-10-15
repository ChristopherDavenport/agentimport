// Package convert converts Codex CLI rollouts into the Agent Session
// Format. docs/formats/codex.md is the mapping and lists every finding
// code it reports.
package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/codex"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "codex"

// Harness is the harness name converted sessions record.
const Harness = "codex"

// Options tune a conversion. The zero value keeps everything.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: item
	// and line members the conversion does not map, harness metadata,
	// rate limits, the session meta's other members. Each drop is still
	// reported. Custom entries, the format's own place for records it
	// does not define, are kept.
	Slim bool
}

// ConvertFile reads the rollout at path and converts it.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	f, err := codex.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Convert(f, opts)
}

// Convert converts one rollout into one session.
func Convert(f *codex.File, opts Options) (*agentimport.Result, error) {
	c := &converter{
		opts:     opts,
		report:   agentimport.NewReport(Source),
		f:        f,
		disp:     map[*codex.Line]agentimport.Disposition{},
		states:   map[string]state{},
		once:     map[string]bool{},
		reported: map[string]bool{},
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

// converter converts one rollout into one session. The rollout is a
// line, not a tree: the converter keeps the tail the next entry hangs
// from, and moves it back to branch when Codex rolled turns back.
type converter struct {
	opts   Options
	report *agentimport.Report
	f      *codex.File
	b      *agentimport.Builder
	meta   *codex.Line

	disp map[*codex.Line]agentimport.Disposition
	// tail is the entry the next one hangs from.
	tail string
	// st is what is in force at the tail.
	st state
	// states maps an entry to what was in force after it, so a rollback
	// to it restores its settings and calls.
	states map[string]state
	// pending is the response being gathered, nil when none is.
	pending *pending
	// once records the codes reported once per conversion.
	once map[string]bool
	// reported records the calls reported without an output.
	reported map[string]bool
	// lastTS is the latest time an entry was written at.
	lastTS time.Time
}

// state is what is in force at a point of the session. Its maps are
// replaced, never changed in place, so a branch shares its trunk's.
type state struct {
	model   string
	effort  string
	summary string
	cwd     string
	// turnContext is the last turn context's members the conversion
	// keeps in codex:turn_context, to tell a change from a repeat.
	turnContext string
	// truncation is the tool output budget in force, in bytes.
	truncation int64
	// calls maps a native call id to the call recorded for it, for the
	// calls on the path.
	calls map[string]string
	// answered marks the recorded call ids on the path that have an
	// output.
	answered map[string]bool
}

func (st *state) addCall(native, recorded string) {
	m := make(map[string]string, len(st.calls)+1)
	for k, v := range st.calls {
		m[k] = v
	}
	m[native] = recorded
	st.calls = m
}

func (st *state) answer(id string) {
	m := make(map[string]bool, len(st.answered)+1)
	for k, v := range st.answered {
		m[k] = v
	}
	m[id] = true
	st.answered = m
}

// defaultTruncation is the tool output budget Codex gives a model whose
// metadata names none (models-manager's fallback): 10,000 bytes.
const defaultTruncation = 10_000

func (c *converter) addf(kind agentimport.Kind, code string, l *codex.Line, format string, args ...any) {
	line := 0
	if l != nil {
		line = l.No
	}
	c.report.Addf(kind, code, "", line, format, args...)
}

// addOnce reports a code once per conversion.
func (c *converter) addOnce(kind agentimport.Kind, code string, l *codex.Line, format string, args ...any) {
	if c.once[code] {
		return
	}
	c.once[code] = true
	c.addf(kind, code, l, format, args...)
}

// mark records where a line ended up; the first mark stands.
func (c *converter) mark(l *codex.Line, d agentimport.Disposition) {
	if _, ok := c.disp[l]; !ok {
		c.disp[l] = d
	}
}

// header builds the session header from the session meta: the thread
// ID, when and where it started, the harness version, and where it came
// from.
func (c *converter) header() (agentsession.Header, error) {
	c.meta = c.f.MetaLine()
	var h agentsession.Header
	var m *codex.SessionMeta
	if c.meta != nil {
		m = c.meta.Meta
		h.ID, h.CWD = m.ID, m.CWD
		if ts, err := time.Parse(time.RFC3339Nano, m.Timestamp); err == nil {
			h.CreatedAt = ts
		}
	}
	if h.ID == "" {
		h.ID = codex.IDFromFileName(c.f.Path)
	}
	if c.meta == nil {
		c.addf(agentimport.Repaired, "codex/no-session-meta", nil, "the rollout has no session meta; the thread ID is taken from the file name")
	}
	if h.ID == "" {
		h.ID = agentsession.NewSessionID()
		c.addf(agentimport.Repaired, "codex/no-session-id", nil, "neither the session meta nor the file name gives a thread ID; one was made")
	}
	if h.CreatedAt.IsZero() {
		for _, l := range c.f.Lines {
			if ts := l.Time(); !ts.IsZero() && (h.CreatedAt.IsZero() || ts.Before(h.CreatedAt)) {
				h.CreatedAt = ts
			}
		}
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Unix(0, 0).UTC()
		c.addf(agentimport.Lossy, "codex/no-timestamp", nil, "the rollout has no timestamps; created_at is the epoch")
	}
	h.Harness = &agentsession.Harness{Name: Harness}
	src := agentimport.Source{Format: Source}
	if m != nil {
		h.Harness.Version = m.CLIVersion
		if m.SessionID != "" && m.SessionID != m.ID {
			src.NativeID = m.SessionID
		}
	}
	if err := agentimport.SetSource(&h, src); err != nil {
		return h, err
	}
	c.addf(agentimport.Gap, "agentimport/provenance", nil, "the import is recorded in the header member %s", agentimport.SourceMember)
	if c.f.Legacy {
		c.addf(agentimport.Lossy, "codex/legacy-layout", nil,
			"the rollout predates the envelope (before September 2025): its items carry no timestamps, so entries take the time of the last line that had one")
	}
	if m == nil {
		return h, nil
	}
	c.mark(c.meta, agentimport.LineMapped)
	if m.ParentThreadID != "" {
		h.ParentSession = m.ParentThreadID
	} else if m.ForkedFromID != "" {
		h.ParentSession = m.ForkedFromID
	}
	if kind, spawn := m.SubAgent(); kind != "" {
		if h.ParentSession == "" && spawn != nil {
			h.ParentSession = spawn.ParentThreadID
		}
		c.addf(agentimport.Lossy, "codex/subagent-unlinked", c.meta,
			"the thread is a %s sub-agent of %s; parent_session names the parent, but the parent's call that spawned it is not in this rollout, so there is no spawned_by and no link", kind, h.ParentSession)
	}
	if len(m.HistoryBase) > 0 && string(bytes.TrimSpace(m.HistoryBase)) != "null" {
		c.addf(agentimport.Lossy, "codex/history-base", c.meta,
			"the thread inherits a prefix of another rollout by reference; that prefix is not in this file and not in the session's context")
	}
	if m.Instructions != nil && *m.Instructions != "" {
		raw, err := json.Marshal(*m.Instructions)
		if err != nil {
			return h, err
		}
		h.Extra[Source+":instructions"] = raw
		c.addf(agentimport.Lossy, "codex/legacy-instructions", c.meta,
			"the legacy session meta's user instructions are the header member %s:instructions; how they reached the model is not recorded", Source)
	}
	extra, err := c.metaMembers(m)
	if err != nil {
		return h, err
	}
	if len(extra) > 0 {
		names := memberNames(extra)
		if c.opts.Slim {
			c.addf(agentimport.Dropped, "codex/session-meta-members", c.meta, "left out: %s", names)
		} else {
			raw, err := json.Marshal(extra)
			if err != nil {
				return h, err
			}
			if raw, err = c.clean(c.meta, raw); err != nil {
				return h, err
			}
			h.Extra[Source+":session_meta"] = raw
			c.addf(agentimport.Kept, "codex/session-meta-members", c.meta, "kept in the header member %s:session_meta: %s", Source, names)
		}
	}
	return h, nil
}

// metaMembers returns the session meta's members the header does not
// map, by name.
func (c *converter) metaMembers(m *codex.SessionMeta) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	for _, k := range []string{"id", "timestamp", "cwd", "cli_version", "base_instructions", "git", "instructions", "parent_thread_id"} {
		delete(all, k)
	}
	if m.SessionID == m.ID {
		delete(all, "session_id")
	}
	return nonNull(all), nil
}

// run writes the entries the session starts with, converts every line
// in file order, names the leaf and tallies every line.
func (c *converter) run() error {
	if err := c.start(); err != nil {
		return err
	}
	for _, l := range c.f.Lines {
		if l == c.meta {
			continue
		}
		if err := c.line(l); err != nil {
			return fmt.Errorf("line %d: %w", l.No, err)
		}
	}
	if err := c.closePending(nil); err != nil {
		return err
	}
	c.reportUnanswered(nil)
	if c.tail != "" {
		if err := c.b.Leaf(c.tail); err != nil {
			return err
		}
	}
	c.tally()
	return nil
}

// start writes what the session meta puts in force before any line: a
// fork's link to the thread it was forked from, the instructions, and
// where the thread ran.
func (c *converter) start() error {
	c.st.truncation = defaultTruncation
	if c.meta == nil {
		return nil
	}
	m := c.meta.Meta
	ts := c.meta.Time()
	if ts.IsZero() {
		ts = c.b.S.Header().CreatedAt
	}
	if m.ForkedFromID != "" {
		link := agentsession.NewLinkEntry(agentsession.RelForkOf, m.ForkedFromID)
		link.Timestamp = ts
		if err := c.appendNow(link, c.meta); err != nil {
			return err
		}
		c.addf(agentimport.Lossy, "codex/fork-prefix", c.meta,
			"the thread was forked from %s; the records it copied from its parent are converted as this session's own entries, not as the prefix of a base", m.ForkedFromID)
	}
	if m.BaseInstructions != nil && m.BaseInstructions.Text != "" {
		text := m.BaseInstructions.Text
		e := &agentsession.ConfigEntry{Instructions: &text}
		e.Timestamp = ts
		if err := c.appendNow(e, c.meta); err != nil {
			return err
		}
		if extra := nonNull(m.BaseInstructions.Extra); len(extra) > 0 || len(m.BaseInstructions.Provenance) > 0 {
			c.addf(agentimport.Kept, "codex/session-meta-members", c.meta, "the base instructions' provenance is in %s:session_meta", Source)
		}
	} else {
		c.addf(agentimport.Lossy, "codex/no-base-instructions", c.meta,
			"the session meta records no base instructions (older rollouts); Codex rendered the model's default, which is not recorded, so the session has none")
	}
	c.addOnce(agentimport.Lossy, "codex/tools-unrecorded", c.meta,
		"Codex does not record the tool definitions it sends the model; the session's config has none")
	if m.CWD != "" || m.Git != nil {
		env := agentsession.NewEnvEntry(m.CWD)
		env.Timestamp = ts
		if g := m.Git; g != nil && (g.CommitHash != "" || g.Branch != "" || g.RepositoryURL != "") {
			env.VCS = &agentsession.VCS{System: "git", Revision: g.CommitHash}
			if g.Branch != "" {
				if err := env.VCS.SetMember(Source+":branch", g.Branch); err != nil {
					return err
				}
				c.addf(agentimport.Gap, "agentimport/vcs-branch", c.meta,
					"the git branch is the vcs member %s:branch; the format's vcs has no branch", Source)
			}
			if g.RepositoryURL != "" {
				if err := env.VCS.SetMember(Source+":repository_url", g.RepositoryURL); err != nil {
					return err
				}
				c.addf(agentimport.Kept, "codex/repository-url", c.meta, "the repository URL is the vcs member %s:repository_url", Source)
			}
		}
		if err := c.appendNow(env, c.meta); err != nil {
			return err
		}
		c.st.cwd = m.CWD
	}
	return nil
}

// tally accounts for every line of the file.
func (c *converter) tally() {
	for _, l := range c.f.Lines {
		d, ok := c.disp[l]
		if !ok {
			d = agentimport.LineDropped
			c.addf(agentimport.Dropped, "codex/unreached", l, "the converter did not reach this %s line", l.Kind)
		}
		c.report.Line(d)
	}
	for _, e := range c.f.Errors {
		c.report.Line(agentimport.LineMalformed)
		c.report.Addf(agentimport.Repaired, "codex/malformed-line", "", e.No, "skipped, as Codex skips it: %v", e.Err)
	}
}

// identity sets the native identity of an entry made from line l: its
// ordinal when the rollout writes one, else its line number.
func (c *converter) identity(e agentsession.Entry, l *codex.Line) error {
	if l == nil {
		return nil
	}
	if l.Ordinal != "" {
		return c.b.Member(e, "ordinal", json.RawMessage(l.Ordinal))
	}
	return c.b.Member(e, "line", l.No)
}

// appendNow appends an entry made from line l under the tail, at once,
// with the line's time and identity, and moves the tail to it.
func (c *converter) appendNow(e agentsession.Entry, l *codex.Line) error {
	if l != nil && e.Base().Timestamp.IsZero() {
		e.Base().Timestamp = l.Time()
	}
	if e.Base().Timestamp.IsZero() {
		// A line with no time of its own sorts where the file put it;
		// before any line had one, at the session's start, never at the
		// clock's.
		e.Base().Timestamp = c.lastTS
		if c.lastTS.IsZero() {
			e.Base().Timestamp = c.b.S.Header().CreatedAt
		}
	}
	if t := e.Base().Timestamp; t.After(c.lastTS) {
		c.lastTS = t
	}
	if err := c.identity(e, l); err != nil {
		return err
	}
	id, err := c.b.Append(c.tail, e)
	if err != nil {
		return err
	}
	c.tail = id
	c.states[id] = c.st
	return nil
}

// emit appends an entry made from line l, or queues it behind the
// response being gathered, whose items take their response id when it
// closes.
func (c *converter) emit(e agentsession.Entry, l *codex.Line) error {
	if c.pending != nil {
		c.pending.queue = append(c.pending.queue, queued{e: e, l: l, st: c.st})
		return nil
	}
	return c.appendNow(e, l)
}

// custom keeps a whole native line as a custom entry, under data's
// member line, and accounts for it as kept.
func (c *converter) custom(l *codex.Line, code string, format string, args ...any) error {
	return c.customAs(agentimport.Kept, l, code, format, args...)
}

// customAs is custom with the finding's kind given.
func (c *converter) customAs(kind agentimport.Kind, l *codex.Line, code string, format string, args ...any) error {
	e, err := c.customEntry(l, l.Raw)
	if err != nil {
		return err
	}
	if err := c.emit(e, l); err != nil {
		return err
	}
	c.mark(l, agentimport.LineKept)
	c.addf(kind, code, l, format, args...)
	return nil
}

// customEntry is a custom entry of namespace codex holding raw, the line
// or part of it, under data's member line.
func (c *converter) customEntry(l *codex.Line, raw json.RawMessage) (*agentsession.CustomEntry, error) {
	line, err := c.clean(l, raw)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"line": line})
	if err != nil {
		return nil, err
	}
	return &agentsession.CustomEntry{NS: Source, Data: data}, nil
}

// drop accounts for a line that is not written.
func (c *converter) drop(l *codex.Line, code, format string, args ...any) {
	c.mark(l, agentimport.LineDropped)
	c.addf(agentimport.Dropped, code, l, format, args...)
}

// clean makes native JSON from line l I-JSON, as the session format
// holds it, and reports what it changed.
func (c *converter) clean(l *codex.Line, raw json.RawMessage) (json.RawMessage, error) {
	if agentimport.RepeatedMembers(raw) {
		c.addf(agentimport.Repaired, "agentimport/repeated-member", l, "an object in the line named a member twice, which I-JSON forbids; the last is kept, as serde_json keeps it")
	}
	if agentimport.InvalidText(raw) {
		c.addf(agentimport.Repaired, "agentimport/invalid-text", l, "text in the line that was not valid Unicode was replaced with U+FFFD")
	}
	out, _, err := agentimport.CleanJSON(raw)
	return out, err
}

// member sets a namespaced member holding native JSON, kept, or left out
// under Slim; either way it is reported under code.
func (c *converter) member(e agentsession.Entry, l *codex.Line, name, code string, raw json.RawMessage, what string) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	if c.opts.Slim {
		c.addf(agentimport.Dropped, code, l, "%s left out", what)
		return nil
	}
	clean, err := c.clean(l, raw)
	if err != nil {
		return err
	}
	if err := c.b.Member(e, name, clean); err != nil {
		return err
	}
	c.addf(agentimport.Kept, code, l, "%s kept in %s:%s", what, Source, name)
	return nil
}

// lineMembers keeps the envelope members of a line the converter does
// not map in the entry's member codex:envelope.
func (c *converter) lineMembers(e agentsession.Entry, l *codex.Line) error {
	if l.Legacy {
		return nil
	}
	m := nonNull(l.Extra)
	if len(m) == 0 {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return c.member(e, l, "envelope", "codex/line-members", raw, "line members "+memberNames(m))
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
	sort.Strings(names)
	return strings.Join(names, ", ")
}
