package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// sconv converts one session: its copies' records into one session of
// the Agent Session Format.
type sconv struct {
	fam   *family
	rec   *record
	tgt   target
	depth int
	// report holds this session's tally and findings, merged into into
	// when the session converts: the family's report, or the parent
	// session's.
	report *agentimport.Report
	into   *agentimport.Report
	b      *agentimport.Builder
	out    *agentsession.Session
	slim   bool

	// units lists the records read, in order, and disp where each ended
	// up.
	units []string
	disp  map[string]agentimport.Disposition

	// The records read.
	msgs  []*opencode.MessageRecord
	rows  []*opencode.Row2
	queue []*opencode.QueueRow
	// native is where the V2 rows OpenCode 2 wrote itself start, past
	// the copy its migration made of the V1 tables; notice is the
	// migration's system row, when it wrote one.
	native int
	notice *opencode.Row2

	// The walk: the entry the next one follows, and the settings in
	// force.
	at       string
	model    string
	provider string
	agent    string
	variant  string
	// switched says a model other than the first has been in force.
	switched bool
	// leaf is the entry the session resumes from, when a staged revert
	// puts it before the last.
	leaf string

	kids     *kids
	children []*sconv
}

func newSconv(fam *family, rec *record, tgt target, depth int) *sconv {
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	return &sconv{fam: fam, rec: rec, tgt: tgt, depth: depth, report: r, slim: fam.opts.Slim, disp: map[string]agentimport.Disposition{}}
}

// sessions returns the session, then its sub-agents', each after its
// parent.
func (sc *sconv) sessions() []*agentsession.Session {
	out := []*agentsession.Session{sc.out}
	for _, ch := range sc.children {
		out = append(out, ch.sessions()...)
	}
	return out
}

// read records that the converter read a unit.
func (sc *sconv) read(unit string) {
	if _, ok := sc.disp[unit]; ok {
		return
	}
	sc.units = append(sc.units, unit)
	sc.disp[unit] = ""
}

// mark records where a unit ended up. A unit that ends in two places
// is counted in the one that keeps the most: mapped, then kept, then
// dropped.
func (sc *sconv) mark(unit string, d agentimport.Disposition) {
	sc.read(unit)
	if rank(d) > rank(sc.disp[unit]) {
		sc.disp[unit] = d
	}
}

func rank(d agentimport.Disposition) int {
	switch d {
	case agentimport.LineMapped:
		return 4
	case agentimport.LineKept:
		return 3
	case agentimport.LineDropped:
		return 2
	case agentimport.LineMalformed:
		return 1
	}
	return 0
}

// keepDisp is the disposition of a record kept in a member or a custom
// entry: dropped under Slim.
func (sc *sconv) keepDisp() agentimport.Disposition {
	if sc.slim {
		return agentimport.LineDropped
	}
	return agentimport.LineKept
}

// addf records a finding against a unit, or the session's record.
func (sc *sconv) addf(kind agentimport.Kind, code, unit string, format string, args ...any) {
	if unit == "" {
		unit = sc.rec.primary().Unit
	}
	sc.report.Addf(kind, code, unit, 0, format, args...)
}

// tally counts every unit read, and merges the report into the
// family's.
func (sc *sconv) tally() {
	for _, u := range sc.units {
		d := sc.disp[u]
		if d == "" {
			d = agentimport.LineDropped
			sc.addf(agentimport.Dropped, "opencode/unreached", u, "the record was read and nothing made of it")
		}
		sc.report.Line(d)
	}
	sc.into.Merge(sc.report)
}

// abandon counts every unit the session and its sub-agents read as
// dropped, when the session could not be converted, and merges the
// findings that say why.
func (sc *sconv) abandon() {
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	var walk func(s *sconv)
	walk = func(s *sconv) {
		for range s.units {
			r.Line(agentimport.LineDropped)
		}
		for _, ch := range s.children {
			walk(ch)
		}
	}
	walk(sc)
	for _, f := range sc.report.Findings {
		if f.Code == "opencode/session-failed" {
			r.Add(f)
		}
	}
	sc.into.Merge(r)
}

