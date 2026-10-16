package clinesdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ChristopherDavenport/agentimport/internal/jsonline"
)

// MaxSize bounds a file the reader reads. A larger file is refused
// rather than read into memory, and reported as malformed.
const MaxSize = 1 << 30

// File name suffixes in a session directory: <id>.json is the
// manifest, <id>.messages.json the history, <id>.compaction.json the
// sidecar; any other <stem>.messages.json is a sub-agent's or a team
// task's history.
const (
	SuffixManifest   = ".json"
	SuffixMessages   = ".messages.json"
	SuffixCompaction = ".compaction.json"
)

// Session is one session directory as read.
type Session struct {
	// Dir is the session directory, and ID its name, the session's ID.
	Dir string
	ID  string

	// Manifest is <id>.json; ManifestErr why it did not read.
	Manifest    *Manifest
	ManifestErr error
	HasManifest bool

	// Messages is the session's own history, nil when the directory
	// holds none.
	Messages *MessagesFile
	// Compaction is the compaction sidecar, nil when there is none.
	Compaction *Compaction
	// Agents are the histories of the sub-agents and team tasks the
	// session ran, by file name.
	Agents []*MessagesFile

	// Others lists the other files of the directory, each a unit the
	// converter does not read; Dirs its directories.
	Others []string
	Dirs   []string
}

// ManifestName is the manifest's file name.
func (s *Session) ManifestName() string { return s.ID + SuffixManifest }

// Units is the number of units the session accounts for: each file it
// reads, each message of each messages array, and each other file.
func (s *Session) Units() int {
	n := len(s.Others)
	if s.HasManifest {
		n++
	}
	if s.Messages != nil {
		n += 1 + len(s.Messages.Messages)
	}
	if s.Compaction != nil {
		n += 1 + len(s.Compaction.Messages)
	}
	for _, a := range s.Agents {
		n += 1 + len(a.Messages)
	}
	return n
}

// SessionDir returns the session directory a path names: the path
// itself when it is a directory, else the directory holding it.
func SessionDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return path, nil
	}
	return filepath.Dir(path), nil
}

// ReadSession reads the session directory path names, or the directory
// of the file it names. A file that cannot be read is recorded with
// its error, never returned as one; ReadSession fails only when the
// directory cannot be listed or holds no session.
func ReadSession(path string) (*Session, error) {
	dir, err := SessionDir(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("the session directory cannot be read: %w", err)
	}
	files := map[string][]byte{}
	errs := map[string]error{}
	var others, dirs []string
	id := filepath.Base(dir)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			dirs = append(dirs, name)
			continue
		}
		if !readable(id, name) {
			others = append(others, name)
			continue
		}
		data, err := readLimited(filepath.Join(dir, name))
		if err != nil {
			errs[name] = err
			continue
		}
		files[name] = data
	}
	s := ParseSession(id, files)
	s.Dir = dir
	for name, err := range errs {
		s.fileError(name, err)
	}
	s.Others = append(s.Others, others...)
	s.Dirs = dirs
	sort.Strings(s.Others)
	sort.Slice(s.Agents, func(i, j int) bool { return s.Agents[i].Name < s.Agents[j].Name })
	if !s.HasManifest && s.Messages == nil {
		return nil, errors.New("the directory holds no Cline session: no " + id + SuffixMessages + " or " + id + SuffixManifest)
	}
	return s, nil
}

// readable reports whether a file of session id's directory is one the
// reader reads.
func readable(id, name string) bool {
	return name == id+SuffixManifest || strings.HasSuffix(name, SuffixMessages) || name == id+SuffixCompaction
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
		return nil, fmt.Errorf("the file is larger than %d bytes", MaxSize)
	}
	return data, nil
}

// fileError records that a file could not be read.
func (s *Session) fileError(name string, err error) {
	switch {
	case name == s.ID+SuffixManifest:
		s.HasManifest, s.ManifestErr = true, err
	case name == s.ID+SuffixCompaction:
		s.Compaction = &Compaction{Name: name, Err: err}
	case name == s.ID+SuffixMessages:
		s.Messages = &MessagesFile{Name: name, Err: err}
	case strings.HasSuffix(name, SuffixMessages):
		s.Agents = append(s.Agents, &MessagesFile{Name: name, Err: err})
	default:
		s.Others = append(s.Others, name)
	}
}

