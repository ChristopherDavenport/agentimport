package convert

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// The tests of this file hold the converter to the findings of its
// review, each on the input that showed it.

// convertScript converts a database built from SQL over the v31
// schema, with the checks every conversion must pass.
func convertScript(t *testing.T, script string, opts Options) *agentimport.Result {
	t.Helper()
	return convertHome(t, buildScript(t, script), opts)
}

// sessionOf returns the converted session of an ID, or fails.
func sessionOf(t *testing.T, res *agentimport.Result, id string) *agentsession.Session {
	t.Helper()
	for _, s := range res.Sessions {
		if s.ID() == id {
			return s
		}
	}
	var ids []string
	for _, s := range res.Sessions {
		ids = append(ids, s.ID())
	}
	t.Fatalf("no session %s among %v", id, ids)
	return nil
}

// leaf renders the context a session resumes with.
func leaf(t *testing.T, s *agentsession.Session) []string {
	t.Helper()
	ctx, err := s.Context()
	if err != nil {
		t.Fatal(err)
	}
	return renderItems(ctx.Items)
}

// codes counts a report's findings by code.
func codes(r *agentimport.Report) map[string]int {
	out := map[string]int{}
	for _, f := range r.Findings {
		out[f.Code]++
	}
	return out
}

// member reads a namespaced member of an entry.
func member(e agentsession.Entry, name string) json.RawMessage {
	return e.Base().Unknown[Source+":"+name]
}

// sameLines fails unless got is want.
func sameLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s:\n got  %q\n want %q", what, got, want)
	}
}

// TestCopiedResponses (F1): an assistant row Hermes copied, a
// compaction's head or tail, a continuation's, a branch's prefix, is a
// response entry marked hermes:copied, as codex marks a fork's copied
// response; every assistant item belongs to a response.
func TestCopiedResponses(t *testing.T) {
	for _, name := range []string{"inplace", "split", "branch"} {
		res := convertFixture(t, name, Options{})
		copied := 0
		for _, s := range res.Sessions {
			responses := map[string]*agentsession.ResponseEntry{}
			for _, e := range s.Entries() {
				if r, ok := e.(*agentsession.ResponseEntry); ok {
					responses[r.ResponseID] = r
					if member(r, "copied") != nil {
						copied++
					}
				}
			}
			for _, e := range s.Entries() {
				ie, ok := e.(*agentsession.ItemEntry)
				if !ok {
					continue
				}
				if m, isMsg := ie.Item.(*openresponses.Message); isMsg && m.Role == openresponses.RoleAssistant && !strings.HasPrefix(m.Text(), "[CONTEXT COMPACTION") {
					if ie.ResponseID == "" || responses[ie.ResponseID] == nil {
						t.Errorf("%s: assistant item %q in %s belongs to no response", name, m.Text(), s.ID())
					}
				}
			}
		}
		if copied == 0 {
			t.Errorf("%s: no response is marked %s:copied", name, Source)
		}
		if n := codes(res.Report)["hermes/copied-row"]; n == 0 {
			t.Errorf("%s: no hermes/copied-row finding", name)
		}
	}
}

