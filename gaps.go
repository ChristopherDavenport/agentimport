package agentimport

import (
	_ "embed"
	"regexp"
	"sync"
)

// incompatibilities is docs/incompatibilities.md, the table of what the
// session format has no place for.
//
//go:embed docs/incompatibilities.md
var incompatibilities []byte

// tableCode matches a backquoted finding code at the start of a table
// row.
var tableCode = regexp.MustCompile("(?m)^\\|\\s*`([a-z0-9_-]+/[a-z0-9_.-]+)`")

var documentedGaps = sync.OnceValue(func() map[string]bool {
	codes := map[string]bool{}
	for _, m := range tableCode.FindAllSubmatch(incompatibilities, -1) {
		codes[string(m[1])] = true
	}
	return codes
})

// DocumentedGap reports whether code is a gap docs/incompatibilities.md
// lists: a known, permanent shape of the session format, which says
// nothing about one transcript. A test holds every gap a converter
// reports to that table, so a gap this does not list is a converter's
// mistake.
func DocumentedGap(code string) bool { return documentedGaps()[code] }
