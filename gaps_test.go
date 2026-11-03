package agentimport

import "testing"

func TestDocumentedGap(t *testing.T) {
	for code, want := range map[string]bool{
		"agentimport/vcs-branch":     true,
		"atif/context-pruning":       true,
		"agentimport/provenance":     false,
		"agentimport/usage-detail":   false,
		"claudecode/unreached":       false,
		"agentimport/same-instant":   false,
		"agentimport/no-such-code-x": false,
	} {
		if got := DocumentedGap(code); got != want {
			t.Errorf("DocumentedGap(%s) = %v, want %v", code, got, want)
		}
	}
}
