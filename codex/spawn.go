package codex

import (
	"encoding/json"
	"strings"
)

// ToolSpawnAgent is the name of the tool a thread spawns a sub-agent
// with: the collab tool of 0.98, the multi_agent_v1 namespace's later,
// and MultiAgentV2's.
const ToolSpawnAgent = "spawn_agent"

// NamespaceMultiAgentV1 is the namespace of the multi_agent_v1 tools.
const NamespaceMultiAgentV1 = "multi_agent_v1"

// EventCollabAgentSpawnEnd is the event a spawn ends with, which Codex
// 0.120 to 0.130 wrote to rollouts in extended persistence.
const EventCollabAgentSpawnEnd = "collab_agent_spawn_end"

// IsSpawnCall reports whether an item is a call of Codex's own
// spawn_agent: with no namespace (0.98's collab tool, MultiAgentV2's)
// or in the multi_agent_v1 namespace. A tool of that name in another
// namespace, an MCP server's, is not. A spawn made from code mode is no
// function call of its own, and is not one either.
func (it *Item) IsSpawnCall() bool {
	return it != nil && it.Type == ItemFunctionCall && it.Name == ToolSpawnAgent &&
		(it.Namespace == "" || it.Namespace == NamespaceMultiAgentV1)
}

// SpawnedThread returns the thread a spawn call's output names, or ""
// when it names none. The spawn_agent of 0.98 and of multi_agent_v1
// answers {"agent_id": "<thread id>"}; MultiAgentV2's answers with the
// agent's task name alone, and its thread is in a record beside the
// call (see [Line.Spawn]). The output is a string or a list of content
// items, as any function call output.
func (it *Item) SpawnedThread() string {
	var out struct {
		AgentID string `json:"agent_id"`
	}
	it.outputJSON(&out)
	return out.AgentID
}

// SpawnedTask returns the agent path a MultiAgentV2 spawn's output
// names, {"task_name": "/root/<task>", ...}, or "". The spawned
// thread's session meta carries the same path in its thread_spawn
// source.
func (it *Item) SpawnedTask() string {
	var out struct {
		TaskName string `json:"task_name"`
	}
	it.outputJSON(&out)
	return out.TaskName
}

// outputJSON decodes a function call output's text as JSON into v,
// leaving v as it was when the output is not such JSON.
func (it *Item) outputJSON(v any) {
	if it == nil || it.Type != ItemFunctionCallOutput {
		return
	}
	var text string
	if json.Unmarshal(it.Output, &text) != nil {
		var parts []struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(it.Output, &parts) != nil {
			return
		}
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		text = b.String()
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(text)), v)
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
//     (multi_agent_v1, paginated);
//   - a collab_agent_spawn_end event, whose call_id and new_thread_id
//     name both (0.120 to 0.130, extended persistence).
func (l *Line) Spawn() (callID, threadID string, ok bool) {
	if l == nil || l.Event == nil {
		return "", "", false
	}
	switch l.Event.Type {
	case EventCollabAgentSpawnEnd:
		var e struct {
			CallID      string `json:"call_id"`
			NewThreadID string `json:"new_thread_id"`
		}
		if json.Unmarshal(l.PayloadRaw(), &e) != nil {
			return "", "", false
		}
		if e.CallID == "" || e.NewThreadID == "" {
			return "", "", false
		}
		return e.CallID, e.NewThreadID, true
	case EventSubAgentActivity:
		var a struct {
			EventID       string `json:"event_id"`
			AgentThreadID string `json:"agent_thread_id"`
			Kind          string `json:"kind"`
		}
		if json.Unmarshal(l.PayloadRaw(), &a) != nil || a.Kind != "started" {
			return "", "", false
		}
		if a.EventID == "" || a.AgentThreadID == "" {
			return "", "", false
		}
		return a.EventID, a.AgentThreadID, true
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