// ParseSession reads a session from its files' contents, by name,
// without touching the file system. id is the session's ID, the
// directory's name; a file whose name is not one of the session's is
// listed in Others.
func ParseSession(id string, files map[string][]byte) *Session {
	s := &Session{ID: id}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := files[name]
		switch {
		case name == id+SuffixManifest:
			s.HasManifest = true
			m := &Manifest{}
			if err := json.Unmarshal(data, m); err != nil {
				s.ManifestErr = readError(err)
				continue
			}
			s.Manifest = m
		case name == id+SuffixCompaction:
			s.Compaction = ParseCompaction(name, data)
		case name == id+SuffixMessages:
			s.Messages = ParseMessagesFile(name, data)
		case strings.HasSuffix(name, SuffixMessages):
			s.Agents = append(s.Agents, ParseMessagesFile(name, data))
		default:
			s.Others = append(s.Others, name)
		}
	}
	return s
}

// readError is a decoding error made to name no Go type.
func readError(err error) error {
	switch {
	case errors.Is(err, errNotObject):
		return errors.New("the file is not a JSON object")
	case errors.As(err, new(*json.SyntaxError)):
		return errors.New("the file is not JSON")
	}
	return err
}

// ParseMessagesFile reads a messages file from its bytes: the envelope's
// members, and its messages array element by element.
func ParseMessagesFile(name string, data []byte) *MessagesFile {
	f := &MessagesFile{Name: name}
	t := bytes.TrimSpace(data)
	if len(t) > 0 && t[0] == '[' {
		f.Bare, f.HasMessages = true, true
		f.Messages, f.Err = elements(data)
		return f
	}
	env, msgs, has, err := envelope(data)
	f.Messages, f.HasMessages, f.Err = msgs, has, err
	if env != nil {
		raw, merr := json.Marshal(env)
		if merr == nil {
			merr = jsonline.DecodeLenient(raw, (*plainMessagesFile)(f), &f.Members)
		}
		if merr != nil && f.Err == nil {
			f.Err = merr
		}
	}
	return f
}

// ParseCompaction reads a compaction sidecar from its bytes.
func ParseCompaction(name string, data []byte) *Compaction {
	c := &Compaction{Name: name}
	env, msgs, has, err := envelope(data)
	c.Messages, c.HasMessages, c.Err = msgs, has, err
	if env != nil {
		raw, merr := json.Marshal(env)
		if merr == nil {
			merr = jsonline.DecodeLenient(raw, (*plainCompaction)(c), &c.Members)
		}
		if merr != nil && c.Err == nil {
			c.Err = merr
		}
	}
	return c
}

// envelope reads a JSON object's members, its messages member element
// by element. env holds the other members, nil when data is no object;
// has says messages was an array. err says why the object was not read
// to its end; what was read before the fault is returned.
func envelope(data []byte) (env map[string]json.RawMessage, msgs []*Message, has bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return nil, nil, false, errors.New("the file is empty")
		}
		return nil, nil, false, errors.New("the file is not JSON")
	}
	if tok != json.Delim('{') {
		return nil, nil, false, errors.New("the file is not a JSON object")
	}
	env = map[string]json.RawMessage{}
	cut := errors.New("the file is cut short or broken after its last whole member")
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return env, msgs, has, cut
		}
		key, _ := tok.(string)
		if key == "messages" && nextIs(dec, data, '[') {
			// A repeated member is read as JSON.parse reads it: the last
			// stands.
			msgs, has = nil, true
			delete(env, key)
			if _, err := dec.Token(); err != nil {
				return env, msgs, has, cut
			}
			for dec.More() {
				var raw json.RawMessage
				if err := dec.Decode(&raw); err != nil {
					return env, msgs, has, errors.New("the messages array is cut short or broken after its last whole element")
				}
				m := &Message{}
				_ = m.UnmarshalJSON(raw)
				msgs = append(msgs, m)
			}
			if _, err := dec.Token(); err != nil {
				return env, msgs, has, cut
			}
			continue
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return env, msgs, has, cut
		}
		if key == "messages" {
			msgs, has = nil, false
		}
		env[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return env, msgs, has, cut
	}
	if rest := bytes.TrimSpace(data[dec.InputOffset():]); len(rest) > 0 {
		return env, msgs, has, errors.New("the object is followed by bytes that are not part of it")
	}
	return env, msgs, has, nil
}

