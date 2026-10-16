package convert

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ChristopherDavenport/agentimport/openhands"
)

// FuzzConvert converts any conversation: a state and four event files
// of any bytes. It must never panic or fail, and it accounts for every
// unit.
func FuzzConvert(f *testing.F) {
	dirs, _ := filepath.Glob("testdata/*/*/*")
	for _, d := range dirs {
		state, _ := os.ReadFile(filepath.Join(d, openhands.FileBaseState))
		names, _ := filepath.Glob(filepath.Join(d, openhands.DirEvents, "event-*.json"))
		sort.Strings(names)
		// The first four events, and the four from the third on, which
		// hold the calls and their answers.
		for _, start := range []int{0, 2} {
			var evs [4][]byte
			for k := range evs {
				if start+k < len(names) {
					evs[k], _ = os.ReadFile(names[start+k])
				}
			}
			f.Add(state, evs[0], evs[1], evs[2], evs[3])
		}
	}
	f.Add([]byte(`{"leaf_event_id":"b","stats":{"usage_to_metrics":{"agent":{"token_usages":[{"response_id":"r","prompt_tokens":5}]}}}}`),
		[]byte(`{"kind":"ActionEvent","id":"a","source":"agent","tool_call_id":"c","llm_response_id":"r","thinking_blocks":[{"type":"thinking","thinking":"t"}],"tool_call":{"id":"c","name":"x","arguments":"{}"}}`),
		[]byte(`{"kind":"ObservationEvent","id":"b","parent_id":"a","tool_call_id":"c","observation":{"kind":"TerminalObservation","content":[{"type":"text","text":"o"}]}}`),
		[]byte(`{"kind":"Condensation","id":"d","forgotten_event_ids":["b","a-summary"],"summary":"s","summary_offset":9}`),
		[]byte(`{"kind":"ObservationEvent","id":"e","parent_id":"__root__","tool_call_id":"c","observation":{"kind":"X"}}`))
	f.Add([]byte(nil),
		[]byte(`{"kind":"ActionEvent","id":"a","tool_call_id":"c","llm_response_id":"r","action":{"kind":"TaskAction","prompt":"p"}}`),
		[]byte(`{"kind":"AgentErrorEvent","id":"b","tool_call_id":"c","error":"e"}`),
		[]byte(`{"kind":"ActionEvent","id":"c","tool_call_id":"d","llm_response_id":"r"}`),
		[]byte(`{"kind":"UserRejectObservation","id":"d","parent_id":"a","tool_call_id":"d"}`))
	f.Fuzz(func(t *testing.T, state, e0, e1, e2, e3 []byte) {
		files := map[string][]byte{}
		if len(state) > 0 {
			files[openhands.FileBaseState] = state
		}
		for k, data := range [][]byte{e0, e1, e2, e3} {
			if data != nil {
				files[fmt.Sprintf("events/event-%05d-0000000%d.json", k, k)] = data
			}
		}
		c := openhands.Parse("0123456789abcdef0123456789abcdef", files)
		res, err := Convert(c, Options{})
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if got, want := res.Report.Lines(), c.Units(); got != want {
			t.Fatalf("the report accounts for %d units; the conversation holds %d", got, want)
		}
	})
}