// TestUnsignedReasoningText (F21): reasoning text no signature
// covers, OpenRouter's reasoning.text without one and the plain text
// columns, is a reasoning item's content, as
// openresponses/chatcompletions reads plain reasoning; signed Anthropic
// thinking keeps its summary and encrypted_content.
func TestUnsignedReasoningText(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('r1', 'cli', 'm', 1760000000.0);
INSERT INTO messages (session_id, role, content, reasoning, reasoning_details, timestamp, finish_reason) VALUES
  ('r1', 'user', 'q', NULL, NULL, 1760000001.0, NULL),
  ('r1', 'assistant', 'a', 'thought', '[{"type":"reasoning.text","text":"thought","format":"unknown","index":0}]', 1760000002.0, 'stop'),
  ('r1', 'user', 'q2', NULL, NULL, 1760000003.0, NULL),
  ('r1', 'assistant', 'b', 'signed', '[{"type":"thinking","thinking":"signed","signature":"c2ln"}]', 1760000004.0, 'stop'),
  ('r1', 'user', 'q3', NULL, NULL, 1760000005.0, NULL),
  ('r1', 'assistant', 'c', 'bare', '[{"type":"thinking","thinking":"bare"}]', 1760000006.0, 'stop');
`, Options{})
	sameLines(t, "context", leaf(t, sessionOf(t, res, "r1")), []string{
		"user: q", "reasoning text thought", "assistant: a",
		"user: q2", "reasoning signed [encrypted]", "assistant: b",
		"user: q3", "reasoning text bare", "assistant: c",
	})
}

// TestToolPairing (F2): a result pairs only with the calls of the
// assistant turn just before it, by any alias of the call (call_id, id,
// response_item_id, each part of "a|b", trimmed), and a call is kept
// only when the run of tool rows right after it answers it, as
// _drop_stray_tool_results and _prune_unanswered_tool_calls do.
func TestToolPairing(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('p1', 'cli', 'm', 1760000000.0);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at) VALUES ('k1', 'subagent', 'm', '{"_delegate_from":"p1"}', 'p1', 1760000020.5);
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp) VALUES
  ('p1', 'user', 'start', NULL, NULL, 1760000001.0),
  ('p1', 'assistant', '', '[{"id":"c1","call_id":"c1","type":"function","function":{"name":"x","arguments":"{}"}}]', NULL, 1760000002.0),
  ('p1', 'user', 'go on', NULL, NULL, 1760000003.0),
  ('p1', 'tool', 'late', NULL, 'c1', 1760000004.0),
  ('p1', 'assistant', '', '[{"id":"call_1","call_id":"call_1","response_item_id":"fc_1","type":"function","function":{"name":"x","arguments":"{}"}},{"id":"call_2","call_id":"call_2","response_item_id":"fc_2","type":"function","function":{"name":"x","arguments":"{}"}},{"id":"call_3","call_id":"call_3","response_item_id":"fc_3","type":"function","function":{"name":"x","arguments":"{}"}}]', NULL, 1760000010.0),
  ('p1', 'tool', 'one', NULL, 'fc_1', 1760000011.0),
  ('p1', 'tool', 'two', NULL, 'call_2|fc_2', 1760000012.0),
  ('p1', 'tool', 'three', NULL, ' call_3', 1760000013.0),
  ('p1', 'assistant', '', '[{"id":"call_9","call_id":"call_9","response_item_id":"fc_9","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"g\"}"}}]', NULL, 1760000020.0),
  ('p1', 'tool', '{"results":[]}', NULL, 'fc_9', 1760000030.0),
  ('p1', 'assistant', 'done', NULL, NULL, 1760000031.0),
  ('k1', 'user', 'g', NULL, NULL, 1760000021.0),
  ('k1', 'assistant', 'ok', NULL, NULL, 1760000022.0);
`, Options{})
	p := sessionOf(t, res, "p1")
	sameLines(t, "leaf", leaf(t, p), []string{
		"user: start", "user: go on",
		"call call_1 x", "call call_2 x", "call call_3 x",
		"output call_1 one", "output call_2 two", "output call_3 three",
		"call call_9 delegate_task", `output call_9 {"results":[]}`,
		"assistant: done",
	})
	links := 0
	for _, e := range p.Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok && l.Rel == agentsession.RelSubsession && l.CallID == "call_9" {
			links++
		}
	}
	if links != 1 {
		t.Errorf("%d subsession links for call_9, want 1", links)
	}
}

// links lists a session's subsession and peer links as "rel call
// session delegation".
func links(s *agentsession.Session) []string {
	var out []string
	for _, e := range s.Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok && (l.Rel == agentsession.RelSubsession || l.Rel == agentsession.RelPeer) {
			line := l.Rel + " " + l.CallID + " " + l.Session
			if d := member(l, "delegation_id"); d != nil {
				line += " " + string(d)
			}
			out = append(out, line)
		}
	}
	return out
}

// TestBackgroundTie (F3): a background run's children start after the
// handle row, as Hermes creates a child's session on its first turn on
// the async worker; the run is open until its result is delivered, and
// the handle's goals and units tell its children apart; a task it ran
// inline is a subsession.
func TestBackgroundTie(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('bg', 'tui', 'm', 1760000000.0);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at) VALUES
  ('k1', 'subagent', 'm', '{"_delegate_from":"bg"}', 'bg', 1760000100.3),
  ('k2', 'subagent', 'm', '{"_delegate_from":"bg"}', 'bg', 1760000200.4),
  ('k3', 'subagent', 'm', '{"_delegate_from":"bg"}', 'bg', 1760000200.5),
  ('k4', 'subagent', 'm', '{"_delegate_from":"bg"}', 'bg', 1760000200.01);
