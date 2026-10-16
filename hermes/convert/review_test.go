package convert

import (
	"encoding/json"
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
