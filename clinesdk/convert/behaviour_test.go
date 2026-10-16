package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
	"github.com/ChristopherDavenport/agentsession"
)

// The wants were printed by node 25 running truncateMiddleByChars,
// transcribed: the length in UTF-16 units and the SHA-256 of the UTF-8.
func TestTruncateMiddle(t *testing.T) {
	for _, tc := range []struct {
		text string
		max  int
		n    int
		sum  string
	}{
		{strings.Repeat("x", 9000), 8000, 8000, "8a7da4b03bfea24b61ce4b536fef6eaffb0e888b160a1a0853df7f3481ec7316"},
		{strings.Repeat("é😀", 3000), 8000, 8000, "fab070d0cbb84e4309255e2ed17e0bda3691d2ead6f25093c5e248e494f35f33"},
		{strings.Repeat("ab", 20), 10, 30, "36ae3ee57e0b2cf7dd44776b6002d0bf2feb28e73abca5332b6bba113367848b"},
		{strings.Repeat("abcdefghij", 100), 30, 32, "851d7100735b90bf3f2de4bbd2680cf5156fc8bca5edc48b46d7c79befe5c8a7"},
	} {
		out := truncateMiddle(tc.text, tc.max, truncatedMarker)
		sum := sha256.Sum256([]byte(out))
		if n := len(jsjson.FromString(out)); n != tc.n || hex.EncodeToString(sum[:]) != tc.sum {
			// A cut through a surrogate pair is U+FFFD in both, one unit.
			t.Errorf("%d units of %q…: got %d units, %x", tc.max, tc.text[:4], n, sum)
		}
	}
	if got := truncateMiddle("short", 8000, truncatedMarker); got != "short" {
		t.Errorf("a short text changed: %q", got)
	}
}

// fakeIndex is an index of rows.
type fakeIndex struct{ rows []*clinesdk.Row }

