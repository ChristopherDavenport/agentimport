package agentimport

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"
)

// CleanJSON returns raw as I-JSON: every string in it, member names
// included, made valid Unicode (invalid UTF-8 and lone surrogate escapes
// become U+FFFD), and an object that repeats a member name keeping the
// last, as JavaScript's JSON.parse reads it. The session format holds
// I-JSON, and its writer normalises a value but refuses a member name
// that is not, or a repeated one, so a native object copied into a
// member or a custom entry is cleaned first. Numbers keep their
// spelling. changed says something was replaced or dropped; see
// [RepeatedMembers] to tell which. raw that is already clean is
// returned as it is.
func CleanJSON(raw json.RawMessage) (out json.RawMessage, changed bool, err error) {
	valid := utf8.Valid(raw)
	repeated := RepeatedMembers(raw)
	if valid && !repeated && !bytes.Contains(raw, []byte(`\ud`)) && !bytes.Contains(raw, []byte(`\uD`)) {
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
	return bytes.TrimRight(b.Bytes(), "\n"), repeated || !valid || loneSurrogate(raw), nil
}

// RepeatedMembers reports whether an object anywhere in raw names a
// member twice, which I-JSON forbids.
func RepeatedMembers(raw json.RawMessage) bool {
	type frame struct {
		keys    map[string]bool
		wantKey bool
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var stack []*frame
	value := func() {
		if n := len(stack); n > 0 && stack[n-1].keys != nil {
			stack[n-1].wantKey = true
		}
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		switch t := tok.(type) {
		case json.Delim:
			switch t {
			case '{':
				stack = append(stack, &frame{keys: map[string]bool{}, wantKey: true})
			case '[':
				stack = append(stack, &frame{})
			default:
				stack = stack[:len(stack)-1]
				value()
			}
		case string:
			if n := len(stack); n > 0 && stack[n-1].keys != nil && stack[n-1].wantKey {
				top := stack[n-1]
				if top.keys[t] {
					return true
				}
				top.keys[t] = true
				top.wantKey = false
				continue
			}
			value()
		default:
			value()
		}
	}
}

// InvalidText reports whether a JSON text holds a string, member names
// included, that is not valid Unicode: invalid UTF-8, or a lone
// surrogate escape. [CleanJSON] replaces such text.
func InvalidText(raw json.RawMessage) bool { return !utf8.Valid(raw) || loneSurrogate(raw) }

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
