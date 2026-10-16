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

// release is an OpenCode release that changed how a request is built,
// and when it was tagged, in milliseconds.
type release struct {
	v    version
	date int64
}

// The releases where OpenCode's V1 behaviour changed, bisected over the
// tags of anomalyco/opencode, dated by their commits.
var (
	// r1316 stopped counting reasoning in output tokens
	// (2026-04-06T03:44:46Z).
	r1316 = release{version{1, 3, 16}, 1775447086000}
	// r142 sends the output an interrupted call had, metadata.output,
	// rather than its error (2026-04-09T16:24:43Z).
	r142 = release{version{1, 4, 2}, 1775751883000}
	// r11440 moves a compaction's tail after its summary, and sends an
	// empty text beside signed Anthropic reasoning as a space
	// (2026-05-07T00:33:57Z).
	r11440 = release{version{1, 14, 40}, 1778114037000}
	// r11442 stops sending an empty user text (2026-05-09T16:54:32Z).
	r11442 = release{version{1, 14, 42}, 1778345672000}
	// v11419 began writing a compaction's tail_start_id.
	v11419 = version{1, 14, 19}
)

// String writes a version as OpenCode does.
func (v version) String() string {
	return strconv.Itoa(v[0]) + "." + strconv.Itoa(v[1]) + "." + strconv.Itoa(v[2])
}
