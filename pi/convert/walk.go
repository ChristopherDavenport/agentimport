package convert

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/pi"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// pathState is what is in force at a point of the tree, along the path
// from its root. Its maps and slices are replaced, never changed in
// place, so a branch shares its trunk's.
type pathState struct {
	// model is the model the config entries put in force.
	model string
	// sys is pi's prompt and tool state, replayed from the system
	// messages on the path.
	sys sysState
	// conversed says a message other than a system message is on the
	// path, so a system message now updates a conversation under way.
	conversed bool
	// calls maps a native tool call id to the call ID recorded for it,
	// for the calls on the path.
	calls map[string]string
	// answered marks the recorded call IDs on the path that have an
	// output.
	answered map[string]bool
}

func (st *pathState) addCall(native, id string) {
	m := make(map[string]string, len(st.calls)+1)
	for k, v := range st.calls {
		m[k] = v
	}
	m[native] = id
	st.calls = m
}

func (st *pathState) answer(id string) {
	m := make(map[string]bool, len(st.answered)+1)
	for k, v := range st.answered {
		m[k] = v
	}
	m[id] = true
	st.answered = m
}

// walk converts an entry and everything below it. It recurses: a path
// is as deep as its file is long, which Go's stacks hold.
func (c *converter) walk(n *pi.Node, parent string, st pathState) error {
	tail, err := c.entry(n.Line, parent, &st)
	if err != nil {
		return err
	}
	c.b.Bind(nodeKey(n), tail)
	for _, ch := range n.Children {
		if err := c.walk(ch, tail, st); err != nil {
			return err
		}
	}
	return nil
}

// entry converts one entry under tail and returns the new tail.
func (c *converter) entry(l *pi.Line, tail string, st *pathState) (string, error) {
	if l.Err != nil {
		return c.custom(l, tail, "pi/undecodable-line", "a line that does not decode as its type (%v) is a custom entry", l.Err)
	}
	switch l.Type {
	case pi.TypeMessage:
		if l.Message == nil {
			return c.custom(l, tail, "pi/no-message", "a message entry with no message is a custom entry")
		}
		return c.message(l, tail, st)
	case pi.TypeModelChange:
		return c.modelChange(l, tail, st)
	case pi.TypeThinkingLevelChange:
		return c.thinkingLevel(l, tail)
	case pi.TypeUsage:
		return c.usageEntry(l, tail)
	case pi.TypeCompaction:
		return c.compaction(l, tail, st)
	case pi.TypeBranchSummary:
		return c.branchSummary(l, tail)
	case pi.TypeCustom:
		return c.custom(l, tail, "pi/custom", "a custom entry (%s) is a custom entry of namespace %s holding the line", l.CustomType, Source)
	case pi.TypeCustomMessage:
		return c.customMessage(l, tail, st)
	case pi.TypeContextEdit:
		return c.contextEdit(l, tail)
	case pi.TypeLabel:
		return c.label(l, tail)
	case pi.TypeSessionInfo:
		e := &agentsession.InfoEntry{}
		if l.Name != nil {
			e.Name = *l.Name
		}
		return c.mapped(l, tail, e)
	}
	return c.custom(l, tail, "pi/unknown-entry-type", "an entry of unknown type %q is a custom entry", l.Type)
}

// mapped appends e, made from all of line l, with the line's unmapped
// members.
func (c *converter) mapped(l *pi.Line, tail string, e agentsession.Entry) (string, error) {
	if err := c.lineMembers(e, l); err != nil {
		return "", err
	}
	id, err := c.append(tail, e, l)
	if err != nil {
		return "", err
	}
	c.mark(l, agentimport.LineMapped)
	return id, nil
}

// modelChange is a config delta naming the model.
func (c *converter) modelChange(l *pi.Line, tail string, st *pathState) (string, error) {
	e := &agentsession.ConfigEntry{Model: l.ModelID}
	if err := c.b.Member(e, "provider", l.Provider); err != nil {
		return "", err
	}
	st.model = l.ModelID
	return c.mapped(l, tail, e)
}

