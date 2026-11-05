package convert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
)

// TestCacheWrites: a response's cache writes are its usage's
// openresponses-experimental.cache_write_tokens, split by TTL from the
// native cache_creation as providers/anthropic splits it (#37); a usage
// with no split carries the count alone.
func TestCacheWrites(t *testing.T) {
	const sid = "22222222-2222-4222-8222-000000000002"
	env := `"isSidechain":false,"userType":"external","entrypoint":"cli","cwd":"/work/demo","version":"2.1.290","sessionId":"` + sid + `"`
	asst := func(uuid, parent, id, ts, usage string) string {
		return `{"type":"assistant","uuid":"` + uuid + `","parentUuid":"` + parent + `","timestamp":"` + ts + `","message":{"id":"` + id +
			`","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"usage":` +
			usage + `},"requestId":"req_` + id + `",` + env + `}`
	}
	user := func(uuid, parent, ts string) string {
		p := `"` + parent + `"`
		if parent == "" {
			p = "null"
		}
		return `{"type":"user","uuid":"` + uuid + `","parentUuid":` + p + `,"timestamp":"` + ts + `","message":{"role":"user","content":"go"},` + env + `}`
	}
	lines := []string{
		user("u1", "", "2026-09-01T10:00:01Z"),
		asst("a1", "u1", "msg_1", "2026-09-01T10:00:02Z", `{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":30,"ephemeral_1h_input_tokens":70}}`),
		user("u2", "a1", "2026-09-01T10:00:03Z"),
		asst("a2", "u2", "msg_2", "2026-09-01T10:00:04Z", `{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":40,"cache_read_input_tokens":100}`),
		user("u3", "a2", "2026-09-01T10:00:05Z"),
		asst("a3", "u3", "msg_3", "2026-09-01T10:00:06Z", `{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":0,"cache_read_input_tokens":140,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}}`),
	}
	path := filepath.Join(t.TempDir(), sid+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, res := convertFixture(t, path, Options{})
	got := usagecheck.CacheWrites(res)
	want := []string{"100 1h=70,5m=30", "40"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
}
