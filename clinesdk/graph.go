// Portions of this file are ported from cline/cline
// (https://github.com/cline/cline) at c8f808b5, Copyright 2026 Cline Bot
// Inc., licensed under the Apache License 2.0; they have been modified.
// See THIRD_PARTY_NOTICES.md.

package clinesdk

import (
	"regexp"
	"strings"
)

// The session IDs Cline gives a session's children
// (core/src/session/models/session-graph.ts): a sub-agent's is
// <root>__<agent>, a team task's <root>__teamtask__<agent>__<nanoid6>,
// each part with every run of characters outside letters, digits, '.',
// '_' and '-' made '_', a sub-agent's cut to 180 characters. A child's
// history is <agent>.messages.json, or <agent>__<task>.messages.json for
// a team task, in its root's directory, whatever its nesting.

const teamTaskMarker = "__teamtask__"

var unsafeToken = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// SanitizeSessionToken is Cline's sanitizeSessionToken.
func SanitizeSessionToken(s string) string { return unsafeToken.ReplaceAllString(s, "_") }

// MakeSubSessionID is the session ID Cline gives agent's sub-agent
// session under root.
func MakeSubSessionID(root, agent string) string {
	id := SanitizeSessionToken(root) + "__" + SanitizeSessionToken(agent)
	if len(id) > 180 {
		// JavaScript cuts UTF-16 units; the sanitised ID is ASCII.
		id = id[:180]
	}
	return id
}

// TeamTask is a team task's session ID read back.
type TeamTask struct {
	Root, Agent, Task string
}

// ParseTeamTaskSubSessionID reads a team task's session ID; false when
// id is not one.
func ParseTeamTaskSubSessionID(id string) (TeamTask, bool) {
	i := strings.Index(id, teamTaskMarker)
	if i <= 0 {
		return TeamTask{}, false
	}
	root, rest := id[:i], id[i+len(teamTaskMarker):]
	j := strings.LastIndex(rest, "__")
	if j <= 0 {
		return TeamTask{}, false
	}
	t := TeamTask{Root: root, Agent: rest[:j], Task: rest[j+2:]}
	if t.Agent == "" || t.Task == "" {
		return TeamTask{}, false
	}
	return t, true
}

// ParseSubSessionID reads a sub-agent's session ID, <root>__<agent>;
// false when id is not one, or is a team task's.
func ParseSubSessionID(id string) (root, agent string, ok bool) {
	if _, team := ParseTeamTaskSubSessionID(id); team {
		return "", "", false
	}
	i := strings.Index(id, "__")
	if i <= 0 {
		return "", "", false
	}
	root, agent = id[:i], id[i+2:]
	if agent == "" {
		return "", "", false
	}
	return root, agent, true
}

// FileStem is the stem of a child's messages file in its root's
// directory: the agent for a sub-agent, <agent>__<task> for a team
// task, the ID itself for a root session.
func FileStem(id string) string {
	if t, ok := ParseTeamTaskSubSessionID(id); ok {
		return t.Agent + "__" + t.Task
	}
	if _, agent, ok := ParseSubSessionID(id); ok {
		return agent
	}
	return id
}
