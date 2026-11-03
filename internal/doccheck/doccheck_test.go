package doccheck

import (
	"path/filepath"
	"testing"
)

func TestSharedCodes(t *testing.T) {
	codes, err := Codes(filepath.Join(Root(), "docs", "formats", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"agentimport/same-instant", "agentimport/provenance"} {
		if !codes[c] {
			t.Errorf("%s is not listed", c)
		}
	}
	gaps, err := Codes(filepath.Join(Root(), "docs", "incompatibilities.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Converter detail is kept under the converter's namespace, not a
	// gap.
	for _, c := range []string{"agentimport/provenance", "agentimport/usage-detail"} {
		if gaps[c] {
			t.Errorf("%s is in incompatibilities.md", c)
		}
	}
	if !gaps["agentimport/vcs-branch"] {
		t.Error("agentimport/vcs-branch is not in incompatibilities.md")
	}
}
