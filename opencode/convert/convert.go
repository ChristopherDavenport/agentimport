// Package convert converts OpenCode's sessions into the Agent Session
// Format: a data directory's storage files, its database's V1 tables
// and its V2 tables, each session converted once, from the copy that
// holds the most of what its model saw. docs/formats/opencode.md is the
// mapping and lists every finding code it reports.
package convert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "opencode"

// Harness is the harness name the sessions carry.
const Harness = "opencode"

// Unit names what a report's tally counts: a storage file or a database
// row, each a session, a message, a part or a V2 message.
const Unit = "records"

// maxDepth bounds how deep sub-agents are followed.
const maxDepth = 16

// Options tune a conversion. The zero value keeps everything.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: the
	// session record, the members of messages and parts the conversion
	// does not map, snapshots and patches, OpenCode 2's queued input.
	// Each drop is still reported.
	Slim bool
	// Alone converts each session without its sub-agents, which are
	// otherwise converted with the session that ran them.
	Alone bool
	// OpenDatabase opens an OpenCode database for ConvertFile:
	// opencode/sqlite's OpenStore, which this package does not import,
	// so that it builds without a database driver. ConvertFile refuses
	// a store holding a database when it is nil.
	OpenDatabase func(path string) (opencode.Database, error)
}

// ConvertFile converts every session of the OpenCode store path names:
// a data directory, such as ~/.local/share/opencode, with its storage/
// tree and its opencode.db, a file in one, or a database file.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	root, ok := opencode.StoreOf(path)
	if !ok {
		return nil, fmt.Errorf("%s is not an OpenCode data directory or database", path)
	}
	var stores []opencode.Store
	var closers []func() error
	defer func() {
		for _, c := range closers {
			c()
		}
	}()
	dbPath := root
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		dbPath = filepath.Join(root, opencode.DatabaseName)
		if t, err := opencode.OpenTree(root); err == nil {
			stores = append(stores, t)
		}
	}
	if info, err := os.Stat(dbPath); err == nil && info.Mode().IsRegular() {
		if opts.OpenDatabase == nil {
			return nil, fmt.Errorf("%s: no database reader; pass opencode/sqlite's OpenStore in Options.OpenDatabase", filepath.Base(dbPath))
		}
		db, err := opts.OpenDatabase(dbPath)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(dbPath), err)
		}
		closers = append(closers, db.Close)
		stores = append(stores, db)
	}
	if len(stores) == 0 {
		return nil, fmt.Errorf("%s holds no OpenCode sessions", path)
	}
	return Convert(opts, stores...)
}

// Convert converts every session the stores hold: a data directory's
// storage tree and its database, or either alone. A session held in
// more than one copy is converted from one, and each other copy is
// reported. Each session that is no sub-agent of another is converted
// with its sub-agents after it. One report covers them all. Only a
// failure to read a store fails the conversion: a session that cannot
// be converted is a finding.
func Convert(opts Options, stores ...opencode.Store) (*agentimport.Result, error) {
	if len(stores) == 0 {
		return nil, errors.New("opencode: no store")
	}
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	fam := &family{opts: opts, report: r, ids: map[string]bool{}, active: map[string]bool{}, done: map[string]bool{}}
	if err := fam.index(stores); err != nil {
		return nil, err
	}
	var out []*agentsession.Session
	for _, id := range fam.roots() {
		out = append(out, fam.convertRoot(id, false)...)
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
		if fam.cycle(id) {
			rec.addf(r, agentimport.Repaired, "opencode/parent-cycle", "the session's parents form a cycle; it is converted with no parent")
			rec.noParent = true
			out = append(out, fam.convertRoot(id, false)...)
			continue
		}
		out = append(out, fam.convertRoot(id, true)...)
	}
	if err := fam.orphans(); err != nil {
		return nil, err
	}
	return &agentimport.Result{Report: r, Sessions: out}, nil
}