// ms is an OpenCode time, epoch milliseconds, as a time.
func ms(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(n).UTC()
}

// run reads the session's records, writes its header and walks them.
func (sc *sconv) run() error {
	if err := sc.load(); err != nil {
		return err
	}
	h, err := sc.header()
	if err != nil {
		return err
	}
	sc.b = agentimport.NewBuilder(h, Source, sc.report)
	sc.kids = sc.findKids()
	if err := sc.start(); err != nil {
		return err
	}
	if sc.rec.v1 != nil {
		if err := sc.walkV1(); err != nil {
			return err
		}
	}
	if sc.rec.v2 != nil && sc.native < len(sc.rows) || sc.rec.v1 == nil {
		if err := sc.walkV2(); err != nil {
			return err
		}
	}
	if err := sc.queued(); err != nil {
		return err
	}
	if err := sc.unlinkedKids(); err != nil {
		return err
	}
	if sc.leaf != "" {
		if err := sc.b.Leaf(sc.leaf); err != nil {
			return err
		}
	} else if last := sc.b.Last(); last != "" {
		if err := sc.b.Leaf(last); err != nil {
			return err
		}
	}
	return nil
}

// load reads the records of every copy of the session: the copies
// converted, and those passed over, which are tallied and reported.
func (sc *sconv) load() error {
	rec := sc.rec
	sc.mark(rec.primary().Unit, agentimport.LineMapped)
	if rec.v1 != nil {
		msgs, strays, err := rec.v1Store.Messages(rec.v1)
		if err != nil {
			return fmt.Errorf("read the messages: %w", err)
		}
		sc.msgs = msgs
		sc.strays(strays)
		for _, m := range msgs {
			sc.read(m.Unit)
			for _, p := range m.Parts {
				sc.read(p.Unit)
			}
		}
	}
	if rec.v2 != nil {
		rows, queue, err := rec.v2Store.Rows(rec.v2)
		if err != nil {
			return fmt.Errorf("read the rows: %w", err)
		}
		sc.rows, sc.queue = rows, queue
		for _, r := range rows {
			sc.read(r.Unit)
		}
		for _, q := range queue {
			sc.read(q.Unit)
		}
		if rec.v1 != nil {
			sc.mark(rec.v2.Unit, agentimport.LineDropped)
			sc.splitMigrated()
		}
	}
	for _, x := range rec.extra {
		sc.passOver(x)
	}
	return nil
}

// strays tallies the records read with the session that belong to none
// of its messages.
func (sc *sconv) strays(strays []*opencode.Stray) {
	for _, s := range strays {
		switch {
		case s.Kind == "directory":
			sc.addf(agentimport.Dropped, "opencode/unreadable", s.Unit, "%s", s.Why)
		case s.Malformed:
			sc.mark(s.Unit, agentimport.LineMalformed)
			sc.addf(agentimport.Repaired, "opencode/malformed", s.Unit, "a %s record that cannot be read is skipped: %s", s.Kind, s.Why)
		default:
			sc.mark(s.Unit, agentimport.LineDropped)
			sc.addf(agentimport.Dropped, "opencode/orphan", s.Unit, "a %s record that belongs to no message of the session is left out: %s", s.Kind, s.Why)
		}
	}
}

