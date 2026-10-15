package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// MaxLineSize bounds one line. A longer line is reported as malformed
// and skipped rather than read into memory.
const MaxLineSize = 256 << 20

// ErrCompressed is returned for a zstd-compressed rollout, which Codex
// writes for cold threads as .jsonl.zst. The standard library has no
// zstd; decompress it first (zstd -d) and read the .jsonl.
var ErrCompressed = errors.New("codex: the rollout is zstd-compressed; decompress it with zstd -d first")

// zstdMagic opens every zstd frame.
var zstdMagic = []byte{0x28, 0xB5, 0x2F, 0xFD}

// File is one rollout file.
type File struct {
	// Path is the file the rollout was read from; empty when it was read
	// from a reader.
	Path string
	// Lines are the lines that decoded as JSON objects, in file order.
	Lines []*Line
	// Errors lists the lines that could not be read, which the reader
	// skipped, as Codex skips them.
	Errors []LineError
	// Legacy says the file is in the layout before the envelope: a bare
	// session meta first, then bare items and state records.
	Legacy bool
}

// LineError is a line the reader could not read.
type LineError struct {
	No  int
	Err error
}

func (e LineError) Error() string { return fmt.Sprintf("line %d: %v", e.No, e.Err) }

// Read reads a rollout. A line that is not a JSON object is recorded in
// Errors and skipped, as Codex skips it; a line that is an object but
// not of the shapes this package declares is kept with Err or
// PayloadErr set. Read fails only when r does, or with [ErrCompressed].
func Read(r io.Reader) (*File, error) {
	f := &File{}
	br := bufio.NewReaderSize(r, 1<<20)
	if head, _ := br.Peek(len(zstdMagic)); bytes.Equal(head, zstdMagic) {
		return nil, ErrCompressed
	}
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
		// serde_json refuses a NUL byte, and Codex skips the line.
		f.Errors = append(f.Errors, LineError{No: no, Err: errors.New("the line holds NUL bytes")})
		return
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		// Codex skips a blank line without counting it.
		return
	}
	var probe map[string]json.RawMessage
	if raw[0] != '{' || json.Unmarshal(raw, &probe) != nil || probe == nil {
		f.Errors = append(f.Errors, LineError{No: no, Err: errors.New("not a JSON object")})
		return
	}
	l := &Line{No: no, Raw: json.RawMessage(bytes.Clone(raw))}
	_, hasType := probe["type"]
	_, hasTimestamp := probe["timestamp"]
	_, hasRecordType := probe["record_type"]
	first := len(f.Lines) == 0
	switch {
	case hasRecordType:
		l.Legacy, l.Kind = true, TypeState
		f.Legacy = true
	case hasType && hasTimestamp:
		if err := json.Unmarshal(raw, l); err != nil {
			l.Err = err
		}
		l.No, l.Raw = no, json.RawMessage(bytes.Clone(raw))
		l.Kind = l.Type
	case hasType:
		// An item with no envelope: the legacy layout writes the items
		// bare, without a timestamp.
		l.Legacy, l.Kind = true, TypeResponseItem
		f.Legacy = true
	case first && probe["id"] != nil:
		l.Legacy, l.Kind = true, TypeSessionMeta
		f.Legacy = true
	default:
		l.Err = errors.New("an object with no type is not a rollout line")
	}
	if l.Legacy && l.Kind == TypeSessionMeta {
		var ts struct {
			Timestamp string `json:"timestamp"`
		}
		_ = json.Unmarshal(raw, &ts)
		l.Timestamp = ts.Timestamp
	}
	if l.Err == nil {
		l.decodePayload()
	}
	f.Lines = append(f.Lines, l)
}

// LineCount is the number of lines the file had that a reader accounts
// for: those read and those that could not be. Blank lines are not
// counted.
func (f *File) LineCount() int { return len(f.Lines) + len(f.Errors) }

// MetaLine returns the line of the thread's session meta: the first
// session_meta line that decodes, as Codex takes the first. Later ones
// are a forked thread's copies of its ancestors'.
func (f *File) MetaLine() *Line {
	for _, l := range f.Lines {
		if l.Meta != nil {
			return l
		}
	}
	return nil
}

// ReadFile reads a rollout from a file.
func ReadFile(path string) (*File, error) {
	if strings.HasSuffix(path, ".zst") {
		return nil, fmt.Errorf("%s: %w", path, ErrCompressed)
	}
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	f, err := Read(fd)
	if errors.Is(err, ErrCompressed) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f != nil {
		f.Path = path
	}
	return f, err
}

// fileName is a rollout's file name: rollout-<local time>-<thread id>,
// with _<rollout id> when the thread has several rollout files.
var fileName = regexp.MustCompile(`^rollout-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-([0-9a-fA-F-]{36})(?:_([0-9a-fA-F-]{36}))?\.jsonl(?:\.zst)?$`)

// IDFromFileName returns the thread id a rollout's file name carries, or
// "" when the name is not of Codex's form.
func IDFromFileName(path string) string {
	m := fileName.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return ""
	}
	return m[1]
}

// FindSessions returns the rollout files under root, every rollout-*
// .jsonl and .jsonl.zst at any depth, sorted. root may be
// ~/.codex/sessions, a day's directory, or a single file.
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
		name := d.Name()
		if !d.IsDir() && strings.HasPrefix(name, "rollout-") &&
			(strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst")) {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}
