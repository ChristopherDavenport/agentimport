// Package convert converts Hermes Agent's sessions into the Agent
// Session Format: every session of a state.db, each with the
// continuations its compressions started and the sub-agents it ran.
// docs/formats/hermes.md is the mapping and lists every finding code it
// reports.
package convert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "hermes"

// Harness is the harness name the sessions carry.
const Harness = "hermes"

// Unit names what a report's tally counts: a row of sessions or of
// messages.
const Unit = "rows"

// maxDepth bounds how deep sub-agents and continuations are followed.
const maxDepth = 16

// Options tune a conversion. The zero value keeps everything.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: the
	// session row, the columns of message rows the conversion does not
	// map, per-model usage. Each drop is still reported.
	Slim bool
	// Alone converts each session without its sub-agents, which are
	// otherwise converted with the session that ran them.
	Alone bool
	// OpenDatabase opens a Hermes database for ConvertFile:
	// hermes/sqlite's OpenStore, which this package does not import, so
	// that it builds without a database driver. ConvertFile refuses
	// without it.
	OpenDatabase func(path string) (hermes.Database, error)
}

// ConvertFile converts every session of the Hermes database path
// names: a state.db, or a home or profile directory holding one. The
// database is read once, from one copy, removed when the conversion
// returns.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	db, err := OpenDatabase(path, opts)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return Convert(opts, db)
}

// OpenDatabase opens the database path names through
// opts.OpenDatabase.
func OpenDatabase(path string, opts Options) (hermes.Database, error) {
	dbPath, ok := hermes.StoreOf(path)
	if !ok {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return nil, fmt.Errorf("%s holds no %s; convert each profile's, or name the database", path, hermes.DatabaseName)
		}
		return nil, fmt.Errorf("%s is not a Hermes database", path)
	}
	if opts.OpenDatabase == nil {
		return nil, fmt.Errorf("%s: no database reader; pass hermes/sqlite's OpenStore in Options.OpenDatabase", filepath.Base(dbPath))
	}
	db, err := opts.OpenDatabase(dbPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(dbPath), err)
	}
	return db, nil
}

// Convert converts every session of a store. A session that a
// compression continued is converted with its continuation after it,
// and one that ran sub-agents with theirs; every other session is
// converted on its own. One report covers them all. Only a failure to
// read the store fails the conversion: a session that cannot be
// converted is a finding.
func Convert(opts Options, st hermes.Store) (*agentimport.Result, error) {
	if st == nil {
		return nil, errors.New("hermes: no store")
	}
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	fam := &family{opts: opts, report: r, st: st, ids: map[string]bool{}, active: map[string]bool{}, done: map[string]bool{}}
	if err := fam.index(); err != nil {
		return nil, err
	}
	switch {
	case !fam.hasVersion:
		r.Addf(agentimport.Lossy, "hermes/schema-version", st.Name(), 0, "the database records no schema version; its rows are read by the columns it has")
	case fam.version > hermes.SchemaVersion:
		r.Addf(agentimport.Lossy, "hermes/schema-version", st.Name(), 0, "the database is at schema version %d, newer than %d, the one this converter was written against; its rows are read by the columns it knows, and the others are kept", fam.version, hermes.SchemaVersion)
	}
	var out []*agentsession.Session
	for _, id := range fam.order {
		rec := fam.recs[id]
		if fam.done[id] || !fam.isRoot(rec) {
			continue
		}
		out = append(out, fam.convertRoot(rec, "")...)
	}
	// A session left was not reached from a root: its parents form a
	// cycle, or it is a sub-agent left out by Alone or by the bound on
	// nesting, or of a session that failed. Each is converted on its
	// own.
	for _, id := range fam.order {
		if fam.done[id] {
			continue
		}
		rec := fam.recs[id]
		why := "its parent's conversion did not convert it"
		switch {
		case fam.opts.Alone && rec.rel != hermes.Continuation:
			why = "Alone converts each session without its sub-agents"
		case fam.depthOf(rec) >= maxDepth:
			why = fmt.Sprintf("it is nested past %d sessions", maxDepth)
		}
		out = append(out, fam.convertRoot(rec, why)...)
	}
	strays, err := st.Orphans(fam.known())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.Name(), err)
	}
	fam.strays(strays)
	return &agentimport.Result{Report: r, Sessions: out}, nil
}

// family is one conversion: the sessions of the store, by native ID.
type family struct {
	opts   Options
	report *agentimport.Report
	st     hermes.Store
	// version is the schema version the store records.
	version    int
	hasVersion bool
	recs       map[string]*record
	order      []string
	// children lists each session's children, by parent ID, in order.
	children map[string][]string
	// delegations are async_delegations' rows, by delegation ID.
	delegations map[string]*hermes.Delegation
	// ids are the session IDs given out; active the sessions being
	// converted; done those converted.
	ids    map[string]bool
	active map[string]bool
	done   map[string]bool
}

