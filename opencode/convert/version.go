package convert

import (
	"strconv"
	"strings"
)

// version is an OpenCode release, major.minor.patch.
type version [3]int

// parseVersion reads a release such as 1.14.40, or 0.0.0-202601010000
// for a snapshot build, whose prerelease is ignored.
func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	var v version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v[i] = n
	}
	return v, true
}

// atLeast reports whether v is o or later.
func (v version) atLeast(o version) bool {
	for i := range v {
		if v[i] != o[i] {
			return v[i] > o[i]
		}
	}
	return true
}

// The releases where OpenCode's V1 behaviour changed.
var (
	// v1316 stopped counting reasoning in output tokens.
	v1316 = version{1, 3, 16}
	// v11419 kept a tail of messages after a compaction, from the
	// message tail_start_id names.
	v11419 = version{1, 14, 19}
	// v11440 moved that tail after the summary in the request.
	v11440 = version{1, 14, 40}
	// latest is the newest V1 release read, whose behaviour a session
	// of no readable version is given.
	latest = version{1, 19, 1}
)
