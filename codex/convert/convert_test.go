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
		// Up to 0.65 a response's calls are each followed by its output.
		{"batch", []string{
			"user: <environment_context>   <cwd>/work/demo</cwd> </environment_context>",
			"user: Build and test.", "reasoning", "call call_b shell", "output call_b", "call call_t shell", "output call_t",
			"assistant: Both pass.",
		}},
		{"streamed", []string{
			"user: Build and test.", "reasoning", "call call_b shell", "call call_t shell", "output call_b", "output call_t",
			"assistant: Both pass.",
		}},
		{"tools", []string{
			"user: Fix the logo.", "openai.tool_search_call", "openai.tool_search_output", "call call_p apply_patch",
			"openai.local_shell_call", "openai.web_search_call", "output call_p", "output call_s",
			"call call_v view_image", "output call_v", "assistant: Fixed.",
		}},
		// The second turn is dropped with the model-switch notice Codex
		// wrote before it.
		{"rollback", []string{
			"user: <environment_context>   <cwd>/work/demo</cwd> </environment_context>",
			"user: First question.", "assistant: First answer.",
			"user: Second question, rephrased.", "assistant: Rephrased answer.",
		}},
		// Rolling back the turn a compaction was written in undoes it.
		{"rollbackcompaction", []string{
			"developer: <permissions instructions> Workspace write. </permissions instructions>",
			"user: First.", "assistant: One.",
		}},
		// Two turns back is the person's second message; the developer
		// bundle and the user message holding a notice above it are
		// trimmed with it, and another agent's instruction is a turn.
		{"turns", []string{"user: One.", "assistant: A."}},
		// The window Codex installed, item for item (an item of it that
		// does not decode left out), then the turn goes on.
		{"compaction", []string{
			"developer: <permissions instructions> Workspace write. </permissions instructions>",
			"user: Refactor the parser.", "call call_b shell", "output call_b", "user: summary Read parser.go; tests pass.",
			"assistant: Done.",
		}},
		// Copies after the window's new summary are written again, the
		// call under a renamed id its output follows.
		{"window", []string{
			"user: Check the build.", "user: summary The build passed.", "call call_x_1 shell", "output call_x_1",
			"user: And test it.", "assistant: Tested.",
		}},
		// A message with an image keeps its text alone.
		{"legacycompaction", []string{
			"user: Write a haiku.", "user: Another, like this:", "user: summary Two haiku written.",
			"user: A third.", "assistant: Rain.",
		}},
		{"legacy", []string{"user: Hello.", "reasoning", "call call_9 shell", "output call_9", "assistant: Hi."}},
		{"subagent", []string{"user: Parent's task.", "assistant: Parent's answer.", "codex.agent_message", "codex.agent_message", "assistant: Reported."}},
		{"damaged", []string{"user: Hello.", "call bad_id__1 shell", "call call_2 apply_patch", "assistant: Answer.", "user: Again."}},
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

// responses returns a session's response entries and how many items
// name each.
func responses(t *testing.T, s *agentsession.Session) ([]*agentsession.ResponseEntry, map[string]int) {
	t.Helper()
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
	return resps, tied
}

