package convert

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
)

// TestSubagentLinks checks the task fixture's links: a sub-agent that
// answers two calls outlives the first, so it is a peer of both, linked
// after each output, and its session names the parent in parent_session
// alone.
func TestSubagentLinks(t *testing.T) {
	res := convertFixture(t, fixtures(t)["subagent-task"], Options{})
	if len(res.Sessions) != 2 {
		t.Fatalf("%d sessions, want 2", len(res.Sessions))
	}
	parent, child := res.Sessions[0], res.Sessions[1]
	var rels []string
	var prev agentsession.Entry
	for _, e := range parent.Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok {
			if l.Session != child.ID() {
				t.Errorf("link names %s, not the child", l.Session)
			}
			if it, ok := prev.(*agentsession.ItemEntry); !ok || it.Item.ItemType() != "function_call_output" {
				t.Errorf("the %s link does not follow its call's output", l.Rel)
			}
			rels = append(rels, l.Rel+" "+l.CallID)
		}
		prev = e
	}
	if got, want := strings.Join(rels, ", "), "peer call_t1, peer call_t2"; got != want {
		t.Errorf("links %s, want %s", got, want)
	}
	if h := child.Header(); h.ParentSession != parent.ID() || h.SpawnedBy != "" || h.ID != "0000000b-0000-4000-9000-000000000002" {
		t.Errorf("child header id %q parent %q spawned_by %q", h.ID, h.ParentSession, h.SpawnedBy)
	}
}

// TestSubagentSubsession checks a sub-agent only one call reached and
// waited for: a subsession, its header agreeing with its link, the link
// before the output, the output naming the sub-agent's leaf.
func TestSubagentSubsession(t *testing.T) {
	res := convertFixture(t, fixtures(t)["subagent-single"], Options{})
	if len(res.Sessions) != 2 {
		t.Fatalf("%d sessions, want 2", len(res.Sessions))
	}
	parent, child := res.Sessions[0], res.Sessions[1]
	byID := map[string]*agentsession.Session{child.ID(): child}
	if err := parent.VerifyLinks(func(id string) (*agentsession.Session, error) { return byID[id], nil }); err != nil {
		t.Fatal(err)
	}
	var link *agentsession.LinkEntry
	for _, e := range parent.Entries() {
		switch e := e.(type) {
		case *agentsession.LinkEntry:
			link = e
		case *agentsession.ItemEntry:
			if e.Item.ItemType() == "function_call_output" && (link == nil || len(e.Parents) != 1 || e.Parents[0].Session != child.ID()) {
				t.Errorf("the output follows link %v and names %v", link, e.Parents)
			}
		}
	}
	if link == nil || link.Rel != agentsession.RelSubsession || link.CallID != "call_one" {
		t.Errorf("link %+v, want a subsession of call_one", link)
	}
	if h := child.Header(); h.SpawnedBy != "call_one" || h.ParentSession != parent.ID() {
		t.Errorf("child header parent %q spawned_by %q", h.ParentSession, h.SpawnedBy)
	}
}

// TestSubagentAlone converts a sub-agent's directory on its own: its
// parent is named in parent_session and it is reported.
func TestSubagentAlone(t *testing.T) {
	dir := filepath.Join(fixtures(t)["subagent-task"], "subagents", "0000000b000040009000000000000002")
	res := convertFixture(t, dir, Options{})
	if got := res.Sessions[0].Header().ParentSession; got != "0000000b-0000-4000-9000-000000000001" {
		t.Errorf("parent_session %q", got)
	}
	found := false
	for _, f := range res.Report.Findings {
		found = found || f.Code == "openhands/subagent-alone"
	}
	if !found {
		t.Error("no openhands/subagent-alone finding")
	}
}

// TestAlone converts a conversation without its sub-agents.
func TestAlone(t *testing.T) {
	res := convertFixture(t, fixtures(t)["subagent-delegate"], Options{Alone: true})
	if len(res.Sessions) != 1 {
		t.Errorf("%d sessions, want the parent alone", len(res.Sessions))
	}
}

