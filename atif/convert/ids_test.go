package convert

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession/jsonl"
)

// TestStoreSafeIDs checks that a native ID a session store refuses is
// replaced by a derived one, the same on every conversion, that the
// store takes, with the native one kept and reported.
func TestStoreSafeIDs(t *testing.T) {
	long := strings.Repeat("r", 300)
	step := `"steps":[{"source":"user","timestamp":"2026-10-01T00:00:00Z","message":"hi"}]`
	child := `"subagent_trajectories":[{"trajectory_id":"t1","session_id":"child run",` + step + `}]`
	for _, tc := range []struct {
		name, doc string
		// member is the header member that keeps the native ID.
		member, native string
	}{
		{"space", `{"session_id":"run 1",` + step + `}`, "atif:session_id", "run 1"},
		{"slash and colon", `{"session_id":"org/run:7",` + step + `}`, "atif:session_id", "org/run:7"},
		{"long", `{"session_id":"` + long + `",` + step + `}`, "atif:session_id", long},
		{"dot", `{"session_id":"..",` + step + `}`, "atif:session_id", ".."},
		{"exporter trajectory_id", `{"trajectory_id":"sess/leaf:1",` + step + `}`, "atif:trajectory_id", "sess/leaf:1"},
		{"embedded", `{"session_id":"main",` + step + `,` + child + `}`, "atif:session_id", "child run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			convert := func() []string {
				f, err := atif.Parse([]byte(tc.doc))
				if err != nil {
					t.Fatal(err)
				}
				res, err := Convert(f, Options{CreatedAt: fixed})
				if err != nil {
					t.Fatal(err)
				}
				doccheck.Check(t, Source, res.Report)
				if !slices.Contains(res.Report.Codes(), "atif/session-id") {
					t.Errorf("no atif/session-id: %v", res.Report.Codes())
				}
				st, err := jsonl.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, s := range res.Sessions {
					h := s.Header()
					if _, err := st.Create(context.Background(), h); err != nil {
						t.Errorf("the store refuses session %q: %v", h.ID, err)
					}
					ids = append(ids, h.ID)
				}
				h := res.Sessions[len(res.Sessions)-1].Header()
				var got string
				if err := json.Unmarshal(h.Extra[tc.member], &got); err != nil || got != tc.native {
					t.Errorf("%s = %s, want %q", tc.member, h.Extra[tc.member], tc.native)
				}
				return ids
			}
			if a, b := convert(), convert(); !slices.Equal(a, b) {
				t.Errorf("IDs differ between conversions: %v, %v", a, b)
			}
		})
	}
}