// family is one conversion: the sessions of the stores, by ID.
type family struct {
	opts   Options
	report *agentimport.Report
	stores []opencode.Store
	// recs holds every session's copies, by ID; order lists the IDs.
	recs  map[string]*record
	order []string
	// children lists each session's children, by parent ID.
	children map[string][]string
	// ids are the session IDs given out; active the sessions being
	// converted; done those converted.
	ids    map[string]bool
	active map[string]bool
	done   map[string]bool
}

// record is one session's copies.
type record struct {
	id string
	// v1 is the copy converted as the V1 model: the database's V1 row,
	// else the storage file; v1Store is its store. v2 is the V2 row.
	v1, v2           *opencode.Session
	v1Store, v2Store opencode.Store
	// extra are the copies passed over: a storage file the database
	// holds again, a second file of one ID.
	extra []copyOf
	// noParent says the session is converted as having no parent.
	noParent bool
}

type copyOf struct {
	s     *opencode.Session
	store opencode.Store
}

// primary is the copy the session's own members are read from.
func (rec *record) primary() *opencode.Session {
	if rec.v1 != nil {
		return rec.v1
	}
	return rec.v2
}

func (rec *record) parentID() string {
	if rec.noParent {
		return ""
	}
	return rec.primary().ParentID
}

func (rec *record) addf(r *agentimport.Report, kind agentimport.Kind, code, format string, args ...any) {
	r.Addf(kind, code, rec.primary().Unit, 0, format, args...)
}

// index reads every store's sessions and chooses, per ID, the copies to
// convert: of the V1 model, the database's row over a storage file,
// which a database OpenCode 1.2 imported from the files holds again and
// then carries on; and the V2 row beside it, which a session OpenCode 2
// migrated holds as a lossy copy and may carry on.
func (fam *family) index(stores []opencode.Store) error {
	fam.stores = stores
	fam.recs = map[string]*record{}
	fam.children = map[string][]string{}
	for _, st := range stores {
		sessions, strays, err := st.Sessions()
		if err != nil {
			return fmt.Errorf("%s: %w", st.Name(), err)
		}
		fam.strays(strays)
		if t, ok := st.(*opencode.Tree); ok {
			for _, l := range t.Legacy {
				fam.report.Addf(agentimport.Dropped, "opencode/legacy-layout", l, 0, "a directory of the layout OpenCode wrote before its MessageV2 model, which this converter does not read; OpenCode 1.1 moved such sessions to storage/ when it started")
			}
		}
		for _, s := range sessions {
			rec := fam.recs[s.ID]
			if rec == nil {
				rec = &record{id: s.ID}
				fam.recs[s.ID] = rec
				fam.order = append(fam.order, s.ID)
			}
			switch {
			case s.Gen == opencode.G3 && rec.v2 == nil:
				rec.v2, rec.v2Store = s, st
			case s.Gen == opencode.G2 && (rec.v1 == nil || rec.v1.Gen == opencode.G1):
				if rec.v1 != nil {
					rec.extra = append(rec.extra, copyOf{rec.v1, rec.v1Store})
				}
				rec.v1, rec.v1Store = s, st
			case s.Gen == opencode.G1 && rec.v1 == nil:
				rec.v1, rec.v1Store = s, st
			default:
				rec.extra = append(rec.extra, copyOf{s, st})
			}
		}
	}
	sort.SliceStable(fam.order, func(i, j int) bool {
		a, b := fam.recs[fam.order[i]].primary(), fam.recs[fam.order[j]].primary()
		if a.Created != b.Created {
			return a.Created < b.Created
		}
		return a.ID < b.ID
	})
	for _, id := range fam.order {
		if p := fam.recs[id].parentID(); p != "" && fam.recs[p] != nil && p != id {
			fam.children[p] = append(fam.children[p], id)
		}
	}
	return nil
}

// roots lists the sessions no session in the stores is the parent of.
func (fam *family) roots() []string {
	var out []string
	for _, id := range fam.order {
		p := fam.recs[id].parentID()
		if p == "" || p == id || fam.recs[p] == nil {
			out = append(out, id)
		}
	}
	return out
}

