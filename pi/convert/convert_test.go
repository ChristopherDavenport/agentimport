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
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentimport/pi"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata, each holding
// one session file; a fork's parent sits in a directory below it.
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
			t.Fatalf("%s: want one session file, have %v", d.Name(), matches)
		}
		out[d.Name()] = matches[0]
	}
	return out
}

// convertFixture converts a fixture and checks what every conversion
// must hold: every line accounted for, every code documented.
func convertFixture(t *testing.T, path string, opts Options) *agentimport.Result {
	t.Helper()
	f, err := pi.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ConvertFile(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Report.Lines(), f.LineCount(); got != want {
		t.Errorf("the report accounts for %d lines; the file holds %d", got, want)
	}
	for _, fd := range res.Report.Findings {
		if fd.Code == "pi/unreached" {
			t.Errorf("unreached line: %+v", fd)
		}
	}
	doccheck.Check(t, Source, res.Report)
	return res
}

// TestGolden converts each fixture and compares the session, as
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
			text := it.Text()
			if i := strings.Index(text, "<summary>\n"); i >= 0 {
				text = "summary " + strings.TrimSuffix(strings.TrimSpace(text[i+len("<summary>\n"):]), "</summary>")
				text = strings.TrimSpace(text)
			}
			out = append(out, string(it.Role)+": "+text)
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
		// Parallel calls and their outputs in order; the image result
		// keeps its parts.
		{"anthropic", []string{
			"user: List the files and read the logo.", "reasoning", "assistant: Looking.",
			`call toolu_A1 bash {"command":"ls"}`, `call toolu_A2 read {"path":"logo.png"}`,
			"output toolu_A1", "output toolu_A2",
			`call toolu_A3 bash {"command":"false"}`, "output toolu_A3",
			"assistant: Two files; the logo is a PNG.",
		}},
		// The Responses call id is split from its item id.
		{"responses", []string{
			"user: What is in the README?", "reasoning", "assistant: Reading it.",
			`call call_1 read {"path":"README.md"}`, "output call_1",
			"reasoning", "assistant: It says Demo.",
		}},
		// The last compaction keeps nothing before it.
		{"compaction", []string{"user: summary All done so far.", "user: Fourth task.", "assistant: Fourth done."}},
		// The omitted prompt is gone, the branch summary stands in for
		// the branch left (and stays, though pi omits it: the format
		// omits items alone), the replaced prompt sits where its edit
		// was, and the reply whose content was replaced stays as it was.
		{"branches", []string{
			"assistant: Blue.", "user: summary Asked why blue; it is calm.",
			"user: Name a fruit.", "assistant: Apple.",
			"user: And a vegetable, briefly?", "assistant: Carrot.",
		}},
		// The branch summary and the label name entries of a branch
		// written before them; the leaf is the last line, an edit that
		// changed nothing, on the X path.
		{"tree", []string{"user: Root.", "assistant: Ok.", "user: Path X.", "assistant: X done."}},
		// The aborted and errored attempts are left out, as pi leaves
		// them out.
		{"failed", []string{"user: Write it.", "user: Try again.", "assistant: Done."}},
		{"customtool", []string{
			"user: Patch it.", `call call_9 apply_patch {"input":"*** Begin Patch\n*** End Patch"}`, "output call_9",
			"assistant: Patched.", `call call_10 read {"path":"a"}`, "output call_10",
		}},
		// The kept range starts after an edit that wrote nothing; the
		// replacement the last compaction keeps without its target is
		// left out; invalid blocks are dropped and a lone surrogate
		// removed.
		{"edges", []string{"user: summary Edited.", "assistant: Edited seen.", "user: After null.", "user: Cut  here.", "assistant: Typed."}},
		{"extensions", []string{
			"user: Start.", "user: Injected context.", "user: A note.",
			"user: Ran `ls`\n```\na\nb\n```", "assistant: Okay.", "user: End.",
		}},
		// The fork's copy of its parent's path is its own.
		{"fork", []string{"user: Plan the work.", "assistant: Step one, step two.", "user: Do step two only.", "assistant: Step two done."}},
		// Version 1: the compaction kept the second exchange by index;
		// the hook message is a custom message.
		{"v1", []string{"user: summary Old first was answered.", "user: Old second.", "assistant: Second reply.", "user: Hook said hi."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convertFixture(t, fx[tc.name], Options{})
			got := contextAtLeaf(t, reread(t, res.Sessions[0]))
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("context at the leaf:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestSettings checks the config the system messages, model changes and
// compactions put in force.
func TestSettings(t *testing.T) {
	fx := fixtures(t)
	res := convertFixture(t, fx["extensions"], Options{})
	ctx, err := reread(t, res.Sessions[0]).Context()
	if err != nil {
		t.Fatal(err)
	}
	s := ctx.Settings
	if s.Model != "gpt-5" || s.Reasoning.Effort != openresponses.ReasoningEffortXHigh {
		t.Errorf("model %q effort %q", s.Model, s.Reasoning.Effort)
	}
	if s.Instructions != "Base.\n\nSection B.\n\nSection C." {
		t.Errorf("instructions = %q", s.Instructions)
	}
	var ids []string
	for _, p := range s.InstructionsParts {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, " ") != "content/1 section/b section/c" {
		t.Errorf("parts = %v", ids)
	}
	var tools []string
	for _, tl := range s.Tools {
		tools = append(tools, agentsession.ToolName(tl))
	}
	if strings.Join(tools, " ") != "read bash grep" {
		t.Errorf("tools = %v", tools)
	}

	res = convertFixture(t, fx["prompt"], Options{})
	ctx, err = reread(t, res.Sessions[0]).Context()
	if err != nil {
		t.Fatal(err)
	}
	// replace is ignored, as pi-ai's getCurrentSystemMessage ignores it;
	// sections named by an integer come first, as a JavaScript object
	// orders them.
	if got := ctx.Settings.Instructions; got != "Base.\n\nNew base.\n\nTwo.\n\nTen.\n\nB." {
		t.Errorf("prompt instructions = %q", got)
	}

	res = convertFixture(t, fx["compaction"], Options{})
	ctx, err = reread(t, res.Sessions[0]).Context()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Settings.Instructions != "Base prompt.\n\nBe brief." || len(ctx.Settings.Tools) != 1 {
		t.Errorf("after the compaction checkpoint: %q, %d tools", ctx.Settings.Instructions, len(ctx.Settings.Tools))
	}
}

func TestFork(t *testing.T) {
	fx := fixtures(t)
	res := convertFixture(t, fx["fork"], Options{})
	s := reread(t, res.Sessions[0])
	if got := s.Header().ParentSession; got != "01a00007-0000-7000-8000-000000000007" {
		t.Errorf("parent_session = %q", got)
	}
	var link *agentsession.LinkEntry
	for _, e := range s.Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok {
			link = l
		}
	}
	if link == nil || link.Rel != agentsession.RelForkOf || link.Session != s.Header().ParentSession {
		t.Errorf("fork link = %+v", link)
	}

	// A fork that copied nothing, made at the parent's first message,
	// reports no copy.
	empty := &pi.File{Lines: []*pi.Line{{Type: pi.TypeSession, ID: "other-parent"}}}
	f0, err := pi.ReadFile(fx["fork"])
	if err != nil {
		t.Fatal(err)
	}
	res, err = Convert(f0, Options{ResolveParent: func(string) (*pi.File, bool) { return empty, true }})
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range res.Report.Findings {
		if fd.Code == "pi/fork-prefix" {
			t.Errorf("a fork that copied nothing reports %+v", fd)
		}
	}

	// Without a resolver the parent is unknown and its path is kept.
	f, err := pi.ReadFile(fx["fork"])
	if err != nil {
		t.Fatal(err)
	}
	res, err = Convert(f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := res.Sessions[0].Header()
	var path string
	if h.ParentSession != "" || json.Unmarshal(h.Extra[Source+":parent_session"], &path) != nil || !strings.HasPrefix(path, "parent/") {
		t.Errorf("unresolved fork header = %+v", h)
	}
}

// TestSlim checks that a slim conversion leaves the sidecars out and
// still accounts for them.
func TestSlim(t *testing.T) {
	for _, name := range []string{"anthropic", "extensions"} {
		res := convertFixture(t, fixtures(t)[name], Options{Slim: true})
		var b bytes.Buffer
		if err := agentsession.Write(&b, res.Sessions[0]); err != nil {
			t.Fatal(err)
		}
		for _, member := range []string{`"pi:details"`, `"pi:message"`, `"pi:line"`, `"pi:bash_execution"`, `"pi:header"`} {
			if bytes.Contains(b.Bytes(), []byte(member)) {
				t.Errorf("%s: slim output holds %s", name, member)
			}
		}
	}
}

func TestShortHash(t *testing.T) {
	// Values from pi-ai's own shortHash, run under node.
	for in, want := range map[string]string{
		"msg_" + strings.Repeat("x", 70): "1f43gnm1wcb1pr",
		"héllo😀":                         "1olj6nu1frhbfw",
	} {
		if got := shortHash(in); got != want {
			t.Errorf("shortHash(%q) = %s, want %s", in, got, want)
		}
	}
}