INSERT INTO async_delegations (delegation_id, origin_session, parent_session_id, state, dispatched_at, completed_at, updated_at) VALUES
  ('d1', 'x', 'bg', 'completed', 1760000100.04, 1760000140.0, 1760000140.0),
  ('d2-1', 'x', 'bg', 'completed', 1760000200.04, 1760000250.0, 1760000250.0),
  ('d2-2', 'x', 'bg', 'running', 1760000200.04, NULL, 1760000250.0);
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, display_metadata, timestamp) VALUES
  ('bg', 'user', 'audit', NULL, NULL, NULL, 1760000001.0),
  ('bg', 'assistant', '', '[{"id":"c1","call_id":"c1","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"Audit.\"}"}}]', NULL, NULL, 1760000100.0),
  ('bg', 'tool', '{"status":"dispatched","mode":"background","count":1,"delegation_id":"d1","goals":["Audit."]}', NULL, 'c1', NULL, 1760000100.05),
  ('bg', 'assistant', 'running', NULL, NULL, NULL, 1760000100.1),
  ('bg', 'user', '[d1 done] fine', NULL, NULL, '{"delegation_id":"d1"}', 1760000150.0),
  ('bg', 'assistant', '', '[{"id":"c2","call_id":"c2","type":"function","function":{"name":"delegate_task","arguments":"{\"tasks\":[{\"goal\":\"One.\"},{\"goal\":\"Two.\"},{\"goal\":\"Three.\"}]}"}}]', NULL, NULL, 1760000200.0),
  ('bg', 'tool', '{"status":"dispatched","mode":"background","count":3,"delegation_id":"d2","goals":["One.","Two.","Three."],"units":[{"delegation_id":"d2-1","group":"a","task_indexes":[0]},{"delegation_id":"d2-2","group":"b","task_indexes":[1]}],"inline_results":[{"task_index":2,"status":"completed"}]}', NULL, 'c2', NULL, 1760000200.05),
  ('bg', 'assistant', 'two running', NULL, NULL, NULL, 1760000200.1),
  ('k1', 'user', 'Audit.', NULL, NULL, NULL, 1760000100.4),
  ('k2', 'user', 'One.', NULL, NULL, NULL, 1760000200.5),
  ('k3', 'user', 'Two.', NULL, NULL, NULL, 1760000200.6),
  ('k4', 'user', 'Three.', NULL, NULL, NULL, 1760000200.02);
`, Options{})
	sameLines(t, "links", links(sessionOf(t, res, "bg")), []string{
		`peer c1 k1 "d1"`,
		"subsession c2 k4",
		`peer c2 k2 "d2-1"`,
		`peer c2 k3 "d2-2"`,
	})
}

// TestOutputlessCall (F4): a delegate_task call with no output stops
// its run at the next turn, so a later call's child is the later
// call's.
func TestOutputlessCall(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('oc', 'cli', 'm', 1760000000.0);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at) VALUES ('kx', 'subagent', 'm', '{"_delegate_from":"oc"}', 'oc', 1760000201.0);
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp) VALUES
  ('oc', 'user', 'go', NULL, NULL, 1760000001.0),
  ('oc', 'assistant', '', '[{"id":"c1","call_id":"c1","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"x\"}"}}]', NULL, 1760000100.0),
  ('oc', 'user', 'again', NULL, NULL, 1760000150.0),
  ('oc', 'assistant', '', '[{"id":"c2","call_id":"c2","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"x\"}"}}]', NULL, 1760000200.0),
  ('oc', 'tool', '{"results":[]}', NULL, 'c2', 1760000210.0),
  ('kx', 'user', 'x', NULL, NULL, 1760000201.5);
`, Options{})
	sameLines(t, "links", links(sessionOf(t, res, "oc")), []string{"subsession c2 " + agentsession.SubsessionID("oc", "c2")})
	if n := codes(res.Report)["hermes/subagent-unlinked"]; n != 0 {
		t.Errorf("%d unlinked sub-agents", n)
	}
}