// passOver reads a copy of the session that is not converted, tallies
// its records dropped and says what it holds that the converted copy
// does not.
func (sc *sconv) passOver(x copyOf) {
	sc.mark(x.s.Unit, agentimport.LineDropped)
	var held []string
	if x.s.Gen != opencode.G3 {
		msgs, strays, err := x.store.Messages(x.s)
		if err != nil {
			sc.addf(agentimport.Dropped, "opencode/duplicate-store", x.s.Unit, "a second copy of the session, in the %s, is not converted, and its messages cannot be read: %s", x.s.Gen, agentimport.ErrorText(err))
			return
		}
		for _, s := range strays {
			if s.Kind != "directory" {
				sc.mark(s.Unit, agentimport.LineDropped)
			}
		}
		have := map[string]bool{}
		for _, m := range sc.msgs {
			have[m.ID] = true
		}
		for _, m := range msgs {
			sc.mark(m.Unit, agentimport.LineDropped)
			for _, p := range m.Parts {
				sc.mark(p.Unit, agentimport.LineDropped)
			}
			if !have[m.ID] {
				held = append(held, m.ID)
			}
		}
	}
	what := fmt.Sprintf("the session is converted from the %s, which OpenCode read after it; this copy, in the %s, is left out", sc.rec.primary().Gen, x.s.Gen)
	if len(held) > 0 {
		sc.addf(agentimport.Lossy, "opencode/duplicate-diverged", x.s.Unit, "%s, and it holds %d messages the converted copy does not, such as %s", what, len(held), held[0])
		return
	}
	sc.addf(agentimport.Dropped, "opencode/duplicate-store", x.s.Unit, "%s", what)
}

// splitMigrated tells, for a session both the V1 and the V2 tables
// hold, the V2 rows OpenCode 2's migration copied from the V1 tables
// from those it wrote after: the copy is the rows up to the last whose
// ID is a V1 message's, every row the migration writes taking its seq
// from its place, and the system row it appends telling the model of
// renamed tools.
func (sc *sconv) splitMigrated() {
	ids := map[string]bool{}
	for _, m := range sc.msgs {
		ids[m.ID] = true
	}
	last := -1
	for i, r := range sc.rows {
		if ids[r.ID] {
			last = i
		}
	}
	sc.native = last + 1
	if last < 0 && len(sc.msgs) > 0 {
		sc.native = 0
	}
	if sc.native < len(sc.rows) {
		r := sc.rows[sc.native]
		if r.Err == nil && r.Type == opencode.TypeSystem && strings.HasPrefix(r.Data.Text, "The available tools have changed.") {
			sc.notice = r
			sc.native++
		}
	}
	if sc.native == len(sc.rows) && sc.notice != nil {
		// OpenCode 2 never sent the notice: nothing came after it.
		sc.mark(sc.notice.Unit, agentimport.LineDropped)
		sc.notice = nil
	}
	copied := 0
	for _, r := range sc.rows[:sc.native] {
		if r != sc.notice {
			sc.mark(r.Unit, agentimport.LineDropped)
			copied++
		}
	}
	if copied > 0 {
		sc.addf(agentimport.Dropped, "opencode/duplicate-store", sc.rec.v2.Unit, "OpenCode 2's migration copied the session's first %d messages into its own tables, a rendering that keeps one assistant message per V1 message and drops step boundaries, retries and snapshots; they are converted from the V1 tables, and the copy is left out", copied)
	}
}

// header writes the session's header.
func (sc *sconv) header() (agentsession.Header, error) {
	p := sc.rec.primary()
	h := agentsession.Header{
		ID:        sc.tgt.id,
		CreatedAt: ms(p.Created),
		Harness:   &agentsession.Harness{Name: Harness, Version: p.Version},
		CWD:       p.Directory,
	}
	if !p.HasCreated {
		h.CreatedAt = sc.firstTime()
		sc.addf(agentimport.Lossy, "opencode/created-at", "", "the session records no creation time; created_at is its first record's")
	}
	if sc.tgt.parent != "" {
		h.ParentSession = sc.tgt.parent
		if sc.tgt.spawnedBy != "" {
			h.SpawnedBy = sc.tgt.spawnedBy
		}
	} else if sc.rec.v2 != nil && sc.rec.v2.ForkSessionID != "" {
		h.ParentSession = sc.rec.v2.ForkSessionID
	}
	native := ""
	if sc.tgt.id != sc.rec.id {
		native = sc.rec.id
	}
	if sc.tgt.derived != "" {
		sc.addf(agentimport.Lossy, "opencode/session-id", "", "%s", sc.tgt.derived)
	}
	if err := agentimport.SetSource(&h, agentimport.Source{Format: Source, NativeID: native}); err != nil {
		return h, err
	}
	sc.addf(agentimport.Gap, "agentimport/provenance", "", "the import is recorded in the header's %s member", agentimport.SourceMember)
	if !sc.slim {
		rec := sessionRest(p)
		if len(rec) > 0 {
			raw, err := json.Marshal(rec)
			if err != nil {
				return h, err
			}
			raw, _, err = agentimport.CleanJSON(raw)
			if err != nil {
				return h, err
			}
			if h.Extra == nil {
				h.Extra = map[string]json.RawMessage{}
			}
			h.Extra[Source+":session"] = raw
			sc.addf(agentimport.Kept, "opencode/session-record", "", "the session record's members the header has no place for are in the header's %s:session member", Source)
		}
	} else {
		sc.addf(agentimport.Dropped, "opencode/session-record", "", "the session record's members the header has no place for are left out")
	}
	return h, nil
}

