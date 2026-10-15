package codex

import (
	"encoding/json"
	"strings"
)

// ToolSpawnAgent is the name of the tool a thread spawns a sub-agent
// with: the collab tool of 0.98, the multi_agent_v1 namespace's later,
// and MultiAgentV2's.
const ToolSpawnAgent = "spawn_agent"

// IsSpawnCall reports whether an item is a call that spawns a sub-agent.
func (it *Item) IsSpawnCall() bool {
	return it != nil && it.Type == ItemFunctionCall && it.Name == ToolSpawnAgent
}

// SpawnedThread returns the thread a spawn call's output names, or ""
// when it names none. The spawn_agent of 0.98 and of multi_agent_v1
// answers {"agent_id": "<thread id>"}; MultiAgentV2's answers with the
// agent's task name alone, and its thread is in a record beside the
// call (see [Line.Spawn]). The output is a string or a list of content
// items, as any function call output.
func (it *Item) SpawnedThread() string {
	if it == nil || it.Type != ItemFunctionCallOutput {
		return ""
	}
	var text string
	if json.Unmarshal(it.Output, &text) != nil {
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(it.Output, &parts) != nil {
			return ""
		}
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		text = b.String()
	}
	var out struct {
		AgentID string `json:"agent_id"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(text)), &out) != nil {
		return ""
	}
	return out.AgentID
}

// Spawn returns the spawn a record beside the call names: the call's id
// and the thread it started. Codex writes one of three, by version and
// history mode:
//
//   - a sub_agent_activity event of kind "started", whose event_id is
//     the spawn call's id (MultiAgentV2, legacy history);
//   - an item_completed event holding a SubAgentActivity item of kind
//     "started", whose id is the call's (MultiAgentV2, paginated);
//   - an item_completed event holding a CollabAgentToolCall item of
//     tool "spawn_agent", whose receiver_thread_ids name the thread
//     (multi_agent_v1, paginated).
func (l *Line) Spawn() (callID, threadID string, ok bool) {
	if l == nil || l.Event == nil {
		return "", "", false
	}
	switch l.Event.Type {
	case EventSubAgentActivity:
		var a struct {
			EventID       string `json:"event_id"`
			AgentThreadID string `json:"agent_thread_id"`
			Kind          string `json:"kind"`
		}
		if json.Unmarshal(l.PayloadRaw(), &a) != nil || a.Kind != "started" {
			return "", "", false
		}
		return a.EventID, a.AgentThreadID, a.EventID != "" && a.AgentThreadID != ""
	case EventItemCompleted:
		var it struct {
			Type              string   `json:"type"`
			ID                string   `json:"id"`
			Kind              string   `json:"kind"`
			AgentThreadID     string   `json:"agent_thread_id"`
			Tool              string   `json:"tool"`
			ReceiverThreadIDs []string `json:"receiver_thread_ids"`
		}
		if json.Unmarshal(l.Event.Item, &it) != nil || it.ID == "" {
			return "", "", false
		}
		switch {
		case it.Type == "SubAgentActivity" && it.Kind == "started" && it.AgentThreadID != "":
			return it.ID, it.AgentThreadID, true
		case it.Type == "CollabAgentToolCall" && it.Tool == ToolSpawnAgent && len(it.ReceiverThreadIDs) > 0:
			return it.ID, it.ReceiverThreadIDs[0], true
		}
	}
	return "", "", false
}
