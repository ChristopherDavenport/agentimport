package openhands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// MaxSize bounds a file the reader reads. A larger file is refused
// rather than read into memory, and reported as malformed.
const MaxSize = 1 << 30

// Names in a conversation directory.
const (
	FileBaseState   = "base_state.json"
	FileMeta        = "meta.json"
	DirEvents       = "events"
	DirSubagents    = "subagents"
	DirObservations = "observations"
)

// eventName matches an event file's name, event-<index>-<event id>.json:
// five or more digits, as the SDK writes them, and an ID of hex digits
// and dashes.
var eventName = regexp.MustCompile(`^event-(\d{5,})-([0-9a-fA-F-]{8,})\.json$`)

// FileStatus is what an events/ file is to the reader.
type FileStatus string

const (
	// StatusEvent: an event in the conversation's sequence.
	StatusEvent FileStatus = "event"
	// StatusMalformed: an event file in the sequence that is empty,
	// not JSON or not an object; OpenHands skips an empty one.
	StatusMalformed FileStatus = "malformed"
	// StatusGap: an event file after a missing index, which OpenHands
	// does not read.
	StatusGap FileStatus = "gap"
	// StatusDuplicate: a second file for an index already taken;
	// OpenHands keeps one of them.
	StatusDuplicate FileStatus = "duplicate"
	// StatusBadName: a file whose name is not an event file's.
	StatusBadName FileStatus = "bad-name"
)

// File is one file of the events directory: every file whose name does
// not start with ".", which OpenHands keeps for its own sidecars.
type File struct {
	Name   string
	Index  int
	ID     string
	Status FileStatus
	// Err says why a malformed file did not read.
	Err error
}

// Conversation is one conversation directory as read.
type Conversation struct {
	// Dir is the directory, and Name its name: the conversation ID as
	// 32 hex digits.
	Dir  string
	Name string

	// State is base_state.json; StateErr says why it did not read.
	State    *State
	StateRaw json.RawMessage
	StateErr error
	HasState bool

	// Meta is the agent server's meta.json; MetaErr says why it did not
	// read.
	Meta    *Meta
	MetaRaw json.RawMessage
	MetaErr error
	HasMeta bool

	// Events are the events OpenHands reads, in index order from 0 up
	// to the first missing index. A malformed file in the sequence is
	// an Event with Err set.
	Events []*Event
	// Files are every file of the events directory, by name.
	Files []*File

	// Others lists the other files of the directory, Dirs its
	// directories, and Subagents the directories under subagents/
	// holding a conversation of their own.
	Others    []string
	Dirs      []string
	Subagents []string
}

// Units is the number of units the conversation accounts for:
// base_state.json, meta.json, each file of the events directory and
// each other file.
func (c *Conversation) Units() int {
	n := len(c.Files) + len(c.Others)
	if c.HasState {
		n++
	}
	if c.HasMeta {
		n++
	}
	return n
}

// ConversationDir returns the conversation directory a path names: the
// path itself when it is a directory, the directory of its
// base_state.json or meta.json, or the conversation of an event file.
func ConversationDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return path, nil
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) == DirEvents && eventName.MatchString(filepath.Base(path)) {
		return filepath.Dir(dir), nil
	}
	return dir, nil
}

// IsConversationDir reports whether dir holds a conversation: a
// base_state.json, or an events directory with an event file in it.
func IsConversationDir(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, FileBaseState)); err == nil && info.Mode().IsRegular() {
		return true
	}
	f, err := os.Open(filepath.Join(dir, DirEvents))
	if err != nil {
		return false
	}
	defer f.Close()
	for {
		names, err := f.Readdirnames(256)
		for _, n := range names {
			if eventName.MatchString(n) {
				return true
			}
		}
		if err != nil {
			return false
		}
	}
}

// IsConversationFile reports whether path is a file a conversation
// directory is found by: its base_state.json, or an event file in its
// events directory.
func IsConversationFile(path string) bool {
	base := filepath.Base(path)
	if base == FileBaseState {
		return true
	}
	return eventName.MatchString(base) && filepath.Base(filepath.Dir(path)) == DirEvents
}