func (f *fakeIndex) Name() string                           { return "fake.db" }
func (f *fakeIndex) Family(string) ([]*clinesdk.Row, error) { return f.rows, nil }
func (f *fakeIndex) Close() error                           { return nil }
func msgs(ms ...string) []byte {
	return []byte(`{"version":1,"messages":[` + strings.Join(ms, ",") + `]}`)
}
func userMsg(text string, ts int) string {
	return fmt.Sprintf(`{"role":"user","content":%q,"ts":%d}`, text, ts)
}
func spawn(id, task string, ts int) string {
	return fmt.Sprintf(`{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"spawn_agent","input":{"systemPrompt":"s","task":%q}}],"ts":%d}`, id, task, ts)
}
func spawned(id string, ts int) string {
	return fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"name":"spawn_agent","content":"{}"}],"ts":%d}`, id, ts)
}
func reply(text string, ts int) string {
	return fmt.Sprintf(`{"role":"assistant","content":[{"type":"text","text":%q}],"ts":%d}`, text, ts)
}

// convertFiles converts a session from its files' contents and checks
// its tally.
func convertFiles(t *testing.T, files map[string][]byte, opts Options) *agentimport.Result {
	t.Helper()
	s := clinesdk.ParseSession("r", files)
	res, err := Convert(s, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Report.Lines(), s.Units(); got != want {
		t.Errorf("the report accounts for %d units; the session holds %d", got, want)
	}
	return res
}

func spawnedBy(res *agentimport.Result) map[string]string {
	out := map[string]string{}
	for _, s := range res.Sessions[1:] {
		h := s.Header()
		out[h.SpawnedBy] = h.ParentSession
	}
	return out
}

// TestIndexParent links a nested sub-agent by the parent the index
// names: without it, the root's call and a sub-agent's call have its
// task, and it is left unlinked.
func TestIndexParent(t *testing.T) {
	files := map[string][]byte{
		"r.messages.json":       msgs(userMsg("go", 1000), spawn("c1", "Dig", 2000), spawn("c2", "Deeper", 2001), spawned("c1", 9000), spawned("c2", 9001), reply("done", 9500)),
		"agent_1.messages.json": msgs(userMsg("Dig", 3000), spawn("n1", "Deeper", 3500), spawned("n1", 8000), reply("dug", 8500)),
		"agent_2.messages.json": msgs(userMsg("Deeper", 4000), reply("deep", 4500)),
	}
	res := convertFiles(t, files, Options{})
	if _, ok := spawnedBy(res)["c2"]; ok {
		t.Error("without an index, the sub-agent two calls could have run is linked")
	}
	ix := &fakeIndex{rows: []*clinesdk.Row{{SessionID: "r__agent_2", ParentSessionID: "r", ParentAgentID: "agent_1", AgentID: "agent_2"}}}
	res = convertFiles(t, files, Options{Index: ix})
	by := spawnedBy(res)
	if by["n1"] == "" || by["c2"] != "" {
		t.Errorf("with the index, agent_2 is linked as %v", by)
	}
	if by["n1"] != agentsession.SubsessionID("r", "c1") {
		t.Errorf("agent_2's parent is %s", by["n1"])
	}
}

// TestDepth follows sub-agents 16 deep: the one past them converts on
// its own, its own sub-agents linked again. A cycle of them ends.
func TestDepth(t *testing.T) {
	files := map[string][]byte{"r.messages.json": msgs(userMsg("go", 1000), spawn("c0", "t0", 2000), spawned("c0", 99000))}
	for i := 0; i < 20; i++ {
		files[fmt.Sprintf("agent_%02d.messages.json", i)] = msgs(userMsg(fmt.Sprintf("t%d", i), 3000+i), spawn(fmt.Sprintf("n%d", i), fmt.Sprintf("t%d", i+1), 3100+i), spawned(fmt.Sprintf("n%d", i), 90000-i))
	}
	res := convertFiles(t, files, Options{})
	if len(res.Sessions) != 21 {
		t.Fatalf("%d sessions", len(res.Sessions))
	}
	linked := 0
	for _, s := range res.Sessions[1:] {
		if s.Header().SpawnedBy != "" {
			linked++
		}
	}
	if linked != 19 {
		t.Errorf("%d sub-agents linked, want 19", linked)
	}
	if !strings.Contains(fmt.Sprint(res.Report.Findings), "nested past 16") {
		t.Error("the sub-agent past the bound is not reported")
	}
	cycle := map[string][]byte{
		"r.messages.json":       msgs(userMsg("go", 1000)),
		"agent_a.messages.json": msgs(userMsg("A", 2000), spawn("x", "B", 2100), spawned("x", 5000)),
		"agent_b.messages.json": msgs(userMsg("B", 3000), spawn("y", "A", 3100), spawned("y", 4000)),
	}
	res = convertFiles(t, cycle, Options{})
	if len(res.Sessions) != 3 {
		t.Errorf("a cycle of sub-agents makes %d sessions", len(res.Sessions))
	}
}

// TestRefusals reads who refused a call.
func TestRefusals(t *testing.T) {
	for _, tc := range []struct{ content, by string }{
		{`"{\"error\":\"This tool call was rejected by the user and not executed. -- NOT a tool or system failure. Clarify with user before proceeding.\"}"`, agentsession.ByHuman},
		{`"{\"error\":\"Tool \\\"x\\\" requires approval but no approval callback is configured -- NOT a tool or system failure. Clarify with user before proceeding.\"}"`, agentsession.ByPolicy},
		{`"{\"error\":\"Tool \\\"x\\\" is disabled by policy\"}"`, agentsession.ByPolicy},
		{`[{"type":"text","text":"Denied. -- NOT a tool or system failure. Clarify with user before proceeding."}]`, agentsession.ByHuman},
		{`"ok"`, ""},
	} {
		v, err := jsjson.Parse([]byte(tc.content))
		if err != nil {
			t.Fatal(err)
		}
		by, _, ok := refusal(v)
		if by != tc.by || ok != (tc.by != "") {
			t.Errorf("%s: by %q, %v", tc.content, by, ok)
		}
	}
}

// TestNoIndex converts the sub-agents fixture without its database:
// its children are matched by task and time, and the report says what
// the index would have added.
func TestNoIndex(t *testing.T) {
	root := build(t, "s3-subagents")
	if err := os.RemoveAll(filepath.Join(root, "db")); err != nil {
		t.Fatal(err)
	}
	res, err := ConvertFile(filepath.Join(root, "sessions", "1759327200000_sub01"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 5 || len(spawnedBy(res)) != 4 {
		t.Errorf("%d sessions, spawned by %v", len(res.Sessions), spawnedBy(res))
	}
	found := false
	for _, f := range res.Report.Findings {
		found = found || f.Code == "clinesdk/no-index"
	}
	if !found {
		t.Error("no clinesdk/no-index finding")
	}
	// An index that cannot be read is reported, and the directory read.
	if err := os.MkdirAll(filepath.Join(root, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "db", clinesdk.DatabaseName), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = ConvertFile(filepath.Join(root, "sessions", "1759327200000_sub01"), Options{OpenIndex: func(string) (clinesdk.Index, error) { return nil, fmt.Errorf("bad") }})
	if err != nil {
		t.Fatal(err)
	}
	codes := res.Report.Codes()
	if !strings.Contains(strings.Join(codes, " "), "clinesdk/index-unread") {
		t.Errorf("codes %v", codes)
	}
}

// TestMissingResult answers a call no result answers before the next
// message, and leaves the last call pending.
func TestMissingResult(t *testing.T) {
	files := map[string][]byte{"r.messages.json": msgs(
		userMsg("go", 1000),
		`{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"ls","input":{}},{"type":"tool_use","id":"b","name":"cat","input":{}}],"ts":2000}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","name":"ls","content":"x"}],"ts":3000}`,
		reply("next", 4000),
		`{"role":"assistant","content":[{"type":"tool_use","id":"c","name":"rm","input":{}}],"ts":5000}`,
	)}
	res := convertFiles(t, files, Options{})
	s := res.Sessions[0]
	ctx, err := s.Context()
	if err != nil {
		t.Fatal(err)
	}
	lines := renderItems(ctx.Items)
	want := []string{"user: go", "call a ls", "call b cat", "output a x", "output b Tool execution was interrupted before a result was", "assistant: next", "call c rm"}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("context:\n%s", strings.Join(lines, "\n"))
	}
}
