package pi

import "fmt"

// Migration says what [Migrate] did to bring a file to the current
// version, as pi does in memory when it loads one.
type Migration struct {
	// From is the version the file declared; To is [CurrentVersion].
	From, To int
	// IDs counts the version 1 entries given an id and a parentId,
	// chaining them in file order.
	IDs int
	// FirstKept counts the version 1 compactions whose
	// firstKeptEntryIndex became a firstKeptEntryId.
	FirstKept int
	// HookMessages counts the messages whose version 2 role hookMessage
	// became custom.
	HookMessages int
}

// Migrate brings f to [CurrentVersion] in place, applying pi's own
// migrations: version 1 entries get ids and are chained in file order,
// with a compaction's firstKeptEntryIndex turned into the id of the
// entry it indexed; version 2's hookMessage role becomes custom. An id
// it gives is "v1-<line>" and the line is marked Synthetic, since the
// file never held it. A file already current is not changed. The lines
// it changes no longer write back as they were read.
func Migrate(f *File) Migration {
	m := Migration{From: f.Version(), To: CurrentVersion}
	if m.From >= CurrentVersion {
		return m
	}
	if m.From < 2 {
		prev := ""
		for _, l := range f.Lines {
			if !l.IsEntry() {
				continue
			}
			l.ID = fmt.Sprintf("v1-%d", l.No)
			l.ParentID = prev
			l.Synthetic = true
			prev = l.ID
			m.IDs++
		}
		for _, l := range f.Lines {
			if l.Type != TypeCompaction || l.FirstKeptEntryIndex == nil {
				continue
			}
			// The index counts the file's entries as pi parsed them,
			// the header first.
			if i := *l.FirstKeptEntryIndex; i >= 0 && i < len(f.Lines) && f.Lines[i].IsEntry() {
				l.FirstKeptEntryID = f.Lines[i].ID
				m.FirstKept++
			}
		}
	}
	for _, l := range f.Lines {
		if l.Type == TypeMessage && l.Message != nil && l.Message.Role == RoleHookMessage {
			l.Message.Role = RoleCustom
			m.HookMessages++
		}
	}
	return m
}

// Tree is a session's entries linked by parentId.
type Tree struct {
	// Roots are the entries with no parent, in file order.
	Roots []*Node
	// Nodes holds every entry in file order.
	Nodes []*Node
	// Leaf is the last entry in the file, where pi resumes; nil when
	// the file has no entry.
	Leaf *Node
	// Dangling lists the entries whose parentId names no entry; each
	// starts a root.
	Dangling []*Line
	// Cycles lists the entries whose parent links led back to
	// themselves; the link was cut and each starts a root.
	Cycles []*Line
	// Duplicates lists the entries whose id an earlier entry had. pi
	// indexes by id and the later entry wins, so a parentId naming the
	// id names the later one; the earlier keeps its place in the tree.
	Duplicates []*Line

	byID map[string]*Node
}

// Node is an entry in the tree.
type Node struct {
	Line     *Line
	Parent   *Node
	Children []*Node
}

// Node returns the entry with the given id, the last one written when
// several have it.
func (t *Tree) Node(id string) *Node { return t.byID[id] }

// BuildTree links the entries of f, as pi does: by id and parentId,
// the last entry with an id winning, children in file order. f should
// have been through [Migrate].
func BuildTree(f *File) *Tree {
	t := &Tree{byID: map[string]*Node{}}
	for _, l := range f.Entries() {
		n := &Node{Line: l}
		t.Nodes = append(t.Nodes, n)
		if l.ID == "" {
			continue
		}
		if _, dup := t.byID[l.ID]; dup {
			t.Duplicates = append(t.Duplicates, l)
		}
		t.byID[l.ID] = n
	}
	for _, n := range t.Nodes {
		if pid := n.Line.ParentID; pid != "" {
			if p := t.byID[pid]; p != nil {
				n.Parent = p
			} else {
				t.Dangling = append(t.Dangling, n.Line)
			}
		}
	}
	// Cut every cycle at the entry whose link closes it.
	const (
		unseen = iota
		walking
		done
	)
	state := map[*Node]int{}
	for _, n := range t.Nodes {
		var path []*Node
		x := n
		for x != nil && state[x] == unseen {
			state[x] = walking
			path = append(path, x)
			if x.Parent != nil && state[x.Parent] == walking {
				t.Cycles = append(t.Cycles, x.Line)
				x.Parent = nil
				break
			}
			x = x.Parent
		}
		for _, p := range path {
			state[p] = done
		}
	}
	for _, n := range t.Nodes {
		if n.Parent == nil {
			t.Roots = append(t.Roots, n)
		} else {
			n.Parent.Children = append(n.Parent.Children, n)
		}
	}
	if len(t.Nodes) > 0 {
		t.Leaf = t.Nodes[len(t.Nodes)-1]
	}
	return t
}
