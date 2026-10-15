package codex

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxMetaSize bounds the session meta line [ReadMeta] reads. The line
// carries the thread's base instructions and dynamic tools, tens of
// kilobytes as a rule; one past this bound is reported as
// [ErrMetaTooLong] rather than read into memory.
const MaxMetaSize = 8 << 20

// ErrMetaTooLong is returned by ReadMeta for a session meta line longer
// than MaxMetaSize.
var ErrMetaTooLong = fmt.Errorf("codex: the session meta line is longer than %d bytes", MaxMetaSize)

// ReadMeta reads a rollout's session meta from its first line and
// nothing after it: Codex writes the meta first, in the envelope and in
// the bare layout of 2025 alike. It returns nil with no error when the
// first line is not a session meta, [ErrCompressed] for a
// zstd-compressed rollout, and [ErrMetaTooLong] for a line past
// MaxMetaSize. A fork copies its ancestors' metas after its own, so a
// rollout whose first line is damaged is not taken for an ancestor's.
func ReadMeta(path string) (*SessionMeta, error) {
	if strings.HasSuffix(path, ".zst") {
		return nil, ErrCompressed
	}
	fd, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	br := bufio.NewReader(fd)
	if head, _ := br.Peek(len(zstdMagic)); bytes.Equal(head, zstdMagic) {
		return nil, ErrCompressed
	}
	var line []byte
	for {
		frag, err := br.ReadSlice('\n')
		if len(line)+len(frag) > MaxMetaSize {
			return nil, ErrMetaTooLong
		}
		line = append(line, frag...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		break
	}
	f := &File{Path: path}
	f.add(1, bytes.TrimRight(line, "\r\n"), false)
	if l := f.MetaLine(); l != nil {
		return l.Meta, nil
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

// SpawnPath returns the agent path a MultiAgentV2 thread was spawned
// at, which its parent's spawn_agent output names: the thread_spawn
// source's agent_path, or the meta's own.
func (m *SessionMeta) SpawnPath() string {
	if _, spawn := m.SubAgent(); spawn != nil && spawn.AgentPath != "" {
		return spawn.AgentPath
	}
	return m.AgentPath
}

// Thread is one rollout as its session meta describes it.
type Thread struct {
	// ID is the thread's id; Parent the thread that spawned it, or "".
	ID, Parent string
	// AgentPath is the MultiAgentV2 path it was spawned at, or "".
	AgentPath string
	Path      string
}

// Skip is a rollout or directory an index left out, and why.
type Skip struct {
	Path string
	Err  error
}

// Index maps the threads of a sessions tree to their rollouts and
// their parents to their children, from each rollout's session meta
// line alone. It holds every thread's ids and paths, a few hundred
// bytes a rollout, and none of their content.
type Index struct {
	// Root is the directory indexed, symbolic links resolved.
	Root string
	// Skipped lists the rollouts whose session meta could not be read
	// and the directories that could not be walked: a thread among them
	// is in no family.
	Skipped []Skip
	threads map[string][]Thread
	kids    map[string][]string
}

// IndexDir indexes every rollout under root, at any depth. root is
// resolved through symbolic links first; a directory that cannot be
// read, or that vanishes during the walk, is listed in Skipped and
// passed over.
func IndexDir(root string) (*Index, error) { return index(root, true) }

// IndexFlat indexes the rollouts directly in dir, not below it.
func IndexFlat(dir string) (*Index, error) { return index(dir, false) }

// IndexFor indexes the tree a rollout's relatives are in: the sessions
// tree when the rollout sits where Codex files it,
// sessions/YYYY/MM/DD/, else the rollout's own directory alone. See
// [SessionsRoot].
func IndexFor(path string) (*Index, error) {
	root, tree, err := SessionsRoot(path)
	if err != nil {
		return nil, err
	}
	return index(root, tree)
}

func index(root string, tree bool) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	} else {
		return nil, err
	}
	x := &Index{Root: abs, threads: map[string][]Thread{}, kids: map[string][]string{}}
	var paths []string
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == abs {
				return err
			}
			x.Skipped = append(x.Skipped, Skip{Path: p, Err: err})
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != abs && !tree {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, "rollout-") && (strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".jsonl.zst")) {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	for _, p := range paths {
		m, err := ReadMeta(p)
		switch {
		case err != nil:
			x.Skipped = append(x.Skipped, Skip{Path: p, Err: err})
			continue
		case m == nil || m.ID == "":
			x.Skipped = append(x.Skipped, Skip{Path: p, Err: errors.New("its first line is not a session meta naming the thread")})
			continue
		}
		t := Thread{ID: m.ID, Parent: m.ParentThread(), AgentPath: m.SpawnPath(), Path: p}
		if len(x.threads[t.ID]) == 0 && t.Parent != "" {
			x.kids[t.Parent] = append(x.kids[t.Parent], t.ID)
		}
		x.threads[t.ID] = append(x.threads[t.ID], t)
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

// Ancestors returns the threads above one, parent first, as far as the
// index holds them, each once: a thread that names itself, or a cycle,
// ends the chain.
func (x *Index) Ancestors(id string) []string {
	var out []string
	seen := map[string]bool{id: true}
	for {
		rs := x.Rollouts(id)
		if len(rs) == 0 {
			return out
		}
		parent := rs[len(rs)-1].Parent
		if parent == "" || seen[parent] {
			return out
		}
		seen[parent] = true
		out = append(out, parent)
		id = parent
	}
}

// SessionsRoot returns where a rollout's relatives are looked for, as
// an absolute path with symbolic links resolved. A rollout Codex filed,
// sessions/YYYY/MM/DD/rollout-*.jsonl, gives the sessions directory and
// tree true: a sub-agent may be filed under another day. Any other
// rollout gives its own directory and tree false, to be indexed alone,
// so a rollout in a home directory does not walk the home directory.
func SessionsRoot(path string) (root string, tree bool, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	dir := filepath.Dir(abs)
	day := dir
	month := filepath.Dir(day)
	year := filepath.Dir(month)
	sessions := filepath.Dir(year)
	if digits(filepath.Base(day), 2) && digits(filepath.Base(month), 2) && digits(filepath.Base(year), 4) &&
		filepath.Base(sessions) == "sessions" {
		return sessions, true, nil
	}
	if filepath.Base(dir) == "sessions" {
		return dir, true, nil
	}
	return dir, false, nil
}

func digits(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
