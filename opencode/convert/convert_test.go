package convert

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentimport/opencode/sqlite"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata. Each is a
// data directory written by hand: a storage/ tree, and db.sql, the rows
// of opencode.db, which is built at test time.
func fixtures(t *testing.T) []string {
	t.Helper()
	dirs, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range dirs {
		if d.IsDir() {
			out = append(out, d.Name())
		}
	}
	return out
}

// directives reads db.sql's leading comments: "-- schema: a b" names
// the schema files of ../testdata it starts from, "-- open" keeps the
// writer open while the store is converted, so rows written after the
// script's checkpoint are read from the write-ahead log alone.
func directives(script string) (schemas []string, open bool) {
	sc := bufio.NewScanner(strings.NewReader(script))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "--") {
			break
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "--"))
		switch {
		case strings.HasPrefix(line, "schema:"):
			schemas = strings.Fields(strings.TrimPrefix(line, "schema:"))
		case line == "open":
			open = true
		}
	}
	return schemas, open
}

// build makes a fixture's data directory in a temporary directory: its
// storage tree copied, its database built from db.sql. The closer, not
// nil, is the database's writer, for a fixture that keeps it open.
func build(t testing.TB, name string) (string, io.Closer) {
	t.Helper()
	src := filepath.Join("testdata", name)
	root := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "golden" || rel == "db.sql" {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(filepath.Join(src, "db.sql"))
	if os.IsNotExist(err) {
		return root, nil
	}
	if err != nil {
		t.Fatal(err)
	}
	schemas, open := directives(string(script))
	var all strings.Builder
	for _, s := range schemas {
		data, err := os.ReadFile(filepath.Join("..", "testdata", "schema-"+s+".sql"))
		if err != nil {
			t.Fatal(err)
		}
		all.Write(data)
		all.WriteString("\n")
	}
	all.Write(script)
	w, err := sqlite.Fixture(filepath.Join(root, opencode.DatabaseName), all.String())
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if open {
		t.Cleanup(func() { w.Close() })
		return root, w
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return root, nil
}

// units counts the records of a store: its storage files and its
// database's rows, read from the copy the conversion read.
func units(t testing.TB, st *Stores) int {
	t.Helper()
	n := 0
	if info, err := os.Stat(st.Root); err == nil && info.IsDir() {
		n = opencode.Units(st.Root)
	}
	if st.DB != nil {
		m, err := st.DB.(*sqlite.DB).Units()
		if err != nil {
			t.Fatal(err)
		}
		n += m
	}
	return n
}

// convertFixture converts a fixture's data directory and checks what
// every conversion must hold: every record accounted for, every code
// documented.
func convertFixture(t *testing.T, name string, opts Options) *agentimport.Result {
	t.Helper()
	root, _ := build(t, name)
	return convertStore(t, root, opts)
}

// convertStore converts a store as ConvertFile does, and checks it.
func convertStore(t *testing.T, root string, opts Options) *agentimport.Result {
	t.Helper()
	opts.OpenDatabase = sqlite.OpenStore
	st, err := OpenStores(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	res, err := Convert(opts, st.Stores...)
	if err != nil {
		t.Fatal(err)
	}
	st.notes(res.Report)
	if got, want := res.Report.Lines(), units(t, st); got != want {
		t.Errorf("the report accounts for %d records; the store holds %d", got, want)
	}
	for _, fd := range res.Report.Findings {
		if fd.Code == "opencode/unreached" {
			t.Errorf("unreached record: %+v", fd)
		}
	}
	doccheck.Check(t, Source, res.Report)
	return res
}

// TestGolden converts each fixture and compares the sessions, as
// written, the report, the context at each leaf and each response's
// request with the golden files.
func TestGolden(t *testing.T) {
	for _, name := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			res := convertFixture(t, name, Options{})
			dir := filepath.Join("testdata", name, "golden")
			got := map[string][]byte{}
			for i, s := range res.Sessions {
				var b bytes.Buffer
				if err := agentsession.Write(&b, s); err != nil {
					t.Fatal(err)
				}
				got[fmt.Sprintf("%d.jsonl", i)] = b.Bytes()
			}
			var rep bytes.Buffer
			if err := res.Report.WriteText(&rep); err != nil {
				t.Fatal(err)
			}
			got["report.txt"] = rep.Bytes()
			var ctx, reqs bytes.Buffer
			for i, s := range res.Sessions {
				fmt.Fprintf(&ctx, "session %d %s\n", i, s.ID())
				for _, line := range contextAtLeaf(t, s) {
					fmt.Fprintf(&ctx, "  %s\n", line)
				}
				fmt.Fprintf(&reqs, "session %d %s\n", i, s.ID())
				for _, e := range s.Entries() {
					r, ok := e.(*agentsession.ResponseEntry)
					if !ok {
						continue
					}
					c, err := s.RequestContext(r.ID)
					if err != nil {
						t.Fatal(err)
					}
					fmt.Fprintf(&reqs, "  request of %s:\n", r.ResponseID)
					for _, line := range renderItems(c.Items) {
						fmt.Fprintf(&reqs, "    %s\n", line)
					}
				}
			}
			got["context.txt"] = ctx.Bytes()
			got["requests.txt"] = reqs.Bytes()
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for f, data := range got {
					if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			files, _ := os.ReadDir(dir)
			if len(files) != len(got) {
				t.Errorf("golden has %d files; the conversion made %d", len(files), len(got))
			}
			for f, data := range got {
				want, err := os.ReadFile(filepath.Join(dir, f))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, data) {
					t.Errorf("%s differs from golden; run go test -update and review the diff\ngot:\n%s", f, data)
				}
			}
		})
	}
}