// efforts maps pi's thinking levels to Responses reasoning efforts.
var efforts = map[string]openresponses.ReasoningEffort{
	"off":     openresponses.ReasoningEffortNone,
	"minimal": openresponses.ReasoningEffortMinimal,
	"low":     openresponses.ReasoningEffortLow,
	"medium":  openresponses.ReasoningEffortMedium,
	"high":    openresponses.ReasoningEffortHigh,
	"xhigh":   openresponses.ReasoningEffortXHigh,
}

// thinkingLevel is a config delta naming the reasoning effort.
func (c *converter) thinkingLevel(l *pi.Line, tail string) (string, error) {
	e := &agentsession.ConfigEntry{}
	effort, ok := efforts[l.ThinkingLevel]
	switch {
	case ok:
	case l.ThinkingLevel == "max":
		effort = openresponses.ReasoningEffortXHigh
		c.addf(agentimport.Lossy, "pi/thinking-level", l, "thinking level max has no Responses effort; it is xhigh, and the level is %s:thinking_level", Source)
	default:
		c.addf(agentimport.Lossy, "pi/thinking-level", l, "thinking level %q has no Responses effort; it is the member %s:thinking_level alone", l.ThinkingLevel, Source)
	}
	if effort != "" {
		e.Reasoning = &openresponses.ReasoningConfig{Effort: effort}
	}
	if !ok {
		if err := c.b.Member(e, "thinking_level", l.ThinkingLevel); err != nil {
			return "", err
		}
	}
	return c.mapped(l, tail, e)
}

// usageEntry is a model call that is not in context: a custom entry
// whose data carries the usage in the Responses shape and the model
// billed, which the format defines as a paid call with no response.
func (c *converter) usageEntry(l *pi.Line, tail string) (string, error) {
	data := map[string]any{"kind": l.Kind}
	if u := usage(l.Usage); u != nil {
		data["usage"] = u
	}
	if l.Model != "" {
		data["model"] = l.Model
	}
	if l.Provider != "" {
		data["provider"] = l.Provider
	}
	if l.Note != "" {
		data["note"] = l.Note
	}
	if l.Usage != nil {
		data[Source+":usage"] = l.Usage
		c.addf(agentimport.Gap, "agentimport/usage-detail", l,
			"cache writes are folded into input_tokens and the cost is the data member %s:usage", Source)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return c.mapped(l, tail, &agentsession.CustomEntry{NS: Source, Data: raw})
}

// label bookmarks the entry a pi label names. pi's labels are free
// text; the format reserves "leaf", so that label is renamed.
func (c *converter) label(l *pi.Line, tail string) (string, error) {
	target, ok := c.lastOf(l.TargetID)
	if !ok {
		return c.custom(l, tail, "pi/label-unlinked", "a label of an entry not converted before it is a custom entry")
	}
	e := &agentsession.LabelEntry{Target: target, Label: l.Label}
	if l.Label != nil && *l.Label == agentsession.LeafLabel {
		renamed := Source + ":" + agentsession.LeafLabel
		e.Label = &renamed
		c.addf(agentimport.Lossy, "pi/label-leaf", l, "the label %q is reserved in the session format; it is written as %q", agentsession.LeafLabel, renamed)
	}
	return c.mapped(l, tail, e)
}

// lastOf returns the last session entry made from the pi entry with the
// given id, which is converted before any entry that names it in a walk
// that reaches it first.
func (c *converter) lastOf(id string) (string, bool) {
	n := c.tree.Node(id)
	if n == nil {
		return "", false
	}
	return c.b.ID(nodeKey(n))
}

// firstOf returns the first session entry made from the pi entry with
// the given id.
func (c *converter) firstOf(id string) (string, bool) {
	n := c.tree.Node(id)
	if n == nil {
		return "", false
	}
	e, ok := c.first[nodeKey(n)]
	return e, ok
}

// onPath reports whether entry id is on the path to tail.
func (c *converter) onPath(id, tail string) bool {
	if id == "" || tail == "" {
		return false
	}
	for _, e := range c.b.S.Path(tail) {
		if e.Base().ID == id {
			return true
		}
	}
	return false
}

func sortStrings(s []string) { sort.Strings(s) }

func joinComma(s []string) string { return strings.Join(s, ", ") }

// itemEntry wraps an item.
func itemEntry(item openresponses.Item) *agentsession.ItemEntry {
	return agentsession.NewItemEntry(item)
}
