package convert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherDavenport/agentimport/clinesdk"
)

// FuzzConvert converts any session: a history, a sidecar and a child's
// history of any bytes. It must never panic or fail, and it accounts
// for every unit.
func FuzzConvert(f *testing.F) {
	dirs, _ := filepath.Glob("testdata/*/sessions/*")
	for _, d := range dirs {
		id := filepath.Base(d)
		msgs, _ := os.ReadFile(filepath.Join(d, id+clinesdk.SuffixMessages))
		comp, _ := os.ReadFile(filepath.Join(d, id+clinesdk.SuffixCompaction))
		children, _ := filepath.Glob(filepath.Join(d, "*"+clinesdk.SuffixMessages))
		var child []byte
		for _, c := range children {
			if filepath.Base(c) != id+clinesdk.SuffixMessages {
				child, _ = os.ReadFile(c)
				break
			}
		}
		man, _ := os.ReadFile(filepath.Join(d, id+clinesdk.SuffixManifest))
		f.Add(msgs, comp, child, man)
	}
	f.Add([]byte(`{"version":1,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"spawn_agent","input":{"task":"t"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"x"},{"type":"tool_result","tool_use_id":"a","content":"again"}]}]}`),
		[]byte(`{"version":1,"updated_at":"2026-10-01T10:00:00Z","source_message_count":1,"source_last_message_key":"ts:user:1","messages":[{"role":"user","content":"s"}]}`),
		[]byte(`{"version":1,"agent":"subagent","messages":[{"role":"user","content":"t"},{"role":"assistant","content":[{"type":"tool_use","id":"b","name":"spawn_agent","input":{"task":"t"}}]}]}`),
		[]byte(`{"version":1,"session_id":"s","metadata":{"fork":{"forkedFromSessionId":"s","forkedAt":"x"}}}`))
	f.Fuzz(func(t *testing.T, msgs, comp, child, man []byte) {
		if len(msgs)+len(comp)+len(child)+len(man) > 1<<20 {
			return
		}
		files := map[string][]byte{}
		if len(msgs) > 0 {
			files["s"+clinesdk.SuffixMessages] = msgs
		}
		if len(comp) > 0 {
			files["s"+clinesdk.SuffixCompaction] = comp
		}
		if len(child) > 0 {
			files["agent_1"+clinesdk.SuffixMessages] = child
			files["agent_2__x"+clinesdk.SuffixMessages] = child
		}
		if len(man) > 0 {
			files["s"+clinesdk.SuffixManifest] = man
		}
		s := clinesdk.ParseSession("s", files)
		res, err := Convert(s, Options{})
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if got, want := res.Report.Lines(), s.Units(); got != want {
			t.Fatalf("the report accounts for %d units; the session holds %d", got, want)
		}
	})
}
