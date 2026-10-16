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
	"github.com/ChristopherDavenport/openresponses"
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

// TestForkOfLegacy converts a fork of a classic task the VS Code
// extension carried on: the fork copied the warning and the legacy
// flags, and is still its source's fork, the copy's usage kept off.
func TestForkOfLegacy(t *testing.T) {
	root := build(t, "s4-legacy-fork")
	res, err := ConvertFile(filepath.Join(root, "sessions", "1759345300000_frk02"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Sessions[0]
	src := agentsession.SubsessionID("1759341700000", Source)
	if h := s.Header(); h.ID != "1759345300000_frk02" || h.ParentSession != src {
		t.Errorf("header id %s, parent %s; want the fork's own ID and parent %s", h.ID, h.ParentSession, src)
	}
	counted := 0
	for _, e := range s.Entries() {
		switch e := e.(type) {
		case *agentsession.LinkEntry:
			if e.Rel == agentsession.RelForkOf && e.Session != src {
				t.Errorf("fork_of names %s", e.Session)
			}
		case *agentsession.ResponseEntry:
			if e.Usage != nil {
				counted++
			}
		}
	}
	if counted != 1 {
		t.Errorf("%d responses carry usage; the fork's own one alone should", counted)
	}
}

// TestUnhashableHistory leaves a sidecar stale over a history Cline
// cannot hash itself, a user part that is no object, even when the
// sidecar's messages copy the history's: Cline throws on it.
func TestUnhashableHistory(t *testing.T) {
	files := map[string][]byte{
		"r.messages.json": msgs(`{"role":"user","content":["not a part"],"ts":1000}`, reply("a", 2000), userMsg("b", 3000), reply("c", 4000)),
		"r.compaction.json": []byte(`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":3,"source_prefix_hash":"sha256:0",` +
			`"messages":[{"role":"user","content":"Context summary:\n\ns","metadata":{"kind":"compaction_summary"}},` + userMsg("b", 3000) + `]}`),
	}
	res := convertFiles(t, files, Options{})
	codes := strings.Join(res.Report.Codes(), " ")
	if !strings.Contains(codes, "clinesdk/stale-compaction") || strings.Contains(codes, "clinesdk/compaction-unverified") {
		t.Errorf("codes %s", codes)
	}
}

// TestIndexPromptGuess matches sub-agents by their own first messages:
// the index's prompt can be Cline's first-in-first-out guess on its hook
// path, or a placeholder.
func TestIndexPromptGuess(t *testing.T) {
	files := map[string][]byte{
		"r.messages.json":       msgs(userMsg("go", 1000), spawn("ca", "Survey A", 2000), spawn("cb", "Survey B", 2001), spawned("ca", 9000), spawned("cb", 9001)),
		"agent_a.messages.json": msgs(userMsg("Survey A", 3000), reply("a", 3500)),
		"agent_b.messages.json": msgs(userMsg("Survey B", 3100), reply("b", 3600)),
	}
	ix := &fakeIndex{rows: []*clinesdk.Row{
		{SessionID: "r__agent_a", ParentSessionID: "r", ParentAgentID: "agent_lead", AgentID: "agent_a", Prompt: "Survey B"},
		{SessionID: "r__agent_b", ParentSessionID: "r", ParentAgentID: "agent_lead", AgentID: "agent_b", Prompt: "Subagent run by agent_lead"},
	}}
	res := convertFiles(t, files, Options{Index: ix})
	got := map[string]string{}
	for _, s := range res.Sessions[1:] {
		first := ""
		if ctx, err := s.Context(); err == nil {
			first = renderItems(ctx.Items)[0]
		}
		got[s.Header().SpawnedBy] = first
	}
	if got["ca"] != "user: Survey A" || got["cb"] != "user: Survey B" {
		t.Errorf("calls ran %v", got)
	}
}

// teamRun is a team_run_task call and its result.
func teamRun(id, task string, ts int, status string) []string {
	return []string{
		fmt.Sprintf(`{"role":"assistant","content":[{"type":"tool_use","id":%q,"name":"team_run_task","input":{"agentId":"tester","task":%q,"runMode":"async"}}],"ts":%d}`, id, task, ts),
		fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"name":"team_run_task","content":"{\"status\":\"%s\"}"}],"ts":%d}`, id, status, ts+10),
	}
}

func peerLinks(res *agentimport.Result) map[string][]string {
	out := map[string][]string{}
	for _, e := range res.Sessions[0].Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok && l.Rel == agentsession.RelPeer {
			out[l.CallID] = append(out[l.CallID], l.Session)
		}
	}
	return out
}

// TestTeamTiming links each team task to the one call that started it,
// told by time when two calls give one agent the same task.
func TestTeamTiming(t *testing.T) {
	root := []string{userMsg("go", 500)}
	root = append(root, teamRun("c1", "Run the tests", 1000, "queued")...)
	root = append(root, teamRun("c2", "Run the tests", 5000, "queued")...)
	files := map[string][]byte{
		"r.messages.json":              msgs(root...),
		"tester__aaaaaa.messages.json": msgs(userMsg("Run the tests", 2000), reply("fail", 2500)),
		"tester__bbbbbb.messages.json": msgs(userMsg("Run the tests", 6000), reply("pass", 6500)),
	}
	links := peerLinks(convertFiles(t, files, Options{}))
	if len(links["c1"]) != 1 || len(links["c2"]) != 1 || links["c1"][0] != "r__teamtask__tester__aaaaaa" || links["c2"][0] != "r__teamtask__tester__bbbbbb" {
		t.Errorf("links %v", links)
	}
	// A sync call Cline collapsed onto another of its batch ran the
	// same task: a peer of both.
	joined := []string{userMsg("go", 500),
		`{"role":"assistant","content":[{"type":"tool_use","id":"j1","name":"team_run_task","input":{"agentId":"tester","task":"Lint"}},{"type":"tool_use","id":"j2","name":"team_run_task","input":{"agentId":"tester","task":"Lint"}}],"ts":1000}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"j1","name":"team_run_task","content":"{\"status\":\"running\"}"}],"ts":3000}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"j2","name":"team_run_task","content":"{\"status\":\"joined\",\"deduped\":true}"}],"ts":3001}`,
	}
	links = peerLinks(convertFiles(t, map[string][]byte{"r.messages.json": msgs(joined...), "tester__cccccc.messages.json": msgs(userMsg("Lint", 2000), reply("ok", 2500))}, Options{}))
	if len(links["j1"]) != 1 || len(links["j2"]) != 1 {
		t.Errorf("joined links %v", links)
	}
}

// TestTeammateSpawn links a sub-agent a teammate spawned: the index
// names the teammate's runtime agent, which no file records, so the
// teammate's calls are candidates as well as the lead's.
func TestTeammateSpawn(t *testing.T) {
	root := append([]string{userMsg("go", 500)}, teamRun("c1", "Review", 1000, "queued")...)
	files := map[string][]byte{
		"r.messages.json":           msgs(root...),
		"rev__aaaaaa.messages.json": msgs(userMsg("Review", 2000), spawn("n1", "Check style", 2100), spawned("n1", 4000), reply("ok", 4500)),
		"agent_9.messages.json":     msgs(userMsg("Check style", 2500), reply("styled", 3000)),
	}
	files["rev__aaaaaa.messages.json"] = []byte(strings.Replace(string(files["rev__aaaaaa.messages.json"]), `{"version":1,`, `{"version":1,"agent":"teammate","sessionId":"r__teamtask__rev__aaaaaa",`, 1))
	files["r.messages.json"] = []byte(strings.ReplaceAll(string(files["r.messages.json"]), `"tester"`, `"rev"`))
	ix := &fakeIndex{rows: []*clinesdk.Row{{SessionID: "r__agent_9", ParentSessionID: "r", ParentAgentID: "agent_3f1c", AgentID: "agent_9"}}}
	res := convertFiles(t, files, Options{Index: ix})
	if by := spawnedBy(res); by["n1"] == "" {
		t.Errorf("the teammate's sub-agent is not linked: %v", by)
	}
}

// TestSpawnWindow leaves a sub-agent unlinked when the one call with its
// task was answered before it started.
func TestSpawnWindow(t *testing.T) {
	files := map[string][]byte{
		"r.messages.json":       msgs(userMsg("go", 1000), spawn("c1", "Dig", 2000), spawned("c1", 3000), reply("done", 3500)),
		"agent_a.messages.json": msgs(userMsg("Dig", 60000), reply("dug", 61000)),
	}
	if by := spawnedBy(convertFiles(t, files, Options{})); by["c1"] != "" {
		t.Errorf("a sub-agent started after its call's result is linked to it: %v", by)
	}
}

// mcpSession is a session whose MCP tool returned many text blocks,
// written by a client of origin.
func mcpSession(origin string) map[string][]byte {
	var parts []string
	for i := 0; i < 40; i++ {
		parts = append(parts, fmt.Sprintf(`{"type":"text","text":%q}`, strings.Repeat(fmt.Sprintf("hit %d ", i), 40)))
	}
	return map[string][]byte{"r.messages.json": []byte(`{"version":1,"origin":` + origin + `,"messages":[` + strings.Join([]string{
		userMsg("search", 1000),
		`{"role":"assistant","content":[{"type":"tool_use","id":"m1","name":"github__search_code","input":{"q":"x"}}],"ts":2000}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"m1","name":"github__search_code","content":[` + strings.Join(parts, ",") + `]}],"ts":3000}`,
		reply("found", 4000),
	}, ",") + `]}`)}
}

func outputOf(t *testing.T, s *agentsession.Session, call string) openresponses.FunctionCallOutputData {
	t.Helper()
	for _, e := range s.Entries() {
		if it, ok := e.(*agentsession.ItemEntry); ok {
			if o, ok := it.Item.(*openresponses.FunctionCallOutput); ok && o.CallID == call {
				return o.Output
			}
		}
	}
	t.Fatalf("no output of %s", call)
	return openresponses.FunctionCallOutputData{}
}

// TestCachePreview sends an MCP tool's oversized result as Cline's
// cache preview from 4.1.23: the whole result as indented JSON, cut to
// 8,000 characters; a VS Code release before cuts block by block.
func TestCachePreview(t *testing.T) {
	res := convertFiles(t, mcpSession(`{"source":"cli","version":"2.3.0"}`), Options{})
	out := outputOf(t, res.Sessions[0], "m1")
	if len(out.Parts) != 1 {
		t.Fatalf("%d parts, want the one preview", len(out.Parts))
	}
	text := out.Parts[0].(*openresponses.InputText).Text
	if utf16Len(text) != maxToolResultChars || !strings.HasPrefix(text, "[\n  {\n    \"type\": \"text\",\n    \"text\": \"hit 0 ") {
		t.Errorf("preview of %d units: %.80q", utf16Len(text), text)
	}
	if !strings.Contains(strings.Join(res.Report.Codes(), " "), "clinesdk/cache-preview") {
		t.Error("no clinesdk/cache-preview finding")
	}
	res = convertFiles(t, mcpSession(`{"source":"vscode","version":"4.1.20"}`), Options{})
	if out := outputOf(t, res.Sessions[0], "m1"); len(out.Parts) != 40 {
		t.Errorf("before 4.1.23: %d parts, want the 40 blocks", len(out.Parts))
	}
}

// TestCompactionBatching reports, at a compaction, that a manual one
// clears Cline's batched rewrites of outdated reads.
func TestCompactionBatching(t *testing.T) {
	body := strings.Repeat("x", 9000)
	ms := []string{userMsg("go", 1000)}
	for i := 0; i < 10; i++ {
		ms = append(ms, fmt.Sprintf(`{"role":"assistant","content":[{"type":"tool_use","id":"r%d","name":"read_files","input":{"files":[{"path":"/a"}]}}],"ts":%d}`, i, 2000+10*i))
		ms = append(ms, fmt.Sprintf(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"r%d","name":"read_files","content":[{"query":"/a","result":%q,"success":true}]}],"ts":%d}`, i, body, 2005+10*i))
	}
	ms = append(ms, reply("read", 3000), userMsg("more", 3100), reply("done", 3200))
	comp := fmt.Sprintf(`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":%d,"source_last_message_key":"ts:assistant:3000","messages":[{"role":"user","content":"Context summary:\n\ns","metadata":{"kind":"compaction_summary"}}]}`, len(ms)-2)
	res := convertFiles(t, map[string][]byte{"r.messages.json": msgs(ms...), "r.compaction.json": []byte(comp)}, Options{})
	if !strings.Contains(fmt.Sprint(res.Report.Findings), "a manual one (/compact) clears them") {
		t.Error("the compaction's effect on the batching is not reported")
	}
}