// TestReviewHarness (F5): a harness prompt drops the message right
// after it only when that is an assistant, a prompt right after another
// arms nothing, the test reads the content as Hermes loads it, and a
// rewound branch starts clean, as _strip_background_review_harness
// runs over the loaded live rows.
func TestReviewHarness(t *testing.T) {
	const h = "Review the conversation above and consider saving to memory"
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('rh', 'cli', 'm', 1760000000.0);
INSERT INTO messages (session_id, role, content, tool_call_id, timestamp, active) VALUES
  ('rh', 'user', 'hi', NULL, 1760000001.0, 1),
  ('rh', 'user', '`+h+` one.', NULL, 1760000002.0, 1),
  ('rh', 'user', '`+h+` two.', NULL, 1760000003.0, 1),
  ('rh', 'assistant', 'A', NULL, 1760000004.0, 1),
  ('rh', 'system', '`+h+` three.', NULL, 1760000005.0, 1),
  ('rh', 'tool', 'out', 'x', 1760000006.0, 1),
  ('rh', 'assistant', 'B', NULL, 1760000007.0, 1),
  ('rh', 'user', '<memory-context>m</memory-context>`+h+` four.', NULL, 1760000008.0, 1),
  ('rh', 'assistant', 'curated', NULL, 1760000009.0, 1),
  ('rh', 'user', '`+h+` five.', NULL, 1760000010.0, 1),
  ('rh', 'assistant', 'rewound', NULL, 1760000011.0, 0),
  ('rh', 'assistant', 'C', NULL, 1760000012.0, 1);
`, Options{})
	s := sessionOf(t, res, "rh")
	// C is the live row right after the fifth prompt, which a resume
	// strips with it; the rewound assistant between them is on its own
	// branch and stays an item there.
	sameLines(t, "leaf", leaf(t, s), []string{"user: hi", "assistant: A", "assistant: B"})
	found := false
	for _, e := range s.Entries() {
		if ie, ok := e.(*agentsession.ItemEntry); ok {
			if m, ok := ie.Item.(*openresponses.Message); ok && m.Text() == "rewound" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the rewound assistant is no item")
	}
}

// TestStoredContentKept (F6): when the text Hermes sends differs from
// the content it stores, sanitized of its memory blocks, stripped, or a
// stale marker blanked, the stored content is kept in the row's columns
// and reported.
func TestStoredContentKept(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('sc', 'cli', 'm', 1760000000.0);
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp) VALUES
  ('sc', 'user', 'Bob: <memory-context>likes Go</memory-context>why?', NULL, NULL, 1760000001.0),
  ('sc', 'assistant', '[terminal]', '[{"id":"c","call_id":"c","type":"function","function":{"name":"terminal","arguments":"{}"}}]', NULL, 1760000002.0),
  ('sc', 'tool', '  out  ', NULL, 'c', 1760000003.0),
  ('sc', 'assistant', 'same', NULL, NULL, 1760000004.0);
`, Options{})
	s := sessionOf(t, res, "sc")
	kept := map[string]string{}
	for _, e := range s.Entries() {
		row := member(e, "row")
		if row == nil {
			continue
		}
		var cols map[string]json.RawMessage
		_ = json.Unmarshal(member(e, "columns"), &cols)
		var content string
		_ = json.Unmarshal(cols["content"], &content)
		kept[string(row)] = content
	}
	want := map[string]string{"1": "Bob: <memory-context>likes Go</memory-context>why?", "2": "[terminal]", "3": "  out  ", "4": ""}
	for row, c := range want {
		if kept[row] != c {
			t.Errorf("row %s keeps content %q, want %q", row, kept[row], c)
		}
	}
	if n := codes(res.Report)["hermes/sanitized-content"]; n != 3 {
		t.Errorf("%d hermes/sanitized-content findings, want 3", n)
	}
}

