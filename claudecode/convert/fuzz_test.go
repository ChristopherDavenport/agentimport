package convert

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherDavenport/agentimport/claudecode"
)

// FuzzConvert converts any bytes as a main transcript: it must never
// panic, and what it converts must verify, which Convert checks itself
// and reports as an error.
func FuzzConvert(f *testing.F) {
	matches, _ := filepath.Glob(filepath.Join("testdata", "*", "*.jsonl"))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		tr, err := claudecode.Read(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		s := &claudecode.Session{ID: "11111111-1111-4111-8111-00000000f022", Main: tr}
		res, err := Convert(s, Options{})
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if got := res.Report.Lines(); got != tr.LineCount() {
			t.Fatalf("the report accounts for %d lines; the input holds %d", got, tr.LineCount())
		}
	})
}
