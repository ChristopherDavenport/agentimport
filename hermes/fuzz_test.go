package hermes

import (
	"encoding/json"
	"testing"
)

// FuzzRead reads any bytes as each column a row decodes: a message's
// content, tool calls, reasoning carriers and display metadata, a
// session's model config. It must never panic, and what decodes must
// write back as JSON that reads again.
func FuzzRead(f *testing.F) {
	f.Add([]byte(`[{"id":"c","type":"function","function":{"name":"x","arguments":"{}"}}]`))
	f.Add([]byte(ContentPrefix + `[{"type":"text","text":"a"},{"type":"image_url","image_url":{"url":"data:,"}}]`))
	f.Add([]byte(`"{\"delegation_id\":\"d\"}"`))
	f.Add([]byte(`[null,7,{"function":[1],"id":{}}]`))
	f.Add([]byte(`{"_branched_from":"p","_delegate_from":7}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		s := string(data)
		m := DecodeMessageRow("u", Columns{
			{"id", int64(1)}, {"session_id", "s"}, {"role", "assistant"}, {"content", s}, {"tool_calls", s},
			{"reasoning_details", s}, {"codex_reasoning_items", s}, {"codex_message_items", s},
			{"display_metadata", s}, {"timestamp", 1e9}, {"reasoning_content", data}, {"active", s},
		})
		_ = m.Content.String()
		for _, c := range m.ToolCalls {
			c.PairingID()
			c.ArgumentsText()
			out, err := json.Marshal(c)
			if err != nil || !json.Valid(out) {
				t.Fatalf("marshal %v: %s", err, out)
			}
		}
		for _, raw := range [][]byte{m.ReasoningDetails, m.CodexReasoningItems, m.CodexMessageItems, m.DisplayMetadata} {
			if len(raw) > 0 && !json.Valid(raw) {
				t.Fatalf("invalid JSON kept: %s", raw)
			}
		}
		sess := DecodeSessionRow("u", Columns{{"id", "c"}, {"parent_session_id", "p"}, {"model_config", s}, {"started_at", s}})
		sess.Marker(MarkerBranch)
		Classify(sess, sess)
		if !json.Valid(m.Cols.JSON()) || !json.Valid(sess.Cols.JSON()) {
			t.Fatal("a row writes as invalid JSON")
		}
	})
}
