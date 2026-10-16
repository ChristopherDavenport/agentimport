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
// tree and its opencode.db, a file in one, or a database file. The
// database is read once, from one copy, removed when the conversion
// returns.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	st, err := OpenStores(path, opts)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	res, err := Convert(opts, st.Stores...)
	if err != nil {
		return nil, err
	}
	st.notes(res.Report)
	return res, nil
}

// Stores are the stores of one OpenCode store path: a data directory's
// storage tree and database, or a database alone.
type Stores struct {
	Root   string
	Stores []opencode.Store
	// DB is the database, when there is one.
	DB opencode.Database
}

// OpenStores opens the stores path names, its database through
// opts.OpenDatabase. Close releases them.
func OpenStores(path string, opts Options) (*Stores, error) {
	root, ok := opencode.StoreOf(path)
	if !ok {
		return nil, fmt.Errorf("%s is not an OpenCode data directory or database", path)
	}
	st := &Stores{Root: root}
	dbPath := root
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		dbPath = filepath.Join(root, opencode.DatabaseName)
		if t, err := opencode.OpenTree(root); err == nil {
			st.Stores = append(st.Stores, t)
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
		st.DB = db
		st.Stores = append(st.Stores, db)
	}
	if len(st.Stores) == 0 {
		return nil, fmt.Errorf("%s holds no OpenCode sessions", path)
	}
	return st, nil
}

// Close closes the database, removing its copy.
func (st *Stores) Close() error {
	if st.DB == nil {
		return nil
	}
	return st.DB.Close()
}