// record is one session row and what the conversion learns of it.
type record struct {
	s *hermes.Session
	// rel is the session's relation to its parent, marked whether a
	// marker or a source told it.
	rel    hermes.Relation
	marked bool
	// parent is the parent's record, when the store holds it.
	parent *record
	// noParent says the session is converted as having none.
	noParent bool
	// own is the session the record becomes under its own ID, chosen
	// before any converts, so a session that names it converted first
	// names the right one.
	own target
	// sid is the session ID the record converted to, once it has.
	sid string
	// msgs are the session's rows, once read.
	msgs   []*hermes.Message
	loaded bool
	// leaf is the entry its converted session resumes from.
	leaf string
}

// index reads the store's sessions and tells each one's relation to its
// parent.
func (fam *family) index() error {
	fam.version, fam.hasVersion = fam.st.Version()
	sessions, strays, err := fam.st.Sessions()
	if err != nil {
		return fmt.Errorf("%s: %w", fam.st.Name(), err)
	}
	fam.strays(strays)
	fam.recs = map[string]*record{}
	fam.children = map[string][]string{}
	for _, s := range sessions {
		if _, dup := fam.recs[s.ID]; dup {
			// sessions.id is the primary key; a second row of one ID is
			// a damaged table.
			fam.report.Line(agentimport.LineDropped)
			fam.report.Addf(agentimport.Dropped, "hermes/duplicate-session", s.Unit, 0, "a second row of session %s is left out", s.ID)
			continue
		}
		fam.recs[s.ID] = &record{s: s}
		fam.order = append(fam.order, s.ID)
	}
	sort.SliceStable(fam.order, func(i, j int) bool {
		a, b := fam.recs[fam.order[i]].s, fam.recs[fam.order[j]].s
		if !a.Started.Equal(b.Started) {
			return a.Started.Before(b.Started)
		}
		return a.ID < b.ID
	})
	for _, id := range fam.order {
		rec := fam.recs[id]
		rec.own = fam.ownTarget(rec)
		if p := rec.s.ParentID; p == id {
			rec.noParent = true
			fam.report.Addf(agentimport.Repaired, "hermes/parent-cycle", rec.s.Unit, 0, "the session names itself as its parent; it is converted with no parent")
		} else if p != "" {
			rec.parent = fam.recs[p]
		}
	}
	fam.breakCycles()
	for _, id := range fam.order {
		rec := fam.recs[id]
		if rec.noParent {
			rec.parent = nil
		}
		var ps *hermes.Session
		if rec.parent != nil {
			ps = rec.parent.s
		}
		rec.rel, rec.marked = hermes.Classify(rec.s, ps)
		if rec.noParent {
			rec.rel, rec.marked = hermes.Root, false
		}
		if rec.parent != nil {
			fam.children[rec.parent.s.ID] = append(fam.children[rec.parent.s.ID], id)
		}
	}
	dels, err := fam.st.Delegations()
	if err != nil {
		return fmt.Errorf("%s: %w", fam.st.Name(), err)
	}
	fam.delegations = map[string]*hermes.Delegation{}
	for _, d := range dels {
		if d.ID != "" {
			fam.delegations[d.ID] = d
		}
	}
	return nil
}

// isRoot reports whether a session is converted on its own rather than
// with its parent: one with no parent in the store, a branch, which is
// a conversation of its own that starts from a copy, and a reset, a new
// conversation on its parent's chat.
func (fam *family) isRoot(rec *record) bool {
	if rec.noParent || rec.parent == nil {
		return true
	}
	switch rec.rel {
	case hermes.Root, hermes.Branch, hermes.Reset:
		return true
	}
	return false
}

// known lists the sessions read.
func (fam *family) known() map[string]bool {
	out := map[string]bool{}
	for id := range fam.recs {
		out[id] = true
	}
	return out
}

// strays tallies rows that belong to no session.
func (fam *family) strays(strays []*hermes.Stray) {
	for _, s := range strays {
		if s.Malformed {
			fam.report.Line(agentimport.LineMalformed)
			fam.report.Addf(agentimport.Repaired, "hermes/malformed", s.Unit, 0, "a %s row that cannot be read is skipped: %s", s.Kind, s.Why)
			continue
		}
		fam.report.Line(agentimport.LineDropped)
		fam.report.Addf(agentimport.Dropped, "hermes/orphan", s.Unit, 0, "a %s row that belongs to no session it can be converted with is left out: %s", s.Kind, s.Why)
	}
}

