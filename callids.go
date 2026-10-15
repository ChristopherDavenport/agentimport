package agentimport

import (
	"strconv"
	"strings"
)

// maxCallID is the longest call ID a writer gives, the limit the
// strictest provider sets.
const maxCallID = 64

// CallIDs hands out call IDs unique across one session, as the format
// requires of every function call on every branch. A native ID is kept
// when it is free and well formed; otherwise the call gets the native ID
// with every character outside letters, digits, "_" and "-" made "_",
// cut so the whole is at most 64 characters, followed by "_" and a
// number ("call" when it has none), the scheme agentsession's
// export.ItemsFrom and agentturn use.
type CallIDs struct {
	taken map[string]bool
	n     int
}

// Assign returns the call ID to record for a call whose native ID is
// native, and whether it differs from native. A converter that gets a
// renamed ID keeps the native one in a namespaced member of the item
// entry and maps the call's output by the same native ID, in the same
// order.
func (c *CallIDs) Assign(native string) (id string, renamed bool) {
	if c.taken == nil {
		c.taken = map[string]bool{}
	}
	if native != "" && !c.taken[native] && wellFormed(native) {
		c.taken[native] = true
		return native, false
	}
	base := sanitise(native)
	if base == "" {
		base = "call"
	}
	for {
		c.n++
		suffix := "_" + strconv.Itoa(c.n)
		id = base
		if len(id)+len(suffix) > maxCallID {
			id = id[:maxCallID-len(suffix)]
		}
		id += suffix
		if !c.taken[id] {
			c.taken[id] = true
			return id, true
		}
	}
}

// Taken reports whether id has been handed out.
func (c *CallIDs) Taken(id string) bool { return c.taken[id] }

func wellFormed(id string) bool {
	return len(id) <= maxCallID && sanitise(id) == id
}

func sanitise(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		}
		return '_'
	}, id)
}
