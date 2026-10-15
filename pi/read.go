package pi

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

// File is one session file.
type File struct {
	// Path is the file the session was read from; empty when it was read
	// from a reader.
	Path string
	// Lines are the lines that decoded as JSON, the header among them,
	// in file order.
	Lines []*Line
	// Errors lists the lines that could not be read, which the reader
	// skipped, as pi skips them.
	Errors []LineError
	// Surrogates lists the lines that held lone UTF-16 surrogate
	// escapes, which the reader removed before decoding; such a line
	// writes back without them.
	Surrogates []int
}

// LineError is a line the reader could not read.
type LineError struct {
	No  int
	Err error
}

func (e LineError) Error() string { return fmt.Sprintf("line %d: %v", e.No, e.Err) }

// Read reads a session file. A line that is not JSON is recorded in
// Errors and skipped, as pi skips it; a line that is JSON but not of the
// shapes this package declares is kept with Err set. Read fails only
// when r does.
func Read(r io.Reader) (*File, error) {
	f := &File{}
	br := bufio.NewReaderSize(r, 1<<20)
	for no := 1; ; no++ {
		raw, tooLong, err := readLine(br)
		if len(raw) > 0 || tooLong {
			f.add(no, raw, tooLong)
		}
		if err == io.EOF {
			return f, nil
		}
		if err != nil {
			return f, err
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

func (f *File) add(no int, raw []byte, tooLong bool) {
	if tooLong {
		f.Errors = append(f.Errors, LineError{No: no, Err: fmt.Errorf("longer than %d bytes", MaxLineSize)})
		return
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		// pi's JSON.parse refuses a line that holds a NUL byte, and pi
		// skips it.
		f.Errors = append(f.Errors, LineError{No: no, Err: errors.New("the line holds NUL bytes")})
		return
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		// pi skips a blank line without counting it.
		return
	}
	if raw[0] != '{' {
		// null pi skips; a number, string or array is not an entry pi
		// can use.
		f.Errors = append(f.Errors, LineError{No: no, Err: errors.New("not a JSON object")})
		return
	}
	text := raw
	if clean, n := StripLoneSurrogates(raw); n > 0 {
		text = clean
		f.Surrogates = append(f.Surrogates, no)
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(text, &head); err != nil {
		f.Errors = append(f.Errors, LineError{No: no, Err: err})
		return
	}
	l := &Line{}
	if err := json.Unmarshal(text, l); err != nil {
		// The envelope alone, when it reads, still places the line in
		// the tree.
		var env struct {
			ID        string `json:"id"`
			ParentID  string `json:"parentId"`
			Timestamp string `json:"timestamp"`
		}
		_ = json.Unmarshal(text, &env)
		l = &Line{Type: head.Type, ID: env.ID, ParentID: env.ParentID, Timestamp: env.Timestamp, Err: err}
	}
	l.No = no
	l.Raw = json.RawMessage(bytes.Clone(raw))
	f.Lines = append(f.Lines, l)
}

// StripLoneSurrogates removes the \u escapes of UTF-16 surrogates that
// are not half of a pair from JSON text, and returns how many it
// removed. JavaScript writes a string cut inside an emoji with such an
// escape; Go would read it as U+FFFD, and pi removes it from what it
// sends a model (pi-ai's sanitizeSurrogates).
func StripLoneSurrogates(raw []byte) ([]byte, int) {
	if !bytes.Contains(raw, []byte(`\u`)) {
		return raw, 0
	}
	out := make([]byte, 0, len(raw))
	n := 0
	inString := false
	surrogate := func(i int) (rune, bool) {
		if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
			return 0, false
		}
		var r rune
		for _, c := range raw[i+2 : i+6] {
			switch {
			case c >= '0' && c <= '9':
				r = r<<4 | rune(c-'0')
			case c >= 'a' && c <= 'f':
				r = r<<4 | rune(c-'a'+10)
			case c >= 'A' && c <= 'F':
				r = r<<4 | rune(c-'A'+10)
			default:
				return 0, false
			}
		}
		return r, r >= 0xD800 && r <= 0xDFFF
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			out = append(out, c)
			continue
		}
		switch c {
		case '"':
			inString = false
			out = append(out, c)
		case '\\':
			if r, ok := surrogate(i); ok {
				if r <= 0xDBFF {
					if lo, ok := surrogate(i + 6); ok && lo >= 0xDC00 {
						out = append(out, raw[i:i+12]...)
						i += 11
						continue
					}
				}
				n++
				i += 5
				continue
			}
			out = append(out, c)
			if i+1 < len(raw) {
				out = append(out, raw[i+1])
				i++
			}
		default:
			out = append(out, c)
		}
	}
	return out, n
}

// LineCount is the number of lines the file had that a reader accounts
// for: those read and those that could not be. Blank lines are not
// counted.
func (f *File) LineCount() int { return len(f.Lines) + len(f.Errors) }

// Header returns the session header: the first line of type session,
// or nil when the file has none.
func (f *File) Header() *Line {
	for _, l := range f.Lines {
		if l.Type == TypeSession && l.Err == nil {
			return l
		}
	}
	return nil
}

// Version is the header's version; a header with none, or no header, is
// version 1, as pi reads it.
func (f *File) Version() int {
	if h := f.Header(); h != nil && h.Version >= 1 {
		return int(h.Version)
	}
	return 1
}

// Entries returns the lines that are entries of the tree, in file order:
// every line but the header. A line that did not decode as its type is
// among them, placed by its envelope.
func (f *File) Entries() []*Line {
	var out []*Line
	for _, l := range f.Lines {
		if l.IsEntry() {
			out = append(out, l)
		}
	}
	return out
}

// ReadFile reads a session from a file.
func ReadFile(path string) (*File, error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	f, err := Read(fd)
	if f != nil {
		f.Path = path
	}
	return f, err
}

// ReadHeader reads only the header of the session file at path, as pi
// does to find a session: the first line that decodes as a session
// line, within the first lines of the file.
func ReadHeader(path string) (*Line, error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	br := bufio.NewReaderSize(fd, 1<<16)
	for range 8 {
		raw, tooLong, err := readLine(br)
		if !tooLong && len(bytes.TrimSpace(raw)) > 0 {
			l := &Line{}
			if json.Unmarshal(bytes.ReplaceAll(raw, []byte{0}, nil), l) == nil && l.Type == TypeSession {
				return l, nil
			}
		}
		if err != nil {
			break
		}
	}
	return nil, fmt.Errorf("%s: no session header", path)
}

// IDFromFileName returns the session id a file name carries, from pi's
// <timestamp>_<session-id>.jsonl, or "" when the name is not of that
// form.
func IDFromFileName(path string) string {
	name, ok := strings.CutSuffix(filepath.Base(path), ".jsonl")
	if !ok {
		return ""
	}
	_, id, ok := strings.Cut(name, "_")
	if !ok {
		return ""
	}
	return id
}

// FindSessions returns the session files under root, every *.jsonl at
// any depth, sorted. root may be ~/.pi/agent/sessions, one project
// directory, or a single file.
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
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}
