package convert

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/codex"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtures lists the fixture directories under testdata, each holding
// one rollout.
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
			t.Fatalf("%s: want one rollout, have %v", d.Name(), matches)
		}
		out[d.Name()] = matches[0]
	}
	return out
}

// convertFixture converts a fixture and checks what every conversion
// must hold: every line accounted for, every code documented.
func convertFixture(t *testing.T, path string, opts Options) *agentimport.Result {
	t.Helper()
	f, err := codex.ReadFile(path)
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
		if fd.Code == "codex/unreached" {
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

// render renders context items, one short line per item.
func render(items openresponses.Items) []string {
	var out []string
	for _, it := range items {
		switch it := it.(type) {
		case *openresponses.Message:
			text := it.Text()
			if strings.HasPrefix(text, summaryPrefix) {
				text = "summary" + strings.TrimPrefix(text, summaryPrefix)
			}
			out = append(out, string(it.Role)+": "+strings.ReplaceAll(text, "\n", " "))
		case *openresponses.FunctionCall:
			out = append(out, "call "+it.CallID+" "+it.Name)
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

// TestContexts checks the context a reader resumes with: the history
// Codex would rebuild and send.
func TestContexts(t *testing.T) {
	fx := fixtures(t)
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"plain", []string{
			"developer: <permissions instructions> Workspace write. </permissions instructions>",
			"user: <environment_context>   <cwd>/work/demo</cwd> </environment_context>",
			"user: List the files.", "reasoning", "assistant: Listing.", "call call_1 shell", "output call_1",
			"assistant: Two files.",
		}},
		{"tools", []string{
			"user: Fix the logo.", "call call_p apply_patch", "openai.local_shell_call", "openai.web_search_call",
			"output call_p", "output call_s", "call call_v view_image", "output call_v", "assistant: Fixed.",
		}},
		// The second turn is dropped with the model-switch notice Codex
		// wrote before it.
		{"rollback", []string{
			"user: <environment_context>   <cwd>/work/demo</cwd> </environment_context>",
			"user: First question.", "assistant: First answer.",
			"user: Second question, rephrased.", "assistant: Rephrased answer.",
		}},
		// The window Codex installed, item for item, then the turn goes
		// on.
		{"compaction", []string{
			"developer: <permissions instructions> Workspace write. </permissions instructions>",
			"user: Refactor the parser.", "call call_b shell", "user: summary Read parser.go; tests next.",
			"output call_b", "assistant: Done.",
		}},
		// Copies after the window's new summary are written again, the
		// call under a renamed id its output follows.
		{"window", []string{
			"user: Check the build.", "user: summary The build passed.", "call call_x_1 shell", "output call_x_1",
			"user: And test it.", "assistant: Tested.",
		}},
		{"legacycompaction", []string{
			"user: Write a haiku.", "user: Another.", "user: summary Two haiku written.",
			"user: A third.", "assistant: Rain.",
		}},
		{"legacy", []string{"user: Hello.", "reasoning", "call call_9 shell", "output call_9", "assistant: Hi."}},
		{"subagent", []string{"user: Parent's task.", "codex.agent_message", "codex.agent_message", "assistant: Reported."}},
		{"damaged", []string{"user: Hello.", "call bad_id__1 shell", "assistant: Answer.", "user: Again."}},
		{"context", []string{"user: Go.", "codex.configuration_update", "codex.additional_tools", "assistant: Delivered earlier.", "codex.context_compaction", "assistant: Ok."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convertFixture(t, fx[tc.name], Options{})
			ctx, err := reread(t, res.Sessions[0]).Context()
			if err != nil {
				t.Fatal(err)
			}
			got := render(ctx.Items)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("context at the leaf:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// TestSettings checks the config the session meta and turn contexts put
// in force.
func TestSettings(t *testing.T) {
	fx := fixtures(t)
	res := convertFixture(t, fx["plain"], Options{})
	ctx, err := reread(t, res.Sessions[0]).Context()
	if err != nil {
		t.Fatal(err)
	}
	s := ctx.Settings
	if s.Model != "gpt-5-codex" || s.Instructions != "You are Codex, a coding agent." ||
		s.Reasoning.Effort != openresponses.ReasoningEffortMedium || s.Reasoning.Summary != openresponses.ReasoningSummaryAuto {
		t.Errorf("settings = %+v", s)
	}
	// The rollback restores the first turn's effort; the next turn sets
	// it again.
	res = convertFixture(t, fx["rollback"], Options{})
	ctx, err = reread(t, res.Sessions[0]).Context()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Settings.Reasoning.Effort != openresponses.ReasoningEffortMedium {
		t.Errorf("effort after the rollback = %q", ctx.Settings.Reasoning.Effort)
	}
}

// TestResponses checks the response entries: ids from the usage
// records, usage from the token counts, items tied to them.
func TestResponses(t *testing.T) {
	res := convertFixture(t, fixtures(t)["plain"], Options{})
	s := reread(t, res.Sessions[0])
	var resps []*agentsession.ResponseEntry
	tied := map[string]int{}
	for _, e := range s.Entries() {
		switch e := e.(type) {
		case *agentsession.ResponseEntry:
			resps = append(resps, e)
		case *agentsession.ItemEntry:
			if e.ResponseID != "" {
				tied[e.ResponseID]++
			}
		}
	}
	if len(resps) != 2 || resps[0].ResponseID != "resp_1" || resps[1].ResponseID != "resp_2" {
		t.Fatalf("responses = %+v", resps)
	}
	if tied["resp_1"] != 3 || tied["resp_2"] != 1 {
		t.Errorf("items tied = %v", tied)
	}
	u := resps[1].Usage
	if u == nil || u.InputTokens != 130 || u.InputTokensDetails.CachedTokens != 100 || u.OutputTokens != 10 || resps[1].Status != openresponses.ResponseStatusCompleted {
		t.Errorf("usage = %+v status %q", u, resps[1].Status)
	}
	if _, ok := resps[1].Unknown[Source+":usage"]; !ok {
		t.Error("the cache writes are not kept")
	}
}

// TestSlim checks that a slim conversion leaves the members out and
// still accounts for them.
func TestSlim(t *testing.T) {
	for _, name := range []string{"plain", "context", "subagent"} {
		res := convertFixture(t, fixtures(t)[name], Options{Slim: true})
		var b bytes.Buffer
		if err := agentsession.Write(&b, res.Sessions[0]); err != nil {
			t.Fatal(err)
		}
		for _, member := range []string{`"codex:item"`, `"codex:metadata"`, `"codex:rate_limits"`, `"codex:session_meta"`, `"codex:turn_context"`, `"codex:token_count"`} {
			if bytes.Contains(b.Bytes(), []byte(member)) {
				t.Errorf("%s: slim output holds %s", name, member)
			}
		}
	}
}

// TestSubagent checks a sub-agent's header names its parent.
func TestSubagent(t *testing.T) {
	res := convertFixture(t, fixtures(t)["subagent"], Options{})
	h := res.Sessions[0].Header()
	if h.ParentSession != "01990000-0000-7000-8000-0000000000aa" || h.SpawnedBy != "" {
		t.Errorf("header = %+v", h)
	}
	var link *agentsession.LinkEntry
	for _, e := range res.Sessions[0].Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok {
			link = l
		}
	}
	if link == nil || link.Rel != agentsession.RelForkOf {
		t.Errorf("fork link = %+v", link)
	}
}

// TestNoTimestamps checks a rollout with no time anywhere converts at
// the epoch, never at the clock.
func TestNoTimestamps(t *testing.T) {
	in := `{"id":"01990000-0000-7000-8000-00000000000b"}` + "\n" + `{"type":"message","role":"user","content":[{"type":"input_text","text":"Hi."}]}` + "\n"
	f, err := codex.Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Sessions[0].Entries() {
		if ts := e.Base().Timestamp; ts.Year() != 1970 {
			t.Errorf("%s at %v", e.EntryType(), ts)
		}
	}
}
