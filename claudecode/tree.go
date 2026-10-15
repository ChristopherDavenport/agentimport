package claudecode

import (
	"sort"
)

// Tree is a transcript's conversation as Claude Code's loader sees it,
// built from the uuid and parentUuid links with the reader's rules
// applied:
//
//   - Every line is indexed before any is linked, since a parent can be
//     written after its child.
//   - The assistant lines of one API response, one content block each
//     and sharing message.id, are one [Response], with the tool_result
//     lines that answer its tool_use blocks. On disk a tool result hangs
//     from the line holding its tool_use while the next block of the
//     response hangs from whatever was written last, so the results of
//     parallel calls are dead ends; inside a Response they are not.
//   - A compact_boundary line is a root on disk; here it hangs from its
//     logicalParentUuid, the line the conversation was at when it was
//     compacted, so the conversation reads through it.
//   - Timestamps are never used to order anything: they go backwards.
//
// Side records, the lines without a uuid, are attached to the unit of
// the tree line written before them, in file order.
type Tree struct {
	// Units are every unit, ordered by the file position of the line
	// each starts at.
	Units []*Unit
	Roots []*Unit
	// Head holds the side records written before the first tree line.
	Head []*Line
	// Orphans are tool_result lines whose tool_use is not in the
	// transcript. Each is also a unit of its own.
	Orphans []*Line
	// Dangling lists lines whose parent is not in the transcript; each
	// is a root here.
	Dangling []*Line
	// Cycles lists lines whose parent links led back to themselves; the
	// first line met of each cycle is a root here.
	Cycles []*Line

	unitOf map[*Line]*Unit
	byUUID map[string]*Line
}

// Unit is a node of the tree: one line, or one API response with the
// lines that answer its tool calls.
type Unit struct {
	// Line is the line of a single-line unit; nil for a response.
	Line     *Line
	Response *Response
	Parent   *Unit
	Children []*Unit
	// Side holds the side records written after this unit's lines and
	// before the next tree line, in file order.
	Side []*Line
	// Logical says Parent is a compaction's logical parent, not the
	// line's parentUuid.
	Logical bool

	first int // file index of the line the unit starts at
}

// Start returns the line the unit starts at: its line, or its
// response's first block.
func (u *Unit) Start() *Line {
	if u.Line != nil {
		return u.Line
	}
	return u.Response.Lines[0]
}

// Lines returns every line of the unit, in file order.
func (u *Unit) Lines() []*Line {
	if u.Line != nil {
		return []*Line{u.Line}
	}
	out := append([]*Line(nil), u.Response.Lines...)
	for _, c := range u.Response.Calls {
		if c.Result != nil && !containsLine(out, c.Result) {
			out = append(out, c.Result)
		}
	}
	for _, l := range u.Response.Extra {
		if !containsLine(out, l) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].No < out[j].No })
	return out
}

func containsLine(ls []*Line, l *Line) bool {
	for _, x := range ls {
		if x == l {
			return true
		}
	}
	return false
}

// Response is one API response: its assistant lines in apiBlockIndex
// order and the tool calls they made.
type Response struct {
	MessageID string
	RequestID string
	Lines     []*Line
	Calls     []*Call
	// Extra holds result lines that answer a call of this response and
	// also carry other blocks, or answer a call twice; they are in the
	// response so their children hang from it.
	Extra []*Line
}

// Last returns the last line of the response, in block order.
func (r *Response) Last() *Line { return r.Lines[len(r.Lines)-1] }

// Call is one tool_use block of a response and the line that answered
// it, if any.
type Call struct {
	Use *ContentBlock
	// Line holds the tool_use block.
	Line *Line
	// Result is the user line holding the tool_result; nil when the call
	// was never answered.
	Result *Line
	// ResultBlock is the tool_result block within Result.
	ResultBlock *ContentBlock
}

// Unit returns the unit a line belongs to; nil for a side record.
func (t *Tree) Unit(l *Line) *Unit { return t.unitOf[l] }

// Line returns the tree line with a uuid.
func (t *Tree) Line(uuid string) *Line { return t.byUUID[uuid] }

