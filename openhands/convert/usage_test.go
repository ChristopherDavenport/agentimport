package convert

import (
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
)

// TestCacheWrites: OpenHands' cache_write_tokens is the usage's
// openresponses-experimental.cache_write_tokens, not a kept member (#37).
func TestCacheWrites(t *testing.T) {
	res := convertFixture(t, fixtures(t)["linear-chat"], Options{})
	if got, want := usagecheck.CacheWrites(res), []string{"150"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
}
