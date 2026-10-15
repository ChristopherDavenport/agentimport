package agentimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/ChristopherDavenport/agentsession"
)

// Module is this module's path, as the provenance member names it.
const Module = "github.com/ChristopherDavenport/agentimport"

// SourceMember is the header member that says where a session was
// imported from. The format defines no import provenance, so it is a
// namespaced member.
const SourceMember = "agentimport:source"

// Source is the value of [SourceMember].
type Source struct {
	// Format names the native format, such as "claudecode".
	Format string `json:"format"`
	// NativeID is the session's ID in the native format, when it had
	// one and the session's own ID is not it.
	NativeID string `json:"native_id,omitempty"`
	// Converter is this module and its version.
	Converter string `json:"converter"`
}

// Converter names this module at the version the running binary was
// built with: "(devel)" in a build of the module itself.
func Converter() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Module
	}
	if bi.Main.Path == Module {
		return Module + "@" + bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path == Module {
			return Module + "@" + d.Version
		}
	}
	return Module
}

// SetSource records where a session came from in its header.
func SetSource(h *agentsession.Header, s Source) error {
	if s.Converter == "" {
		s.Converter = Converter()
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if h.Extra == nil {
		h.Extra = map[string]json.RawMessage{}
	}
	h.Extra[SourceMember] = raw
	return nil
}

// Result is one conversion: the sessions it made and what it did to
// the input.
type Result struct {
	// Sessions holds the main session first, then its subsessions, each
	// after the session that spawned it.
	Sessions []*agentsession.Session
	Report   *Report
}

// Builder appends a native log's entries to a session. It holds the map
// from native IDs to entry IDs, hands out call IDs, and appends in
// whatever order the converter walks the native tree, which must put a
// parent before its children.
type Builder struct {
	S      *agentsession.Session
	Report *Report
	// NS is the source's namespace, the prefix of every member and
	// custom entry the converter writes: "claudecode", "pi", "codex".
	NS string
	// File is the source file findings name; empty for the main file.
	File  string
	Calls CallIDs

	ids  map[string]string
	last time.Time
	// tail is the entry appended last, the file's last line.
	tail string
	// leaf is the entry Leaf named.
	leaf string
}

// NewBuilder starts a session with header h.
func NewBuilder(h agentsession.Header, ns string, r *Report) *Builder {
	return &Builder{S: agentsession.New(h), Report: r, NS: ns, ids: map[string]string{}}
}

// Member sets a namespaced member, "<ns>:<name>", on e's body. A nil or
// empty value is not set.
func (b *Builder) Member(e agentsession.Entry, name string, v any) error {
	var raw json.RawMessage
	switch v := v.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
	case json.RawMessage:
		if len(v) == 0 {
			return nil
		}
		raw = v
	}
	if raw == nil {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return fmt.Errorf("member %s:%s: %w", b.NS, name, err)
		}
	}
	repeated := RepeatedMembers(raw)
	badText := InvalidText(raw)
	raw, _, err := CleanJSON(raw)
	if err != nil {
		return fmt.Errorf("member %s:%s: %w", b.NS, name, err)
	}
	if repeated {
		b.Report.Addf(Repaired, "agentimport/repeated-member", b.File, 0,
			"an object in member %s:%s named a member twice, which I-JSON forbids; the last is kept, as JSON.parse keeps it", b.NS, name)
	}
	if badText {
		b.Report.Addf(Repaired, "agentimport/invalid-text", b.File, 0,
			"text in member %s:%s that was not valid Unicode was replaced with U+FFFD", b.NS, name)
	}
	base := e.Base()
	if base.Unknown == nil {
		base.Unknown = map[string]json.RawMessage{}
	}
	base.Unknown[b.NS+":"+name] = raw
	return nil
}

// Append adds e under parent, an entry ID this builder returned, or as
// a new root when parent is empty. A zero timestamp takes the last one
// appended, so a native record with no time of its own sorts where the
// file put it. An entry identical to one the session holds, which the
// format would fold into it, is moved a nanosecond later and the move
// reported.
func (b *Builder) Append(parent string, e agentsession.Entry) (string, error) {
	base := e.Base()
	if parent == "" {
		b.S.ResetLeaf()
	}
	base.Parent = parent
	if base.Timestamp.IsZero() {
		base.Timestamp = b.last
	}
	for {
		// Prepare makes the checks Append makes and says whether the
		// session already holds the entry, without adding it.
		r, err := b.S.Prepare(e)
		if err == nil && r.Outcome != agentsession.Held {
			r, err = b.S.Commit(e)
		}
		if err != nil {
			return "", fmt.Errorf("append %s: %w", e.EntryType(), err)
		}
		if r.Outcome != agentsession.Held {
			if base.Timestamp.After(b.last) {
				b.last = base.Timestamp
			}
			b.tail = r.ID
			return r.ID, nil
		}
		b.Report.Addf(Repaired, "agentimport/same-instant", b.File, 0,
			"a %s identical to one already written at the same instant was moved 1ns later", e.EntryType())
		base.Timestamp = base.Timestamp.Add(time.Nanosecond)
		base.ID = ""
	}
}

// Bind records that native ID native became entry id.
func (b *Builder) Bind(native, id string) { b.ids[native] = id }

// ID returns the entry a native ID became.
func (b *Builder) ID(native string) (string, bool) {
	id, ok := b.ids[native]
	return id, ok
}

// Last returns the entry appended last, which is the file's last line.
func (b *Builder) Last() string { return b.tail }

// Leaf makes id the leaf a reader resumes from. A reader with no leaf
// label takes the file's last line, so nothing is written when that
// line is id; a leaf label naming id is appended otherwise, so the
// reader resumes on id exactly, never on a later line of another
// branch. A converter calls it last, with the entry the native format
// names as current.
func (b *Builder) Leaf(id string) error {
	b.leaf = id
	if b.tail == id {
		return nil
	}
	l := agentsession.NewLabelEntry(id, agentsession.LeafLabel)
	l.Timestamp = b.last
	_, err := b.Append(b.tail, l)
	return err
}

// Resume returns the entry a reader of the session resumes from: the
// one Leaf named, or the last appended.
func (b *Builder) Resume() string {
	if b.leaf != "" {
		return b.leaf
	}
	return b.tail
}

// Finish writes s, reads it back and checks the records on every leaf,
// the checks a reader of the converted file will make, and returns the
// session as read back. A converter returns that session rather than
// the one it built: a reader of the written file resumes on the leaf
// the file names, which an in-memory session's leaf can differ from.
// A converter whose output fails the checks has a bug.
func Finish(s *agentsession.Session) (*agentsession.Session, error) {
	var buf bytes.Buffer
	if err := agentsession.Write(&buf, s); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	r, err := agentsession.Read(&buf)
	if err != nil {
		return nil, fmt.Errorf("read back: %w", err)
	}
	for _, leaf := range r.Leaves() {
		if err := r.VerifyRecords(leaf); err != nil {
			return nil, fmt.Errorf("leaf %s: %w", leaf, err)
		}
	}
	return r, nil
}

// Verify is [Finish] for a caller that only needs the checks.
func Verify(s *agentsession.Session) error {
	_, err := Finish(s)
	return err
}