// TestConversationMismatch leaves a sidecar for another conversation
// stale when the index records the session's own.
func TestConversationMismatch(t *testing.T) {
	files := map[string][]byte{
		"r.messages.json": msgs(userMsg("a", 1000), reply("b", 2000), userMsg("c", 3000), reply("d", 4000)),
		"r.compaction.json": []byte(`{"version":1,"updated_at":"2026-10-01T10:00:00Z","conversation_id":"conv_other","source_message_count":2,"source_last_message_key":"ts:assistant:2000",` +
			`"messages":[{"role":"user","content":"Context summary:\n\ns","metadata":{"kind":"compaction_summary"}}]}`),
	}
	ix := &fakeIndex{rows: []*clinesdk.Row{{SessionID: "r", ConversationID: "conv_mine"}}}
	if codes := strings.Join(convertFiles(t, files, Options{Index: ix}).Report.Codes(), " "); !strings.Contains(codes, "clinesdk/stale-compaction") {
		t.Errorf("codes %s", codes)
	}
}

// TestMissingAfterResults answers a missing call after the results a
// message leads with, as Cline's request does, not before them.
func TestMissingAfterResults(t *testing.T) {
	files := map[string][]byte{"r.messages.json": msgs(
		userMsg("go", 1000),
		`{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"ls","input":{}},{"type":"tool_use","id":"b","name":"cat","input":{}}],"ts":2000}`,
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","name":"ls","content":"x"},{"type":"text","text":"and then"}],"ts":3000}`,
		reply("next", 4000),
	)}
	ctx, err := convertFiles(t, files, Options{}).Sessions[0].Context()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(renderItems(ctx.Items), "\n")
	want := "user: go\ncall a ls\ncall b cat\noutput a x\noutput b Tool execution was interrupted before a result was\nuser: and then\nassistant: next"
	if got != want {
		t.Errorf("context:\n%s", got)
	}
}

