package atif

import (
	"bufio"
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
)

// MaxSize bounds a document. A larger file is refused rather than read
// into memory.
const MaxSize = 1 << 30

// ErrNotDocument is returned for bytes that are not a JSON object, nor
// the start of one.
var ErrNotDocument = errors.New("atif: not a JSON object")

// File is one ATIF document as read.
type File struct {
	// Path is the file the document was read from; empty when it was
	// read from a reader.
	Path string
	// Doc is the document, or, when Cut is set, what of it the reader
	// could read.
	Doc *Trajectory
	// Cut is set when the document does not parse to its end, as a file
	// a producer was rewriting in place when it was killed does not:
	// Doc then holds the root members and the steps that end before
	// CutAt, the byte after the last value that parsed. It is taken
	// where the parse last succeeded, not where it failed, since where
	// encoding/json stops on an error differs between Go releases.
	Cut   error
	CutAt int64
	// CutStep says a step was cut short. It is a unit the reader could
	// not read, and is not among Doc's steps.
	CutStep bool
	// CutMembers lists the root members that were cut short, or that
	// follow the cut, as far as the reader can tell: those whose name
	// it read.
	CutMembers []string
}

// Units is the number of units the file accounts for: the document's
// root and steps, at any depth, and a step cut short.
func (f *File) Units() int {
	n := f.Doc.Units()
	if f.CutStep {
		n++
	}
	return n
}

// Read reads a document. A step, tool call, result, part or embedded
// trajectory that is not an object is kept with its error and its
// neighbours still read; a document cut short is salvaged and Cut set.
// Read fails when r does, when the document exceeds MaxSize, and with
// [ErrNotDocument] for bytes that are not a JSON object.
func Read(r io.Reader) (*File, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxSize {
		return nil, fmt.Errorf("atif: the document is larger than %d bytes", MaxSize)
	}
	return Parse(data)
}

// Parse reads a document from its bytes; see [Read].
func Parse(data []byte) (*File, error) {
	f := &File{}
	if trimmed := bytes.TrimLeft(data, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, ErrNotDocument
	}
	if json.Valid(data) {
		t := &Trajectory{}
		if err := json.Unmarshal(data, t); err != nil || t.Err != nil {
			return nil, ErrNotDocument
		}
		f.Doc = t
		return f, nil
	}
	if err := f.salvage(data); err != nil {
		return nil, err
	}
	return f, nil
}

// salvage reads the root members that parse whole, and the steps and
// embedded trajectories of an array cut short that end before the cut.
func (f *File) salvage(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return ErrNotDocument
	}
	members := map[string]json.RawMessage{}
	cut := func(err error, at int64) {
		f.Cut, f.CutAt = err, at
	}
	for {
		at := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			cut(err, at)
			break
		}
		if tok == json.Delim('}') {
			// The object ended and something after it does not parse.
			cut(errors.New("the document is followed by bytes that are not JSON"), dec.InputOffset())
			break
		}
		key, ok := tok.(string)
		if !ok {
			cut(errors.New("a member name is not a string"), at)
			break
		}
		start := dec.InputOffset()
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == nil {
			members[key] = raw
			continue
		}
		f.CutMembers = append(f.CutMembers, key)
		if key == "steps" || key == "subagent_trajectories" {
			elems, at, partial, err := salvageArray(data, start)
			if elems != nil {
				list, merr := json.Marshal(elems)
				if merr == nil {
					members[key] = list
				}
			}
			if key == "steps" && partial {
				f.CutStep = true
			}
			cut(err, at)
			break
		}
		cut(err, start)
		break
	}
	if f.Cut == nil {
		f.Cut = errors.New("the document does not parse")
	}
	obj, err := json.Marshal(members)
	if err != nil {
		return err
	}
	t := &Trajectory{}
	if err := json.Unmarshal(obj, t); err != nil || t.Err != nil {
		return ErrNotDocument
	}
	f.Doc = t
	return nil
}

