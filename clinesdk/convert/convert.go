// Package convert converts the Cline SDK's session directories into the
// Agent Session Format: a session's history, rebuilt as each request
// sent it, and the histories of the sub-agents and team tasks it ran,
// linked to the calls that ran them. docs/formats/clinesdk.md is the
// mapping and lists every finding code it reports.
package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "clinesdk"

// Harness is the harness name the sessions carry.
const Harness = "cline"

// Unit names what a report's tally counts: each file of a session
// directory, and each message of each messages array.
const Unit = "files and elements"

// maxDepth bounds how deep sub-agents are followed.
const maxDepth = 16

// ClassicSource is the format the cline package's converter writes a
// classic task as, whose session the VS Code extension's migration of
// that task names.
const ClassicSource = "cline"

// Options tune a conversion. The zero value keeps everything.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: the
	// manifest, the members of messages and blocks the conversion does
	// not map, the original text of what a request rewrote, display-only
	// messages, a stale sidecar. Each drop is still reported.
	Slim bool
	// Alone converts a session without its sub-agents and team tasks,
	// which are otherwise converted with it.
	Alone bool
	// OpenIndex opens Cline's sessions.db for ConvertFile:
	// clinesdk/sqlite's OpenIndex, which this package does not import,
	// so that it builds without a database driver. When it is nil, or
	// the database is absent or cannot be read, the conversion reads
	// sessions.index.json when there is one, and otherwise the session
	// directory alone, and reports what the index would have added.
	OpenIndex func(path string) (clinesdk.Index, error)
	// Index is an index the caller opened, used by ConvertFile and
	// Convert in place of opening one; the caller closes it.
	Index clinesdk.Index
}

// ConvertFile reads the session directory path names, or the directory
// of a file in it, and converts it with its sub-agents and team tasks.
// Its index is read from beside the sessions directory, as
// clinesdk.IndexPaths names it.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	s, err := clinesdk.ReadSession(path)
	if err != nil {
		return nil, err
	}
	var notes []indexNote
	if opts.Index == nil {
		ix, n := openIndex(s.Dir, opts)
		notes = n
		if ix != nil {
			defer ix.Close()
			opts.Index = ix
		}
	}
	res, err := Convert(s, opts)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		res.Report.Add(n.finding)
	}
	return res, nil
}

// indexNote is a finding about the index, reported after the
// conversion.
type indexNote struct{ finding agentimport.Finding }

// openIndex opens the index of the session directory dir: sessions.db
// through opts.OpenIndex, else sessions.index.json.
func openIndex(dir string, opts Options) (clinesdk.Index, []indexNote) {
	dbPath, filePath := clinesdk.IndexPaths(dir)
	var notes []indexNote
	if info, err := os.Stat(dbPath); err == nil && info.Mode().IsRegular() {
		if opts.OpenIndex == nil {
			notes = append(notes, indexNote{agentimport.Finding{Code: "clinesdk/index-unread", Kind: agentimport.Lossy, File: clinesdk.DatabaseName,
				Detail: "sessions.db is not read: no database reader was given (Options.OpenIndex); the session directory is read alone"}})
		} else if ix, err := opts.OpenIndex(dbPath); err != nil {
			notes = append(notes, indexNote{agentimport.Finding{Code: "clinesdk/index-unread", Kind: agentimport.Lossy, File: clinesdk.DatabaseName,
				Detail: "sessions.db cannot be read: " + agentimport.ErrorText(err) + "; the session directory is read alone"}})
		} else {
			return ix, notes
		}
	}
	if _, err := os.Stat(filePath); err == nil {
		ix, err := clinesdk.ReadFileIndex(filePath)
		if err == nil {
			return ix, notes
		}
		notes = append(notes, indexNote{agentimport.Finding{Code: "clinesdk/index-unread", Kind: agentimport.Lossy, File: clinesdk.FileIndexName,
			Detail: clinesdk.FileIndexName + " cannot be read: " + agentimport.ErrorText(err)}})
	}
	return nil, notes
}

// Convert converts a session directory: the session, then each
// sub-agent and team task after the session that ran it. One report
// covers them all. Only the main history's failure fails the
// conversion: a child that cannot be converted is a finding.
func Convert(s *clinesdk.Session, opts Options) (*agentimport.Result, error) {
	if s == nil {
		return nil, errors.New("clinesdk: no session")
	}
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	fam := newFamily(s, opts, r)
	if err := fam.index(); err != nil {
		return nil, err
	}
	sessions, err := fam.run()
	if err != nil {
		return nil, err
	}
	return &agentimport.Result{Report: r, Sessions: sessions}, nil
}

// target is the session a history becomes.
type target struct {
	id string
	// native is the history's own session ID.
	native string
	// derived says why the session ID is not the native one, empty when
	// it is.
	derived string
	// parent and spawnedBy are set for a child's session.
	parent, spawnedBy string
	// system is the system prompt a sub-agent's spawning call gave it.
	system string
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

// ClassicSessionID is the session ID the cline converter gives a
// classic task: the task's ID when a session store takes it, else one
// derived from it.
func ClassicSessionID(task string) string {
	if storeName(task) {
		return task
	}
	key := "task/" + task
	if task == "" {
		sum := sha256.Sum256([]byte(task))
		key = "task/" + hex.EncodeToString(sum[:])
	}
	return agentsession.SubsessionID(ClassicSource, key)
}

// ownID is the session ID a history converts to under its own native
// ID: the native ID when a store takes it and no other session of the
// conversion has it, else one derived from it.
func (fam *family) ownID(native string) target {
	if storeName(native) && !fam.ids[native] {
		fam.ids[native] = true
		return target{id: native, native: native}
	}
	id := agentsession.SubsessionID(Source, "session/"+native)
	for n := 2; fam.ids[id]; n++ {
		id = agentsession.SubsessionID(Source, fmt.Sprintf("session/%s/%d", native, n))
	}
	fam.ids[id] = true
	why := "the session ID is not a name a session store takes (" + storeRule + "); the session ID is derived from it"
	if storeName(native) {
		why = "another session of this conversion has the session's ID; the session ID is derived from it"
	}
	return target{id: id, native: native, derived: why}
}

// sessionsDir is the directory holding a session directory and its
// siblings.
func sessionsDir(s *clinesdk.Session) string {
	if s.Dir == "" {
		return ""
	}
	return filepath.Dir(s.Dir)
}