// sessionMapped are the members of a session record the header maps.
var sessionMapped = map[string]bool{"id": true, "directory": true, "version": true, "time_created": true}

// sessionRest is the session record without the members the header
// maps: a storage file's members, or a row's columns, with the V2 row's
// beside the V1 copy's when both are read.
func sessionRest(s *opencode.Session) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(s.Record, &all) != nil {
		return nil
	}
	for k := range sessionMapped {
		delete(all, k)
	}
	for k, v := range all {
		if opencode.IsNull(v) {
			delete(all, k)
		}
	}
	if s.Gen == opencode.G1 {
		delete(all, "id")
	}
	return all
}

// firstTime is the time of the session's first record.
func (sc *sconv) firstTime() time.Time {
	for _, m := range sc.msgs {
		if m.Created > 0 {
			return ms(m.Created)
		}
	}
	for _, r := range sc.rows {
		if r.Created > 0 {
			return ms(r.Created)
		}
	}
	return time.Unix(0, 0).UTC()
}

// start writes the entries every session opens with: a fork's link to
// its origin, the first settings, the title and the project.
func (sc *sconv) start() error {
	p := sc.rec.primary()
	if v2 := sc.rec.v2; v2 != nil && v2.ForkSessionID != "" && sc.tgt.parent == "" {
		link := agentsession.NewLinkEntry(agentsession.RelForkOf, v2.ForkSessionID)
		link.Timestamp = ms(p.Created)
		if err := sc.member(link, "fork", v2.ForkBoundary, v2.Unit); err != nil {
			return err
		}
		id, err := sc.b.Append("", link)
		if err != nil {
			return err
		}
		sc.at = id
	}
	model, provider, agent, variant := sc.firstSettings()
	cfg := &agentsession.ConfigEntry{Model: model}
	cfg.Timestamp = ms(p.Created)
	sc.model, sc.provider, sc.agent, sc.variant = model, provider, agent, variant
	if err := sc.settingsMembers(cfg); err != nil {
		return err
	}
	id, err := sc.b.Append(sc.at, cfg)
	if err != nil {
		return err
	}
	sc.at = id
	sc.addf(agentimport.Dropped, "opencode/system-prompt", "", "OpenCode builds the system prompt and the tool list for each request and does not store them; the settings carry the model alone")
	if p.Title != "" {
		info := &agentsession.InfoEntry{Name: p.Title}
		info.Timestamp = ms(p.Created)
		if id, err = sc.b.Append(sc.at, info); err != nil {
			return err
		}
		sc.at = id
	}
	st := sc.rec.v1Store
	if st == nil {
		st = sc.rec.v2Store
	}
	if proj, ok := st.Project(p.ProjectID); ok && proj.VCS == "git" {
		env := agentsession.NewEnvEntry(p.Directory)
		env.VCS = &agentsession.VCS{System: "git"}
		env.Timestamp = ms(p.Created)
		if proj.Worktree != "" && proj.Worktree != p.Directory {
			if err := sc.b.Member(env, "worktree", proj.Worktree); err != nil {
				return err
			}
		}
		if id, err = sc.b.Append(sc.at, env); err != nil {
			return err
		}
		sc.at = id
	}
	return nil
}

