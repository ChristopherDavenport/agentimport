package convert

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
)

// exporterDoc wraps steps in a document agentsession's exporter would
// be taken to have written.
func exporterDoc(steps string) []byte {
	return []byte(`{"schema_version":"ATIF-v1.7","session_id":"exp","agent":{"name":"rt","version":"1","model_name":"m-1"},` +
		`"extra":{"agentsession":{"format":"agentsession/0.12"}},"steps":[` + steps + `]}`)
}

// TestExporterBadItems checks that a raw item that does not decode, at
// any level, is a finding and the step or result is mapped from its
// declared fields, with nothing the rebuild began left behind.
func TestExporterBadItems(t *testing.T) {
	for _, tc := range []struct {
		name, steps string
		want        []string
	}{
		{"result null", `{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"agent","message":"","extra":{"openresponses":{"items":[]}},"observation":{"results":[{"content":"x","extra":{"openresponses":{"item":null}}}]}}`,
			[]string{"user: x"}},
		{"result 42", `{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"agent","message":"","extra":{"openresponses":{"items":[]}},"observation":{"results":[{"content":"x","extra":{"openresponses":{"item":42}}}]}}`,
			[]string{"user: x"}},
		{"result typeless", `{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"agent","message":"","extra":{"openresponses":{"items":[]}},"observation":{"results":[{"content":"x","extra":{"openresponses":{"item":{"foo":1}}}}]}}`,
			[]string{"user: x"}},
		// The second item does not decode: the step is mapped from its
		// declared fields, its model change in force and its call
		// neither renamed nor left unanswered.
		{"item 42", `{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"agent","model_name":"m-2","message":"",` +
			`"tool_calls":[{"tool_call_id":"c1","function_name":"f","arguments":{}}],"observation":{"results":[{"source_call_id":"c1","content":"ok"}]},` +
			`"extra":{"openresponses":{"items":[{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},42]}}}`,
			[]string{"call c1 f {}", "output c1 ok"}},
		{"item null", `{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"user","message":"hi","extra":{"openresponses":{"item":null}}}`,
			[]string{"user: hi"}},
		{"pinned 42", `{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"system","message":"s","is_copied_context":true,` +
			`"observation":{"results":[{"content":"summary"}]},"extra":{"context_management":{"type":"compaction","boundary":"replace"},` +
			`"openresponses":{"item":{"type":"message","role":"user","content":"summary"},"pinned":[42]}}}`,
			[]string{"user: summary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := atif.Parse(exporterDoc(tc.steps))
			if err != nil {
				t.Fatal(err)
			}
			res, err := Convert(f, Options{CreatedAt: fixed})
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			doccheck.Check(t, Source, res.Report)
			codes := res.Report.Codes()
			if !slices.Contains(codes, "atif/raw-items") {
				t.Errorf("no atif/raw-items: %v", codes)
			}
			for _, bad := range []string{"atif/call-id", "atif/call-without-result"} {
				if slices.Contains(codes, bad) {
					t.Errorf("spurious %s: the rebuild left state behind", bad)
				}
			}
			s := reread(t, res.Sessions[0])
			if got := contextAtLeaf(t, s); strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("context:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
			if tc.name == "item 42" {
				ctx, err := s.Context()
				if err != nil {
					t.Fatal(err)
				}
				if ctx.Settings.Model != "m-2" {
					t.Errorf("model %q, want m-2", ctx.Settings.Model)
				}
			}
		})
	}
}

// TestExporterMembers checks that members an edit after export leaves
// on an exporter's step, result or observation are kept and reported,
// as they are on any other producer's, whose observation members are
// now written where they are reported.
func TestExporterMembers(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  []byte
		want []string
	}{
		{"exporter", exporterDoc(`{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"agent","message":"","edited":true,` +
			`"extra":{"openresponses":{"items":[{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"}],"response":{"response_id":"r1"}}},` +
			`"observation":{"obs_note":1,"results":[{"source_call_id":"c1","note":"x","extra":{"openresponses":{"item":{"type":"function_call_output","call_id":"c1","output":"ok"}}}}]}}`),
			[]string{`"atif:step":{"edited":true}`, `"atif:result":{"note":"x"}`, `"atif:observation":{"obs_note":1}`}},
		{"exporter item", exporterDoc(`{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"user","message":"hi","edited":true,` +
			`"extra":{"openresponses":{"item":{"type":"message","role":"user","content":"hi"}}}}`),
			[]string{`"atif:step":{"edited":true}`}},
		{"plain", []byte(`{"schema_version":"ATIF-v1.7","session_id":"p","steps":[{"step_id":1,"timestamp":"2026-10-01T00:00:00Z","source":"agent","message":"",` +
			`"tool_calls":[{"tool_call_id":"c1","function_name":"f","arguments":{}}],"observation":{"obs_note":1,"results":[{"source_call_id":"c1","content":"ok"}]}}]}`),
			[]string{`"atif:observation":{"obs_note":1}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := atif.Parse(tc.doc)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Convert(f, Options{CreatedAt: fixed})
			if err != nil {
				t.Fatal(err)
			}
			doccheck.Check(t, Source, res.Report)
			if !slices.Contains(res.Report.Codes(), "atif/unknown-member") {
				t.Errorf("no atif/unknown-member: %v", res.Report.Codes())
			}
			var b bytes.Buffer
			if err := agentsession.Write(&b, res.Sessions[0]); err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(b.String(), w) {
					t.Errorf("the session holds no %s", w)
				}
			}
		})
	}
}
