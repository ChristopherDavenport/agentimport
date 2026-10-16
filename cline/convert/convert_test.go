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
	"github.com/ChristopherDavenport/agentimport/cline"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata, each holding a
// storage directory and main.txt naming the task to convert in it.
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

// fixed is the created_at a fixture with no timestamp gets.
var fixed = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)

// convertFixture converts a fixture's task and checks what every
// conversion must hold: every unit of every task it read accounted
// for, every code documented.
func convertFixture(t *testing.T, path string, opts Options) *agentimport.Result {
	t.Helper()
	if opts.CreatedAt.IsZero() {
		opts.CreatedAt = fixed
	}
	done := &Converted{}
	opts.Converted = done
	res, err := ConvertFile(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := cline.FindSessions(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, p := range tasks {
		if !done.Has(p) {
			continue
		}
		task, err := cline.ReadTask(p)
		if err != nil {
			t.Fatal(err)
		}
		want += task.Units()
	}
	if got := res.Report.Lines(); got != want {
		t.Errorf("the report accounts for %d units; the tasks hold %d", got, want)
	}
	for _, fd := range res.Report.Findings {
		if fd.Code == "cline/unreached" {
			t.Errorf("unreached unit: %+v", fd)
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

// TestSlim converts each fixture with Slim: it still accounts for every
// unit and documents every code, and keeps no member of its own beyond
// those that carry what the model saw.
func TestSlim(t *testing.T) {
	for name, path := range fixtures(t) {
		t.Run(name, func(t *testing.T) {
			res := convertFixture(t, path, Options{Slim: true})
			for _, fd := range res.Report.Findings {
				if fd.Kind == agentimport.Kept && fd.Code == "cline/members" {
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
	for _, it := range ctx.Items {
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

// short is a text's first line, cut to 40 bytes.
func short(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

// TestContexts checks the context at the leaf of the fixtures whose
// requests the native agent rebuilt: the first pair and the tail after
// a deleted range, the summary from a fresh start on, the first message
// and the summary before what an older condense kept.
func TestContexts(t *testing.T) {
	fx := fixtures(t)
	env := "<environment_details>"
	for _, tc := range []struct {
		name string
		want []string
	}{
		// Messages 0 and 1, the result Cline sends for message 1's call
		// whose result the range cut, then message 6 without its tool
		// result, which answered a deleted call.
		{"classic-truncation", []string{
			"user: <task> | " + env, "assistant: Step 1.", "call toolu_1 read_file", "output toolu_1 result missing",
			"user: " + env,
			"assistant: Step 4.", "call toolu_4 read_file", "output toolu_4 [read_file for 'p4.go'] Result:", "user: " + env,
			"assistant: Step 5.", "call toolu_5 read_file", "output toolu_5 [read_file for 'p5.go'] Result:", "user: " + env,
			"assistant: Step 6.", "call toolu_6 read_file",
		}},
		// The range [2,9] keeps the first pair and what follows the
		// summarize call, whose message loses its results.
		{"classic-summarize", []string{
			"user: <task> | " + env, "call toolu_1 read_file", "output toolu_1 result missing",
			"user: " + env,
			"assistant: Continuing with p5.go.", "call toolu_6 read_file", "output toolu_6 [read_file for 'p5.go'] Result:", "user: " + env,
			"call toolu_7 attempt_completion",
		}},
		{"roo-fresh-start", []string{
			"user: ## Conversation Summary | <system-reminder>Continue the task.</sys",
			"assistant: Turn 3.", "call toolu_3 read_file", "output toolu_3 File: f3.ts", "user: <environment_details>",
			"assistant: Logging is in place.", "call toolu_4 attempt_completion",
		}},
		{"roo-tagged-condense", []string{
			"user: <task>", "assistant: Summary: docs for f1 and f2 are drafted.",
			"assistant: Turn 3.", "call toolu_3 read_file", "output toolu_3 File: f3.ts", "user: <environment_details>",
			"assistant: Turn 4.", "call toolu_4 read_file", "output toolu_4 File: f4.ts", "user: <environment_details>",
			"assistant: Turn 5.", "call toolu_5 read_file",
		}},
		{"roo-destructive-condense", []string{
			"user: <task>", "assistant: Summary: config keys a and b moved.",
			"assistant: Turn 3.", "call toolu_3 read_file", "output toolu_3 File: f3.ts", "user: <environment_details>",
			"assistant: Turn 4.", "call toolu_4 read_file", "output toolu_4 File: f4.ts", "user: <environment_details>",
			"assistant: Turn 5.", "call toolu_5 read_file",
		}},
		// The marker after the first message; the orphan condense tag
		// keeps its message.
		{"roo-sliding-window", []string{
			"user: <task>", "user: [Sliding window truncation: 2 messages h",
			"assistant: Turn 2.", "call toolu_2 read_file", "output toolu_2 File: f2.ts", "user: <environment_details>",
			"assistant: Turn 3.", "call toolu_3 read_file", "output toolu_3 File: f3.ts", "user: <environment_details>",
			"assistant: Turn 4.", "call toolu_4 read_file",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convertFixture(t, fx[tc.name], Options{})
			got := contextAtLeaf(t, res.Sessions[0])
			if !slices.Equal(got, tc.want) {
				t.Errorf("context:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestDelegation checks a Roo sub-task the parent waited for is a
// subsession linked before the call's output, which names its leaf.
func TestDelegation(t *testing.T) {
	res := convertFixture(t, fixtures(t)["roo-delegation"], Options{})
	if len(res.Sessions) != 2 {
		t.Fatalf("got %d sessions, want the parent and the sub-task", len(res.Sessions))
	}
	parent, child := res.Sessions[0], res.Sessions[1]
	if h := child.Header(); h.ParentSession != parent.ID() || h.SpawnedBy != "toolu_1" {
		t.Errorf("child header: parent %q spawned_by %q", h.ParentSession, h.SpawnedBy)
	}
	if want := agentsession.SubsessionID(parent.ID(), "toolu_1"); child.ID() != want {
		t.Errorf("child ID %q, want %q", child.ID(), want)
	}
	path := parent.Path(parent.Leaf())
	linked := false
	for _, e := range path {
		switch e := e.(type) {
		case *agentsession.LinkEntry:
			linked = e.Rel == agentsession.RelSubsession && e.Session == child.ID() && e.CallID == "toolu_1"
		case *agentsession.ItemEntry:
			if out, ok := e.Item.(*openresponses.FunctionCallOutput); ok && out.CallID == "toolu_1" {
				if !linked {
					t.Error("the output does not follow the link")
				}
				if len(e.Parents) != 1 || e.Parents[0].Session != child.ID() {
					t.Errorf("the output's parents %v do not name the child", e.Parents)
				}
			}
		}
	}
	alone := convertFixture(t, fixtures(t)["roo-delegation"], Options{Alone: true})
	if len(alone.Sessions) != 1 {
		t.Errorf("Alone made %d sessions", len(alone.Sessions))
	}
}

// TestRejection checks a refusal is a reject decision before the
// output, by the human or the policy.
func TestRejection(t *testing.T) {
	res := convertFixture(t, fixtures(t)["classic-reject"], Options{})
	s := res.Sessions[0]
	var got []string
	for _, e := range s.Path(s.Leaf()) {
		if d, ok := e.(*agentsession.DecisionEntry); ok {
			got = append(got, d.CallID+" "+d.Verdict+" "+d.By+" "+d.Reason)
		}
	}
	want := []string{
		"call_a reject human No, keep build.",
		"call_b reject policy Access to .env is blocked by the .clineignore file settings. You must try to continue in the task without using this file, or ask the user to update the .clineignore file.",
	}
	if !slices.Equal(got, want) {
		t.Errorf("decisions:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestUsage checks the usage a request record gives a response, by the
// provider's meaning of its input count, and that retries count once.
func TestUsage(t *testing.T) {
	fx := fixtures(t)
	for _, tc := range []struct {
		name string
		want []string
	}{
		// Anthropic leaves the cache out of tokensIn: 12+800+100.
		{"classic-native", []string{"912/30 cached 800", "1240/50 cached 1000", "1508/20 cached 1500"}},
		// OpenAI's count holds the cache; the retried request counts
		// once and records two attempts.
		{"classic-reject", []string{"500/20 cached 300", "600/30 cached 400 attempts 2", "700/10 cached 0", "800/25 cached 0", "900/0 cached 0"}},
		// Roo writes the total input whatever the protocol.
		{"roo-fresh-start", []string{"1000/20 cached 800", "1000/20 cached 800", "3000/20 cached 0", "3500/20 cached 3000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := convertFixture(t, fx[tc.name], Options{}).Sessions[0]
			var got []string
			for _, e := range s.Path(s.Leaf()) {
				r, ok := e.(*agentsession.ResponseEntry)
				if !ok || r.Usage == nil {
					continue
				}
				line := fmt.Sprintf("%d/%d cached %d", r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.InputTokensDetails.CachedTokens)
				if r.Attempts > 1 {
					line += fmt.Sprintf(" attempts %d", r.Attempts)
				}
				got = append(got, line)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("usage:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestVariant checks the variant is told by the storage directory, and
// by shape under any other.
func TestVariant(t *testing.T) {
	fx := fixtures(t)
	for name, want := range map[string]cline.Variant{
		"classic-native": cline.Classic, "roo-fresh-start": cline.Roo, "roo-reasoning": cline.Roo,
	} {
		task, err := cline.ReadTask(fx[name])
		if err != nil {
			t.Fatal(err)
		}
		if task.Variant != want {
			t.Errorf("%s: variant %s, want %s", name, task.Variant, want)
		}
		if guessed := name == "roo-reasoning"; task.Guessed != guessed {
			t.Errorf("%s: guessed %v (%s)", name, task.Guessed, task.VariantBy)
		}
	}
}