// salvageArray reads the elements of the array that starts at or after
// offset start, a member's value following its name, up to the first
// that does not parse. It returns those elements, the byte after the
// last of them (where the array starts, with none), whether an element
// was cut short, and why; nil elements when the value is not an array.
func salvageArray(data []byte, start int64) (elems []json.RawMessage, at int64, partial bool, err error) {
	i := int(start)
	for i < len(data) && (data[i] == ':' || data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r') {
		i++
	}
	dec := json.NewDecoder(bytes.NewReader(data[i:]))
	tok, terr := dec.Token()
	if terr != nil || tok != json.Delim('[') {
		if terr == nil {
			terr = errors.New("the member is cut short")
		}
		return nil, int64(i), false, terr
	}
	elems = []json.RawMessage{}
	for dec.More() {
		before := dec.InputOffset()
		var raw json.RawMessage
		if derr := dec.Decode(&raw); derr != nil {
			// Something of the element was read when the decoder got past
			// its first byte.
			rest := bytes.TrimLeft(data[i+int(before):], " \t\r\n,")
			return elems, int64(i) + before, len(rest) > 0, derr
		}
		elems = append(elems, raw)
	}
	end := dec.InputOffset()
	if _, terr := dec.Token(); terr != nil {
		return elems, int64(i) + end, false, terr
	}
	return elems, int64(i) + dec.InputOffset(), false, errors.New("the document is cut short after the array")
}

// ReadFile reads a document from a file.
func ReadFile(path string) (*File, error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	f, err := Read(fd)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f.Path = path
	return f, nil
}

// probeMembers bounds the root members Probe reads before it gives up.
const probeMembers = 64

// Probe reports whether the file at path holds an ATIF document: a
// JSON object whose schema_version starts "ATIF-v", read from the
// members before it, whatever comes after. It returns the version.
func Probe(path string) (string, bool) {
	fd, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer fd.Close()
	dec := json.NewDecoder(bufio.NewReaderSize(io.LimitReader(fd, MaxSize), 1<<16))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", false
	}
	for range probeMembers {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		key, ok := tok.(string)
		if !ok {
			return "", false
		}
		if key != "schema_version" {
			var skip json.RawMessage
			if dec.Decode(&skip) != nil {
				return "", false
			}
			continue
		}
		var v string
		if dec.Decode(&v) != nil || !strings.HasPrefix(v, "ATIF-v") {
			return "", false
		}
		return v, true
	}
	return "", false
}

// FindSessions returns the ATIF documents under root: every *.json file
// at any depth that [Probe] accepts, sorted. root may be a directory of
// Harbor jobs, one trial, or a single file, which is returned as it is.
// A directory that cannot be read is skipped.
func FindSessions(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
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
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".json") {
			if _, ok := Probe(p); ok {
				out = append(out, p)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// Names lists the files a document names, joined to dir when relative
// and cleaned, in the order the document names them: its continuation,
// then each sub-agent reference's trajectory_path, in its own steps and
// in those of every trajectory it embeds. A URL is not a file and is
// left out.
func Names(t *Trajectory, dir string) []string {
	var out []string
	add := func(p string) {
		if p == "" || strings.Contains(p, "://") {
			return
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		out = append(out, filepath.Clean(p))
	}
	var walk func(t *Trajectory)
	walk = func(t *Trajectory) {
		if t == nil || t.Err != nil {
			return
		}
		add(t.ContinuedRef)
		for _, s := range t.Steps {
			if s == nil || s.Err != nil || s.Observation == nil || s.Observation.Err != nil {
				continue
			}
			for _, r := range s.Observation.Results {
				if r == nil || r.Err != nil {
					continue
				}
				for _, ref := range r.Refs.List {
					if ref != nil && ref.Err == nil {
						add(ref.TrajectoryPath)
					}
				}
			}
		}
		for _, sub := range t.Subagents {
			walk(sub)
		}
	}
	walk(t)
	return out
}
