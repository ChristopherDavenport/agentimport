package cline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// seeds adds every fixture file named name to f.
func seeds(f *testing.F, name string) {
	matches, _ := filepath.Glob("convert/testdata/*/*/tasks/*/" + name)
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
}

// FuzzRead reads any bytes as each of a task's files: it must never
// panic, and an element it reads must write back as JSON that reads
// again.
func FuzzRead(f *testing.F) {
	seeds(f, FileAPI)
	seeds(f, FileUI)
	seeds(f, FileContextHistory)
	f.Add([]byte(`[null,1,{"role":"assistant","content":[null,{"type":"tool_use","input":"x"},{"type":7}],"ts":"12"}]`))
	f.Add([]byte(`[{"ts":1,"type":"say","say":"api_req_started","text":"{\"tokensIn\":\"7\"}","conversationHistoryDeletedRange":[2]}]`))
	f.Add([]byte(`[[0,[0,[[0,[[1,"text",["x"],[]]]]]]],[1,[0,{}]]]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		api := ParseMessages(data)
		for _, m := range api.Elems {
			out, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !json.Valid(out) {
				t.Fatalf("invalid JSON written back: %s", out)
			}
			m.Time()
			m.ToolUses()
		}
		ui := ParseUI(data)
		for _, m := range ui.Elems {
			if _, err := json.Marshal(m); err != nil {
				t.Fatalf("marshal: %v", err)
			}
			m.ReqInfo()
			m.DeletedRange()
			m.HistoryIndex()
		}
		ParseContextHistory(data)
		ParseXMLTools(string(data))
		task := ParseTask("t", map[string][]byte{FileAPI: data, FileUI: data, FileContextHistory: data, FileMetadata: data, FileHistoryItem: data})
		task.Units()
	})
}