// notes reports what a data directory holds that is not converted:
// OpenCode 2's preview database, which its migration imports into the
// V2 tables, while that migration has not completed.
func (st *Stores) notes(r *agentimport.Report) {
	info, err := os.Stat(st.Root)
	if err != nil || !info.IsDir() {
		return
	}
	next := filepath.Join(st.Root, "opencode-next.db")
	if _, err := os.Stat(next); err != nil {
		return
	}
	if st.DB != nil && st.DB.MigrationDone() {
		return
	}
	r.Addf(agentimport.Dropped, "opencode/next-db", "opencode-next.db", 0, "OpenCode 2's preview database is not read; OpenCode 2's migration imports its sessions into the V2 tables, and has not run here")
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
		if rec.skipped {
			continue
		}
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

// family is one conversion: the sessions of the stores, by key.
type family struct {
	opts   Options
	report *agentimport.Report
	stores []opencode.Store
	// recs holds every session's copies, by key: the native ID, or,
	// for OpenCode 2's continuation of a migrated session, twinKey of
	// it; order lists the keys.
	recs  map[string]*record
	order []string
	// children lists each session's children, by parent key.
	children map[string][]string
	// ids are the session IDs given out; active the sessions being
	// converted; done those converted.
	ids    map[string]bool
	active map[string]bool
	done   map[string]bool
}

// record is one session's copies.
type record struct {
	// key is the record's in recs; id the native session ID.
	key, id string
	// twinOf, on the record of OpenCode 2's rows of a session OpenCode
	// 2 migrated from the V1 tables, is the V1 record, which converts
	// as a session of its own.
	twinOf *record
	// sid is the session ID the record converted to, once it has.
	sid string
	// msgIDs are the IDs of a V1 record's message rows read, malformed
	// ones included.
	msgIDs map[string]bool
	// skipped says the record converted to no session: a migration's
	// copy OpenCode 2 never carried on.
	skipped bool
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

// twinKey is the key of OpenCode 2's continuation of the session id.
func twinKey(id string) string { return "opencode2/" + id }

// parentKey is the key of a record's parent: the V1 record of a V1
// session's parent, the OpenCode 2 record of a V2 session's.
func (fam *family) parentKey(rec *record) string {
	if rec.noParent {
		return ""
	}
	p := rec.primary().ParentID
	if p == "" {
		return ""
	}
	if rec.v1 == nil {
		if _, ok := fam.recs[twinKey(p)]; ok {
			return twinKey(p)
		}
	}
	return p
}

// parentSID is the session ID of a record's parent: the one it
// converted to, else its native ID.
func (fam *family) parentSID(rec *record) string {
	k := fam.parentKey(rec)
	if p := fam.recs[k]; p != nil && p.sid != "" {
		return p.sid
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
				rec = &record{key: s.ID, id: s.ID}
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
	// A session in the V1 tables and the V2 tables both is two: the V1
	// record, and OpenCode 2's, which starts from its migration's copy
	// of the V1 record and carries it on.
	for _, id := range append([]string(nil), fam.order...) {
		rec := fam.recs[id]
		if rec.v1 == nil || rec.v2 == nil {
			continue
		}
		twin := &record{key: twinKey(id), id: id, v2: rec.v2, v2Store: rec.v2Store, twinOf: rec}
		rec.v2, rec.v2Store = nil, nil
		fam.recs[twin.key] = twin
		fam.order = append(fam.order, twin.key)
	}
	sort.SliceStable(fam.order, func(i, j int) bool {
		a, b := fam.recs[fam.order[i]].primary(), fam.recs[fam.order[j]].primary()
		if a.Created != b.Created {
			return a.Created < b.Created
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		// A V1 record converts before OpenCode 2's carrying it on.
		return fam.recs[fam.order[i]].twinOf == nil && fam.recs[fam.order[j]].twinOf != nil
	})
	for _, k := range fam.order {
		if p := fam.parentKey(fam.recs[k]); p != "" && fam.recs[p] != nil && p != k {
			fam.children[p] = append(fam.children[p], k)
		}
	}
	return nil
}

// roots lists the sessions no session in the stores is the parent of.
func (fam *family) roots() []string {
	var out []string
	for _, k := range fam.order {
		p := fam.parentKey(fam.recs[k])
		if p == "" || p == k || fam.recs[p] == nil {
			out = append(out, k)
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
		case s.Kind == "table":
			fam.report.Addf(agentimport.Dropped, "opencode/v1-projection", s.Unit, 0, "%s", s.Why)
		case s.Malformed:
			fam.report.Line(agentimport.LineMalformed)
			fam.report.Addf(agentimport.Repaired, "opencode/malformed", s.Unit, 0, "a %s record that cannot be read is skipped: %s", s.Kind, strayWhy(s))
		default:
			fam.report.Line(agentimport.LineDropped)
			fam.report.Addf(agentimport.Dropped, "opencode/orphan", s.Unit, 0, "a %s record that belongs to no session it can be converted with is left out: %s", s.Kind, s.Why)
		}
	}
}

// orphans tallies the records of every store that no session read.
func (fam *family) orphans() error {
	known := map[string]bool{}
	for _, rec := range fam.recs {
		known[rec.id] = true
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
	if p := fam.parentKey(rec); p != "" && p != id {
		tgt.parent = fam.parentSID(rec)
	}
	if alone {
		why := "its parent's conversion did not convert it"
		switch {
		case fam.opts.Alone:
			why = "Alone converts each session without its sub-agents"
		case fam.depthOf(id) >= maxDepth:
			why = fmt.Sprintf("it is nested past %d sessions", maxDepth)
		}
		rec.addf(fam.report, agentimport.Lossy, "opencode/subagent-alone", "the session is a sub-agent of %s converted on its own (%s): parent_session names the parent, and no link in the parent's session names this one", rec.primary().ParentID, why)
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
		cur = fam.parentKey(rec)
	}
	return false
}

// depthOf counts a session's ancestors in the stores.
func (fam *family) depthOf(id string) int {
	n := 0
	seen := map[string]bool{id: true}
	for cur := fam.parentKey(fam.recs[id]); cur != "" && fam.recs[cur] != nil && !seen[cur]; cur = fam.parentKey(fam.recs[cur]) {
		seen[cur] = true
		n++
	}
	return n
}

// ownTarget is the session a session becomes under its own ID.
func (fam *family) ownTarget(rec *record) target {
	if rec.twinOf != nil {
		id := agentsession.SubsessionID(rec.id, "opencode2")
		for n := 2; fam.ids[id]; n++ {
			id = agentsession.SubsessionID(rec.id, fmt.Sprintf("opencode2/%d", n))
		}
		fam.ids[id] = true
		return target{id: id, derived: "OpenCode 2 carried on a session it migrated from the V1 tables, which converts under the native ID; this one's ID is derived from it, as SubsessionID(native ID, \"opencode2\")"}
	}
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
