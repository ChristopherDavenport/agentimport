package convert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherDavenport/agentimport/atif"
)

// FuzzConvert converts any document: it must never panic or fail, and
// it accounts for every unit.
func FuzzConvert(f *testing.F) {
	matches, _ := filepath.Glob("testdata/*/*.json")
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"agentsession":1,"extra":{"agentsession":{"format":"agentsession/0.12"}},"steps":[{"source":"agent","extra":{"openresponses":{"items":[{"type":"function_call","call_id":"a","name":"f","arguments":"{}"}],"response":{"response_id":"r"}}},"observation":{"results":[{"source_call_id":"a","extra":{"openresponses":{"item":{"type":"function_call_output","call_id":"a","output":"x"}}}}]}}]}`))
	f.Add([]byte(`{"extra":{"agentsession":{"format":"agentsession/0.12"}},"steps":[{"source":"system","is_copied_context":true,"extra":{"context_management":{"boundary":"replace"},"openresponses":{"item":{"type":"message","role":"user","content":"s"},"pinned":[null]}}},{"source":"user","is_copied_context":true,"extra":{"openresponses":{"item":null}}}]}`))
	f.Add([]byte(`{"steps":[{"source":"system","extra":{"context_management":{"boundary":"replace"}},"observation":{"results":[{"subagent_trajectory_ref":[{"trajectory_id":"a"},{"trajectory_id":"a"}]}]}}],"subagent_trajectories":[{"trajectory_id":"a","steps":[{"source":"agent","llm_call_count":0,"tool_calls":[{"tool_call_id":""}]}]}]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := atif.Parse(data)
		if err != nil {
			return
		}
		res, err := Convert(file, Options{CreatedAt: fixed})
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if got, want := res.Report.Lines(), file.Units(); got != want {
			t.Fatalf("the report accounts for %d units; the document holds %d", got, want)
		}
	})
}