// strays tallies records that belong to no session.
func (fam *family) strays(strays []*opencode.Stray) {
	for _, s := range strays {
		switch {
		case s.Kind == "directory":
			fam.report.Addf(agentimport.Dropped, "opencode/unreadable", s.Unit, 0, "%s", s.Why)
		case s.Malformed:
			fam.report.Line(agentimport.LineMalformed)
			fam.report.Addf(agentimport.Repaired, "opencode/malformed", s.Unit, 0, "a %s record that cannot be read is skipped: %s", s.Kind, s.Why)
		default:
			fam.report.Line(agentimport.LineDropped)
			fam.report.Addf(agentimport.Dropped, "opencode/orphan", s.Unit, 0, "a %s record that belongs to no session it can be converted with is left out: %s", s.Kind, s.Why)
		}
	}
}

// orphans tallies the records of every store that no session read.
func (fam *family) orphans() error {
	known := map[string]bool{}
	for id := range fam.recs {
		known[id] = true
	}
	for _, st := range fam.stores {
		strays, err := st.Orphans(known)
		if err != nil {
			return fmt.Errorf("%s: %w", st.Name(), err)
		}
		fam.strays(strays)
	}
	return nil
}

// target is the session a native session becomes.
type target struct {
	id string
	// derived says why the session ID is not the native one, empty when
	// it is.
	derived string
	// parent and spawnedBy are set for a sub-agent's session.
	parent, spawnedBy string
	// peer says the session is a peer of the calls that reached it.
	peer bool
}

// convertRoot converts a session that no conversion of its parent
// holds, and its sub-agents: one with no parent in the stores, or, when
// alone is set, a sub-agent converted on its own.
func (fam *family) convertRoot(id string, alone bool) []*agentsession.Session {
	rec := fam.recs[id]
	tgt := fam.ownTarget(rec)
	if p := rec.parentID(); p != "" && p != id {
		tgt.parent = p
	}
	if alone {
		why := "its parent's conversion did not convert it"
		switch {
		case fam.opts.Alone:
			why = "Alone converts each session without its sub-agents"
		case fam.depthOf(id) >= maxDepth:
			why = fmt.Sprintf("it is nested past %d sessions", maxDepth)
		}
		rec.addf(fam.report, agentimport.Lossy, "opencode/subagent-alone", "the session is a sub-agent of %s converted on its own (%s): parent_session names the parent, and no link in the parent's session names this one", rec.parentID(), why)
	}
	sc := fam.convertInto(rec, tgt, 0, fam.report)
	if sc == nil {
		return nil
	}
	return sc.sessions()
}

// cycle reports whether a session's parents lead back to one of them.
func (fam *family) cycle(id string) bool {
	seen := map[string]bool{}
	for cur := id; cur != ""; {
		if seen[cur] {
			return true
		}
		seen[cur] = true
		rec := fam.recs[cur]
		if rec == nil {
			return false
		}
		cur = rec.parentID()
	}
	return false
}

// depthOf counts a session's ancestors in the stores.
func (fam *family) depthOf(id string) int {
	n := 0
	seen := map[string]bool{id: true}
	for cur := fam.recs[id].parentID(); cur != "" && fam.recs[cur] != nil && !seen[cur]; cur = fam.recs[cur].parentID() {
		seen[cur] = true
		n++
	}
	return n
}

// ownTarget is the session a session becomes under its own ID.
func (fam *family) ownTarget(rec *record) target {
	if storeName(rec.id) && !fam.ids[rec.id] {
		fam.ids[rec.id] = true
		return target{id: rec.id}
	}
	id := agentsession.SubsessionID(Source, "session/"+rec.id)
	for n := 2; fam.ids[id]; n++ {
		id = agentsession.SubsessionID(Source, fmt.Sprintf("session/%s/%d", rec.id, n))
	}
	fam.ids[id] = true
	why := "the session ID is not a name a session store takes (" + storeRule + "); the session ID is derived from it"
	if storeName(rec.id) {
		why = "another session of this conversion has the session's ID; the session ID is derived from it"
	}
	return target{id: id, derived: why}
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
