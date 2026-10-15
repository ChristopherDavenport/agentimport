package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherDavenport/agentimport/codex"
)

// FuzzConvert converts any bytes: it must never panic or fail, and it
// accounts for every line.
func FuzzConvert(f *testing.F) {
	matches, _ := filepath.Glob("testdata/*/*.jsonl")
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"timestamp":"t","type":"response_item","payload":{"type":"message","role":"user","content":[null]}}`))
	f.Add([]byte(`{"timestamp":"t","type":"compacted","payload":{"message":"m","replacement_history":[null,{"type":"function_call_output","call_id":"x","output":null}]}}`))
	f.Add([]byte(`{"timestamp":"t","type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":-3}}`))
	f.Add([]byte(`{"type":"custom_tool_call","name":"x","input":"y"}`))
	f.Add([]byte(`{"timestamp":"t","type":"compacted","payload":{"message":"s","replacement_history":[{"type":"message","role":"user","content":"hi"},{"type":"custom_tool_call","call_id":"c","name":"n","input":1}]}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := codex.Read(bytes.NewReader(data))
		if err != nil {
			return
		}
		lines := file.LineCount()
		res, err := Convert(file, Options{})
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if got := res.Report.Lines(); got != lines {
			t.Fatalf("the report accounts for %d lines; the input holds %d", got, lines)
		}
	})
}