// ReadConversation reads the conversation directory path names, or the
// conversation of the file it names. A file that cannot be read or
// parsed is recorded on the conversation, never fatal: ReadConversation
// fails only when the directory cannot be listed or holds no
// conversation.
func ReadConversation(path string) (*Conversation, error) {
	dir, err := ConversationDir(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	errs := map[string]error{}
	var others, dirs []string
	hasEvents := false
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir() && name == DirEvents:
			hasEvents = true
		case e.IsDir():
			dirs = append(dirs, name)
		case name == FileBaseState || name == FileMeta:
			data, err := readLimited(filepath.Join(dir, name))
			if err != nil {
				errs[name] = err
				continue
			}
			files[name] = data
		case strings.HasPrefix(name, "."):
		default:
			others = append(others, name)
		}
	}
	if hasEvents {
		evs, err := os.ReadDir(filepath.Join(dir, DirEvents))
		if err != nil {
			return nil, fmt.Errorf("%s: the events directory cannot be listed", dir)
		}
		for _, e := range evs {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") {
				continue
			}
			key := DirEvents + "/" + name
			if !eventName.MatchString(name) {
				files[key] = nil
				continue
			}
			data, err := readLimited(filepath.Join(dir, DirEvents, name))
			if err != nil {
				errs[key] = err
				data = nil
			}
			files[key] = data
		}
	}
	if files[FileBaseState] == nil && errs[FileBaseState] == nil && !hasEventFile(files) {
		return nil, fmt.Errorf("%s: not an OpenHands conversation directory: no %s and no event files", dir, FileBaseState)
	}
	c := parse(filepath.Base(dir), files, errs)
	c.Dir = dir
	c.Others = others
	c.Dirs = dirs
	if slices.Contains(dirs, DirSubagents) {
		c.Subagents = subagentDirs(filepath.Join(dir, DirSubagents))
	}
	return c, nil
}

func hasEventFile(files map[string][]byte) bool {
	for k := range files {
		if strings.HasPrefix(k, DirEvents+"/") && eventName.MatchString(strings.TrimPrefix(k, DirEvents+"/")) {
			return true
		}
	}
	return false
}

// subagentDirs lists the directories under subagents/ that hold a
// conversation, sorted.
func subagentDirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && IsConversationDir(filepath.Join(dir, e.Name())) {
			out = append(out, e.Name())
		}
	}
	return out
}

// readLimited reads a file of at most MaxSize bytes; the error names no
// path.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("the file cannot be opened")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, errors.New("the file cannot be read")
	}
	if len(data) > MaxSize {
		return nil, errLarge
	}
	return data, nil
}

// Parse reads a conversation from its files' contents, without touching
// the file system: base_state.json and meta.json by name, and each file
// of the events directory as "events/<name>". name is the directory's
// name.
func Parse(name string, files map[string][]byte) *Conversation {
	return parse(name, files, nil)
}

