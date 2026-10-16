package clinesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRead reads any bytes as each of a session's files: it must never
// panic, and a message it reads must write back as JSON that reads
// again.
func FuzzRead(f *testing.F) {
	matches, _ := filepath.Glob("convert/testdata/*/sessions/*/*.json")
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"version":1,"messages":[null,1,{"role":"assistant","content":[null,{"type":"tool_use","input":"x"},{"type":7}],"ts":"12"}]}`))
	f.Add([]byte(`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":2,"source_prefix_hash":"sha256:0","messages":[{"role":"user","content":[{"type":"text"}]}]}`))
	f.Add([]byte(`[{"role":"user","content":"<user_command slash=\"team\">x</user_command>","metadata":{"displayOnly":true}}]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		mf := ParseMessagesFile("s.messages.json", data)
		for _, m := range mf.Messages {
			out, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !json.Valid(out) {
				t.Fatalf("invalid JSON written back: %s", out)
			}
			m.Time()
			m.DisplayOnly()
			BoundaryKey(m)
		}
		if _, err := json.Marshal(mf); err != nil {
			t.Fatalf("marshal the file: %v", err)
		}
		source := CanonicalSource(mf.Messages)
		SourcePrefixHash(source, len(source))
		c := ParseCompaction("s.compaction.json", data)
		c.Valid()
		c.Project(mf.Messages)
		s := ParseSession("s", map[string][]byte{"s.json": data, "s.messages.json": data, "s.compaction.json": data, "a.messages.json": data})
		s.Units()
		if s.Manifest != nil {
			s.Manifest.Fork()
			s.Manifest.ImportedFrom()
			s.Manifest.Title()
		}
		FormatDisplayUserInput(string(data))
		ParseTeamTaskSubSessionID(string(data))
		ParseSubSessionID(string(data))
	})
}
