package convert

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/clinesdk/sqlite"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata. Each is a
// Cline data directory written by hand: sessions/<id>/, and db.sql, the
// rows of db/sessions.db, which is built at test time.
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

// build makes a fixture's data directory in a temporary directory: its
// sessions copied, its database built from db.sql.
func build(t testing.TB, name string) string {
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
		return root
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := sqlite.Fixture(filepath.Join(root, "db", clinesdk.DatabaseName), string(script))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

// convertFixture converts every session of a fixture's data directory
// and checks what every conversion must hold: every unit accounted for,
// every code documented. The results are in the order the sessions are
// found.
func convertFixture(t *testing.T, name string, opts Options) []*agentimport.Result {
	t.Helper()
	root := build(t, name)
	paths, err := clinesdk.FindSessions(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	opts.OpenIndex = sqlite.OpenIndex
	var out []*agentimport.Result
	for _, p := range paths {
		s, err := clinesdk.ReadSession(p)
		if err != nil {
			t.Fatal(err)
		}
		res, err := ConvertFile(p, opts)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(filepath.Dir(p)), err)
		}
		if got, want := res.Report.Lines(), s.Units(); got != want {
			t.Errorf("%s: the report accounts for %d units; the session holds %d", s.ID, got, want)
		}
		for _, fd := range res.Report.Findings {
			if fd.Code == "clinesdk/unreached" {
				t.Errorf("%s: unreached unit: %+v", s.ID, fd)
			}
		}
		doccheck.Check(t, Source, res.Report)
		out = append(out, res)
	}
	return out
}

// TestGolden converts each fixture and compares the sessions, as
// written, the reports, the context at each leaf and each response's
// request with the golden files.
func TestGolden(t *testing.T) {
	for _, name := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			results := convertFixture(t, name, Options{})
			dir := filepath.Join("testdata", name, "golden")
			got := map[string][]byte{}
			var rep, ctx, reqs bytes.Buffer
			i := 0
			for _, res := range results {
				for _, s := range res.Sessions {
					var b bytes.Buffer
					if err := agentsession.Write(&b, s); err != nil {
						t.Fatal(err)
					}
					got[fmt.Sprintf("%d.jsonl", i)] = b.Bytes()
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
					i++
				}
				if err := res.Report.WriteText(&rep); err != nil {
					t.Fatal(err)
				}
			}
			got["report.txt"] = rep.Bytes()
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
// unit and documents every code, and keeps nothing in members.
func TestSlim(t *testing.T) {
	for _, name := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			for _, res := range convertFixture(t, name, Options{Slim: true}) {
				for _, fd := range res.Report.Findings {
					if fd.Kind == agentimport.Kept && (fd.Code == "clinesdk/members" || fd.Code == "clinesdk/manifest" || fd.Code == "clinesdk/display-only") {
						t.Errorf("slim keeps members: %+v", fd)
					}
				}
				for _, s := range res.Sessions {
					for k := range s.Header().Extra {
						if k == Source+":manifest" || k == Source+":file" {
							t.Errorf("slim keeps header member %s", k)
						}
					}
				}
			}
		})
	}
}

// TestAlone converts each fixture with Alone: every session still
// converts, every unit is accounted for, and no child is converted.
func TestAlone(t *testing.T) {
	for _, name := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			for _, res := range convertFixture(t, name, Options{Alone: true}) {
				if len(res.Sessions) > 1 {
					t.Errorf("%d sessions under Alone", len(res.Sessions))
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