// TestTruncate checks the port of maybe_truncate and its helpers on
// cases worked by hand from the SDK's source.
func TestTruncate(t *testing.T) {
	c := &conv{report: nil}
	content := strings.Repeat("a", 100) + strings.Repeat("b", 100)
	// 125 - 113 leaves 12: six from each end.
	got := c.truncateQuiet(content, 125, "")
	if want := "aaaaaa" + truncateNotice + "bbbbbb"; got != want {
		t.Errorf("truncate = %q", got)
	}
	// The notice that names a saved file does not fit: it is cut.
	if got := c.truncateQuiet(content, 125, "/save"); len([]rune(got)) != 125 || !strings.HasPrefix(got, "<response clipped><NOTE>Due to the max output limit, only part of the full response has been shown to you. The complete") {
		t.Errorf("truncate with a file = %q", got)
	}
	if got := c.truncateQuiet("short", 125, "/save"); got != "short" {
		t.Errorf("truncate of a short text = %q", got)
	}
	for in, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "\n\n": 2, "a\r\nb\rc\u0085d\u2028": 4} {
		if got := splitLines(in); got != want {
			t.Errorf("splitLines(%q) = %d, want %d", in, got, want)
		}
	}
	for dir, want := range map[string]string{"/tmp/oh//obs/": "/tmp/oh/obs/f", "rel/./d": "rel/d/f", "//net/a": "//net/a/f", "": "f"} {
		if got := pyJoin(dir, "f"); got != want {
			t.Errorf("pyJoin(%q) = %q, want %q", dir, got, want)
		}
	}
	s := strings.Repeat("x", textContentLimit+10)
	cut, ok := sendCut(s)
	if !ok || len([]rune(cut)) != textContentLimit {
		t.Errorf("sendCut kept %d characters", len([]rune(cut)))
	}
}