func parse(name string, files map[string][]byte, errs map[string]error) *Conversation {
	c := &Conversation{Name: name}
	if data, ok := files[FileBaseState]; ok {
		c.HasState = true
		c.StateRaw = bytes.TrimSpace(data)
		var s State
		switch {
		case !json.Valid(data):
			c.StateErr = errors.New("the file is not JSON")
		case !isObject(data) || s.UnmarshalJSON(data) != nil:
			c.StateErr = errNotObject
		default:
			c.State = &s
		}
	} else if err := errs[FileBaseState]; err != nil {
		c.HasState, c.StateErr = true, err
	}
	if data, ok := files[FileMeta]; ok {
		c.HasMeta = true
		c.MetaRaw = bytes.TrimSpace(data)
		var m Meta
		switch {
		case !json.Valid(data):
			c.MetaErr = errors.New("the file is not JSON")
		case !isObject(data) || m.UnmarshalJSON(data) != nil:
			c.MetaErr = errNotObject
		default:
			c.Meta = &m
		}
	} else if err := errs[FileMeta]; err != nil {
		c.HasMeta, c.MetaErr = true, err
	}
	var names []string
	for k := range files {
		if strings.HasPrefix(k, DirEvents+"/") {
			names = append(names, strings.TrimPrefix(k, DirEvents+"/"))
		}
	}
	for k := range errs {
		if strings.HasPrefix(k, DirEvents+"/") {
			if _, ok := files[k]; !ok {
				names = append(names, strings.TrimPrefix(k, DirEvents+"/"))
			}
		}
	}
	sort.Strings(names)
	names = slices.Compact(names)
	// byIndex holds the file each index reads, the last by name when
	// two share one, as OpenHands keeps the last its listing gives.
	byIndex := map[int]*File{}
	for _, n := range names {
		f := &File{Name: n, Index: -1}
		c.Files = append(c.Files, f)
		m := eventName.FindStringSubmatch(n)
		if m == nil {
			f.Status = StatusBadName
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil || idx > 1<<31 {
			f.Status = StatusBadName
			continue
		}
		f.Index, f.ID = idx, m[2]
		if prev := byIndex[idx]; prev != nil {
			prev.Status = StatusDuplicate
		}
		byIndex[idx] = f
		f.Status = StatusEvent
	}
	n := 0
	for byIndex[n] != nil {
		n++
	}
	for _, f := range c.Files {
		if f.Status == StatusEvent && f.Index >= n {
			f.Status = StatusGap
		}
	}
	for i := range n {
		f := byIndex[i]
		key := DirEvents + "/" + f.Name
		var e *Event
		var err error
		if rerr := errs[key]; rerr != nil {
			err = rerr
		} else {
			e, err = parseEvent(files[key])
		}
		if err != nil {
			f.Status, f.Err = StatusMalformed, err
			e = &Event{Err: err}
		}
		e.Index, e.File = i, f.Name
		if e.ID == "" {
			e.ID = f.ID
		}
		c.Events = append(c.Events, e)
	}
	return c
}

// FindSessions returns the conversation directories under root, sorted.
// root may be a directory of conversations, such as
// ~/.openhands/conversations, a directory above several, such as
// ~/.openhands, one conversation directory, or a file in one, whose
// conversation is returned. Sub-agents' conversations under subagents/
// are part of their parent's and are not listed; nor are events,
// observations and version-control directories walked. A directory that
// cannot be read is skipped.
func FindSessions(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		dir, err := ConversationDir(root)
		if err != nil {
			return nil, err
		}
		return []string{dir}, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != root {
			switch d.Name() {
			case DirEvents, DirObservations, DirSubagents, ".git", "node_modules":
				return filepath.SkipDir
			}
		}
		if IsConversationDir(p) {
			out = append(out, p)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// SubagentParent returns the conversation directory a sub-agent's
// directory, <parent>/subagents/<hex>, is under, and whether it is one.
func SubagentParent(dir string) (string, bool) {
	sub := filepath.Dir(filepath.Clean(dir))
	if filepath.Base(sub) != DirSubagents {
		return "", false
	}
	parent := filepath.Dir(sub)
	return parent, IsConversationDir(parent)
}

// EventIDs lists the event IDs the events directory of dir names, from
// the file names alone: what a fork's copied events are told by.
func EventIDs(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(filepath.Join(dir, DirEvents))
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, e := range entries {
		if m := eventName.FindStringSubmatch(e.Name()); m != nil {
			ids[m[2]] = true
		}
	}
	return ids, nil
}

// HexID is a conversation ID as its directory names it: 32 hex digits,
// no dashes, lowercase.
func HexID(id string) string { return trimDash(id) }

// StateID reads the conversation ID the base_state.json of dir holds;
// empty when it holds none.
func StateID(dir string) string {
	data, err := readLimited(filepath.Join(dir, FileBaseState))
	if err != nil {
		return ""
	}
	var s struct {
		ID json.RawMessage `json:"id"`
	}
	var id string
	if json.Unmarshal(data, &s) == nil {
		_ = json.Unmarshal(s.ID, &id)
	}
	return id
}