// firstSettings are the settings of the session's first request: the
// first assistant message's model, else the first user message's, else
// the session's.
func (sc *sconv) firstSettings() (model, provider, agent, variant string) {
	for _, m := range sc.msgs {
		if m.Info.Role == opencode.RoleAssistant && m.Info.ModelID != "" {
			return m.Info.ModelID, m.Info.ProviderID, m.Info.Agent, m.Info.Variant
		}
	}
	for _, m := range sc.msgs {
		if m.Info.Role == opencode.RoleUser && m.Info.Model != nil {
			v := m.Info.Model.Variant
			if v == "" {
				v = m.Info.Variant
			}
			return m.Info.Model.Name(), m.Info.Model.ProviderID, m.Info.Agent, v
		}
	}
	for _, r := range sc.rows {
		if r.Err == nil && r.Type == opencode.TypeAssistant && r.Data.Model != nil {
			return r.Data.Model.Name(), r.Data.Model.ProviderID, r.Data.Agent, r.Data.Model.Variant
		}
	}
	if m := sc.rec.primary().Model; m != nil {
		return m.Name(), m.ProviderID, sc.rec.primary().Agent, m.Variant
	}
	return "", "", sc.rec.primary().Agent, ""
}

// settingsMembers sets the members of a config entry that say what
// OpenCode's settings were beyond the model: the provider, the agent and
// the variant.
func (sc *sconv) settingsMembers(cfg *agentsession.ConfigEntry) error {
	for _, m := range []struct{ name, value string }{{"provider", sc.provider}, {"agent", sc.agent}, {"variant", sc.variant}} {
		if err := sc.b.Member(cfg, m.name, m.value); err != nil {
			return err
		}
	}
	return nil
}

// settings writes a config entry when the settings of a request differ
// from those in force. The first change of model also starts leaving
// out the reasoning earlier models wrote.
func (sc *sconv) settings(ts time.Time, unit, model, provider, agent, variant string) error {
	if model == "" {
		model, provider = sc.model, sc.provider
	}
	if model == sc.model && provider == sc.provider && agent == sc.agent && variant == sc.variant {
		return nil
	}
	cfg := &agentsession.ConfigEntry{}
	if model != sc.model {
		cfg.Model = model
		if !sc.switched {
			sc.switched = true
			cfg.Omit = &agentsession.Omit{Reasoning: agentsession.OmitOtherModels}
			sc.addf(agentimport.Gap, "agentimport/context-replace", unit, "the model changes from %s to %s: OpenCode sends the reasoning an earlier model wrote without its provider's metadata, as plain text in later releases, which replaces the item in place; the session leaves it out instead, by omit other_models", sc.model, model)
		}
	}
	sc.model, sc.provider, sc.agent, sc.variant = model, provider, agent, variant
	if err := sc.settingsMembers(cfg); err != nil {
		return err
	}
	cfg.Timestamp = ts
	id, err := sc.b.Append(sc.at, cfg)
	if err != nil {
		return err
	}
	sc.at = id
	return nil
}

// append adds an entry after the current one and makes it current.
func (sc *sconv) append(e agentsession.Entry) (string, error) {
	id, err := sc.b.Append(sc.at, e)
	if err != nil {
		return "", err
	}
	sc.at = id
	return id, nil
}

// member sets a namespaced member from a native value, or drops it
// under Slim and says so. unit names the record it came from.
func (sc *sconv) member(e agentsession.Entry, name string, v any, unit string) error {
	if isEmpty(v) {
		return nil
	}
	if sc.slim {
		sc.addf(agentimport.Dropped, "opencode/members", unit, "%s is left out", name)
		return nil
	}
	sc.addf(agentimport.Kept, "opencode/members", unit, "kept in %s:%s", Source, name)
	return sc.b.Member(e, name, v)
}

// isEmpty reports a value that says nothing: nil, an empty string, an
// empty or null raw value, an empty map or slice.
func isEmpty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case json.RawMessage:
		t := bytes.TrimSpace(v)
		return len(t) == 0 || string(t) == "null" || string(t) == "{}" || string(t) == "[]"
	case map[string]json.RawMessage:
		return len(v) == 0
	case []json.RawMessage:
		return len(v) == 0
	case []string:
		return len(v) == 0
	}
	return false
}

