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

// TestSubagentLinks checks the task fixture's links: the first call's
// sub-agent a subsession whose header agrees with its link, the call
// resuming it a peer link after its output naming the same session.
func TestSubagentLinks(t *testing.T) {
	res := convertFixture(t, fixtures(t)["subagent-task"], Options{})
	if len(res.Sessions) != 2 {
		t.Fatalf("%d sessions, want 2", len(res.Sessions))
	}
	parent, child := res.Sessions[0], res.Sessions[1]
	byID := map[string]*agentsession.Session{child.ID(): child}
	if err := parent.VerifyLinks(func(id string) (*agentsession.Session, error) { return byID[id], nil }); err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, e := range parent.Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok {
			if l.Session != child.ID() {
				t.Errorf("link names %s, not the child", l.Session)
			}
			rels = append(rels, l.Rel+" "+l.CallID)
		}
	}
	if got, want := strings.Join(rels, ", "), "subsession call_t1, peer call_t2"; got != want {
		t.Errorf("links %s, want %s", got, want)
	}
	if h := child.Header(); h.ParentSession != parent.ID() || h.SpawnedBy != "call_t1" {
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
