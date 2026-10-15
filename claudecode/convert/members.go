package convert

import (
	"encoding/json"
	"sort"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentsession"
)

// envelopeExtra are undeclared members every line repeats, which the
// header already says: a copy of the session ID and the session's slug.
var envelopeExtra = map[string]bool{"session_id": true, "slug": true}

// unmapped returns a line's members that no core entry or member says:
// those the package does not declare, and the declared ones the
// conversion does not map where they say something the header, the env
// entries or the file's kind do not.
func (c *converter) unmapped(l *claudecode.Line, skip ...string) map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	for k, v := range nonNull(l.Extra) {
		if !envelopeExtra[k] {
			m[k] = v
		}
	}
	str := func(k, v string) {
		m[k], _ = json.Marshal(v)
	}
	if l.PermissionMode != "" {
		str("permissionMode", l.PermissionMode)
	}
	e := c.envelope
	if l.Has("sessionId") && l.SessionID != e.sessionID {
		str("sessionId", l.SessionID)
	}
	if l.Has("agentId") && l.AgentID != e.agentID {
		str("agentId", l.AgentID)
	}
	if l.Has("isSidechain") && l.IsSidechain != e.sidechain {
		m["isSidechain"], _ = json.Marshal(l.IsSidechain)
	}
	if l.Has("entrypoint") && l.Entrypoint != e.entrypoint {
		str("entrypoint", l.Entrypoint)
	}
	if l.Has("userType") && l.UserType != e.userType {
		str("userType", l.UserType)
	}
	if l.LogicalParentUUID != "" && l.Subtype != claudecode.SubtypeCompactBoundary {
		str("logicalParentUuid", l.LogicalParentUUID)
	}
	if !l.IsSynthetic() {
		if len(l.Error) > 0 && string(l.Error) != "null" {
			m["error"] = l.Error
		}
		if l.APIErrorStatus != 0 {
			m["apiErrorStatus"], _ = json.Marshal(l.APIErrorStatus)
		}
	}
	if len(l.RenderedInHumanTurn) > 0 && len(l.Rendered) > 0 {
		// The other rendering of a queued prompt, which was not sent.
		m["renderedInHumanTurn"], _ = json.Marshal(l.RenderedInHumanTurn)
	}
	for _, k := range skip {
		delete(m, k)
	}
	return m
}

// lineMembers keeps the members of a line the conversion does not map
// in the entry's member claudecode:<name>, conventionally "line".
func (c *converter) lineMembers(e agentsession.Entry, l *claudecode.Line, name string, skip ...string) error {
	m := c.unmapped(l, skip...)
	if len(m) == 0 {
		return nil
	}
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "claudecode/line-members", l, "left out: %s", memberNames(m))
		return nil
	}
	c.addf(agentimport.Kept, "claudecode/line-members", l, "kept in %s:%s: %s", Source, name, memberNames(m))
	return c.b.Member(e, name, m)
}

// messageMembers keeps a message's members beyond those mapped in the
// entry's member claudecode:message.
func (c *converter) messageMembers(e agentsession.Entry, l *claudecode.Line, msg *claudecode.Message, skip ...string) error {
	m := nonNull(msg.Extra)
	if msg.StopSequence != "" {
		m["stop_sequence"], _ = json.Marshal(msg.StopSequence)
	}
	for _, k := range skip {
		delete(m, k)
	}
	if len(m) == 0 {
		return nil
	}
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "claudecode/message-members", l, "left out: %s", memberNames(m))
		return nil
	}
	c.addf(agentimport.Kept, "claudecode/message-members", l, "kept in %s:message: %s", Source, memberNames(m))
	return c.b.Member(e, "message", m)
}

// blockMembers keeps the members of blocks the item does not carry
// (a tool_use's caller, a text's citations) in claudecode:block for one
// block, or claudecode:blocks, aligned with the content, for several.
func (c *converter) blockMembers(e agentsession.Entry, l *claudecode.Line, blocks ...*claudecode.ContentBlock) error {
	all := make([]map[string]json.RawMessage, len(blocks))
	any := false
	var names []string
	for i, b := range blocks {
		if b == nil || b.IsRaw() {
			continue
		}
		m := nonNull(b.Extra)
		if len(m) > 0 {
			all[i] = m
			any = true
			for k := range m {
				names = append(names, k)
			}
		}
	}
	if !any {
		return nil
	}
	sort.Strings(names)
	names = compactStrings(names)
	if c.opts.Slim {
		c.addf(agentimport.Dropped, "claudecode/block-members", l, "left out: %v", names)
		return nil
	}
	c.addf(agentimport.Kept, "claudecode/block-members", l, "block members %v kept in %s:block", names, Source)
	if len(blocks) == 1 {
		return c.b.Member(e, "block", all[0])
	}
	return c.b.Member(e, "blocks", all)
}

func compactStrings(s []string) []string {
	out := s[:0]
	for i, x := range s {
		if i == 0 || x != s[i-1] {
			out = append(out, x)
		}
	}
	return out
}

func sortStrings(s []string) { sort.Strings(s) }
