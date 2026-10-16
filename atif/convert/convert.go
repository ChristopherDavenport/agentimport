// Package convert converts ATIF trajectories into the Agent Session
// Format. docs/formats/atif.md is the mapping and lists every finding
// code it reports.
package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "atif"

// Unit names what a report's tally counts: an ATIF document is one JSON
// value, not lines, so each document's root and each of its steps is a
// unit.
const Unit = "roots and steps"

// Options tune a conversion. The zero value keeps everything and reads
// no file beside the document.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: the
	// extra objects, members the conversion does not map, notes, final
	// metrics, token ids and logprobs. Each drop is still reported.
	// Custom entries are kept.
	Slim bool
	// CreatedAt is the session's created_at when a document holds no
	// timestamp at all. Zero leaves it the epoch, and either is
	// reported. ConvertFile sets it to the file's modification time.
	CreatedAt time.Time
	// Dir is the directory the files a document names are read from:
	// images, continuations and sub-agent files, by a path relative to
	// the document naming them, or an absolute one. A file outside Dir,
	// once symbolic links are resolved, is not read. Empty reads no
	// file. ConvertFile sets it to the document's directory.
	Dir string
	// Converted, when set, records the files each conversion converted,
	// so a caller converting many inputs can skip a related file another
	// input's conversion already holds.
	Converted *Converted
}

// Converted is the set of files conversions have converted, by their
// real path. It is safe for concurrent use.
type Converted struct {
	mu    sync.Mutex
	files map[string]bool
}

// Has reports whether a conversion converted the file at path.
func (c *Converted) Has(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.files[realPath(path)]
}

func (c *Converted) add(paths []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.files == nil {
		c.files = map[string]bool{}
	}
	for _, p := range paths {
		c.files[p] = true
	}
}

// ConvertFile reads the document at path and converts it, with the
// continuations and sub-agent files it names that its directory holds.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	f, err := atif.ReadFile(path)
	if err != nil {
		return nil, err
	}
	mtime := false
	if opts.CreatedAt.IsZero() {
		if info, err := os.Stat(path); err == nil {
			opts.CreatedAt = info.ModTime().UTC()
			mtime = true
		}
	}
	if opts.Dir == "" {
		opts.Dir = filepath.Dir(path)
	}
	fam := newFamily(opts)
	fam.mtime = mtime
	return fam.run(f, path)
}

// Convert converts a document into a session, and each sub-agent and
// continuation it names that can be resolved into one of its own: the
// main session first, every other after the session that spawned or
// preceded it. One report covers them all; a finding in another
// document names it. Only the main document's failure fails the
// conversion: a sub-agent or continuation that cannot be read or
// converted is a finding.
func Convert(f *atif.File, opts Options) (*agentimport.Result, error) {
	return newFamily(opts).run(f, "")
}

// family converts a document and the documents it names.
type family struct {
	opts   Options
	report *agentimport.Report
	// root is Opts.Dir with its links resolved; empty reads nothing.
	root  string
	mtime bool
	// ids are the session IDs the conversion has given out.
	ids map[string]bool
	// files maps a real path to its read document; done marks the files
	// and embedded trajectories converted or being converted, so each
	// converts once and a cycle ends.
	files    map[string]*atif.File
	fileErrs map[string]error
	done     map[string]bool
	embedded map[*atif.Trajectory]bool
	// held lists the files the conversion converted; taken those it
	// began to convert, in order.
	held, taken []string
	// active holds the documents being converted, by docInput.key, and
	// depth how many are below the main one.
	active map[string]bool
	depth  int
}

func newFamily(opts Options) *family {
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	fam := &family{opts: opts, report: r, ids: map[string]bool{},
		files: map[string]*atif.File{}, fileErrs: map[string]error{}, done: map[string]bool{},
		embedded: map[*atif.Trajectory]bool{}, active: map[string]bool{}}
	if opts.Dir != "" {
		fam.root = realPath(opts.Dir)
	}
	return fam
}

func (fam *family) run(f *atif.File, path string) (*agentimport.Result, error) {
	if f == nil || f.Doc == nil {
		return nil, atif.ErrNotDocument
	}
	if path == "" && fam.root != "" {
		// Convert of a file read from disk: a reference to it is to
		// itself.
		path = f.Path
	}
	if path != "" {
		real := realPath(path)
		fam.files[real] = f
		fam.done[real] = true
		fam.held = append(fam.held, real)
	}
	in := docInput{t: f.Doc, file: f, dir: fam.opts.Dir}
	fam.active[in.key()] = true
	id, native, derived := fam.mainID(f.Doc)
	sessions, _, err := fam.one(in, target{id: id, native: native, derived: derived}, fam.report)
	if err != nil {
		return nil, err
	}
	if fam.opts.Converted != nil {
		fam.opts.Converted.add(fam.held)
	}
	return &agentimport.Result{Report: fam.report, Sessions: sessions}, nil
}

// take marks a file as converting, so it converts once.
func (fam *family) take(real string) {
	fam.done[real] = true
	fam.taken = append(fam.taken, real)
}

