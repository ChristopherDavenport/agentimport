package convert

import (
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
)

// TestCacheWrites: a step's cache write count is the usage's
// openresponses-experimental.cache_write_tokens (#37).
func TestCacheWrites(t *testing.T) {
	res := convertFixture(t, "g2-steps", Options{})
	if got, want := usagecheck.CacheWrites(res), []string{"10"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
}
