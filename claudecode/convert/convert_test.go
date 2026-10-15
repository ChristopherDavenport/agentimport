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
		if !d.IsDir() || d.Name() == "fuzz" {
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
			line := string(it.Role) + ": " + it.Text()
			for _, p := range it.Content {
				if r, ok := p.(*openresponses.Refusal); ok {
					line += " [refused: " + r.Refusal + "]"
				}
			}
			out = append(out, line)
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
			`call toolu_D Bash {"command":"rm -rf /tmp/x"}`, "output toolu_D",
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
		// The attachment and the meta line written between the two
		// calls stay on the path, after the results, where Claude Code
		// sends them.
		{"between", []string{
			"user: Two commands.",
			`call toolu_P Bash {"command":"pwd"}`, `call toolu_Q Bash {"command":"ls"}`,
			"output toolu_P", "output toolu_Q",
			"user: <system-reminder>The user did not see that output.</system-reminder>",
			"user: <system-reminder>Meta note.</system-reminder>",
			"assistant: Both ran.",
		}},
		// Blocks and lines of shapes the package does not type keep
		// their place: the server tool result is an anthropic item, the
		// line whose apiBlockIndex is a string is a custom entry the
		// rest still hangs from, null blocks are left out, and the
		// synthetic line's call never reaches the context.
		{"crafted", []string{
			"user: Fetch it.", "anthropic.server_tool_use", "anthropic.web_fetch_tool_result",
			"user: after nulls", "user: Keep going.", "assistant: Done.",
		}},
		{"context", []string{
			"user: <local-command-caveat>Caveat.</local-command-caveat>",
			"system: <system-reminder>Today is 2026-09-01.</system-reminder>",
			"user: Run it.",
			`call toolu_X Bash {"command":"echo hi"}`, "output toolu_X",
			// One rendering of the queued prompt, the one sent.
			"system: <system-reminder>The user sent: later (mid-turn)</system-reminder>",
			"user: Refuse this.",
			"assistant: I can't help with that. [refused: Declined for safety.]",
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
	mainID := main.Header().ID
	byNative := map[string]*agentsession.Session{}
	for _, s := range res.Sessions[1:] {
		var src agentimport.Source
		if err := json.Unmarshal(s.Header().Extra[agentimport.SourceMember], &src); err != nil {
			t.Fatal(err)
		}
		byNative[src.NativeID] = s
	}
	if len(res.Sessions) != 6 {
		t.Fatalf("%d sessions; want main and five agents", len(res.Sessions))
	}
	links := map[string]*agentsession.LinkEntry{}
	var outputs []*agentsession.ItemEntry
	var sources []*agentsession.Trigger
	for _, e := range main.Entries() {
		switch e := e.(type) {
		case *agentsession.LinkEntry:
			links[e.CallID] = e
		case *agentsession.ItemEntry:
			if _, ok := e.Item.(*openresponses.FunctionCallOutput); ok {
				outputs = append(outputs, e)
			}
			if e.Source != nil {
				sources = append(sources, e.Source)
			}
		}
	}
	// The agent the call waited for is a subsession; the ones it only
	// started, and the team member, are peers.
	for call, tc := range map[string]struct{ agent, rel string }{
		"toolu_AG": {"a0001", agentsession.RelPeer},
		"toolu_FK": {"a0002", agentsession.RelPeer},
		"toolu_SY": {"a0005", agentsession.RelSubsession},
		"toolu_TM": {"ateam-0003", agentsession.RelPeer},
	} {
		l, child := links[call], byNative[tc.agent]
		if l == nil || child == nil {
			t.Fatalf("%s: link %v, session %v", call, l, child)
		}
		want := agentsession.SubsessionID(mainID, call)
		h := child.Header()
		spawned := ""
		if tc.rel == agentsession.RelSubsession {
			spawned = call
		}
		if l.Rel != tc.rel || l.Session != want || h.ID != want || h.ParentSession != mainID || h.SpawnedBy != spawned {
			t.Errorf("%s: link %s %s, header id %s parent %s spawned_by %q", call, l.Rel, l.Session, h.ID, h.ParentSession, h.SpawnedBy)
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
	// The waited-for agent's output names its leaf.
	sync := byNative["a0005"]
	var named bool
	for _, o := range outputs {
		if o.Item.(*openresponses.FunctionCallOutput).CallID == "toolu_SY" {
			named = len(o.Parents) == 1 && o.Parents[0].Session == sync.Header().ID && o.Parents[0].Entry == sync.Leaf()
		}
	}
	if !named {
		t.Error("the subsession's output does not name its leaf")
	}
	// The peer's report names it as its source.
	if len(sources) != 1 || sources[0].Kind != "peer" || sources[0].Ref != "a0001" || sources[0].Source != Source {
		t.Errorf("sources = %+v", sources)
	}
	// The fork's copy of the parent's response carries no usage.
	for _, e := range byNative["a0002"].Entries() {
		if r, ok := e.(*agentsession.ResponseEntry); ok && r.ResponseID == "msg_31" {
			if r.Usage != nil || string(r.Unknown["claudecode:copied"]) != "true" {
				t.Errorf("copied response = %+v", r)
			}
		}
	}
	lone := byNative["ateam-0006"].Header()
	if lone.ParentSession != mainID || lone.SpawnedBy != "" {
		t.Errorf("unlinked agent header = %+v", lone)
	}
}

// TestDecision checks that a call the user refused is a reject decision
// before its output, by a human, with the output as its reason.
func TestDecision(t *testing.T) {
	_, res := convertFixture(t, fixtures(t)["parallel"], Options{})
	s := reread(t, res.Sessions[0])
	var d *agentsession.DecisionEntry
	for _, e := range s.Entries() {
		if x, ok := e.(*agentsession.DecisionEntry); ok {
			d = x
		}
	}
	if d == nil {
		t.Fatal("no decision")
	}
	if d.CallID != "toolu_D" || d.Verdict != agentsession.VerdictReject || d.By != agentsession.ByHuman ||
		d.Reason != "The user doesn't want to proceed with this tool use." {
		t.Errorf("decision = %+v", d)
	}
	call, _ := s.Entry(d.Target)
	if fc, ok := call.(*agentsession.ItemEntry).Item.(*openresponses.FunctionCall); !ok || fc.CallID != "toolu_D" {
		t.Errorf("decision target = %+v", call)
	}
}

// TestLeaf checks that a session whose native leaf is on an earlier
// branch resumes there, at the end of the response, outputs and all.
func TestLeaf(t *testing.T) {
	_, res := convertFixture(t, fixtures(t)["rewind"], Options{})
	s := reread(t, res.Sessions[0])
	leaf, _ := s.Entry(s.Leaf())
	r, ok := leaf.(*agentsession.ResponseEntry)
	if !ok || r.ResponseID != "msg_11" {
		t.Errorf("leaf = %T %+v; want the response msg_11", leaf, leaf)
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