// TestMarkupSpaces counts tool-call markup with JavaScript's \s, which
// takes in a no-break space: eight such tags cap the text at 12,000.
func TestMarkupSpaces(t *testing.T) {
	text := strings.Repeat("<\u00a0tool_call>", 8) + strings.Repeat("x", 13000)
	if n := utf16Len(truncateAssistantText(text)); n > maxAssistantToolMarkupChars || n < maxAssistantToolMarkupChars-1 {
		t.Errorf("cut to %d", n)
	}
	if got := truncateAssistantText(strings.Repeat("<D\u017fML invoke>", 8) + strings.Repeat("x", 13000)); utf16Len(got) <= maxAssistantToolMarkupChars {
		t.Error("\u017f matched s, which JavaScript's i flag does not fold")
	}
}

// TestRootID reports a session whose children Cline files elsewhere.
func TestRootID(t *testing.T) {
	s := clinesdk.ParseSession("17__x", map[string][]byte{"17__x.messages.json": msgs(userMsg("go", 1000))})
	res, err := Convert(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(res.Report.Codes(), " "), "clinesdk/root-id") {
		t.Error("no clinesdk/root-id finding")
	}
}

// TestUnsignedThinking leaves thinking with no signature out of the
// requests after it for Anthropic, and keeps it for another provider.
func TestUnsignedThinking(t *testing.T) {
	for provider, kept := range map[string]bool{"anthropic": false, "openai": true} {
		files := map[string][]byte{"r.messages.json": msgs(
			userMsg("go", 1000),
			fmt.Sprintf(`{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"ok"}],"modelInfo":{"id":"m","provider":%q},"ts":2000}`, provider),
			userMsg("more", 3000),
		)}
		ctx, err := convertFiles(t, files, Options{}).Sessions[0].Context()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(strings.Join(renderItems(ctx.Items), "\n"), "reasoning hmm"); got != kept {
			t.Errorf("%s: thinking in context %v, want %v", provider, got, kept)
		}
	}
}
