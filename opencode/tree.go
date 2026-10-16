package opencode

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Directory names of a data directory and its storage/ tree.
const (
	DirStorage = "storage"
	DirSession = "session"
	DirMessage = "message"
	DirPart    = "part"
	DirProject = "project"
	// DatabaseName is the database OpenCode 1.2 and later keep in the
	// data directory.
	DatabaseName = "opencode.db"
)

// maxFile bounds the size of a storage file read; a larger one is
// reported and skipped.
const maxFile = 256 << 20

// Tree is the storage/ tree of a data directory, the G1 store: one JSON
// file per session, message and part. It is read on demand; a session's
// messages and parts are read when [Tree.Messages] is called. A Tree is
// not safe for concurrent use.
type Tree struct {
	// Root is the data directory.
	Root string
	// Legacy lists the directories of the layout OpenCode wrote before
	// its MessageV2 model, project/<dir>/storage/session, which this
	// package does not read.
	Legacy []string

	dir string
	// read marks the message and part directories read with a session.
	read map[string]bool
}

// OpenTree opens the storage/ tree of the data directory root.
func OpenTree(root string) (*Tree, error) {
	dir := filepath.Join(root, DirStorage)
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	t := &Tree{Root: root, dir: dir, read: map[string]bool{}}
	legacy, _ := filepath.Glob(filepath.Join(root, DirProject, "*", DirStorage, DirSession))
	for _, l := range legacy {
		if rel, err := filepath.Rel(root, l); err == nil {
			t.Legacy = append(t.Legacy, filepath.ToSlash(rel))
		}
	}
	sort.Strings(t.Legacy)
	return t, nil
}

// Name names the store in findings.
func (t *Tree) Name() string { return DirStorage }

// unit names a file of the tree in findings: its path under the data
// directory.
func (t *Tree) unit(path string) string {
	rel, err := filepath.Rel(t.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// jsonFiles lists the .json files of a directory by name; a directory
// that does not exist holds none.
func jsonFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// subdirs lists the directories of a directory by name, symbolic links
// left out.
func subdirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// readFile reads a storage file, refusing one over maxFile.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFile {
		return nil, fmt.Errorf("the file is over %d bytes", maxFile)
	}
	return data, nil
}

// Sessions lists the session files, storage/session/<project>/<id>.json.
func (t *Tree) Sessions() ([]*Session, []*Stray, error) {
	base := filepath.Join(t.dir, DirSession)
	projects, err := subdirs(base)
	if err != nil {
		return nil, nil, err
	}
	var out []*Session
	var strays []*Stray
	for _, p := range projects {
		names, err := jsonFiles(filepath.Join(base, p))
		if err != nil {
			strays = append(strays, &Stray{Unit: t.unit(filepath.Join(base, p)), Kind: "directory", Why: "the directory cannot be read: " + err.Error()})
			continue
		}
		for _, n := range names {
			path := filepath.Join(base, p, n)
			data, err := readFile(path)
			if err != nil {
				strays = append(strays, &Stray{Unit: t.unit(path), Kind: "session", Why: err.Error(), Malformed: true})
				continue
			}
			s := DecodeSessionFile(t.unit(path), data)
			if s.Err != nil {
				strays = append(strays, &Stray{Unit: s.Unit, Kind: "session", Why: s.Err.Error(), Malformed: true})
				continue
			}
			out = append(out, s)
		}
	}
	return out, strays, nil
}

// Messages reads a session's message files and each one's part files.
func (t *Tree) Messages(s *Session) ([]*MessageRecord, []*Stray, error) {
	if s.ID == "" || !safeName(s.ID) {
		return nil, nil, fmt.Errorf("session ID %q is not a file name", s.ID)
	}
	dir := filepath.Join(t.dir, DirMessage, s.ID)
	t.read[dir] = true
	names, err := jsonFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	var out []*MessageRecord
	var strays []*Stray
	for _, n := range names {
		path := filepath.Join(dir, n)
		id := strings.TrimSuffix(n, ".json")
		data, err := readFile(path)
		var m *MessageRecord
		if err != nil {
			m = &MessageRecord{Unit: t.unit(path), ID: id, Err: err}
		} else {
			m = DecodeMessage(t.unit(path), id, s.ID, 0, data)
			if m.Err == nil && legacyMessage(m.Info) {
				m.Err = errors.New("a message of the shape OpenCode wrote before its MessageV2 model, which this package does not read")
			}
			if m.Info != nil {
				m.Created, _ = m.Info.Created()
			}
		}
		parts, more := t.parts(id)
		strays = append(strays, more...)
		if m.Err != nil {
			strays = append(strays, &Stray{Unit: m.Unit, Session: s.ID, Kind: "message", Why: m.Err.Error(), Malformed: true})
			for _, p := range parts {
				strays = append(strays, &Stray{Unit: p.Unit, Session: s.ID, Kind: "part", Why: "its message cannot be read"})
			}
			continue
		}
		for _, p := range parts {
			if p.Err != nil {
				strays = append(strays, &Stray{Unit: p.Unit, Session: s.ID, Kind: "part", Why: p.Err.Error(), Malformed: true})
				continue
			}
			m.Parts = append(m.Parts, p)
		}
		out = append(out, m)
	}
	SortMessages(G1, out)
	return out, strays, nil
}

