package convert

import (
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
)

// TestCacheWrites: Codex's cache_write_input_tokens is the usage's
// openresponses-experimental.cache_write_tokens (#37).
func TestCacheWrites(t *testing.T) {
	res := convertFixture(t, fixtures(t)["plain"], Options{})
	if got, want := usagecheck.CacheWrites(res), []string{"40"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
}
