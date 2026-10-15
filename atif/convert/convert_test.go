package convert

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata, each holding a
// trajectory.json and the files it names.
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
		p := filepath.Join("testdata", d.Name(), "trajectory.json")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s: %v", d.Name(), err)
		}
		out[d.Name()] = p
	}
	return out
}

// units counts the units of every document in a fixture's directory:
// a conversion of its trajectory.json reads each of them.
func units(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, m := range matches {
		f, err := atif.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		n += f.Units()
	}
	return n
}

// fixed is the created_at a fixture with no timestamp gets, so golden
// files do not turn on a checkout's modification times.
var fixed = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

// convertFixture converts a fixture and checks what every conversion
// must hold: every unit accounted for, every code documented.
func convertFixture(t *testing.T, path string, opts Options) *agentimport.Result {
	t.Helper()
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = fixed
	}
	res, err := ConvertFile(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Report.Lines(), units(t, filepath.Dir(path)); got != want {
		t.Errorf("the report accounts for %d units; the documents hold %d", got, want)
	}
	for _, fd := range res.Report.Findings {
		if fd.Code == "atif/unreached" {
			t.Errorf("unreached step: %+v", fd)
		}
	}
	doccheck.Check(t, Source, res.Report)
	return res
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
			var parts []string
			for _, p := range it.Content {
				switch p := p.(type) {
				case *openresponses.InputText:
					parts = append(parts, p.Text)
				case *openresponses.OutputText:
					parts = append(parts, p.Text)
				case *openresponses.InputImage:
					url := p.ImageURL
					if strings.HasPrefix(url, "data:") {
						url = url[:strings.Index(url, ",")]
					}
					parts = append(parts, "<image "+url+">")
				default:
					parts = append(parts, "<"+p.ContentType()+">")
				}
			}
			out = append(out, string(it.Role)+": "+strings.Join(parts, " "))
		case *openresponses.FunctionCall:
			out = append(out, "call "+it.CallID+" "+it.Name+" "+it.Arguments)
		case *openresponses.FunctionCallOutput:
			out = append(out, "output "+it.CallID+" "+it.Output.String())
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
		name    string
		session int
		want    []string
	}{
		{"minimal", 0, []string{"user: list files", `call c1 bash {"cmd":"ls"}`, "output c1 a.txt", "assistant: done"}},
		// The leading system steps are the instructions, not messages.
		{"system-tools", 0, []string{
			"user: Where is the config read?", "reasoning", "assistant: Searching.",
			`call call_a grep {"pattern":"LoadConfig"}`, `call call_b read {"path":"config.go"}`,
			"output call_a config.go:12: func LoadConfig", "output call_b package config",
			"assistant: In config.go, LoadConfig.",
		}},
		// The file inside the directory is inlined, the URL kept, the
		// missing file and the one outside the directory are named.
		{"multimodal", 0, []string{
			"user: What is on screen? <image data:image/png;base64>",
			`call shot_1 screenshot {}`, "output shot_1 captured[image: images/missing.png]",
			"user: And this one? <image https://example.com/pic.jpg> [image: ../outside.png]",
			"assistant: A green pixel, then a remote picture.",
		}},
		{"audio", 0, []string{"user: Transcribe these. <atif.audio> <atif.audio>", "assistant: Hello; goodbye."}},
		// The RFC's replace boundary: its content is the context.
		{"compaction", 0, []string{
			"user: Summary: prior conversation covered topic X, parts A and B.",
			"user: And part three?", "assistant: Part three is C.",
		}},
		// Terminus 2: a replace boundary with no content takes the
		// handoff after it as its summary; the result that names no
		// call is a user message.
		{"file-refs", 0, []string{
			"user: Summary so far: the build fails at make.\nContinue.",
			"assistant: Analysis: fixed.\nPlan: done.", "call call_1_1 mark_task_complete {}", "output call_1_1 Task marked complete.",
		}},
		// Injection appends; pruning and an unknown boundary keep
		// everything; a replace with no summary keeps everything.
		{"boundaries", 0, []string{
			"user: Plan a trip.", "user: Fact: trains run hourly.", "assistant: Take a train.",
			"system: Reminder: be brief.", "user: Which station?", "assistant: Central.",
		}},
		// Results paired by order; a result naming no call after the
		// outputs; a result naming an earlier step's call answers it; a
		// result naming no call of the document is a user message; the
		// output in a later user step answers its call.
		{"chat-results", 0, []string{
			"user: Fix the test.", "assistant: Two commands.",
			`call t1 bash {"command":"ls"}`, `call t2 bash {"command":"go test"}`,
			"output t1 a_test.go", "output t2 FAIL",
			"assistant: Looking again.", `call t3 bash {"command":"cat a_test.go"}`, "output t3 package a",
			"user: Environment note: 3 steps left.", "user: from nowhere",
			`call t4 bash {"command":"go test"}`, "user: ok", "output t4 PASS",
		}},
		// The claude-code adapter's raw block is the output.
		{"claude-code", 0, []string{
			"user: Read the file.", "reasoning", "assistant: Reading.",
			`call toolu_1 Read {"file_path":"/work/a.txt"}`, "output toolu_1 hello", "assistant: It says hello.",
		}},
		// A continuation starts from its copied history.
		{"continuation", 1, []string{
			"user: Do the task.", "assistant: Step one done.", "user: Handoff: continue with step two.", "assistant: Step two done.",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convertFixture(t, fx[tc.name], Options{})
			got := contextAtLeaf(t, reread(t, res.Sessions[tc.session]))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("context at the leaf:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestSettings checks the config the agent and the leading system
// steps put in force, and a step's model change.
func TestSettings(t *testing.T) {
	res := convertFixture(t, fixtures(t)["system-tools"], Options{})
	ctx, err := reread(t, res.Sessions[0]).Context()
	if err != nil {
		t.Fatal(err)
	}
	s := ctx.Settings
	if s.Model != "openai/gpt-5-mini" || s.Reasoning.Effort != openresponses.ReasoningEffortLow {
		t.Errorf("model %q effort %q", s.Model, s.Reasoning.Effort)
	}
	if s.Instructions != "You are a careful assistant.\n\nAnswer briefly." || len(s.InstructionsParts) != 2 {
		t.Errorf("instructions = %q, %d parts", s.Instructions, len(s.InstructionsParts))
	}
	var tools []string
	for _, tl := range s.Tools {
		tools = append(tools, agentsession.ToolName(tl))
	}
	if strings.Join(tools, " ") != "read grep" {
		t.Errorf("tools = %v", tools)
	}
}

// TestSubagents checks the sessions sub-agents become and how their
// parents link them.
func TestSubagents(t *testing.T) {
	fx := fixtures(t)
	// want is a session's parent, by index in the result, -1 for none,
	// and the call that spawned it.
	type want struct {
		parent    int
		spawnedBy string
	}
	check := func(t *testing.T, res *agentimport.Result, wants []want) {
		t.Helper()
		if len(res.Sessions) != len(wants) {
			t.Fatalf("%d sessions, want %d", len(res.Sessions), len(wants))
		}
		ids := map[string]bool{}
		for i, s := range res.Sessions {
			h := s.Header()
			if ids[h.ID] {
				t.Errorf("session %d repeats id %s", i, h.ID)
			}
			ids[h.ID] = true
			parent := ""
			if w := wants[i]; w.parent >= 0 {
				parent = res.Sessions[w.parent].ID()
			}
			if h.ParentSession != parent || h.SpawnedBy != wants[i].spawnedBy {
				t.Errorf("session %d (%s): parent %q spawned_by %q, want %q %q", i, h.ID, h.ParentSession, h.SpawnedBy, parent, wants[i].spawnedBy)
			}
		}
	}
	// A call's reference is a subsession; a nested one too; an
	// embedded trajectory no reference names has parent_session alone.
	res := convertFixture(t, fx["embedded"], Options{})
	check(t, res, []want{{-1, ""}, {0, "task_1"}, {1, "task_1"}, {0, ""}})
	main := reread(t, res.Sessions[0])
	var link *agentsession.LinkEntry
	var out *agentsession.ItemEntry
	for _, e := range main.Entries() {
		switch e := e.(type) {
		case *agentsession.LinkEntry:
			link = e
		case *agentsession.ItemEntry:
			if _, ok := e.Item.(*openresponses.FunctionCallOutput); ok {
				out = e
			}
		}
	}
	if link == nil || link.Rel != agentsession.RelSubsession || link.CallID != "task_1" || link.Session != res.Sessions[1].ID() {
		t.Fatalf("link = %+v", link)
	}
	if out == nil || out.Parent != link.ID || len(out.Parents) != 1 || out.Parents[0].Session != link.Session || out.Parents[0].Entry != res.Sessions[1].Leaf() {
		t.Errorf("output = %+v, link %s", out, link.ID)
	}

	// Terminus 2's summarisation helpers: no call, a link with none.
	res = convertFixture(t, fx["file-refs"], Options{})
	check(t, res, []want{{-1, ""}, {0, ""}, {0, ""}, {0, ""}})
	n := 0
	for _, e := range reread(t, res.Sessions[0]).Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok && l.Rel == agentsession.RelSubsession && l.CallID == "" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d call-less links, want 3", n)
	}
}

func TestContinuation(t *testing.T) {
	res := convertFixture(t, fixtures(t)["continuation"], Options{})
	if len(res.Sessions) != 2 {
		t.Fatalf("%d sessions", len(res.Sessions))
	}
	first, next := reread(t, res.Sessions[0]), res.Sessions[1]
	if got := first.SupersededBy(); got != next.ID() || next.ID() != "lin-run-cont-1" {
		t.Errorf("continued in %q, successor %q", got, next.ID())
	}
	if next.Header().ParentSession != first.ID() {
		t.Errorf("successor's parent_session = %q", next.Header().ParentSession)
	}
}

// TestCycle checks that a reference to the document itself, a repeated
// reference and a continuation naming itself each end, and that every
// file converts once, through a symbolic link too.
func TestCycle(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"trajectory.json", "child.json", "next.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", "cycle", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("child.json", filepath.Join(dir, "alias.json")); err != nil {
		t.Skip("no symbolic links here:", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "trajectory.json"))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"./child.json"`), []byte(`"alias.json"`), 1)
	if err := os.WriteFile(filepath.Join(dir, "trajectory.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	conv := &Converted{}
	res, err := ConvertFile(filepath.Join(dir, "trajectory.json"), Options{CreatedAt: fixed, Converted: conv})
	if err != nil {
		t.Fatal(err)
	}
	doccheck.Check(t, Source, res.Report)
	if len(res.Sessions) != 3 {
		t.Errorf("%d sessions, want the main one, the child and the continuation", len(res.Sessions))
	}
	codes := res.Report.Codes()
	for _, code := range []string{"atif/ref-repeated", "atif/continuation-repeated", "atif/ref-unresolved"} {
		if !slices.Contains(codes, code) {
			t.Errorf("no %s finding", code)
		}
	}
	for _, name := range []string{"trajectory.json", "child.json", "alias.json", "next.json"} {
		if !conv.Has(filepath.Join(dir, name)) {
			t.Errorf("%s is not recorded as converted", name)
		}
	}
	related := Related(filepath.Join(dir, "trajectory.json"))
	if len(related) != 2 {
		t.Errorf("related = %v", related)
	}
}

// TestConvertWithoutDir checks that Convert reads no file beside the
// document unless it is told where.
func TestConvertWithoutDir(t *testing.T) {
	f, err := atif.ReadFile(fixtures(t)["file-refs"])
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 || !slices.Contains(res.Report.Codes(), "atif/ref-unresolved") {
		t.Errorf("%d sessions, codes %v", len(res.Sessions), res.Report.Codes())
	}
	doccheck.Check(t, Source, res.Report)
}

// TestSlim checks that a slim conversion leaves the sidecars out and
// still accounts for them.
func TestSlim(t *testing.T) {
	for _, name := range []string{"system-tools", "claude-code", "violations", "file-refs"} {
		res := convertFixture(t, fixtures(t)[name], Options{Slim: true})
		for _, s := range res.Sessions {
			var b bytes.Buffer
			if err := agentsession.Write(&b, s); err != nil {
				t.Fatal(err)
			}
			for _, member := range []string{`"atif:extra"`, `"atif:metrics"`, `"atif:step"`, `"atif:root"`, `"atif:final_metrics"`, `"atif:notes"`, `"atif:agent_extra"`} {
				if bytes.Contains(b.Bytes(), []byte(member)) {
					t.Errorf("%s: slim output holds %s", name, member)
				}
			}
		}
	}
}

func TestNotDocument(t *testing.T) {
	for _, in := range []string{"", "null", "[1]", "x", `"s"`} {
		if _, err := atif.Parse([]byte(in)); err == nil {
			t.Errorf("%q parses", in)
		}
	}
}
