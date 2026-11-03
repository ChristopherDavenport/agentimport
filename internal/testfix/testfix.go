// Package testfix copies a fixture to a temporary directory with some
// of its text replaced, for tests that need a variant of a fixture
// without a second copy in testdata, whose IDs would collide.
package testfix

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Variant copies the tree at src into a temporary directory, replacing
// in every file each old string with its new one, in the order given,
// and returns the copy's root. A replacement that matches nothing fails
// the test, so a fixture that changes cannot silently stop exercising
// what the test means.
func Variant(t testing.TB, src string, oldnew ...string) string {
	t.Helper()
	dst := t.TempDir()
	used := make([]bool, len(oldnew)/2)
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(data)
		for i := 0; i+1 < len(oldnew); i += 2 {
			if strings.Contains(s, oldnew[i]) {
				s = strings.ReplaceAll(s, oldnew[i], oldnew[i+1])
				used[i/2] = true
			}
		}
		return os.WriteFile(out, []byte(s), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range used {
		if !u {
			t.Fatalf("fixture %s holds no %q", src, oldnew[2*i])
		}
	}
	return dst
}
