package convert

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
)

// claudeCodeDoc wraps steps in a document Harbor's Claude Code adapter
// would have written.
func claudeCodeDoc(steps string) []byte {
	return []byte(`{"schema_version":"ATIF-v1.7","session_id":"cc","agent":{"name":"claude-code","version":"2","model_name":"m"},"steps":[` + steps + `]}`)
}

// TestSidechains checks where a sub-agent's interleaved steps go: a
// peer of the call that started it and did not wait, or a session with
// parent_session alone when no call's result names it; never the main
// session's context.
func TestSidechains(t *testing.T) {
	const (
		user = `{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"user","message":"go"}`
		side = `{"step_id":3,"timestamp":"2026-10-01T00:00:02Z","source":"user","message":"sub task","extra":{"is_sidechain":true,"agent_id":"b1"}},` +
			`{"step_id":4,"timestamp":"2026-10-01T00:00:03Z","source":"agent","message":"sub done","extra":{"is_sidechain":true,"agent_id":"b1"}}`
		last = `{"step_id":5,"timestamp":"2026-10-01T00:00:04Z","source":"agent","message":"ok"}`
	)
	task := func(status string) string {
		return `{"step_id":2,"timestamp":"2026-10-01T00:00:01Z","source":"agent","message":"",` +
			`"tool_calls":[{"tool_call_id":"t1","function_name":"Task","arguments":{"prompt":"sub task"}}],` +
			`"observation":{"results":[{"source_call_id":"t1","content":"started",` +
			`"extra":{"tool_result_metadata":{"tool_use_result":{"status":"` + status + `","agentId":"b1"}}}}]}}`
	}
	notask := `{"step_id":2,"timestamp":"2026-10-01T00:00:01Z","source":"agent","message":"",` +
		`"tool_calls":[{"tool_call_id":"t1","function_name":"Bash","arguments":{}}],` +
		`"observation":{"results":[{"source_call_id":"t1","content":"done"}]}}`
	mainCtx := []string{"user: go", "call t1 Task {\"prompt\":\"sub task\"}", "output t1 started", "assistant: ok"}
	for _, tc := range []struct {
		name  string
		steps string
		// rel is the link the main session holds to the child, or "".
		rel       string
		spawnedBy string
		want      []string
	}{
		{"peer", user + "," + task("async_launched") + "," + side + "," + last, agentsession.RelPeer, "", mainCtx},
		{"subsession", user + "," + task("completed") + "," + side + "," + last, agentsession.RelSubsession, "t1", mainCtx},
		{"unlinked", user + "," + notask + "," + side + "," + last, "", "",
			[]string{"user: go", "call t1 Bash {}", "output t1 done", "assistant: ok"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := atif.Parse(claudeCodeDoc(tc.steps))
			if err != nil {
				t.Fatal(err)
			}
			res, err := Convert(f, Options{CreatedAt: fixed})
			if err != nil {
				t.Fatal(err)
			}
			doccheck.Check(t, Source, res.Report)
			if got, want := res.Report.Lines(), f.Units(); got != want {
				t.Errorf("the report accounts for %d units; the document holds %d", got, want)
			}
			if !slices.Contains(res.Report.Codes(), "atif/sidechain") {
				t.Errorf("no atif/sidechain: %v", res.Report.Codes())
			}
			if len(res.Sessions) != 2 {
				t.Fatalf("%d sessions, want the main one and the sub-agent's", len(res.Sessions))
			}
			main, child := reread(t, res.Sessions[0]), reread(t, res.Sessions[1])
			if got := contextAtLeaf(t, main); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("main context:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			if got, want := contextAtLeaf(t, child), []string{"user: sub task", "assistant: sub done"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("child context:\n%s", strings.Join(got, "\n"))
			}
			h := child.Header()
			if h.ParentSession != main.ID() || h.SpawnedBy != tc.spawnedBy {
				t.Errorf("child parent %q spawned_by %q, want %q %q", h.ParentSession, h.SpawnedBy, main.ID(), tc.spawnedBy)
			}
			// Where the link sits: a subsession's before the output, a
			// peer's after it.
			var kinds []string
			for _, e := range main.Entries() {
				switch e := e.(type) {
				case *agentsession.LinkEntry:
					if e.Session != child.ID() || e.Rel != tc.rel || e.CallID != "t1" {
						t.Errorf("link %+v", e)
					}
					kinds = append(kinds, "link")
				case *agentsession.ItemEntry:
					if e.Item.ItemType() == "function_call_output" {
						kinds = append(kinds, "output")
					}
				}
			}
			want := map[string]string{agentsession.RelPeer: "output link", agentsession.RelSubsession: "link output", "": "output"}[tc.rel]
			if strings.Join(kinds, " ") != want {
				t.Errorf("entries %v, want %s", kinds, want)
			}
		})
	}
}

