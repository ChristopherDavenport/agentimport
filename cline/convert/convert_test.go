package convert

import (
	"bytes"
	"encoding/json"
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
// requests the native agent rebuilt. Classic's range is an omit:
// messages 0 and 1 where they stand, the outputs of message 1's calls
// (Cline sends "result missing" for them), and the first message after
// the range without its tool results. A Roo summary is a compaction
// whose summary is the text the requests carried and whose pinned items
// are the entries the requests sent with it.
func TestContexts(t *testing.T) {
	fx := fixtures(t)
	env := "<environment_details>"
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"classic-truncation", []string{
			"user: <task> | " + env, "assistant: Step 1.", "call toolu_1 read_file", "output toolu_1 [read_file for 'p1.go'] Result:",
			"user: " + env,
			"assistant: Step 4.", "call toolu_4 read_file", "output toolu_4 [read_file for 'p4.go'] Result:", "user: " + env,
			"assistant: Step 5.", "call toolu_5 read_file", "output toolu_5 [read_file for 'p5.go'] Result:", "user: " + env,
			"assistant: Step 6.", "call toolu_6 read_file",
		}},
		// The range [2,9] keeps the first pair and what follows the
		// summarize call, whose message loses its results.
		{"classic-summarize", []string{
			"user: <task> | " + env, "call toolu_1 read_file", "output toolu_1 [read_file for 'p1.go'] Result:",
			"user: " + env,
			"assistant: Continuing with p5.go.", "call toolu_6 read_file", "output toolu_6 [read_file for 'p5.go'] Result:", "user: " + env,
			"call toolu_7 attempt_completion",
		}},
		{"roo-fresh-start", []string{
			"user: ## Conversation Summary | <system-reminder>Continue the task.</sys",
			"assistant: Turn 3.", "call toolu_3 read_file", "output toolu_3 File: f3.ts", "user: <environment_details>",
			"assistant: Logging is in place.", "call toolu_4 attempt_completion",
		}},
		// Roo 3.40 sent the first message, the summary with the call its
		// first kept result answers, and the kept messages.
		{"roo-tagged-condense", []string{
			"user: Summary: docs for f1 and f2 are drafted.", "user: <task>", "call toolu_2 read_file",
			"output toolu_2 File: f2.ts", "user: <environment_details>",
			"assistant: Turn 3.", "call toolu_3 read_file", "output toolu_3 File: f3.ts", "user: <environment_details>",
			"assistant: Turn 4.", "call toolu_4 read_file", "output toolu_4 File: f4.ts", "user: <environment_details>",
			"assistant: Turn 5.", "call toolu_5 read_file",
		}},
		{"roo-destructive-condense", []string{
			"user: Summary: config keys a and b moved.", "user: <task>",
			"assistant: Turn 3.", "call toolu_3 read_file", "output toolu_3 File: f3.ts", "user: <environment_details>",
			"assistant: Turn 4.", "call toolu_4 read_file", "output toolu_4 File: f4.ts", "user: <environment_details>",
			"assistant: Turn 5.", "call toolu_5 read_file",
		}},
		// The marker and the first message; the orphan condense tag
		// keeps its message.
		{"roo-sliding-window", []string{
			"user: [Sliding window truncation: 2 messages h", "user: <task>",
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

// TestSummarizeRequest checks the request that produced the summary
// sent the whole history: classic's summarize_task sets its range while
// its own response streams, for the request after.
func TestSummarizeRequest(t *testing.T) {
	s := convertFixture(t, fixtures(t)["classic-summarize"], Options{}).Sessions[0]
	for _, e := range s.Entries() {
		r, ok := e.(*agentsession.ResponseEntry)
		if !ok || r.ResponseID != "req_5" {
			continue
		}
		ctx, err := s.ContextAt(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		for _, it := range ctx.Items {
			if _, ok := it.(*openresponses.FunctionCallOutput); ok {
				calls++
			}
		}
		if calls != 4 {
			t.Errorf("the summarize request's context holds %d outputs, want the 4 before it", calls)
		}
		return
	}
	t.Fatal("no response req_5")
}

// TestCompactionRecord checks what every compaction records, in every
// fixture: pinned items are items of entries on the path before
// first_kept, as RFC 0001 requires, and the summary is no copy of an
// entry's item.
func TestCompactionRecord(t *testing.T) {
	for name, path := range fixtures(t) {
		for _, s := range convertFixture(t, path, Options{}).Sessions {
			for _, e := range s.Entries() {
				comp, ok := e.(*agentsession.CompactionEntry)
				if !ok {
					continue
				}
				before := map[string]bool{}
				all := map[string]bool{}
				reached := false
				for _, pe := range s.Path(comp.ID) {
					if pe.Base().ID == comp.FirstKept {
						reached = true
					}
					ie, ok := pe.(*agentsession.ItemEntry)
					if !ok {
						continue
					}
					raw, _ := json.Marshal(ie.Item)
					all[string(raw)] = true
					if !reached {
						before[string(raw)] = true
					}
				}
				for _, p := range comp.Pinned {
					raw, _ := json.Marshal(p)
					if !before[string(raw)] {
						t.Errorf("%s: pinned item on no path before first_kept: %s", name, raw)
					}
				}
				if raw, _ := json.Marshal(comp.Summary); all[string(raw)] {
					t.Errorf("%s: the summary copies an entry's item: %s", name, raw)
				}
			}
		}
	}
}

// TestCallsAnswered checks, at every response of every fixture, that
// each output in the request's context follows its call there.
func TestCallsAnswered(t *testing.T) {
	for name, path := range fixtures(t) {
		for _, s := range convertFixture(t, path, Options{}).Sessions {
			for _, e := range s.Entries() {
				if _, ok := e.(*agentsession.ResponseEntry); !ok {
					continue
				}
				ctx, err := s.ContextAt(e.Base().ID)
				if err != nil {
					t.Fatal(err)
				}
				calls := map[string]bool{}
				for _, it := range ctx.Items {
					switch it := it.(type) {
					case *openresponses.FunctionCall:
						calls[it.CallID] = true
					case *openresponses.FunctionCallOutput:
						if !calls[it.CallID] {
							t.Errorf("%s: at %s an output for %s has no call before it", name, e.Base().ID, it.CallID)
						}
					}
				}
			}
		}
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
	// Cline writes "[tool for 'x'] Result:" before the refusal, the
	// feedback in a text block before the result, a permission or
	// .clineignore refusal inside a tool error, and a text block for a
	// call it skipped after the user rejected an earlier one.
	want := []string{
		"call_a reject human No, keep build.",
		"call_b reject policy The tool execution failed with the following error:\n<error>\nAccess to .env is blocked by the .clineignore file settings. You must try to continue in the task without using this file, or ask the user to update the .clineignore file.\n</error>",
		"call_p reject policy The tool execution failed with the following error:\n<error>\nCommand execution blocked by CLINE_COMMAND_PERMISSIONS: curl is not allowed. You must try a different approach or ask the user to update the permission settings.\n</error>",
		"call_e reject human The user denied this operation.",
		"call_f reject human Skipping tool due to user rejecting a previous tool. [read_file for 'b.go']",
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
		// Roo 3.30 and later write the total input whatever the protocol.
		{"roo-fresh-start", []string{"1000/20 cached 800", "1000/20 cached 800", "3000/20 cached 0", "3500/20 cached 3000"}},
		// Before 3.30 the raw count: the cache is added when the count
		// cannot hold it.
		{"roo-old-usage", []string{"912/30 cached 800", "55/7 cached 50"}},
		// A request whose retries were exhausted, then the resumed one
		// naming the same index: the response is the second's, counted
		// as its second attempt.
		{"classic-resume", []string{"100/20 cached 0", "300/30 cached 90 attempts 2"}},
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

// TestResultOrder checks classic results are written in the order of
// the calls, as Cline sends them, whatever order the message holds.
func TestResultOrder(t *testing.T) {
	s := convertFixture(t, fixtures(t)["classic-reject"], Options{}).Sessions[0]
	var got []string
	for _, e := range s.Path(s.Leaf()) {
		if ie, ok := e.(*agentsession.ItemEntry); ok {
			if out, ok := ie.Item.(*openresponses.FunctionCallOutput); ok {
				got = append(got, out.CallID)
			}
		}
	}
	want := []string{"call_a", "call_b", "call_c", "call_p", "call_d", "call_e", "call_f"}
	if !slices.Equal(got, want) {
		t.Errorf("outputs %v, want %v", got, want)
	}
}

// TestPaidUsage checks the model calls no response entry holds keep
// their usage as a custom entry's top-level usage, which readers count:
// sub-agents that ran in memory, requests a restore deleted, a request
// that failed.
func TestPaidUsage(t *testing.T) {
	fx := fixtures(t)
	for name, want := range map[string][]string{
		"classic-reject": {"50/5", "100/10"},
		"classic-resume": {"0/0"},
	} {
		s := convertFixture(t, fx[name], Options{Slim: name == "classic-resume"}).Sessions[0]
		var got []string
		for _, e := range s.Path(s.Leaf()) {
			ce, ok := e.(*agentsession.CustomEntry)
			if !ok {
				continue
			}
			var d struct {
				Usage *openresponses.Usage `json:"usage"`
			}
			if err := json.Unmarshal(ce.Data, &d); err != nil {
				t.Fatal(err)
			}
			if d.Usage != nil {
				got = append(got, fmt.Sprintf("%d/%d", d.Usage.InputTokens, d.Usage.OutputTokens))
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: paid usage %v, want %v", name, got, want)
		}
	}
}

// TestResumeStatus checks a failed request does not take the response
// of the request a resume made after it.
func TestResumeStatus(t *testing.T) {
	s := convertFixture(t, fixtures(t)["classic-resume"], Options{}).Sessions[0]
	for _, e := range s.Path(s.Leaf()) {
		if r, ok := e.(*agentsession.ResponseEntry); ok && r.ResponseID == "req_2" && r.Status != openresponses.ResponseStatusCompleted {
			t.Errorf("req_2 status %s", r.Status)
		}
	}
}

// rooTask builds a Roo task from its files' contents.
func rooTask(t *testing.T, api, ui string) *cline.Task {
	t.Helper()
	task := cline.ParseTask("t1", map[string][]byte{cline.FileAPI: []byte(api), cline.FileUI: []byte(ui)})
	task.Variant = cline.Roo
	return task
}

// TestRecordMatching checks every summary takes its own UI record by ID
// before any pairs in order: a summary whose record is gone does not
// take the next one's.
func TestRecordMatching(t *testing.T) {
	task := rooTask(t, `[
		{"role":"user","content":"task","ts":1000,"condenseParent":"c-1"},
		{"role":"assistant","content":"a","ts":2000,"condenseParent":"c-1"},
		{"role":"user","content":"Summary one","isSummary":true,"condenseId":"c-1","ts":2001,"condenseParent":"c-2"},
		{"role":"assistant","content":"b","ts":3000,"condenseParent":"c-2"},
		{"role":"user","content":"Summary two","isSummary":true,"condenseId":"c-2","ts":3001},
		{"role":"assistant","content":"c","ts":4000}]`,
		`[{"ts":3002,"type":"say","say":"condense_context","contextCondense":{"condenseId":"c-2","prevContextTokens":500}}]`)
	res, err := Convert(task, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, e := range res.Sessions[0].Entries() {
		if comp, ok := e.(*agentsession.CompactionEntry); ok {
			got = append(got, comp.TokensBefore)
		}
	}
	if !slices.Equal(got, []int{0, 500}) {
		t.Errorf("tokens before %v, want the second summary's record on the second", got)
	}
}

// TestFreshStartOrphan checks a result whose call is before a
// fresh-start summary is left out, as Roo leaves it out.
func TestFreshStartOrphan(t *testing.T) {
	task := rooTask(t, `[
		{"role":"user","content":"task","ts":1000,"condenseParent":"c-1"},
		{"role":"assistant","id":"r1","content":[{"type":"tool_use","id":"t1","name":"read_file","input":{}}],"ts":2000,"condenseParent":"c-1"},
		{"role":"user","content":"Summary","isSummary":true,"condenseId":"c-1","ts":2001},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"late"},{"type":"text","text":"go on"}],"ts":3000},
		{"role":"assistant","id":"r2","content":"done","ts":4000}]`, `[]`)
	res, err := Convert(task, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := contextAtLeaf(t, res.Sessions[0])
	want := []string{"user: Summary", "user: go on", "assistant: done"}
	if !slices.Equal(got, want) {
		t.Errorf("context %v, want %v", got, want)
	}
}
