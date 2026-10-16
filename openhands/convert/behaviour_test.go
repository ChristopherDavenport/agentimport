package convert

import (
	"path/filepath"
	"strings"
	"testing"

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