// TestRequestPasses (F7): an assistant turn of reasoning alone is left
// out of every request after it, as drop_thinking_only_and_merge_users
// leaves it out of each request's copy; an empty turn, which
// repair_empty_non_final_messages sends as a placeholder once it is not
// the last, is reported.
func TestRequestPasses(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('rp', 'cli', 'm', 1760000000.0);
INSERT INTO messages (session_id, role, content, reasoning_content, timestamp) VALUES
  ('rp', 'user', 'one', NULL, 1760000001.0),
  ('rp', 'assistant', '', 'thinking', 1760000002.0),
  ('rp', 'user', 'two', NULL, 1760000003.0),
  ('rp', 'assistant', '', NULL, 1760000004.0),
  ('rp', 'user', 'three', NULL, 1760000005.0),
  ('rp', 'assistant', 'done', NULL, 1760000006.0);
`, Options{})
	s := sessionOf(t, res, "rp")
	sameLines(t, "leaf", leaf(t, s), []string{"user: one", "user: two", "assistant: ", "user: three", "assistant: done"})
	for _, e := range s.Entries() {
		r, ok := e.(*agentsession.ResponseEntry)
		if !ok || r.ResponseID != "row-4" {
			continue
		}
		c, err := s.RequestContext(r.ID)
		if err != nil {
			t.Fatal(err)
		}
		sameLines(t, "request of row-4", renderItems(c.Items), []string{"user: one", "user: two"})
	}
	got := codes(res.Report)
	if got["hermes/thinking-only"] != 1 || got["hermes/empty-turn"] != 1 {
		t.Errorf("findings %v", got)
	}
}

// TestCodexMessageItems (F8): every assistant message item of
// codex_message_items is a message of its own, with its phase, and
// keeps its ID unless the turn's Codex reasoning items carry encrypted
// content, as _replay_message_items replays them.
func TestCodexMessageItems(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('cm', 'cli', 'm', 1760000000.0);
INSERT INTO messages (session_id, role, content, reasoning_details, codex_message_items, timestamp) VALUES
  ('cm', 'user', 'go', NULL, NULL, 1760000001.0),
  ('cm', 'assistant', 'Looking.Done.', '[{"type":"thinking","thinking":"t","signature":"s"}]',
   '[{"type":"message","role":"assistant","id":"msg_1","phase":"commentary","content":[{"type":"output_text","text":"Looking."}]},{"type":"message","role":"assistant","id":"msg_2","phase":"final_answer","content":[{"type":"output_text","text":"Done."}]}]', 1760000002.0);
`, Options{})
	s := sessionOf(t, res, "cm")
	var got []string
	for _, e := range s.Entries() {
		if ie, ok := e.(*agentsession.ItemEntry); ok {
			if m, ok := ie.Item.(*openresponses.Message); ok && m.Role == openresponses.RoleAssistant {
				got = append(got, m.ID+" "+string(m.Phase)+" "+m.Text())
			}
		}
	}
	sameLines(t, "messages", got, []string{"msg_1 commentary Looking.", "msg_2 final_answer Done."})
}

// compactions counts a session's compaction entries.
func compactions(s *agentsession.Session) int {
	n := 0
	for _, e := range s.Entries() {
		if _, ok := e.(*agentsession.CompactionEntry); ok {
			n++
		}
	}
	return n
}

// copiedResponses lists the response IDs marked hermes:copied.
func copiedResponses(s *agentsession.Session) []string {
	var out []string
	for _, e := range s.Entries() {
		if r, ok := e.(*agentsession.ResponseEntry); ok && member(r, "copied") != nil {
			out = append(out, r.ResponseID)
		}
	}
	return out
}

// TestBranchCompactsItself (F9): a branch whose parent never compacted
// compacts in place itself; its first summary is its own, a generation
// boundary, not one the copy brought.
func TestBranchCompactsItself(t *testing.T) {
	const sum = "[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below. "
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at, end_reason, ended_at) VALUES ('P', 'tui', 'm', 1760000000.0, 'branched', 1760000050.0);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at) VALUES ('B', 'tui', 'm', '{"_branched_from":"P"}', 'P', 1760000060.0);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, _compressed_summary) VALUES
  (1, 'P', 'user', 'u1', 1760000001.0, 1, 0, 0),
  (2, 'P', 'assistant', 'a1', 1760000002.0, 1, 0, 0),
  (3, 'B', 'user', 'u1', 1760000001.0, 0, 1, 0),
  (4, 'B', 'assistant', 'a1', 1760000002.0, 0, 1, 0),
  (5, 'B', 'user', 'u2', 1760000061.0, 0, 1, 0),
  (6, 'B', 'assistant', 'a2', 1760000062.0, 0, 1, 0),
  (7, 'B', 'user', '`+sum+`one', 1760000100.0, 0, 1, 1),
  (8, 'B', 'user', 'u3', 1760000101.0, 0, 1, 0),
  (9, 'B', 'assistant', 'a3', 1760000102.0, 0, 1, 0),
  (10, 'B', 'assistant', '`+sum+`two', 1760000200.0, 1, 0, 1),
  (11, 'B', 'user', 'u4', 1760000201.0, 1, 0, 0),
  (12, 'B', 'assistant', 'a4', 1760000202.0, 1, 0, 0);
