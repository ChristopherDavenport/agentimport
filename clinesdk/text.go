package clinesdk

import (
	"regexp"
	"strings"
)

// The user-input rules of @cline/shared's prompt/format.ts, which the
// compaction hash and each request apply to a user's text. Cline wraps
// what a user types in <user_input mode="...">, a slash command in
// <user_command slash="...">, and a mode switch adds a <mode_notice>.

// Trim is JavaScript's String.prototype.trim: it removes white space and
// line terminators as ECMAScript defines them, which differ from Go's
// unicode.IsSpace (U+FEFF is one, U+0085 is not).
func Trim(s string) string { return strings.TrimFunc(s, jsSpace) }

func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

var (
	tagElement = map[string]*regexp.Regexp{
		"user_input":   regexp.MustCompile(`<user_input\b[^>]*>([\s\S]*?)</user_input>`),
		"user_command": regexp.MustCompile(`<user_command\b[^>]*>([\s\S]*?)</user_command>`),
	}
	tagOpen = map[string]*regexp.Regexp{
		"user_input":   regexp.MustCompile(`<user_input[^>]*>`),
		"user_command": regexp.MustCompile(`<user_command[^>]*>`),
	}
	commandEnvelope = regexp.MustCompile(`(?i)^<user_command\b([^>]*)>([\s\S]*?)</user_command>$`)
	slashAttribute  = regexp.MustCompile(`(?i)slash="([^"]+)"`)
)

// NormalizeUserInput is Cline's normalizeUserInput: the text trimmed,
// and each <user_input> and <user_command> element replaced by its
// content, or, when the text has none, their opening tags removed. Each
// request sends a user's text so.
func NormalizeUserInput(input string) string {
	if Trim(input) == "" {
		return ""
	}
	next := Trim(input)
	for _, tag := range []string{"user_input", "user_command"} {
		extracted := ""
		if Trim(next) != "" {
			extracted = tagElement[tag].ReplaceAllString(next, "${1}")
		}
		if extracted != next {
			next = Trim(extracted)
		} else {
			next = Trim(tagOpen[tag].ReplaceAllString(next, ""))
		}
	}
	return next
}

// StripModeNotices is Cline's stripModeNotices: every
// <mode_notice>...</mode_notice> element removed, and the text trimmed.
func StripModeNotices(input string) string {
	if Trim(input) == "" {
		return ""
	}
	const open, close = "<mode_notice>", "</mode_notice>"
	result := input
	start := strings.Index(result, open)
	for start != -1 {
		end := strings.Index(result[start+len(open):], close)
		if end == -1 {
			break
		}
		end += start + len(open)
		result = result[:start] + result[end+len(close):]
		next := strings.Index(result[start:], open)
		if next == -1 {
			break
		}
		start += next
	}
	return Trim(result)
}

// FormatDisplayUserInput is Cline's formatDisplayUserInput: the text a
// user typed as the chat view shows it, a slash command as "/<slash>
// <text>". The compaction hash reads a user's text so.
func FormatDisplayUserInput(input string) string {
	normalized := StripModeNotices(NormalizeUserInput(input))
	slash, ok := userCommandSlash(input)
	if !ok {
		return normalized
	}
	if strings.ToLower(slash) == "team" {
		const prefix = "spawn a team of agents for the following task:"
		stripped := normalized
		if strings.HasPrefix(strings.ToLower(normalized), prefix) && len(normalized) >= len(prefix) {
			stripped = Trim(normalized[len(prefix):])
		}
		if stripped == "" {
			return "/team"
		}
		return "/team " + stripped
	}
	if normalized == "" {
		return "/" + slash
	}
	return "/" + slash + " " + normalized
}

// userCommandSlash is the slash of a text that is one <user_command>
// element with a slash attribute.
func userCommandSlash(input string) (string, bool) {
	if Trim(input) == "" {
		return "", false
	}
	m := commandEnvelope.FindStringSubmatch(Trim(input))
	if m == nil {
		return "", false
	}
	a := slashAttribute.FindStringSubmatch(m[1])
	if a == nil {
		return "", false
	}
	slash := Trim(a[1])
	return slash, slash != ""
}
