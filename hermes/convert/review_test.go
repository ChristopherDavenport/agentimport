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
