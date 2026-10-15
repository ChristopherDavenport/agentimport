package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherDavenport/agentimport/pi"
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
	f.Add([]byte(`{"type":"message","id":"a","parentId":null,"message":{"role":"user","content":[null]}}`))
	f.Add([]byte(`{"type":"message","id":"a","parentId":null,"message":{"role":"system","toolsAdded":[null],"toolsRemoved":[null],"sections":{"1":"x"}}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := pi.Read(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
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
