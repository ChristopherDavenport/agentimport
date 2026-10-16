package opencode

import (
	"errors"
	"sort"
)

// Memory is a store held in memory: what a test or a fuzz target
// builds, or a reader that has its records already.
type Memory struct {
	StoreName   string
	SessionList []*Session
	Strays      []*Stray
	// MessagesOf holds each V1 session's messages, by session ID.
	MessagesOf map[string][]*MessageRecord
	// RowsOf and QueueOf hold each V2 session's rows and queued input.
	RowsOf   map[string][]*Row2
	QueueOf  map[string][]*QueueRow
	Projects map[string]Project
	Migrated bool

	read map[string]bool
}

// Name names the store in findings.
func (m *Memory) Name() string {
	if m.StoreName == "" {
		return "memory"
	}
	return m.StoreName
}

// Sessions lists the sessions.
func (m *Memory) Sessions() ([]*Session, []*Stray, error) {
	return m.SessionList, m.Strays, nil
}

// Messages returns a V1 session's messages, ordered as its generation
// orders them.
func (m *Memory) Messages(s *Session) ([]*MessageRecord, []*Stray, error) {
	m.mark(s)
	var out []*MessageRecord
	var strays []*Stray
	for _, r := range m.MessagesOf[s.ID] {
		if r.Err != nil {
			strays = append(strays, &Stray{Unit: r.Unit, ID: r.ID, Session: s.ID, Kind: "message", Why: r.Err.Error(), Err: r.Err, Malformed: true})
			for _, p := range r.Parts {
				strays = append(strays, &Stray{Unit: p.Unit, Session: s.ID, Kind: "part", Why: "its message cannot be read"})
			}
			continue
		}
		cp := *r
		cp.Parts = nil
		for _, p := range r.Parts {
			if p.Err != nil {
				strays = append(strays, &Stray{Unit: p.Unit, Session: s.ID, Kind: "part", Why: p.Err.Error(), Malformed: true})
				continue
			}
			cp.Parts = append(cp.Parts, p)
		}
		out = append(out, &cp)
	}
	SortMessages(s.Gen, out)
	return out, strays, nil
}

// Rows returns a V2 session's rows in seq order.
func (m *Memory) Rows(s *Session) ([]*Row2, []*QueueRow, error) {
	if s.Gen != G3 {
		return nil, nil, errors.New("not an OpenCode 2 session")
	}
	m.mark(s)
	rows := append([]*Row2(nil), m.RowsOf[s.ID]...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Seq < rows[j].Seq })
	return rows, m.QueueOf[s.ID], nil
}

func (m *Memory) mark(s *Session) {
	if m.read == nil {
		m.read = map[string]bool{}
	}
	m.read[key(s.Gen, s.ID)] = true
}

func key(g Generation, id string) string { return g.String() + "/" + id }

// Orphans lists the records of sessions no conversion read.
func (m *Memory) Orphans(map[string]bool) ([]*Stray, error) {
	var out []*Stray
	ids := make([]string, 0, len(m.MessagesOf))
	for id := range m.MessagesOf {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if m.read[key(G1, id)] || m.read[key(G2, id)] {
			continue
		}
		for _, r := range m.MessagesOf[id] {
			out = append(out, &Stray{Unit: r.Unit, Kind: "message", Why: "its session has no record"})
			for _, p := range r.Parts {
				out = append(out, &Stray{Unit: p.Unit, Kind: "part", Why: "its session has no record"})
			}
		}
	}
	ids = ids[:0]
	seen := map[string]bool{}
	for id := range m.RowsOf {
		seen[id] = true
	}
	for id := range m.QueueOf {
		seen[id] = true
	}
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if m.read[key(G3, id)] {
			continue
		}
		for _, r := range m.RowsOf[id] {
			out = append(out, &Stray{Unit: r.Unit, Kind: "row", Why: "its session has no record"})
		}
		for _, q := range m.QueueOf[id] {
			out = append(out, &Stray{Unit: q.Unit, Kind: "row", Why: "its session has no record"})
		}
	}
	return out, nil
}

// Project returns a project.
func (m *Memory) Project(id string) (Project, bool) {
	p, ok := m.Projects[id]
	return p, ok
}

// MigrationDone reports Migrated.
func (m *Memory) MigrationDone() bool { return m.Migrated }
