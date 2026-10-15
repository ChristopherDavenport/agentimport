package agentimport

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// CleanJSON returns raw with every string in it, member names included,
// made valid Unicode: invalid UTF-8 and lone surrogate escapes become
// U+FFFD. The session format holds I-JSON, and its writer normalises a
// value but refuses a member name that is not, so a native object
// copied into a member or a custom entry is cleaned first. Numbers keep
// their spelling. changed says something was replaced. raw that is
// already clean is returned as it is.
func CleanJSON(raw json.RawMessage) (out json.RawMessage, changed bool, err error) {
	valid := utf8.Valid(raw)
	if valid && !bytes.Contains(raw, []byte(`\ud`)) && !bytes.Contains(raw, []byte(`\uD`)) {
		return raw, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, false, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), !valid || loneSurrogate(raw), nil
}

// loneSurrogate reports whether a JSON text holds a \u escape of a
// surrogate that is not half of a pair.
func loneSurrogate(raw []byte) bool {
	inString := false
	pendingHigh := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			if pendingHigh {
				return true
			}
			inString = false
		case '\\':
			if i+1 >= len(raw) {
				return false
			}
			if raw[i+1] != 'u' || i+5 >= len(raw) {
				if pendingHigh {
					return true
				}
				i++
				continue
			}
			r := hex4(raw[i+2 : i+6])
			switch {
			case r >= 0xD800 && r <= 0xDBFF:
				if pendingHigh {
					return true
				}
				pendingHigh = true
			case r >= 0xDC00 && r <= 0xDFFF:
				if !pendingHigh {
					return true
				}
				pendingHigh = false
			default:
				if pendingHigh {
					return true
				}
			}
			i += 5
		default:
			if pendingHigh {
				return true
			}
		}
	}
	return pendingHigh
}

func hex4(b []byte) rune {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return -1
		}
	}
	return r
}
