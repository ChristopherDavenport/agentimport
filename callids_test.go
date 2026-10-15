package agentimport

import (
	"strings"
	"testing"
)

func TestCallIDs(t *testing.T) {
	long := strings.Repeat("x", 70)
	var c CallIDs
	for _, tc := range []struct {
		native, want string
		renamed      bool
	}{
		{"toolu_01", "toolu_01", false},
		{"toolu_01", "toolu_01_1", true},
		{"", "call_2", true},
		{"call_abc|fc_123", "call_abc_fc_123_3", true},
		{long, strings.Repeat("x", 62) + "_4", true},
		{"call_2", "call_2_5", true},
	} {
		got, renamed := c.Assign(tc.native)
		if got != tc.want || renamed != tc.renamed {
			t.Errorf("Assign(%q) = %q, %v; want %q, %v", tc.native, got, renamed, tc.want, tc.renamed)
		}
		if len(got) > maxCallID {
			t.Errorf("Assign(%q) = %q, longer than %d", tc.native, got, maxCallID)
		}
	}
}