// parts reads a message's part files.
func (t *Tree) parts(messageID string) ([]*PartRecord, []*Stray) {
	if !safeName(messageID) {
		return nil, nil
	}
	dir := filepath.Join(t.dir, DirPart, messageID)
	t.read[dir] = true
	names, err := jsonFiles(dir)
	if err != nil {
		return nil, []*Stray{{Unit: t.unit(dir), Kind: "directory", Why: "the directory cannot be read: " + err.Error()}}
	}
	var out []*PartRecord
	for _, n := range names {
		path := filepath.Join(dir, n)
		id := strings.TrimSuffix(n, ".json")
		data, err := readFile(path)
		if err != nil {
			out = append(out, &PartRecord{Unit: t.unit(path), ID: id, MessageID: messageID, Err: err})
			continue
		}
		out = append(out, DecodePart(t.unit(path), id, messageID, data))
	}
	return out, nil
}

// Rows: the storage tree holds no V2 rows.
func (t *Tree) Rows(*Session) ([]*Row2, []*QueueRow, error) {
	return nil, nil, errors.New("the storage files hold no OpenCode 2 sessions")
}

// Orphans lists the message files of sessions not in the set, and the
// part files of messages no session read named.
func (t *Tree) Orphans(sessions map[string]bool) ([]*Stray, error) {
	var out []*Stray
	for _, sub := range []string{DirMessage, DirPart} {
		base := filepath.Join(t.dir, sub)
		dirs, err := subdirs(base)
		if err != nil {
			out = append(out, &Stray{Unit: t.unit(base), Kind: "directory", Why: "the directory cannot be read: " + err.Error()})
			continue
		}
		for _, d := range dirs {
			dir := filepath.Join(base, d)
			if t.read[dir] {
				continue
			}
			names, err := jsonFiles(dir)
			if err != nil {
				out = append(out, &Stray{Unit: t.unit(dir), Kind: "directory", Why: "the directory cannot be read: " + err.Error()})
				continue
			}
			kind, why := "message", "its session has no session file"
			if sub == DirPart {
				kind, why = "part", "its message was read with no session"
			} else if sessions[d] {
				why = "its session was not converted from the storage files"
			}
			for _, n := range names {
				out = append(out, &Stray{Unit: t.unit(filepath.Join(dir, n)), Kind: kind, Why: why})
			}
		}
	}
	return out, nil
}

// Project reads storage/project/<id>.json.
func (t *Tree) Project(id string) (Project, bool) {
	if !safeName(id) {
		return Project{}, false
	}
	data, err := readFile(filepath.Join(t.dir, DirProject, id+".json"))
	if err != nil {
		return Project{}, false
	}
	var p struct {
		Worktree string `json:"worktree"`
		VCS      string `json:"vcs"`
	}
	if json.Unmarshal(bytes.TrimSpace(data), &p) != nil {
		return Project{}, false
	}
	return Project{Worktree: p.Worktree, VCS: p.VCS}, true
}

// MigrationDone: the storage tree is not migrated.
func (t *Tree) MigrationDone() bool { return false }

// legacyMessage reports a message of the shape OpenCode wrote before
// MessageV2: its parts inline and its times under metadata.
func legacyMessage(m *Message) bool {
	if m == nil {
		return false
	}
	raw, ok := m.Extra["parts"]
	if !ok {
		return false
	}
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '['
}

// safeName reports whether s names one file of a directory.
func safeName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\`) && !strings.ContainsRune(s, 0)
}

// Units counts the files of a data directory's storage/ tree that hold
// a session, a message or a part: every record a conversion of the tree
// reads, so a test can hold a report's tally to it.
func Units(root string) int {
	n := 0
	for _, sub := range []string{DirSession, DirMessage, DirPart} {
		base := filepath.Join(root, DirStorage, sub)
		dirs, _ := subdirs(base)
		for _, d := range dirs {
			names, _ := jsonFiles(filepath.Join(base, d))
			n += len(names)
		}
	}
	return n
}
