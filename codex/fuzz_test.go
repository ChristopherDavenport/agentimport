package codex

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRead reads any bytes: it must never panic, and every line it keeps
// must write back.
func FuzzRead(f *testing.F) {
	matches, _ := filepath.Glob("convert/testdata/*/*.jsonl")
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Read(bytes.NewReader(data))
		if err != nil {
			return
		}
		for _, l := range file.Lines {
			if _, err := json.Marshal(l); err != nil {
				t.Fatalf("line %d: %v", l.No, err)
			}
			l.MistypedMembers()
			if l.Meta != nil {
				l.Meta.SubAgent()
				l.Meta.SourceKind()
			}
			if l.Item != nil {
				l.Item.Texts()
				l.Item.Parts()
			}
			if l.TurnContext != nil {
				l.TurnContext.Truncation()
				l.TurnContext.SummaryMode()
			}
			if l.InterAgent != nil {
				l.InterAgent.ModelInput()
			}
		}
		file.MetaLine()
	})
}
