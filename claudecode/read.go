package claudecode

import (
	"bufio"
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

// MaxLineSize bounds one line. A longer line is reported as malformed
// and skipped rather than read into memory.
const MaxLineSize = 256 << 20

// Transcript is one JSONL file: a session's main transcript or one
// sub-agent's.
type Transcript struct {
	// Path is the file the transcript was read from; empty when it was
	// read from a reader.
	Path  string
	Lines []*Line
	// Errors lists the lines that could not be read, which the reader
	// skipped.
	Errors []LineError
	// NULLines lists the lines that held NUL bytes, which the reader
	// removed before decoding. Claude Code leaves runs of them where a
	// write was torn.
	NULLines []int
}

// LineError is a line the reader could not read.
type LineError struct {
	No  int
	Err error
}

func (e LineError) Error() string { return fmt.Sprintf("line %d: %v", e.No, e.Err) }

// Read reads a transcript. A line that is not JSON is recorded in
// Errors and skipped, wherever it is in the file: Claude Code files hold
// torn lines mid-file, not only at the end. A line that is JSON but not
// of the shapes this package declares is kept with Err set. Read fails
// only when r does.
func Read(r io.Reader) (*Transcript, error) {
	t := &Transcript{}
	br := bufio.NewReaderSize(r, 1<<20)
	for no := 1; ; no++ {
		raw, tooLong, err := readLine(br)
		// A line ends at a newline; the bytes after the last one are a
		// line only when there are any.
		if len(raw) > 0 || tooLong || err == nil {
			t.add(no, raw, tooLong)
		}
		if err == io.EOF {
			return t, nil
		}
		if err != nil {
			return t, err
		}
	}
}

// readLine reads one line without its newline. A line over MaxLineSize
// is consumed and reported as too long, with no bytes.
func readLine(br *bufio.Reader) ([]byte, bool, error) {
	var buf []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(buf)+len(frag) > MaxLineSize {
			buf = nil
			for err == bufio.ErrBufferFull {
				_, err = br.ReadSlice('\n')
			}
			return nil, true, err
		}
		buf = append(buf, frag...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), false, err
	}
}

func (t *Transcript) add(no int, raw []byte, tooLong bool) {
	if tooLong {
		t.Errors = append(t.Errors, LineError{No: no, Err: fmt.Errorf("longer than %d bytes", MaxLineSize)})
		return
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		raw = bytes.ReplaceAll(raw, []byte{0}, nil)
		t.NULLines = append(t.NULLines, no)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		// A line of NULs alone, or blank: nothing to read. It is still
		// a line of the file, and the caller accounts for it.
		t.Errors = append(t.Errors, LineError{No: no, Err: errors.New("empty line")})
		return
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Errors = append(t.Errors, LineError{No: no, Err: err})
		return
	}
	l := &Line{}
	if err := json.Unmarshal(raw, l); err != nil {
		l = envelope(raw, err)
	}
	l.No = no
	l.Raw = json.RawMessage(bytes.Clone(raw))
	t.Lines = append(t.Lines, l)
}

// envelope reads what it can of a line that does not decode: its
// string envelope members, so it keeps its place in the tree.
func envelope(raw []byte, err error) *Line {
	l := &Line{Err: err}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return l
	}
	for name, dst := range map[string]*string{
		"type": &l.Type, "uuid": &l.UUID, "parentUuid": &l.ParentUUID,
		"logicalParentUuid": &l.LogicalParentUUID, "timestamp": &l.Timestamp,
		"cwd": &l.CWD, "gitBranch": &l.GitBranch, "version": &l.Version,
		"sessionId": &l.SessionID, "agentId": &l.AgentID, "subtype": &l.Subtype,
	} {
		_ = json.Unmarshal(m[name], dst)
	}
	return l
}

// LineCount is the number of lines the file had: those read and those
// that could not be.
func (t *Transcript) LineCount() int { return len(t.Lines) + len(t.Errors) }

// ReadFile reads a transcript from a file.
func ReadFile(path string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	t, err := Read(f)
	if t != nil {
		t.Path = path
	}
	return t, err
}

// Session is a main transcript and the sub-agent transcripts beside it.
type Session struct {
	// ID is the session's ID: the main transcript's file name.
	ID     string
	Main   *Transcript
	Agents []*Agent
}

