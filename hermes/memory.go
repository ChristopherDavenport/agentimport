package hermes

import "sort"

// Memory is a store held in memory, for tests and fuzzing: a database's
// rows, decoded.
type Memory struct {
	StoreName   string
	Schema      int
	SessionRows []*Session
	Strays      []*Stray
	MessagesOf  map[string][]*Message
	Prompts     map[string]string
	Usage       map[string][]Columns
	Delegated   []*Delegation
	// Loose are rows of messages whose session is not in
	// SessionRows.
	Loose []*Message
}

// Name implements [Store].
func (m *Memory) Name() string { return m.StoreName }

// Version implements [Store].
func (m *Memory) Version() (int, bool) { return m.Schema, m.Schema > 0 }

// Sessions implements [Store].
func (m *Memory) Sessions() ([]*Session, []*Stray, error) { return m.SessionRows, m.Strays, nil }

// Messages implements [Store].
func (m *Memory) Messages(id string) ([]*Message, error) {
	out := append([]*Message(nil), m.MessagesOf[id]...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SystemPrompt implements [Store].
func (m *Memory) SystemPrompt(hash string) (string, bool) {
	p, ok := m.Prompts[hash]
	return p, ok
}

// ModelUsage implements [Store].
func (m *Memory) ModelUsage(id string) ([]Columns, error) { return m.Usage[id], nil }

// Delegations implements [Store].
func (m *Memory) Delegations() ([]*Delegation, error) { return m.Delegated, nil }

// Orphans implements [Store].
func (m *Memory) Orphans(known map[string]bool) ([]*Stray, error) {
	var out []*Stray
	for _, msg := range m.Loose {
		if !known[msg.SessionID] {
			out = append(out, &Stray{Unit: msg.Unit, Kind: "message", Why: "its session has no row in sessions"})
		}
	}
	return out, nil
}

var _ Store = (*Memory)(nil)