`, Options{})
	b := sessionOf(t, res, "B")
	if n := compactions(b); n != 2 {
		t.Errorf("%d compactions, want 2", n)
	}
	sameLines(t, "copied", copiedResponses(b), []string{"row-4"})
}

// TestBranchOfCompactedParent (F10): a branch's copy is told against
// every row of its parent, not only those live now, as the parent may
// compact after the branch.
func TestBranchOfCompactedParent(t *testing.T) {
	const sum = "[CONTEXT COMPACTION — REFERENCE ONLY] Earlier turns were compacted into the summary below. "
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('P', 'tui', 'm', 1760000000.0);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at) VALUES ('B', 'tui', 'm', '{"_branched_from":"P"}', 'P', 1760000060.0);
INSERT INTO messages (id, session_id, role, content, timestamp, active, compacted, _compressed_summary) VALUES
  (1, 'P', 'user', 'u1', 1760000001.0, 0, 1, 0),
  (2, 'P', 'assistant', 'a1', 1760000002.0, 0, 1, 0),
  (3, 'P', 'user', 'u2', 1760000003.0, 0, 1, 0),
  (4, 'P', 'assistant', 'a2', 1760000004.0, 0, 1, 0),
  (5, 'B', 'user', 'u1', 1760000001.0, 1, 0, 0),
  (6, 'B', 'assistant', 'a1', 1760000002.0, 1, 0, 0),
  (7, 'B', 'user', 'u2', 1760000003.0, 1, 0, 0),
  (8, 'B', 'assistant', 'a2', 1760000004.0, 1, 0, 0),
  (9, 'B', 'user', 'mine', 1760000061.0, 1, 0, 0),
  (10, 'P', 'user', '`+sum+`s', 1760000100.0, 1, 0, 1),
  (11, 'P', 'user', 'later', 1760000101.0, 1, 0, 0);
`, Options{})
	sameLines(t, "copied", copiedResponses(sessionOf(t, res, "B")), []string{"row-6", "row-8"})
}

// TestSummaryByText (F11): a summary row a database's flag does not
// mark, written before the column, is told by its text, standalone or
// merged into the first tail row, as _is_context_summary_message tells
// it.
func TestSummaryByText(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at, ended_at, end_reason) VALUES ('P', 'cli', 'm', 1760000000.0, 1760000100.0, 'compression');
INSERT INTO sessions (id, source, model, parent_session_id, started_at, ended_at, end_reason) VALUES ('C', 'cli', 'm', 'P', 1760000100.5, 1760000200.0, 'compression');
INSERT INTO sessions (id, source, model, parent_session_id, started_at) VALUES ('D', 'cli', 'm', 'C', 1760000200.5);
INSERT INTO messages (session_id, role, content, timestamp, _compressed_summary) VALUES
  ('P', 'user', 'u1', 1760000001.0, 0),
  ('P', 'assistant', 'a1', 1760000002.0, 0),
  ('P', 'user', 'u2', 1760000003.0, 0),
  ('P', 'assistant', 'a2', 1760000004.0, 0),
  ('C', 'user', 'u1', 1760000001.0, 0),
  ('C', 'assistant', 'a1', 1760000002.0, 0),
  ('C', 'user', '[CONTEXT SUMMARY]: old', 1760000100.2, 0),
  ('C', 'assistant', 'a2', 1760000004.0, 0),
  ('C', 'user', 'u3', 1760000110.0, 0),
  ('D', 'user', 'u1', 1760000001.0, 0),
  ('D', 'assistant', '[PRIOR CONTEXT — for reference only; not a new message]' || char(10) || 'a1' || char(10) || char(10) || '[END OF PRIOR CONTEXT — COMPACTION SUMMARY BELOW]' || char(10) || char(10) || '[CONTEXT SUMMARY]: merged', 1760000200.2, 0),
  ('D', 'user', 'u4', 1760000210.0, 0);
