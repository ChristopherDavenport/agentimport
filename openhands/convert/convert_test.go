package convert

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata, each holding
// conversations and main.txt naming the one to convert.
func fixtures(t *testing.T) map[string]string {
	t.Helper()
	dirs, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		main, err := os.ReadFile(filepath.Join("testdata", d.Name(), "main.txt"))
		if err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		out[d.Name()] = filepath.Join("testdata", d.Name(), strings.TrimSpace(string(main)))
	}
	return out
}

// convertFixture converts a fixture's conversation and checks what
// every conversion must hold: every unit of every conversation it read
// accounted for, every code documented.
func convertFixture(t *testing.T, path string, opts Options) *agentimport.Result {
	t.Helper()
	done := &Converted{}
	opts.Converted = done
	res, err := ConvertFile(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, dir := range done.list() {
		c, err := openhands.ReadConversation(dir)
		if err != nil {
			t.Fatal(err)
		}
		want += c.Units()
	}
	if got := res.Report.Lines(); got != want {
		t.Errorf("the report accounts for %d units; the conversations hold %d", got, want)
	}
	for _, fd := range res.Report.Findings {
		if fd.Code == "openhands/unreached" {
			t.Errorf("unreached unit: %+v", fd)
		}
	}
	doccheck.Check(t, Source, res.Report)
	return res
}

// list returns the converted directories, for tests.
func (c *Converted) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for d := range c.dirs {
		out = append(out, d)
	}
	return out
}

// TestGolden converts each fixture and compares the sessions, as
// written, and the report with the golden files.
func TestGolden(t *testing.T) {
	for name, path := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			res := convertFixture(t, path, Options{})
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
			var ctx bytes.Buffer
			for i, s := range res.Sessions {
				fmt.Fprintf(&ctx, "session %d %s\n", i, s.ID())
				for _, line := range contextAtLeaf(t, s) {
					fmt.Fprintf(&ctx, "  %s\n", line)
				}
			}
			got["context.txt"] = ctx.Bytes()
			var reqs bytes.Buffer
			for i, s := range res.Sessions {
				fmt.Fprintf(&reqs, "session %d %s\n", i, s.ID())
				for _, e := range s.Entries() {
					r, ok := e.(*agentsession.ResponseEntry)
					if !ok {
						continue
					}
					ctx, err := s.RequestContext(r.ID)
					if err != nil {
						t.Fatal(err)
					}
					fmt.Fprintf(&reqs, "  request of %s:\n", r.ResponseID)
					for _, line := range renderItems(ctx.Items) {
						fmt.Fprintf(&reqs, "    %s\n", line)
					}
				}
			}
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
// unit and documents every code, and keeps no event members.
func TestSlim(t *testing.T) {
	for name, path := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			res := convertFixture(t, path, Options{Slim: true})
			for _, fd := range res.Report.Findings {
				if fd.Kind == agentimport.Kept && (fd.Code == "openhands/event-members" || fd.Code == "openhands/observation" || fd.Code == "openhands/state") {
					t.Errorf("slim keeps members: %+v", fd)
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
	if ins := ctx.Settings.Instructions; ins != "" {
		out = append(out, "instructions: "+short(ins))
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
			out = append(out, "reasoning")
		default:
			out = append(out, it.ItemType())
		}
	}
	return out
}

// short is a text's first line, cut to 50 bytes.
func short(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	if len(s) > 50 {
		s = s[:50]
	}
	return s
}

// TestLocation reads the naive timestamps in the zone given.
func TestLocation(t *testing.T) {
	loc := time.FixedZone("UTC+2", 2*3600)
	res := convertFixture(t, fixtures(t)["linear-chat"], Options{Location: loc})
	h := res.Sessions[0].Header()
	if want := time.Date(2026, 5, 1, 8, 0, 1, 1000, time.UTC); !h.CreatedAt.Equal(want) {
		t.Errorf("created_at %s, want %s", h.CreatedAt, want)
	}
}