// breakCycles breaks each cycle of parents at its member that started
// first, which is converted with no parent; the sessions below a cycle
// keep their parents.
func (fam *family) breakCycles() {
	state := map[*record]int{} // 1 on the walk, 2 done
	for _, id := range fam.order {
		start := fam.recs[id]
		var path []*record
		cur := start
		for cur != nil && state[cur] == 0 && !cur.noParent {
			state[cur] = 1
			path = append(path, cur)
			cur = cur.parent
		}
		if cur != nil && state[cur] == 1 && !cur.noParent {
			// cur is on a cycle: the members are path from cur on.
			k := 0
			for path[k] != cur {
				k++
			}
			first := cur
			for _, r := range path[k:] {
				if r.s.Started.Before(first.s.Started) || r.s.Started.Equal(first.s.Started) && r.s.ID < first.s.ID {
					first = r
				}
			}
			first.noParent = true
			fam.report.Addf(agentimport.Repaired, "hermes/parent-cycle", first.s.Unit, 0, "the session's parents form a cycle of %d sessions; it, the one that started first, is converted with no parent", len(path)-k)
		}
		for _, r := range path {
			state[r] = 2
		}
	}
}

// depthOf counts a session's ancestors in the store.
func (fam *family) depthOf(rec *record) int {
	n := 0
	seen := map[*record]bool{rec: true}
	for cur := rec.parent; cur != nil && !seen[cur]; cur = cur.parent {
		seen[cur] = true
		n++
	}
	return n
}

// convertRoot converts a session that no conversion of its parent holds,
// with its continuations and sub-agents. alone, when not empty, says why
// a child is converted on its own.
func (fam *family) convertRoot(rec *record, alone string) []*agentsession.Session {
	tgt := rec.own
	if rec.parent != nil && !rec.noParent {
		switch rec.rel {
		case hermes.Branch, hermes.Continuation, hermes.Delegate, hermes.Tool:
			tgt.parent = rec.parent.sidOrID()
		}
	} else if rec.parent == nil && !rec.noParent && rec.s.ParentID != "" && rec.rel != hermes.Reset {
		tgt.parent = rec.s.ParentID
		fam.report.Addf(agentimport.Lossy, "hermes/parent-missing", rec.s.Unit, 0, "the session's parent, %s, has no row in the database; parent_session names it", rec.s.ParentID)
	}
	if rec.rel == hermes.Reset && rec.parent != nil {
		how := "its model_config's _reset_from names the parent"
		if !rec.marked {
			how = "its parent ended " + rec.parent.s.EndReason + " on the same routing key"
		}
		fam.report.Addf(agentimport.Lossy, "hermes/reset", rec.s.Unit, 0, "the session is a reset of %s (%s): a new conversation on the same chat, carrying nothing over, so it names no parent_session; the row's parent_session_id is kept with the session row", rec.parent.s.ID, how)
	}
	if alone != "" {
		fam.report.Addf(agentimport.Lossy, "hermes/child-alone", rec.s.Unit, 0, "the session is a %s of %s converted on its own (%s): parent_session names the parent, and no link in the parent's session names this one", rec.rel, rec.s.ParentID, alone)
	}
	sc := fam.convertInto(rec, tgt, 0, fam.report)
	if sc == nil {
		return nil
	}
	return sc.sessions()
}

// target is the session a native session becomes.
type target struct {
	id string
	// derived says why the session ID is not the native one, empty when
	// it is.
	derived string
	// parent and spawnedBy are set for a child's session.
	parent, spawnedBy string
}

// ownTarget is the session a session becomes under its own ID.
func (fam *family) ownTarget(rec *record) target {
	id := rec.s.ID
	if storeName(id) && !fam.ids[id] {
		fam.ids[id] = true
		return target{id: id}
	}
	sid := agentsession.SubsessionID(Source, "session/"+id)
	for n := 2; fam.ids[sid]; n++ {
		sid = agentsession.SubsessionID(Source, fmt.Sprintf("session/%s/%d", id, n))
	}
	fam.ids[sid] = true
	why := "the session ID is not a name a session store takes (" + storeRule + "); the session ID is derived from it"
	if storeName(id) {
		why = "another session of this conversion has the session's ID; the session ID is derived from it"
	}
	return target{id: sid, derived: why}
}

// storeRule is the rule storeName keeps, as findings state it.
const storeRule = "letters, digits, '.', '_' and '-', at most 200"

// storeName reports whether a session store takes id as a session's
// name.
func storeName(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 200 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// sidOrID is the session ID a record converted to, else its native ID.
func (rec *record) sidOrID() string {
	if rec.sid != "" {
		return rec.sid
	}
	return rec.own.id
}

// messages reads a session's rows once.
func (fam *family) messages(rec *record) ([]*hermes.Message, error) {
	if rec.loaded {
		return rec.msgs, nil
	}
	msgs, err := fam.st.Messages(rec.s.ID)
	if err != nil {
		return nil, err
	}
	rec.msgs, rec.loaded = msgs, true
	return msgs, nil
}
