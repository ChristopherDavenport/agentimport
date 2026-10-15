package claudecode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRead reads any bytes: it must never panic, every line it keeps
// must write back, and the tree must build.
func FuzzRead(f *testing.F) {
	for _, pattern := range []string{"testdata/*.jsonl", "convert/testdata/*/*.jsonl"} {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(data)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tr, err := Read(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range tr.Lines {
			if _, err := json.Marshal(l); err != nil {
				t.Fatalf("line %d: %v", l.No, err)
			}
		}
		BuildTree(tr)
		for _, l := range tr.Lines {
			l.PromptSnapshot()
			l.ToolResults()
			l.ToolUses()
		}
	})
}