// BuildTree builds the conversation tree of a transcript.
func BuildTree(tr *Transcript) *Tree {
	t := &Tree{unitOf: map[*Line]*Unit{}, byUUID: map[string]*Line{}}
	index := map[*Line]int{}
	for i, l := range tr.Lines {
		index[l] = i
		if l.Err == nil && l.InTree() {
			if _, dup := t.byUUID[l.UUID]; !dup {
				t.byUUID[l.UUID] = l
			}
		}
	}

	// Group the assistant lines of one response by message.id.
	responses := map[string]*Response{}
	var order []*Response
	for _, l := range tr.Lines {
		if l.Err != nil || !l.InTree() || l.Type != TypeAssistant || l.Message == nil || l.Message.ID == "" {
			continue
		}
		r := responses[l.Message.ID]
		if r == nil {
			r = &Response{MessageID: l.Message.ID, RequestID: l.RequestID}
			responses[l.Message.ID] = r
			order = append(order, r)
		}
		r.Lines = append(r.Lines, l)
	}
	uses := map[string]*Call{} // tool_use id -> the latest call in file order
	for _, r := range order {
		sort.SliceStable(r.Lines, func(i, j int) bool {
			a, b := r.Lines[i], r.Lines[j]
			if a.APIBlockIndex != b.APIBlockIndex {
				return a.APIBlockIndex < b.APIBlockIndex
			}
			return index[a] < index[b]
		})
		for _, l := range r.Lines {
			for _, b := range l.Message.Content.Blocks {
				if b.Type == BlockToolUse {
					c := &Call{Use: b, Line: l}
					r.Calls = append(r.Calls, c)
					uses[b.ID] = c
				}
			}
		}
	}
	callResponse := map[*Call]*Response{}
	for _, r := range order {
		for _, c := range r.Calls {
			callResponse[c] = r
		}
	}

	// Each line's unit.
	for _, r := range order {
		u := &Unit{Response: r, first: index[r.Lines[0]]}
		for _, l := range r.Lines {
			t.unitOf[l] = u
			if index[l] < u.first {
				u.first = index[l]
			}
		}
		t.Units = append(t.Units, u)
	}
	for _, l := range tr.Lines {
		if l.Err != nil || !l.InTree() || t.unitOf[l] != nil {
			continue
		}
		if results := l.ToolResults(); len(results) > 0 {
			var owner *Response
			for _, b := range results {
				c := uses[b.ToolUseID]
				if c == nil {
					continue
				}
				r := callResponse[c]
				if owner == nil {
					owner = r
				}
				if r != owner {
					continue
				}
				if c.Result == nil {
					c.Result, c.ResultBlock = l, b
				} else if !containsLine(r.Extra, l) {
					r.Extra = append(r.Extra, l)
				}
			}
			if owner != nil {
				if len(results) != len(l.Message.Content.Blocks) && !containsLine(owner.Extra, l) {
					owner.Extra = append(owner.Extra, l)
				}
				t.unitOf[l] = t.unitOf[owner.Lines[0]]
				continue
			}
			t.Orphans = append(t.Orphans, l)
		}
		u := &Unit{Line: l, first: index[l]}
		t.unitOf[l] = u
		t.Units = append(t.Units, u)
	}
	sort.SliceStable(t.Units, func(i, j int) bool { return t.Units[i].first < t.Units[j].first })

	// Link each unit to its parent: the unit of the parent of the line
	// it starts at, or for a compaction boundary its logical parent. A
	// response starts at its first block whose parent is outside it.
	for _, u := range t.Units {
		start := u.Start()
		parent := start.ParentUUID
		if start.Type == TypeSystem && start.Subtype == SubtypeCompactBoundary && parent == "" && start.LogicalParentUUID != "" {
			if _, ok := t.byUUID[start.LogicalParentUUID]; ok {
				parent, u.Logical = start.LogicalParentUUID, true
			}
		}
		if u.Response != nil {
			for _, l := range u.Response.Lines {
				if pl, ok := t.byUUID[l.ParentUUID]; !ok || t.unitOf[pl] != u {
					start, parent = l, l.ParentUUID
					break
				}
			}
		}
		if parent == "" {
			continue
		}
		pl, ok := t.byUUID[parent]
		if !ok {
			t.Dangling = append(t.Dangling, start)
			continue
		}
		if pu := t.unitOf[pl]; pu != u {
			u.Parent = pu
		}
	}
	// Break cycles: the first unit met again on a walk up loses its
	// parent. Each unit is walked once.
	const (
		unseen = iota
		walking
		done
	)
	state := map[*Unit]int{}
	for _, u := range t.Units {
		var chain []*Unit
		for p := u; p != nil && state[p] != done; p = p.Parent {
			if state[p] == walking {
				t.Cycles = append(t.Cycles, p.Start())
				p.Parent = nil
				break
			}
			state[p] = walking
			chain = append(chain, p)
		}
		for _, p := range chain {
			state[p] = done
		}
	}
	for _, u := range t.Units {
		if u.Parent == nil {
			t.Roots = append(t.Roots, u)
		} else {
			u.Parent.Children = append(u.Parent.Children, u)
		}
	}

	// Side records follow the tree line written before them.
	var last *Unit
	for _, l := range tr.Lines {
		if l.Err == nil && l.InTree() {
			if u := t.unitOf[l]; u != nil {
				last = u
			}
			continue
		}
		if last == nil {
			t.Head = append(t.Head, l)
		} else {
			last.Side = append(last.Side, l)
		}
	}
	return t
}