// TestParentByFileName resolves parent_id through the event IDs the
// file names hold, as OpenHands does, and reports an event whose id is
// another.
func TestParentByFileName(t *testing.T) {
	c := openhands.Parse("0123456789abcdef0123456789abcdef", map[string][]byte{
		"events/event-00000-aaaaaaaa.json": []byte(`{"kind":"MessageEvent","id":"not-the-file","timestamp":"2026-01-01T00:00:00","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"a"}]}}`),
		"events/event-00001-bbbbbbbb.json": []byte(`{"kind":"MessageEvent","id":"bbbbbbbb","timestamp":"2026-01-01T00:00:01","source":"user","parent_id":"aaaaaaaa","llm_message":{"role":"user","content":[{"type":"text","text":"b"}]}}`),
	})
	res, err := Convert(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, f := range res.Report.Findings {
		codes[f.Code] = true
	}
	if codes["openhands/parent-missing"] || !codes["openhands/id-mismatch"] {
		t.Errorf("codes %v: want the parent resolved by its file name and the mismatch reported", res.Report.Codes())
	}
}

// convertEvents converts a conversation of the events given, in order.
func convertEvents(t *testing.T, state string, events ...string) *agentimport.Result {
	t.Helper()
	files := map[string][]byte{}
	if state != "" {
		files[openhands.FileBaseState] = []byte(state)
	}
	for k, e := range events {
		var id struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(e), &id); err != nil {
			t.Fatalf("event %d: %v", k, err)
		}
		files[fmt.Sprintf("events/event-%05d-%s.json", k, id.ID)] = []byte(e)
	}
	c := openhands.Parse("0123456789abcdef0123456789abcdef", files)
	res, err := Convert(c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := res.Report.Lines(), c.Units(); got != want {
		t.Errorf("the report accounts for %d units; the conversation holds %d", got, want)
	}
	doccheck.Check(t, Source, res.Report)
	return res
}

// ev builds an event's JSON: its envelope and members.
func ev(kind, id, parent string, members string) string {
	p := ""
	if parent != "" {
		p = `,"parent_id":"` + parent + `"`
	}
	if members != "" {
		members = "," + members
	}
	return `{"kind":"` + kind + `","id":"` + id + `","timestamp":"2026-01-01T00:00:00","source":"agent"` + p + members + `}`
}

func userEv(id, parent, text string) string {
	e := ev("MessageEvent", id, parent, `"llm_message":{"role":"user","content":[{"type":"text","text":"`+text+`"}]}`)
	return strings.Replace(e, `"source":"agent"`, `"source":"user"`, 1)
}

func actionEv(id, parent, call, resp string, extra string) string {
	m := `"thought":[],"action":{"kind":"TerminalAction","command":"ls"},"tool_name":"terminal","tool_call_id":"` + call + `","tool_call":{"id":"` + call + `","name":"terminal","arguments":"{}","origin":"completion"},"llm_response_id":"` + resp + `"`
	if extra != "" {
		m += "," + extra
	}
	return ev("ActionEvent", id, parent, m)
}

func obsEv(id, parent, call, text string) string {
	return ev("ObservationEvent", id, parent, `"tool_name":"terminal","tool_call_id":"`+call+`","action_id":"x","observation":{"kind":"TerminalObservation","content":[{"type":"text","text":"`+text+`"}],"metadata":{"exit_code":-1}}`)
}

// responses lists the response entries of a session by response ID.
func responses(s *agentsession.Session) []string {
	var out []string
	for _, e := range s.Entries() {
		if r, ok := e.(*agentsession.ResponseEntry); ok {
			out = append(out, r.ResponseID)
		}
	}
	return out
}

// TestBatchPastRecords keeps the actions of one response together when
// an event the view never holds, a state update, is written between
// them.
func TestBatchPastRecords(t *testing.T) {
	res := convertEvents(t, "",
		userEv("e0000000", "", "go"),
		actionEv("e1000000", "e0000000", "c1", "r1", ""),
		ev("ConversationStateUpdateEvent", "e2000000", "e1000000", `"key":"stats","value":{}`),
		actionEv("e3000000", "e1000000", "c2", "r1", ""),
		obsEv("e4000000", "e3000000", "c1", "one"),
		obsEv("e5000000", "e4000000", "c2", "two"),
	)
	if got := responses(res.Sessions[0]); len(got) != 1 || got[0] != "r1" {
		t.Errorf("responses %v, want r1 alone", got)
	}
}

// codes is the set of codes a report holds.
func codes(r *agentimport.Report) map[string]bool {
	out := map[string]bool{}
	for _, c := range r.Codes() {
		out[c] = true
	}
	return out
}

// TestAnswerPerBranch answers a call again on a second branch, as
// OpenHands does when a conversation navigated back to the action is
// resumed: each branch has its own output, and a branch where the call
// has none reports it pending.
func TestAnswerPerBranch(t *testing.T) {
	res := convertEvents(t, "",
		userEv("e0000000", "", "go"),
		actionEv("e1000000", "e0000000", "c1", "r1", ""),
		obsEv("e2000000", "e1000000", "c1", "first"),
		userEv("e3000000", "e2000000", "next"),
		obsEv("e4000000", "e1000000", "c1", "again"),
		userEv("e5000000", "e1000000", "never mind"),
	)
	outputs := 0
	for _, e := range res.Sessions[0].Entries() {
		if it, ok := e.(*agentsession.ItemEntry); ok && it.Item.ItemType() == "function_call_output" {
			outputs++
		}
	}
	got := codes(res.Report)
	if outputs != 2 || got["openhands/duplicate-output"] {
		t.Errorf("%d outputs, codes %v: want one per branch", outputs, res.Report.Codes())
	}
	if !got["openhands/pending-call"] {
		t.Error("the branch where the call has no answer does not report it pending")
	}
}

// TestForkUsageOnce converts a fork made with reset_metrics=False,
// whose metrics hold the source's calls: only the fork's own calls are
// counted, as top-level usage of a custom entry or on a response.
func TestForkUsageOnce(t *testing.T) {
	res := convertFixture(t, fixtures(t)["fork"], Options{})
	var counted []string
	for _, e := range res.Sessions[0].Entries() {
		switch e := e.(type) {
		case *agentsession.CustomEntry:
			var d struct {
				Usage      json.RawMessage `json:"usage"`
				ResponseID string          `json:"response_id"`
			}
			if json.Unmarshal(e.Data, &d) == nil && d.Usage != nil {
				counted = append(counted, d.ResponseID)
			}
		case *agentsession.ResponseEntry:
			if e.Usage != nil {
				counted = append(counted, e.ResponseID)
			}
		}
	}
	if got, want := strings.Join(counted, ", "), "chatcmpl-f3, title-2"; got != want {
		t.Errorf("counted %s, want %s", got, want)
	}
}

func replyEv(id, parent, resp, text string) string {
	return ev("MessageEvent", id, parent, `"llm_message":{"role":"assistant","content":[{"type":"text","text":"`+text+`"}]},"llm_response_id":"`+resp+`"`)
}

// stateLive is a base_state as SDK 1.18 and later write it.
const stateLive = `{"id":"s","agent":{"kind":"Agent","llm":{"model":"m"}},"persistence_dir":"p","invoked_skills":[]}`

// requestOf renders the request context of response resp.
func requestOf(t *testing.T, s *agentsession.Session, resp string) string {
	t.Helper()
	for _, e := range s.Entries() {
		if r, ok := e.(*agentsession.ResponseEntry); ok && r.ResponseID == resp {
			ctx, err := s.RequestContext(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			return strings.Join(renderItems(ctx.Items), "; ")
		}
	}
	t.Fatalf("no response %s", resp)
	return ""
}

// TestEraSummaries keeps an earlier summary a later condensation does
// not forget, as SDK 1.10 and later send it, and drops it for 1.0 to
// 1.9, which sent the last summary alone.
func TestEraSummaries(t *testing.T) {
	events := func(user string) []string {
		return []string{
			userEv("e0000000", "", "start"),
			replyEv("e1000000", "", "r1", "one"),
			userEv("e2000000", "", "more"),
			ev("Condensation", "e3000000", "", `"forgotten_event_ids":["e1000000"],"summary":"S1","summary_offset":1,"llm_response_id":"c1"`),
			userEv("e4000000", "", "again"),
			ev("Condensation", "e5000000", "", `"forgotten_event_ids":["e2000000"],"summary":"S2","summary_offset":1,"llm_response_id":"c2"`),
			replyEv("e6000000", "", "r2", "two"),
			user,
		}
	}
	live := convertEvents(t, stateLive, events(userEv("e7000000", "", "end"))...)
	if got := requestOf(t, live.Sessions[0], "r2"); !strings.HasPrefix(got, "user: S2 | S1;") {
		t.Errorf("1.10 and later: request %s, want both summaries", got)
	}
	old := strings.Replace(userEv("e7000000", "", "end"), `"role":"user"`, `"role":"user","cache_enabled":false`, 1)
	last := convertEvents(t, `{"id":"s","agent":{"kind":"Agent"}}`, events(old)...)
	if got := requestOf(t, last.Sessions[0], "r2"); !strings.HasPrefix(got, "user: S2;") {
		t.Errorf("1.0 to 1.9: request %s, want the last summary alone", got)
	}
}

// TestReloadAtBranchSwitch builds a 1.31+ view incrementally: a call
// left unanswered is sent while the run is live, reported as a guess,
// and left out from the branch switch, where OpenHands reloads.
func TestReloadAtBranchSwitch(t *testing.T) {
	res := convertEvents(t, stateLive,
		userEv("e0000000", "", "go"),
		actionEv("e1000000", "e0000000", "c1", "r1", ""),
		userEv("e2000000", "e1000000", "wait"),
		replyEv("e3000000", "e2000000", "r2", "waiting"),
		userEv("e4000000", "e2000000", "other"),
		replyEv("e5000000", "e4000000", "r3", "done"),
	)
	s := res.Sessions[0]
	if got := requestOf(t, s, "r2"); !strings.Contains(got, "call c1") {
		t.Errorf("live request %s, want the unanswered call sent", got)
	}
	if got := requestOf(t, s, "r3"); strings.Contains(got, "call c1") {
		t.Errorf("request after the branch switch %s, want the call left out", got)
	}
	if !codes(res.Report)["openhands/reload-guess"] {
		t.Error("the live request that a reload would have trimmed is not reported")
	}
}

// TestProbableResume reports an answer whose call waited across a
// user message, which suggests the conversation was reloaded.
func TestProbableResume(t *testing.T) {
	res := convertEvents(t, stateLive,
		userEv("e0000000", "", "go"),
		actionEv("e1000000", "", "c1", "r1", ""),
		userEv("e2000000", "", "approve it"),
		obsEv("e3000000", "", "c1", "ran"),
	)
	if !codes(res.Report)["openhands/reload-guess"] {
		t.Errorf("codes %v: want the probable resume reported", res.Report.Codes())
	}
}

// TestBatchReasoningBefore143 leaves the Responses reasoning item of a
// response of several calls out of later requests in a file older than
// 1.31, as OpenHands before 1.43 sent such a response without it.
func TestBatchReasoningBefore143(t *testing.T) {
	rs := `"responses_reasoning_item":{"id":"rs_1","summary":["think"],"encrypted_content":"enc"}`
	res := convertEvents(t, stateLive,
		userEv("e0000000", "", "go"),
		actionEv("e1000000", "", "c1", "r1", rs),
		actionEv("e2000000", "", "c2", "r1", ""),
		obsEv("e3000000", "", "c1", "one"),
		obsEv("e4000000", "", "c2", "two"),
		replyEv("e5000000", "", "r2", "done"),
	)
	if got := requestOf(t, res.Sessions[0], "r2"); strings.Contains(got, "reasoning") {
		t.Errorf("request %s, want no reasoning item", got)
	}
	if !codes(res.Report)["openhands/batch-reasoning"] {
		t.Error("the left-out reasoning item is not reported")
	}
}

// TestSendForms reports what the send forms of a release change: a
// Responses tool result of several parts sent as several outputs, a
// long text SDK 1.0 to 1.9 cut, and an ACP agent's cache reads read as
// counted in its input when they are not larger than it.
func TestSendForms(t *testing.T) {
	split := convertEvents(t, stateLive,
		userEv("e0000000", "", "go"),
		strings.Replace(actionEv("e1000000", "", "c1", "r1", ""), `"origin":"completion"`, `"origin":"responses"`, 1),
		strings.Replace(obsEv("e2000000", "", "c1", "bad"), `"metadata"`, `"is_error":true,"metadata"`, 1),
	)
	if !codes(split.Report)["openhands/responses-output-split"] {
		t.Error("a Responses result of two parts is not reported")
	}

	long := strings.Repeat("x", 50_001)
	old := convertEvents(t, `{"id":"s","agent":{"kind":"Agent"}}`,
		strings.Replace(userEv("e0000000", "", long), `"role":"user"`, `"role":"user","cache_enabled":false`, 1))
	found := false
	for _, f := range old.Report.Findings {
		found = found || f.Code == "openhands/version-guess" && f.Line == 1
	}
	if !found {
		t.Error("a long user text 1.0 to 1.9 cut is not reported")
	}

	acp := convertEvents(t, `{"id":"s","agent":{"kind":"ACPAgent"},"invoked_skills":[],"stats":{"usage_to_metrics":{"agent":{"token_usages":[{"prompt_tokens":1000,"cache_read_tokens":400,"completion_tokens":5,"response_id":"x"}]}}}}`,
		userEv("e0000000", "", "go"))
	for _, e := range acp.Sessions[0].Entries() {
		if ce, ok := e.(*agentsession.CustomEntry); ok {
			var d struct {
				Usage struct {
					InputTokens int `json:"input_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal(ce.Data, &d) == nil && d.Usage.InputTokens != 1000 {
				t.Errorf("ACP input %d, want 1000: reads not larger than the input are in it", d.Usage.InputTokens)
			}
		}
	}
}
