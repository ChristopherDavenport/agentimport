package agentimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/ChristopherDavenport/agentsession"
)

// SkillReadNS is the namespace of the custom entry that records a skill
// read: agentskill's RecordNS, which agentturn's recorder writes when
// agentskill's skill tool serves one. An imported session writes the
// same record, so a reader finds a skill the same way in a session a
// harness recorded and in one converted from another agent's log.
const SkillReadNS = "agentskill:read"

// SkillRead is the record's data, in the shape agentskill's Read
// writes: the skill's name, and when the log says it, the SKILL.md
// behind the name, the file inside the skill that was served ("" for the
// instructions), and the size and hex SHA-256 digest of the bytes
// served. A member the log does not say is left out.
type SkillRead struct {
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
	Path     string `json:"path,omitempty"`
	Bytes    *int   `json:"bytes,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}

// Served sets the size and digest of the text served.
func (r *SkillRead) Served(text string) {
	n := len(text)
	sum := sha256.Sum256([]byte(text))
	r.Bytes, r.SHA256 = &n, hex.EncodeToString(sum[:])
}

// Entry is the record as a custom entry, naming the call that served
// the skill when one did.
func (r SkillRead) Entry(callID string) (*agentsession.CustomEntry, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return &agentsession.CustomEntry{NS: SkillReadNS, Data: data, CallID: callID}, nil
}
