package convert

import (
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
	"github.com/ChristopherDavenport/agentimport/pi"
	"github.com/ChristopherDavenport/agentsession"
)

// TestCacheWrites: pi's cacheWrite is the usage's
// openresponses-experimental.cache_write_tokens, split by TTL when pi
// gives cacheWrite1h, the part of it kept an hour (#37).
func TestCacheWrites(t *testing.T) {
	res := convertFixture(t, fixtures(t)["anthropic"], Options{})
	if got, want := usagecheck.CacheWrites(res), []string{"200"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
	h := int64(200)
	u := usage(&pi.Usage{Input: 5, Output: 1, CacheWrite: 300, CacheWrite1h: &h})
	r := &agentimport.Result{Sessions: []*agentsession.Session{agentsession.New(agentsession.Header{})}}
	if _, err := r.Sessions[0].Append(&agentsession.ResponseEntry{ResponseID: "r", Usage: u}); err != nil {
		t.Fatal(err)
	}
	if got, want := usagecheck.CacheWrites(r), []string{"300 1h=200,5m=100"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
}