// mainID is the main session's ID: the document's session_id, or its
// trajectory_id, or one derived from its content. A native ID a session
// store refuses is replaced by one derived from it.
func (fam *family) mainID(t *atif.Trajectory) (id, native, derived string) {
	switch {
	case t.SessionID != "" && storeName(t.SessionID):
		id = t.SessionID
	case t.SessionID != "":
		id = agentsession.SubsessionID(Source, "session/"+t.SessionID)
		derived = "the document's session_id is not a name a session store takes (" + storeRule + "); the session ID is derived from it, and it is " + Source + ":session_id"
	case t.TrajectoryID != "" && storeName(t.TrajectoryID):
		id, derived = t.TrajectoryID, "the document has no session_id; the session ID is its trajectory_id"
	case t.TrajectoryID != "":
		id = agentsession.SubsessionID(Source, "trajectory/"+t.TrajectoryID)
		derived = "the document has no session_id, and its trajectory_id is not a name a session store takes (" + storeRule + "); the session ID is derived from the trajectory_id, which is " + Source + ":trajectory_id"
	default:
		raw, _ := json.Marshal(t)
		sum := sha256.Sum256(raw)
		id = agentsession.SubsessionID(Source, "document/"+hex.EncodeToString(sum[:]))
		derived = "the document has neither session_id nor trajectory_id; the session ID is derived from its content"
	}
	fam.ids[id] = true
	return id, t.SessionID, derived
}

// claim returns a session ID for a document: its own session_id when
// a session store takes it and no other session of the conversion has
// it, else one derived from the parent's ID and key. derived explains a
// derived ID; empty when the native one was taken.
func (fam *family) claim(native, parent, key string) (id, derived string) {
	if native != "" && !fam.ids[native] && storeName(native) {
		fam.ids[native] = true
		return native, ""
	}
	base := parent
	if base == "" {
		base = Source
	}
	id = agentsession.SubsessionID(base, key)
	for n := 2; fam.ids[id]; n++ {
		id = agentsession.SubsessionID(base, fmt.Sprintf("%s/%d", key, n))
	}
	fam.ids[id] = true
	switch {
	case native == "":
		return id, "the document has no session_id; the session ID is derived from its parent's and " + key
	case !storeName(native):
		return id, "the document's session_id is not a name a session store takes (" + storeRule + "); the session ID is derived from its parent's and " + key + ", and it is " + Source + ":session_id"
	}
	return id, fmt.Sprintf("the document's session_id %s is another session's of this conversion; the session ID is derived from its parent's and %s", native, key)
}

// storeRule is the rule storeName keeps, as findings state it.
const storeRule = "letters, digits, '.', '_' and '-', at most 200"

// storeName reports whether a session store takes id as a session's
// name: agentsession's jsonl and cas stores refuse any other, so a
// native ID that breaks the rule would convert and then fail to be
// written.
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

// errOutside is why a file outside the conversion's directory is not
// read.
var errOutside = errors.New("outside the directory the conversion reads")

// errNoDir is why no file is read by Convert without Options.Dir.
var errNoDir = errors.New("the conversion reads no file (Options.Dir is empty)")

// errURL is why a file named by a URL is not read.
var errURL = errors.New("a URL, which the converter does not fetch")

// resolve turns a path a document in dir names into a real path the
// conversion may read: inside the conversion's directory once links are
// resolved.
func (fam *family) resolve(dir, p string) (string, error) {
	if strings.Contains(p, "://") {
		return "", errURL
	}
	if fam.root == "" {
		return "", errNoDir
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	real := realPath(p)
	rel, err := filepath.Rel(fam.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errOutside
	}
	return real, nil
}

// readDoc reads a related document once, by its real path. Its errors
// name no path, so a finding reads the same wherever the files are.
func (fam *family) readDoc(real string) (*atif.File, error) {
	if f, ok := fam.files[real]; ok {
		return f, nil
	}
	if err, ok := fam.fileErrs[real]; ok {
		return nil, err
	}
	f, err := readFile(real)
	if err != nil {
		fam.fileErrs[real] = err
		return nil, err
	}
	fam.files[real] = f
	return f, nil
}

// readFile is atif.ReadFile with errors that name no path.
func readFile(path string) (*atif.File, error) {
	fd, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("no such file")
		}
		return nil, errors.New("the file cannot be opened")
	}
	defer fd.Close()
	f, err := atif.Read(fd)
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			// A read error names the path, a directory's among them.
			return nil, errors.New("the file cannot be read")
		}
		return nil, err
	}
	f.Path = path
	return f, nil
}

// display names a file in findings: relative to the conversion's
// directory.
func (fam *family) display(real string) string {
	if fam.root != "" {
		if rel, err := filepath.Rel(fam.root, real); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return real
}

// realPath is p made absolute with its symbolic links resolved, or p
// cleaned when it does not exist.
func realPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// Related lists the files the document at path names that exist, at
// any depth through the documents they name, as real paths: what
// ConvertFile would convert with it, those inside its directory. It
// reads each file once and stops at a cycle.
func Related(path string) []string {
	root := realPath(filepath.Dir(path))
	seen := map[string]bool{realPath(path): true}
	var out []string
	queue := []string{path}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		f, err := atif.ReadFile(p)
		if err != nil || f.Doc == nil {
			continue
		}
		for _, n := range atif.Names(f.Doc, filepath.Dir(p)) {
			real := realPath(n)
			rel, err := filepath.Rel(root, real)
			if seen[real] || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			if _, err := os.Stat(real); err != nil {
				continue
			}
			seen[real] = true
			out = append(out, real)
			queue = append(queue, real)
		}
	}
	return out
}
