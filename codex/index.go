package codex

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// metaProbeLines bounds how far ReadMeta looks for the session meta,
// which Codex writes first.
const metaProbeLines = 8

// ReadMeta reads a rollout's session meta and nothing after it: the
// first line that decodes as one, among the first few. It returns nil
// with no error when the file has none, and [ErrCompressed] for a
// zstd-compressed rollout.
func ReadMeta(path string) (*SessionMeta, error) {
	if strings.HasSuffix(path, ".zst") {
		return nil, ErrCompressed
	}
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	br := bufio.NewReaderSize(fd, 1<<20)
	if head, _ := br.Peek(len(zstdMagic)); bytes.Equal(head, zstdMagic) {
		return nil, ErrCompressed
	}
	f := &File{Path: path}
	for no := 1; no <= metaProbeLines; no++ {
		raw, tooLong, err := readLine(br)
		if len(raw) > 0 || tooLong {
			f.add(no, raw, tooLong)
			if l := f.MetaLine(); l != nil {
				return l.Meta, nil
			}
		}
		if err == io.EOF {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// ParentThread returns the thread that spawned this one: the session
// meta's parent_thread_id, or the thread_spawn source's parent. A fork
// the user made names the thread it copied in forked_from_id instead,
// and has no parent here.
func (m *SessionMeta) ParentThread() string {
	if m.ParentThreadID != "" {
		return m.ParentThreadID
	}
	if _, spawn := m.SubAgent(); spawn != nil {
		return spawn.ParentThreadID
	}
	return ""
}

// Thread is one rollout as its session meta describes it.
type Thread struct {
	// ID is the thread's id; Parent the thread that spawned it, or "".
	ID, Parent string
	Path       string
}

// Index maps the threads of a sessions tree to their rollouts and
// their parents to their children, from each rollout's session meta
// alone.
type Index struct {
	// Root is the directory indexed.
	Root    string
	threads map[string][]Thread
	kids    map[string][]string
}

// IndexDir indexes every rollout under root. A rollout whose session
// meta cannot be read, a compressed one among them, is left out.
func IndexDir(root string) (*Index, error) {
	paths, err := FindSessions(root)
	if err != nil {
		return nil, err
	}
	x := &Index{Root: root, threads: map[string][]Thread{}, kids: map[string][]string{}}
	for _, p := range paths {
		m, err := ReadMeta(p)
		if err != nil || m == nil || m.ID == "" {
			continue
		}
		t := Thread{ID: m.ID, Parent: m.ParentThread(), Path: p}
		if len(x.threads[t.ID]) == 0 && t.Parent != "" {
			x.kids[t.Parent] = append(x.kids[t.Parent], t.ID)
		}
		x.threads[t.ID] = append(x.threads[t.ID], t)
	}
	for _, ts := range x.threads {
		// A reverted thread has several rollouts, named in time order.
		sort.Slice(ts, func(i, j int) bool { return ts[i].Path < ts[j].Path })
	}
	for _, ids := range x.kids {
		sort.Strings(ids)
	}
	return x, nil
}

// Rollouts returns the rollout files of a thread, oldest first: a
// reverted thread has several, the last being the one Codex continues.
// A nil index holds none.
func (x *Index) Rollouts(id string) []Thread {
	if x == nil {
		return nil
	}
	return x.threads[id]
}

// Children returns the ids of the threads a thread spawned, sorted. A
// nil index holds none.
func (x *Index) Children(id string) []string {
	if x == nil {
		return nil
	}
	return x.kids[id]
}

// SessionsRoot returns the tree a rollout's relatives are looked for
// in: the nearest enclosing directory named "sessions", as Codex's
// ~/.codex/sessions is, or else the rollout's own directory.
func SessionsRoot(path string) string {
	dir := filepath.Dir(path)
	for d := dir; ; {
		if filepath.Base(d) == "sessions" {
			return d
		}
		up := filepath.Dir(d)
		if up == d {
			return dir
		}
		d = up
	}
}