// rest returns the members of raw, a JSON object, without those
// mapped and those that are null or empty.
func rest(raw json.RawMessage, mapped ...string) map[string]json.RawMessage {
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	for _, k := range mapped {
		delete(all, k)
	}
	for k, v := range all {
		if isEmpty(v) {
			delete(all, k)
		}
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

// custom writes a custom entry holding a native record, which the
// model never saw, or drops it under Slim; the unit is tallied either
// way.
func (sc *sconv) custom(ts time.Time, unit, what string, data any) error {
	if sc.slim {
		sc.mark(unit, agentimport.LineDropped)
		sc.addf(agentimport.Dropped, "opencode/custom", unit, "%s, which is not context, is left out", what)
		return nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	raw, _, err = agentimport.CleanJSON(raw)
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: raw}
	e.Timestamp = ts
	if _, err := sc.append(e); err != nil {
		return err
	}
	sc.mark(unit, agentimport.LineKept)
	sc.addf(agentimport.Kept, "opencode/custom", unit, "%s, which is not context, is a custom entry", what)
	return nil
}

// keepNone returns the entry a compaction that keeps nothing after its
// summary names as first_kept: the current one when it puts nothing in
// context, else a custom marker written for it.
func (sc *sconv) keepNone(ts time.Time, unit string) (string, error) {
	if e, ok := sc.b.S.Entry(sc.at); ok {
		switch e.(type) {
		case *agentsession.ItemEntry, *agentsession.CompactionEntry, *agentsession.BranchSummaryEntry:
		default:
			return sc.at, nil
		}
	}
	marker := &agentsession.CustomEntry{NS: Source, Data: json.RawMessage(`{"keeps_nothing":true}`)}
	marker.Timestamp = ts
	id, err := sc.append(marker)
	if err != nil {
		return "", err
	}
	sc.addf(agentimport.Kept, "opencode/compaction-marker", unit, "the compaction keeps nothing after its summary, so a custom marker written before it is its first_kept, which puts nothing in context")
	return id, nil
}

// checkpoint is the settings in force at the current entry, with the
// omitted items limited to those of the window a compaction keeps.
func (sc *sconv) checkpoint(window map[string]bool) (agentsession.Settings, error) {
	ctx, err := sc.b.S.ContextAt(sc.at)
	if err != nil {
		return agentsession.Settings{}, err
	}
	s := ctx.Settings
	var keep []string
	for _, id := range s.Omit.Items {
		if window[id] {
			keep = append(keep, id)
		}
	}
	sort.Strings(keep)
	s.Omit.Items = keep
	return s, nil
}

// visible is a pointer to b, for an item entry's visible member.
func visible(b bool) *bool { return &b }

// userText is a user message of one text.
func userText(text string) *openresponses.Message { return openresponses.UserText(text) }

// mistyped reports the members of a native object that held another
// JSON type than OpenCode writes, which the reader kept as written.
func (sc *sconv) mistyped(unit string, m interface{ Mistyped() []string }) {
	if bad := m.Mistyped(); len(bad) > 0 {
		sc.addf(agentimport.Repaired, "opencode/mistyped", unit, "members %s hold another JSON type than OpenCode writes; they are kept as written and read as absent", strings.Join(bad, ", "))
	}
}

// keepKind is the kind of a finding about something kept in a member or
// a custom entry: dropped under Slim.
func (sc *sconv) keepKind() agentimport.Kind {
	if sc.slim {
		return agentimport.Dropped
	}
	return agentimport.Kept
}

// keepNote reports something the model never saw that is kept in the
// member named, or left out under Slim.
func (sc *sconv) keepNote(code, unit, member, format string, args ...any) {
	what := fmt.Sprintf(format, args...)
	if sc.slim {
		sc.addf(agentimport.Dropped, code, unit, "%s; it is left out", what)
		return
	}
	sc.addf(agentimport.Kept, code, unit, "%s; it is kept in %s:%s", what, Source, member)
}
