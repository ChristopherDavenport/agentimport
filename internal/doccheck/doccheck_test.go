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
	if !gaps["agentimport/provenance"] {
		t.Error("agentimport/provenance is not in incompatibilities.md")
	}
}
