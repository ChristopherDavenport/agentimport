package convert

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata, each holding
// one session's main transcript and its subagents directory.
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
		matches, err := filepath.Glob(filepath.Join("testdata", d.Name(), "*.jsonl"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("%s: want one main transcript, have %v", d.Name(), matches)
		}
		out[d.Name()] = matches[0]
	}
	return out
}

// convertFixture converts a fixture and checks what every conversion
// must hold: every line accounted for, every code documented.
func convertFixture(t *testing.T, path string, opts Options) (*claudecode.Session, *agentimport.Result) {
	t.Helper()
	s, err := claudecode.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := s.Main.LineCount()
	for _, a := range s.Agents {
		if a.Transcript != nil {
			want += a.Transcript.LineCount()
		}
	}
	if got := res.Report.Lines(); got != want {
		t.Errorf("the report accounts for %d lines; the files hold %d", got, want)
	}
	if n := res.Report.Tally[agentimport.LineDropped]; n > 0 {
		for _, f := range res.Report.Findings {
			if f.Code == "claudecode/unreached" {
				t.Errorf("unreached line: %+v", f)
			}
		}
	}
	doccheck.Check(t, Source, res.Report)
	return s, res
}

// TestGolden converts each fixture and compares every session, as
// written, and the report with the golden files.
func TestGolden(t *testing.T) {
	for name, path := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			_, res := convertFixture(t, path, Options{})
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

// reread writes a session and reads it back, as a reader of the file
// sees it.
func reread(t *testing.T, s *agentsession.Session) *agentsession.Session {
	t.Helper()
	var b bytes.Buffer
	if err := agentsession.Write(&b, s); err != nil {
		t.Fatal(err)
	}
	r, err := agentsession.Read(&b)
	if err != nil {
		t.Fatal(err)
	}
	return r
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
	for _, it := range ctx.Items {
		switch it := it.(type) {
		case *openresponses.Message:
			out = append(out, string(it.Role)+": "+it.Text())
		case *openresponses.FunctionCall:
			out = append(out, "call "+it.CallID+" "+it.Name+" "+it.Arguments)
		case *openresponses.FunctionCallOutput:
			out = append(out, "output "+it.CallID)
		case *openresponses.ReasoningItem:
			out = append(out, "reasoning")
		default:
			out = append(out, it.ItemType())
		}
	}
	return out
}

func TestContexts(t *testing.T) {
	fx := fixtures(t)
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"plain", []string{"user: What is two plus two?", "reasoning", "assistant: Four."}},
		// Both parallel results are on the path, after the response,
		// and the model's own arguments, which the CLI echoes, are
		// replayed only when the snapshot says it does: here it does
		// not, so the normalised input stands.
		{"parallel", []string{
			"user: List and read.", "reasoning",
			`call toolu_A Bash {"command":"ls -la"}`, `call toolu_B Read {"file_path":"/work/demo/b.png"}`,
			"output toolu_A", "output toolu_B",
			`call toolu_C Bash {"command":"false"}`, "output toolu_C",
			"assistant: Done.",
		}},
		// The leaf is the branch the last last-prompt names, not the
		// one written last.
		{"rewind", []string{"user: Name a colour.", "assistant: Blue.", "user: Name a fruit.", "assistant: Apple."}},
		// The summary, then the preserved turn, then what followed.
		{"compaction", []string{
			"user: Summary: the first task was done.",
			"user: Second task.", "assistant: Second done.",
			"user: Third task.", "assistant: Third done.",
		}},
		// The snapshot says the CLI echoes the model's arguments.
		{"context", []string{
			"user: <local-command-caveat>Caveat.</local-command-caveat>",
			"system: <system-reminder>Today is 2026-09-01.</system-reminder>",
			"user: Run it.",
			`call toolu_X Bash {"command":"echo hi"}`, "output toolu_X",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, res := convertFixture(t, fx[tc.name], Options{})
			got := contextAtLeaf(t, reread(t, res.Sessions[0]))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("context at the leaf:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func TestSubagents(t *testing.T) {
	_, res := convertFixture(t, fixtures(t)["subagents"], Options{})
	main := reread(t, res.Sessions[0])
	if len(res.Sessions) != 4 {
		t.Fatalf("%d sessions; want main, two spawned agents and one unlinked", len(res.Sessions))
	}
	var links []*agentsession.LinkEntry
	for _, e := range main.Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok {
			links = append(links, l)
		}
	}
	if len(links) != 2 {
		t.Fatalf("%d links in the main session; want 2", len(links))
	}
	for i, call := range []string{"toolu_AG", "toolu_FK"} {
		child := res.Sessions[i+1].Header()
		want := agentsession.SubsessionID(main.Header().ID, call)
		if links[i].CallID != call || links[i].Session != want || child.ID != want ||
			child.ParentSession != main.Header().ID || child.SpawnedBy != call {
			t.Errorf("link %d = %+v, child header %+v; want call %s, session %s", i, links[i], child, call, want)
		}
	}
	err := main.VerifyLinks(func(id string) (*agentsession.Session, error) {
		for _, s := range res.Sessions {
			if s.Header().ID == id {
				return s, nil
			}
		}
		return nil, fmt.Errorf("no session %s", id)
	})
	if err != nil {
		t.Error(err)
	}
	team := res.Sessions[3].Header()
	if team.ParentSession != main.Header().ID || team.SpawnedBy != "" {
		t.Errorf("unlinked agent header = %+v", team)
	}
	var src agentimport.Source
	if err := json.Unmarshal(team.Extra[agentimport.SourceMember], &src); err != nil || src.NativeID != "ateam-0003" {
		t.Errorf("unlinked agent source = %+v, %v", src, err)
	}
}

// TestSlim checks that a slim conversion leaves the sidecars out and
// still accounts for them.
func TestSlim(t *testing.T) {
	_, res := convertFixture(t, fixtures(t)["parallel"], Options{Slim: true})
	var b bytes.Buffer
	if err := agentsession.Write(&b, res.Sessions[0]); err != nil {
		t.Fatal(err)
	}
	for _, member := range []string{`"claudecode:tool_use_result"`, `"claudecode:line"`, `"claudecode:message"`} {
		if bytes.Contains(b.Bytes(), []byte(member)) {
			t.Errorf("slim output holds %s", member)
		}
	}
	dropped := false
	for _, f := range res.Report.Findings {
		if f.Code == "claudecode/tool-use-result" && f.Kind == agentimport.Dropped {
			dropped = true
		}
	}
	if !dropped {
		t.Error("the sidecar drop is not reported")
	}
}