// Agent is one sub-agent: its transcript and the meta file beside it.
// Either may be missing; an agent that never wrote a line has a meta
// file alone.
type Agent struct {
	// ID is the agentId, from the file name agent-<agentId>.jsonl.
	ID         string
	Meta       *AgentMeta
	Transcript *Transcript
	// MetaRaw and MetaErr hold a meta file that did not decode; Meta is
	// nil then.
	MetaRaw json.RawMessage
	MetaErr error
}

// AgentMeta is a sub-agent's agent-<agentId>.meta.json.
type AgentMeta struct {
	AgentType     string `json:"agentType"`
	Description   string `json:"description"`
	ToolUseID     string `json:"toolUseId"`
	ParentAgentID string `json:"parentAgentId"`
	IsFork        bool   `json:"isFork"`
	Name          string `json:"name"`
	TeamName      string `json:"teamName"`
	Model         string `json:"model"`
	SpawnDepth    int    `json:"spawnDepth"`
	// RequestShape is "foreground" for an agent the call waited on and
	// "background" for one it started and left running.
	RequestShape string `json:"requestShape"`

	Members

	// Raw is the file as read.
	Raw json.RawMessage `json:"-"`
}

type plainMeta AgentMeta

// UnmarshalJSON decodes the meta file, keeping undeclared members.
func (m *AgentMeta) UnmarshalJSON(data []byte) error {
	return decode(data, (*plainMeta)(m), &m.Members)
}

// MarshalJSON writes the meta file back as it was read.
func (m AgentMeta) MarshalJSON() ([]byte, error) {
	return encode(plainMeta(m), &m.Members)
}

// Open reads the session whose main transcript is path,
// <project>/<session-id>.jsonl, and every sub-agent under
// <project>/<session-id>/subagents. Agents are ordered by ID.
func Open(path string) (*Session, error) {
	main, err := ReadFile(path)
	if err != nil {
		return nil, err
	}
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	s := &Session{ID: id, Main: main}
	dir := filepath.Join(filepath.Dir(path), id, "subagents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	agents := map[string]*Agent{}
	agent := func(id string) *Agent {
		a := agents[id]
		if a == nil {
			a = &Agent{ID: id}
			agents[id] = a
		}
		return a
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "agent-") {
			continue
		}
		p := filepath.Join(dir, name)
		switch {
		case strings.HasSuffix(name, ".meta.json"):
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			a := agent(strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json"))
			m := &AgentMeta{}
			if err := json.Unmarshal(data, m); err != nil {
				// A meta file that does not decode is not fatal: the
				// agent is read without it, and the caller told.
				a.MetaRaw, a.MetaErr = data, err
				continue
			}
			m.Raw = data
			a.Meta = m
		case strings.HasSuffix(name, ".jsonl"):
			t, err := ReadFile(p)
			if err != nil {
				return nil, err
			}
			agent(strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")).Transcript = t
		}
	}
	for _, a := range agents {
		s.Agents = append(s.Agents, a)
	}
	sort.Slice(s.Agents, func(i, j int) bool { return s.Agents[i].ID < s.Agents[j].ID })
	return s, nil
}

// FindSessions returns the main transcripts under root: every
// <session-id>.jsonl, at any depth, its name a UUID as session IDs are. root
// may be ~/.claude/projects, one project directory, or a single file.
// A session's own directory beside its transcript, <session-id>/, holds
// its sub-agents and stored tool results and no main transcript, so it
// is not searched, even as root. No directory is skipped for its name
// alone: a project or fixture directory called "subagents" or "memory"
// is searched like any other.
func FindSessions(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if sessionDir(p) {
				return filepath.SkipDir
			}
			return nil
		}
		if name, ok := strings.CutSuffix(d.Name(), ".jsonl"); ok && isUUID(name) {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// sessionDir reports whether dir is a session's own directory: named
// by a session ID, beside that session's transcript.
func sessionDir(dir string) bool {
	if !isUUID(filepath.Base(dir)) {
		return false
	}
	info, err := os.Stat(dir + ".jsonl")
	return err == nil && !info.IsDir()
}

// isUUID reports whether s has the shape of a UUID.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
				return false
			}
		}
	}
	return true
}
