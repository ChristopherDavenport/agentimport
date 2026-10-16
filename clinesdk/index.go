package clinesdk

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
)

// Row is a session's row in Cline's index: the sessions table of
// sessions.db, or an entry of sessions.index.json. It records what the
// files leave out: which agent spawned a sub-agent (ParentAgentID, the
// AgentID of another row, else the root's lead), the sub-agent's own
// agent and conversation, and the task it was given (Prompt).
type Row struct {
	SessionID       string
	Source          string
	StartedAt       string
	EndedAt         string
	Status          string
	ParentSessionID string
	ParentAgentID   string
	AgentID         string
	ConversationID  string
	IsSubagent      bool
	Prompt          string
	MessagesPath    string
	// Metadata is the row's metadata, JSON.
	Metadata json.RawMessage
	// Unit names the row in findings.
	Unit string
	// Raw is the whole row as a JSON object, its columns by name.
	Raw json.RawMessage
}

// Index is Cline's index of its sessions, read for one root session
// and its children.
type Index interface {
	// Name names the index in findings: its file name.
	Name() string
	// Family returns the rows of root and of every session whose
	// parent is root, or whose ID starts with root and "__".
	Family(root string) ([]*Row, error)
	// Close releases the index.
	Close() error
}

// FileIndex is sessions.index.json, the index Cline keeps in the
// sessions directory when it cannot open SQLite. It implements Index.
type FileIndex struct {
	name string
	rows map[string]*Row
}

// fileRow is an entry of sessions.index.json: Cline's SessionRow.
type fileRow struct {
	SessionID       string          `json:"sessionId"`
	Source          string          `json:"source"`
	StartedAt       string          `json:"startedAt"`
	EndedAt         *string         `json:"endedAt"`
	Status          string          `json:"status"`
	ParentSessionID *string         `json:"parentSessionId"`
	ParentAgentID   *string         `json:"parentAgentId"`
	AgentID         *string         `json:"agentId"`
	ConversationID  *string         `json:"conversationId"`
	IsSubagent      bool            `json:"isSubagent"`
	Prompt          *string         `json:"prompt"`
	MessagesPath    *string         `json:"messagesPath"`
	Metadata        json.RawMessage `json:"metadata"`
}

// ReadFileIndex reads sessions.index.json. An entry that does not read
// is skipped.
func ReadFileIndex(path string) (*FileIndex, error) {
	data, err := readLimited(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Version  json.RawMessage            `json:"version"`
		Sessions map[string]json.RawMessage `json:"sessions"`
	}
	if json.Unmarshal(data, &doc) != nil || string(doc.Version) != "1" || doc.Sessions == nil {
		return nil, errors.New("the file is not Cline's session index, version 1")
	}
	ix := &FileIndex{name: FileIndexName, rows: map[string]*Row{}}
	for key, raw := range doc.Sessions {
		var r fileRow
		if json.Unmarshal(raw, &r) != nil {
			continue
		}
		if r.SessionID == "" {
			r.SessionID = key
		}
		ix.rows[r.SessionID] = &Row{
			SessionID: r.SessionID, Source: r.Source, StartedAt: r.StartedAt, EndedAt: deref(r.EndedAt), Status: r.Status,
			ParentSessionID: deref(r.ParentSessionID), ParentAgentID: deref(r.ParentAgentID), AgentID: deref(r.AgentID),
			ConversationID: deref(r.ConversationID), IsSubagent: r.IsSubagent, Prompt: deref(r.Prompt),
			MessagesPath: deref(r.MessagesPath), Metadata: r.Metadata, Unit: FileIndexName + ":" + r.SessionID, Raw: raw,
		}
	}
	return ix, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Name is the index's file name.
func (ix *FileIndex) Name() string { return ix.name }

// Family returns the rows of root and its children, by session ID.
func (ix *FileIndex) Family(root string) ([]*Row, error) {
	var out []*Row
	for id, r := range ix.rows {
		if id == root || r.ParentSessionID == root || strings.HasPrefix(id, root+"__") {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

// Close does nothing; the index is read whole.
func (ix *FileIndex) Close() error { return nil }

// OpenFileIndex is ReadFileIndex as an Index, for a caller that takes
// an opener.
func OpenFileIndex(path string) (Index, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return ReadFileIndex(path)
}