`, Options{})
	sameLines(t, "C copied", copiedResponses(sessionOf(t, res, "C")), []string{"row-6", "row-8"})
	if n := codes(res.Report)["hermes/summary-row"]; n != 1 {
		t.Errorf("%d summary rows", n)
	}
}

// header reads a converted session's parent_session.
func parentOf(t *testing.T, res *agentimport.Result, id string) string {
	t.Helper()
	return sessionOf(t, res, id).Header().ParentSession
}

// TestSelfParent (F12): a session that names itself as its parent is
// converted with no parent, and the cycle reported.
func TestSelfParent(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, parent_session_id, started_at) VALUES ('s1', 'cli', 'm', 's1', 1760000000.0);
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('s1', 'user', 'hi', 1760000001.0);
`, Options{})
	if p := parentOf(t, res, "s1"); p != "" {
		t.Errorf("parent_session %q", p)
	}
	got := codes(res.Report)
	if got["hermes/parent-cycle"] != 1 || got["hermes/parent-missing"] != 0 {
		t.Errorf("findings %v", got)
	}
}

// TestCycleBelow (F13): a session whose ancestors form a cycle keeps
// its parent; the cycle is broken at a member of it, the earliest
// started.
func TestCycleBelow(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, parent_session_id, started_at) VALUES
  ('C', 'cli', 'm', 'A', 1760000001.0),
  ('A', 'cli', 'm', 'B', 1760000002.0),
  ('B', 'cli', 'm', 'A', 1760000003.0);
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('C', 'user', 'c', 1760000004.0), ('A', 'user', 'a', 1760000005.0), ('B', 'user', 'b', 1760000006.0);
`, Options{})
	if p := parentOf(t, res, "C"); p != "A" {
		t.Errorf("C's parent_session %q, want A", p)
	}
	if p := parentOf(t, res, "A"); p != "" {
		t.Errorf("A's parent_session %q, want none", p)
	}
	if p := parentOf(t, res, "B"); p != "A" {
		t.Errorf("B's parent_session %q, want A", p)
	}
	got := codes(res.Report)
	if got["hermes/parent-cycle"] != 1 || got["hermes/subagent-cycle"] != 0 {
		t.Errorf("findings %v", got)
	}
}

// TestParentIDBeforeConversion (F18): a branch converted before its
// parent names the session ID its parent converts to.
func TestParentIDBeforeConversion(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('p 1', 'cli', 'm', 1760000010.0);
INSERT INTO sessions (id, source, model, model_config, parent_session_id, started_at) VALUES ('B', 'cli', 'm', '{"_branched_from":"p 1"}', 'p 1', 1760000001.0);
INSERT INTO messages (session_id, role, content, timestamp) VALUES ('p 1', 'user', 'p', 1760000011.0), ('B', 'user', 'b', 1760000012.0);
`, Options{})
	want := agentsession.SubsessionID(Source, "session/p 1")
	sessionOf(t, res, want)
	if p := parentOf(t, res, "B"); p != want {
		t.Errorf("B's parent_session %q, want %q", p, want)
	}
	for _, e := range sessionOf(t, res, "B").Entries() {
		if l, ok := e.(*agentsession.LinkEntry); ok && l.Rel == agentsession.RelForkOf && l.Session != want {
			t.Errorf("fork_of names %q", l.Session)
		}
	}
}

// TestNestedFailure (F14): when a sub-agent fails and then its parent
// does, every row is still counted, the tally checked by convertHome.
func TestNestedFailure(t *testing.T) {
	testHookRun = func(sc *sconv) error {
		switch sc.rec.s.ID {
		case "20261005_060000_ee0007", "20261005_060010_ee0008":
			return errors.New("injected")
		}
		return nil
	}
	defer func() { testHookRun = nil }()
	convertFixture(t, "subagents", Options{})
}

