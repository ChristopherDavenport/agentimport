package openhands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRead reads any bytes as an event file, base_state.json and
// meta.json: it must never panic, and what it decodes must write back
// as JSON that reads again.
func FuzzRead(f *testing.F) {
	matches, _ := filepath.Glob("convert/testdata/*/*/*/events/*.json")
	more, _ := filepath.Glob("convert/testdata/*/*/*/base_state.json")
	for _, m := range append(matches, more...) {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"kind":"ActionEvent","thought":"text","thinking_blocks":[null,7],"tool_call":{"id":5},"action":[]}`))
	f.Add([]byte(`{"kind":"ObservationEvent","observation":{"kind":"TerminalObservation","content":[null,"x",{"type":"image","image_urls":"u"}],"metadata":{"exit_code":"0"}}}`))
	f.Add([]byte(`{"stats":{"usage_to_metrics":{"a":7,"b":{"token_usages":[null,{"prompt_tokens":"9"}]}}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		c := Parse("x", map[string][]byte{
			FileBaseState:                      data,
			FileMeta:                           data,
			"events/event-00000-aaaaaaaa.json": data,
		})
		c.Units()
		if c.State != nil {
			if _, err := json.Marshal(c.State); err != nil {
				t.Fatalf("state: %v", err)
			}
			c.State.AgentInfo()
			c.State.WorkingDir()
		}
		if c.Meta != nil {
			if _, err := json.Marshal(c.Meta); err != nil {
				t.Fatalf("meta: %v", err)
			}
		}
		for _, e := range c.Events {
			var vs []any
			if v, err := e.SystemPrompt(); err == nil {
				vs = append(vs, v)
			}
			if v, err := e.Message(); err == nil {
				vs = append(vs, v)
			}
			if v, err := e.Action(); err == nil {
				vs = append(vs, v)
				if a := ParseAction(v.Action); a != nil {
					vs = append(vs, a)
				}
			}
			if v, err := e.Observation(); err == nil {
				vs = append(vs, v)
				if v.Observation != nil {
					v.Observation.Terminal()
				}
			}
			if v, err := e.Condensation(); err == nil {
				vs = append(vs, v)
				v.Forgotten()
				v.Offset()
			}
			for _, v := range vs {
				out, err := json.Marshal(v)
				if err != nil {
					t.Fatalf("marshal: %v", err)
				}
				if !json.Valid(out) {
					t.Fatalf("invalid JSON written back: %s", out)
				}
			}
		}
	})
}