// nextIs reports whether the next value the decoder reads starts with
// c.
func nextIs(dec *json.Decoder, data []byte, c byte) bool {
	for i := int(dec.InputOffset()); i < len(data); i++ {
		switch data[i] {
		case ' ', '\t', '\r', '\n', ':':
			continue
		}
		return data[i] == c
	}
	return false
}

// elements reads a JSON array of messages up to the first element that
// does not parse.
func elements(data []byte) ([]*Message, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return nil, errors.New("the file is not JSON")
	}
	var out []*Message
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out, errors.New("the array is cut short or broken after its last whole element")
		}
		m := &Message{}
		_ = m.UnmarshalJSON(raw)
		out = append(out, m)
	}
	if _, err := dec.Token(); err != nil {
		return out, errors.New("the array is cut short or broken after its last whole element")
	}
	if rest := bytes.TrimSpace(data[dec.InputOffset():]); len(rest) > 0 {
		return out, errors.New("the array is followed by bytes that are not part of it")
	}
	return out, nil
}

// IsSessionFile reports whether path is a file of a session directory
// the reader reads: its manifest, its history or its sidecar, named by
// the directory, or another messages file beside them.
func IsSessionFile(path string) bool {
	dir := filepath.Dir(path)
	id := filepath.Base(dir)
	name := filepath.Base(path)
	switch name {
	case id + SuffixMessages, id + SuffixCompaction:
		return true
	case id + SuffixManifest:
		return isManifest(path)
	}
	if strings.HasSuffix(name, SuffixMessages) {
		return isFile(filepath.Join(dir, id+SuffixMessages)) || isFile(filepath.Join(dir, id+SuffixManifest)) && isManifest(filepath.Join(dir, id+SuffixManifest))
	}
	return false
}

// isManifest reports whether a <id>.json is a session manifest: an
// object whose session_id is the directory's name, as far as its first
// bytes tell.
func isManifest(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := io.ReadFull(f, head)
	id := filepath.Base(filepath.Dir(path))
	quoted, _ := json.Marshal(id)
	return bytes.Contains(head[:n], []byte(`"session_id"`)) && bytes.Contains(head[:n], quoted)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// FindSessions returns the session directories under root as the path
// of each one's main file, its history or else its manifest, sorted.
// root may be ~/.cline, its data directory, the sessions directory, a
// session directory, or a file in one, which names its directory. A
// directory that cannot be read is skipped, and a session directory is
// not walked into.
func FindSessions(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if IsSessionFile(root) {
			if main := mainFile(filepath.Dir(root)); main != "" {
				return []string{main}, nil
			}
		}
		return []string{root}, nil
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
		switch d.Name() {
		case ".git", "node_modules", "checkpoints", "tasks":
			if p != root {
				return filepath.SkipDir
			}
		}
		if main := mainFile(p); main != "" {
			out = append(out, main)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// MainFile returns the file the session directory path names, or the
// directory of the file it names, is found by: its history, or its
// manifest; "" when it is no session directory.
func MainFile(path string) string {
	dir, err := SessionDir(path)
	if err != nil {
		return ""
	}
	return mainFile(dir)
}

// mainFile returns the file a session directory is found by, or ""
// when dir is not a session directory: its history, or its manifest.
func mainFile(dir string) string {
	id := filepath.Base(dir)
	if p := filepath.Join(dir, id+SuffixMessages); isFile(p) {
		return p
	}
	if p := filepath.Join(dir, id+SuffixManifest); isFile(p) && isManifest(p) {
		return p
	}
	return ""
}

// IndexPaths are where Cline's index of a session directory's sessions
// would be: sessions.db in the data directory's db directory, and
// sessions.index.json in the sessions directory, which Cline writes
// when it cannot open SQLite. Cline's CLINE_DB_DATA_DIR can move the
// database elsewhere, which a reader of the files cannot know.
func IndexPaths(sessionDir string) (database, fileIndex string) {
	sessions := filepath.Dir(sessionDir)
	return filepath.Join(filepath.Dir(sessions), "db", DatabaseName), filepath.Join(sessions, FileIndexName)
}

// Names of Cline's index files.
const (
	DatabaseName  = "sessions.db"
	FileIndexName = "sessions.index.json"
)
