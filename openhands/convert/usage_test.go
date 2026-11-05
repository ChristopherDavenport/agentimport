package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/internal/usagecheck"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// TestCacheWrites: OpenHands' cache_write_tokens is the usage's
// openresponses-experimental.cache_write_tokens, not a kept member (#37).
func TestCacheWrites(t *testing.T) {
	res := convertFixture(t, fixtures(t)["linear-chat"], Options{})
	if got, want := usagecheck.CacheWrites(res), []string{"150"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("cache writes %q, want %q", got, want)
	}
}

// TestACPCacheWrites: the ACP adapters that report cache writes
// (claude-agent-acp, codex-acp from 2026-10-10) count both the reads and
// the writes apart from inputTokens, so an ACP usage with writes has
// both folded into the input, reads or no reads; without writes, reads
// no larger than the input are still taken as counted in it.
func TestACPCacheWrites(t *testing.T) {
	for _, tc := range []struct {
		name               string
		usage              string
		input, read, write int
		acpUsage           bool
	}{
		{"writes only", `"prompt_tokens":1000,"cache_write_tokens":300`, 1300, 0, 300, false},
		{"reads and writes", `"prompt_tokens":1000,"cache_read_tokens":400,"cache_write_tokens":300`, 1700, 400, 300, false},
		{"reads only", `"prompt_tokens":1000,"cache_read_tokens":400`, 1000, 400, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := convertEvents(t, `{"id":"s","agent":{"kind":"ACPAgent"},"invoked_skills":[],"stats":{"usage_to_metrics":{"agent":{"token_usages":[{`+tc.usage+`,"completion_tokens":5,"response_id":"x"}]}}}}`,
				userEv("e0000000", "", "go"))
			var got *openresponses.Usage
			for _, e := range res.Sessions[0].Entries() {
				if ce, ok := e.(*agentsession.CustomEntry); ok {
					var d struct {
						Usage *openresponses.Usage `json:"usage"`
					}
					if json.Unmarshal(ce.Data, &d) == nil && d.Usage != nil {
						got = d.Usage
					}
				}
			}
			if got == nil {
				t.Fatal("no usage")
			}
			w, _ := got.InputTokensDetails.CacheWriteTokens()
			if got.InputTokens != tc.input || got.InputTokensDetails.CachedTokens != tc.read || w != tc.write {
				t.Errorf("input %d cached %d writes %d, want %d %d %d", got.InputTokens, got.InputTokensDetails.CachedTokens, w, tc.input, tc.read, tc.write)
			}
			var acp bool
			for _, f := range res.Report.Findings {
				acp = acp || f.Code == "openhands/acp-usage"
				if f.Code == agentimport.UsageInconsistentCode {
					t.Errorf("%+v", f)
				}
			}
			if acp != tc.acpUsage {
				t.Errorf("openhands/acp-usage reported %v, want %v", acp, tc.acpUsage)
			}
		})
	}
}
