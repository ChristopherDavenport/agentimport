package pi

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// FuzzRead reads any bytes: it must never panic, every line it keeps
// must write back, migration and the tree must build.
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
			t.Fatal(err)
		}
		for _, l := range file.Lines {
			if _, err := json.Marshal(l); err != nil {
				t.Fatalf("line %d: %v", l.No, err)
			}
			l.MistypedMembers()
			l.EditReplacement()
			l.IsRetainNone()
		}
		Migrate(file)
		BuildTree(file)
	})
}