// TestNesting checks that the conversion follows documents only so
// deep, and never into one it is converting already, whatever names it.
func TestNesting(t *testing.T) {
	t.Run("depth", func(t *testing.T) {
		const levels = maxDepth + 8
		doc := `{"source":"user","message":"x"}],"subagent_trajectories":[]}`
		for n := levels; n > 0; n-- {
			doc = `{"source":"user","message":"x"}],"subagent_trajectories":[{"trajectory_id":"t` + fmt.Sprint(n) + `","steps":[` + doc + `]}`
		}
		f, err := atif.Parse([]byte(`{"schema_version":"ATIF-v1.7","session_id":"deep","steps":[` + doc))
		if err != nil {
			t.Fatal(err)
		}
		res, err := Convert(f, Options{CreatedAt: fixed})
		if err != nil {
			t.Fatal(err)
		}
		doccheck.Check(t, Source, res.Report)
		if got, want := res.Report.Lines(), f.Units(); got != want {
			t.Errorf("the report accounts for %d units; the document holds %d", got, want)
		}
		if len(res.Sessions) != maxDepth+1 {
			t.Errorf("%d sessions, want %d", len(res.Sessions), maxDepth+1)
		}
		if !slices.Contains(res.Report.Codes(), "atif/subagent-nesting") {
			t.Errorf("no atif/subagent-nesting: %v", res.Report.Codes())
		}
	})
	// A document that names itself through another: the files stop it
	// before the bound does.
	t.Run("mutual", func(t *testing.T) {
		dir := t.TempDir()
		ref := func(path string) string {
			return `{"schema_version":"ATIF-v1.7","steps":[{"source":"agent","message":"","tool_calls":[{"tool_call_id":"c","function_name":"f","arguments":{}}],` +
				`"observation":{"results":[{"source_call_id":"c","content":"x","subagent_trajectory_ref":[{"trajectory_path":"` + path + `"}]}]}}]}`
		}
		for name, data := range map[string]string{"a.json": ref("b.json"), "b.json": ref("a.json")} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		res, err := ConvertFile(filepath.Join(dir, "a.json"), Options{CreatedAt: fixed})
		if err != nil {
			t.Fatal(err)
		}
		doccheck.Check(t, Source, res.Report)
		if len(res.Sessions) != 2 || !slices.Contains(res.Report.Codes(), "atif/ref-repeated") {
			t.Errorf("%d sessions, codes %v", len(res.Sessions), res.Report.Codes())
		}
	})
	// A document named while it converts, as a split that did not stop
	// at a sub-agent's own steps once did: it is refused, not recursed.
	t.Run("repeat", func(t *testing.T) {
		f, err := atif.Parse(claudeCodeDoc(`{"source":"user","message":"x"}`))
		if err != nil {
			t.Fatal(err)
		}
		fam := newFamily(Options{CreatedAt: fixed})
		in := docInput{t: f.Doc, file: f}
		fam.active[in.key()] = true
		_, _, err = fam.child(in, target{id: "again"}, fam.report, "")
		if !errors.Is(err, errNesting) {
			t.Fatalf("err = %v", err)
		}
		if !slices.Contains(fam.report.Codes(), "atif/subagent-nesting") || fam.report.Lines() != f.Units() {
			t.Errorf("codes %v, %d units", fam.report.Codes(), fam.report.Lines())
		}
	})
}