// TestResponses checks the response entries of each record order: ids
// from the usage records, usage from the records and token counts,
// items tied to them, the count's members kept.
func TestResponses(t *testing.T) {
	fx := fixtures(t)
	for _, tc := range []struct {
		name  string
		ids   []string
		input []int
		tied  []int
	}{
		{"plain", []string{"resp_1", "resp_2"}, []int{100, 130}, []int{3, 1}},
		{"batch", []string{"codex-line-8", "codex-line-15"}, []int{50, 70}, []int{3, 1}},
		{"streamed", []string{"codex-line-4", "codex-line-10"}, []int{50, 70}, []int{3, 1}},
		{"tools", []string{"resp_t1", "resp_t2", "resp_t3"}, []int{50, 100, 150}, []int{5, 1, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convertFixture(t, fx[tc.name], Options{})
			resps, tied := responses(t, res.Sessions[0])
			if len(resps) != len(tc.ids) {
				t.Fatalf("%d responses, want %d", len(resps), len(tc.ids))
			}
			for i, r := range resps {
				if r.ResponseID != tc.ids[i] || r.Usage == nil || r.Usage.InputTokens != tc.input[i] ||
					tied[r.ResponseID] != tc.tied[i] || r.Status != openresponses.ResponseStatusCompleted {
					t.Errorf("response %d = %s usage %+v status %q, %d items; want %s, %d input tokens, %d items",
						i, r.ResponseID, r.Usage, r.Status, tied[r.ResponseID], tc.ids[i], tc.input[i], tc.tied[i])
				}
				if _, ok := r.Unknown[Source+":rate_limits"]; !ok {
					t.Errorf("response %d keeps no rate limits", i)
				}
			}
		})
	}
	res := convertFixture(t, fx["plain"], Options{})
	resps, _ := responses(t, res.Sessions[0])
	if _, ok := resps[1].Unknown[Source+":usage"]; !ok || resps[1].Usage.InputTokensDetails.CachedTokens != 100 {
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

// TestSubagent checks a sub-agent's header names its parent, and the
// response copied from the parent carries no usage of its own.
func TestSubagent(t *testing.T) {
	res := convertFixture(t, fixtures(t)["subagent"], Options{})
	s := res.Sessions[0]
	h := s.Header()
	if h.ParentSession != "01990000-0000-7000-8000-0000000000aa" || h.SpawnedBy != "" {
		t.Errorf("header = %+v", h)
	}
	var link *agentsession.LinkEntry
	for _, e := range s.Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok {
			link = l
		}
	}
	if link == nil || link.Rel != agentsession.RelForkOf {
		t.Errorf("fork link = %+v", link)
	}
	resps, _ := responses(t, s)
	if len(resps) != 2 {
		t.Fatalf("%d responses", len(resps))
	}
	if r := resps[0]; r.ResponseID != "resp_parent" || r.Usage != nil || string(r.Unknown[Source+":copied"]) != "true" {
		t.Errorf("copied response = %+v", r)
	}
	if r := resps[1]; r.ResponseID != "resp_own" || r.Usage == nil || r.Usage.InputTokens != 40 {
		t.Errorf("own response = %+v", r)
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

// TestNeverFatal converts the crafted lines a review found aborting a
// conversion: each must convert, verify and account for every line.
func TestNeverFatal(t *testing.T) {
	for name, in := range map[string]string{
		"custom call with no call id":            `{"type":"custom_tool_call","name":"x","input":"y"}`,
		"custom call with an empty call id":      `{"timestamp":"2026-10-01T10:00:01Z","type":"response_item","payload":{"type":"custom_tool_call","call_id":"","name":"x","input":"y"}}`,
		"window message of string content":       `{"timestamp":"2026-10-01T10:00:09Z","type":"compacted","payload":{"message":"s","replacement_history":[{"type":"message","role":"user","content":"hi"}]}}`,
		"window call with object arguments":      `{"timestamp":"2026-10-01T10:00:09Z","type":"compacted","payload":{"message":"s","replacement_history":[{"type":"function_call","call_id":"c","name":"n","arguments":{}}]}}`,
		"window custom call with a number input": `{"timestamp":"2026-10-01T10:00:09Z","type":"compacted","payload":{"message":"s","replacement_history":[{"type":"custom_tool_call","call_id":"c","name":"n","input":1}]}}`,
		"call that does not decode, then its output": `{"timestamp":"t","type":"response_item","payload":{"type":"function_call","call_id":"c","name":"n","arguments":{}}}` + "\n" +
			`{"timestamp":"t","type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":"o"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			f, err := codex.Read(strings.NewReader(in + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			res, err := Convert(f, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := res.Report.Lines(), f.LineCount(); got != want {
				t.Errorf("accounts for %d lines of %d", got, want)
			}
			doccheck.Check(t, Source, res.Report)
			if name == "call that does not decode, then its output" {
				ctx, err := res.Sessions[0].Context()
				if err != nil {
					t.Fatal(err)
				}
				if len(ctx.Items) != 0 {
					t.Errorf("the output of a call that did not decode is in context: %v", render(ctx.Items))
				}
			}
		})
	}
}

// TestRecordOrderGuess checks that a rollout with no readable version
// is read by where its first token count falls, and says so.
func TestRecordOrderGuess(t *testing.T) {
	path := fixtures(t)["batch"]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(`"cli_version":"0.40.0"`), []byte(`"cli_version":"dev"`), 1)
	f, err := codex.Read(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	guessed := false
	for _, fd := range res.Report.Findings {
		if fd.Code == "codex/record-order" && strings.Contains(fd.Detail, "the count first") {
			guessed = true
		}
	}
	if !guessed {
		t.Error("the batch order was not guessed")
	}
	resps, tied := responses(t, res.Sessions[0])
	if len(resps) != 2 || tied[resps[0].ResponseID] != 3 || resps[0].Usage == nil || resps[0].Usage.InputTokens != 50 {
		t.Errorf("responses = %+v, %v", resps, tied)
	}
}
