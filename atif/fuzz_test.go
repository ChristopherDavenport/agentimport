package atif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRead reads any bytes: it must never panic, a document it reads
// whole must write back, and its units and names must be countable.
func FuzzRead(f *testing.F) {
	matches, _ := filepath.Glob("convert/testdata/*/*.json")
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"steps":[null,1,{"message":[null,{"type":"image","source":7}],"observation":{"results":[{"subagent_trajectory_ref":{"x":1}}]}}]}`))
	f.Add([]byte(`{"subagent_trajectories":[{"subagent_trajectories":[null]}],"steps":[{"tool_calls":"x","metrics":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Parse(data)
		if err != nil {
			return
		}
		file.Units()
		Names(file.Doc, "d")
		if file.Cut != nil {
			return
		}
		out, err := json.Marshal(file.Doc)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := Parse(out); err != nil {
			t.Fatalf("the document written back does not read: %v", err)
		}
	})
}