// TestSlim converts each fixture with Slim: it still accounts for every
// record and documents every code, and keeps nothing in members.
func TestSlim(t *testing.T) {
	for _, name := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			res := convertFixture(t, name, Options{Slim: true})
			for _, fd := range res.Report.Findings {
				if fd.Kind == agentimport.Kept && (fd.Code == "opencode/members" || fd.Code == "opencode/session-record" || fd.Code == "opencode/custom") {
					t.Errorf("slim keeps members: %+v", fd)
				}
			}
		})
	}
}

// TestAlone converts each fixture with Alone: every session still
// converts and every record is accounted for.
func TestAlone(t *testing.T) {
	for _, name := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			res := convertFixture(t, name, Options{Alone: true})
			for _, s := range res.Sessions {
				if s.Header().SpawnedBy != "" {
					t.Errorf("session %s is a subsession under Alone", s.ID())
				}
			}
		})
	}
}

// contextAtLeaf renders the context a reader resumes with, one short
// line per item.
func contextAtLeaf(t *testing.T, s *agentsession.Session) []string {
	t.Helper()
	ctx, err := s.Context()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	if m := ctx.Settings.Model; m != "" {
		out = append(out, "model: "+m)
	}
	return append(out, renderItems(ctx.Items)...)
}

// renderItems renders items one short line each.
func renderItems(items openresponses.Items) []string {
	var out []string
	for _, it := range items {
		switch it := it.(type) {
		case *openresponses.Message:
			var parts []string
			for _, p := range it.Content {
				switch p := p.(type) {
				case *openresponses.InputText:
					parts = append(parts, short(p.Text))
				case *openresponses.OutputText:
					parts = append(parts, short(p.Text))
				default:
					parts = append(parts, "<"+p.ContentType()+">")
				}
			}
			out = append(out, string(it.Role)+": "+strings.Join(parts, " | "))
		case *openresponses.FunctionCall:
			out = append(out, "call "+it.CallID+" "+it.Name)
		case *openresponses.FunctionCallOutput:
			out = append(out, "output "+it.CallID+" "+short(it.Output.String()))
		case *openresponses.ReasoningItem:
			s := "reasoning"
			if t := it.Summary.Text(); t != "" {
				s += " " + short(t)
			}
			if it.EncryptedContent != "" {
				s += " [encrypted]"
			}
			out = append(out, s)
		default:
			out = append(out, it.ItemType())
		}
	}
	return out
}

// short is a text's first line, cut to 50 bytes.
func short(s string) string {
	s = strings.TrimLeft(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}