// TestRowsReleased (F15): no session's rows outlive its conversion,
// and no conversion holds the session it built beside the one it read
// back.
func TestRowsReleased(t *testing.T) {
	var held []string
	testHookDone = func(fam *family) {
		for id, rec := range fam.recs {
			if rec.loaded || rec.msgs != nil {
				held = append(held, id)
			}
		}
	}
	defer func() { testHookDone = nil }()
	for _, name := range []string{"subagents", "branch", "split"} {
		held = nil
		res := convertFixture(t, name, Options{})
		if len(held) > 0 {
			t.Errorf("%s: rows of %v held after the conversion", name, held)
		}
		_ = res
	}
}

// TestGenerationStart (F17): a generation whose first item is a call
// starts with no summary item made of it, the call keeps its entry,
// and a generation whose first row makes no item does not start on a
// branch of rows out of context.
func TestGenerationStart(t *testing.T) {
	res := convertScript(t, `
INSERT INTO sessions (id, source, model, started_at) VALUES ('g1', 'cli', 'm', 1760000000.0), ('g2', 'telegram', 'm', 1760000000.0);
INSERT INTO messages (session_id, role, content, tool_calls, tool_call_id, timestamp, active, compacted) VALUES
  ('g1', 'user', 'old', NULL, NULL, 1760000001.0, 0, 1),
  ('g1', 'assistant', 'old answer', NULL, NULL, 1760000002.0, 0, 1),
  ('g1', 'assistant', '', '[{"id":"c","call_id":"c","type":"function","function":{"name":"x","arguments":"{}"}}]', NULL, 1760000003.0, 1, 0),
  ('g1', 'tool', '{"error":"BLOCKED: User denied this. The user has NOT consented to this action."}', NULL, 'c', 1760000004.0, 1, 0),
  ('g1', 'user', 'next', NULL, NULL, 1760000005.0, 1, 0),
  ('g2', 'user', 'old', NULL, NULL, 1760000001.0, 0, 1),
  ('g2', 'assistant', 'old answer', NULL, NULL, 1760000002.0, 0, 1),
  ('g2', 'session_meta', NULL, NULL, NULL, 1760000003.0, 1, 0),
  ('g2', 'assistant', 'rewound', NULL, NULL, 1760000004.0, 0, 0),
  ('g2', 'user', 'next', NULL, NULL, 1760000005.0, 1, 0);
`, Options{})
	g1 := sessionOf(t, res, "g1")
	sameLines(t, "g1 leaf", leaf(t, g1), []string{"call c x", `output c {"error":"BLOCKED: User denied this. The user has `, "user: next"})
	for _, e := range g1.Entries() {
		if c, ok := e.(*agentsession.CompactionEntry); ok {
			if _, isMsg := c.Summary.(*openresponses.Message); !isMsg {
				t.Errorf("a compaction's summary is a %s", c.Summary.ItemType())
			}
		}
		if d, ok := e.(*agentsession.DecisionEntry); ok {
			if target, ok := g1.Entry(d.Target); !ok {
				t.Error("the decision names no entry")
			} else if _, isItem := target.(*agentsession.ItemEntry); !isItem {
				t.Errorf("the decision names a %s", target.EntryType())
			}
		}
	}
	sameLines(t, "g2 leaf", leaf(t, sessionOf(t, res, "g2")), []string{"user: next"})
}

// TestFallbackPromptKept (F19): when the prompt comes from
// system_prompts, the row's own system_prompt, Hermes' read fallback,
// stays with the session row.
func TestFallbackPromptKept(t *testing.T) {
	res := convertScript(t, `
INSERT INTO system_prompts VALUES ('h', 'new prompt');
INSERT INTO sessions (id, source, model, system_prompt, system_prompt_hash, started_at) VALUES ('fp', 'cli', 'm', 'old prompt', 'h', 1760000000.0);
`, Options{})
	var row map[string]json.RawMessage
	if err := json.Unmarshal(sessionOf(t, res, "fp").Header().Extra[Source+":session"], &row); err != nil {
		t.Fatal(err)
	}
	if string(row["system_prompt"]) != `"old prompt"` {
		t.Errorf("system_prompt %s", row["system_prompt"])
	}
}
