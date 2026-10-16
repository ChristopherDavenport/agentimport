// Package jsjson holds a JSON value as JavaScript holds what JSON.parse
// returns, and writes it as JSON.stringify does, for a reader that must
// reproduce a JavaScript program's own bytes: a hash over its
// serialisation, or the text it sends. Strings are UTF-16 code units,
// so a lone surrogate an escape wrote survives; numbers are doubles,
// written as Number.prototype.toString writes them; an object keeps its
// members in JavaScript's order, the array-index keys first in
// ascending order and then the rest in the order first written, a
// repeated member keeping its first place and its last value. It
// imports the standard library alone.
package jsjson

import (
	"bytes"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Kind is a value's JSON type.
type Kind uint8

// The kinds.
const (
	Null Kind = iota
	Bool
	Number
	Str
	Array
	Object
)

// String is a JavaScript string: UTF-16 code units.
type String []uint16

// FromString is s as UTF-16; invalid UTF-8 becomes U+FFFD.
func FromString(s string) String {
	out := make(String, 0, len(s))
	for _, r := range s {
		out = utf16.AppendRune(out, r)
	}
	return out
}

// String is s as UTF-8; a lone surrogate becomes U+FFFD.
func (s String) String() string { return string(utf16.Decode(s)) }

// Lone reports whether s holds a surrogate that is not half of a pair,
// which a Go string cannot hold.
func (s String) Lone() bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 0xD800 && c < 0xDC00:
			if i+1 < len(s) && s[i+1] >= 0xDC00 && s[i+1] < 0xE000 {
				i++
				continue
			}
			return true
		case c >= 0xDC00 && c < 0xE000:
			return true
		}
	}
	return false
}

// Value is a JSON value as JSON.parse returns it.
type Value struct {
	Kind Kind
	Bool bool
	Num  float64
	Str  String
	// Items are an array's elements.
	Items []*Value
	// Keys and Vals are an object's members, in JavaScript's order.
	Keys []String
	Vals []*Value
}

// NewString is a string value.
func NewString(s string) *Value { return &Value{Kind: Str, Str: FromString(s)} }

// Get returns an object's member, nil when v is no object or has none.
func (v *Value) Get(key string) *Value {
	if v == nil || v.Kind != Object {
		return nil
	}
	k := FromString(key)
	for i, have := range v.Keys {
		if equal(have, k) {
			return v.Vals[i]
		}
	}
	return nil
}

// Text is the string a value holds, and whether it is a string.
func (v *Value) Text() (string, bool) {
	if v == nil || v.Kind != Str {
		return "", false
	}
	return v.Str.String(), true
}

// Set sets an object's member as an assignment does: an existing member
// keeps its place, a new one goes where JavaScript's order puts it.
func (v *Value) Set(key string, val *Value) {
	v.set(FromString(key), val)
}

func (v *Value) set(k String, val *Value) {
	for i, have := range v.Keys {
		if equal(have, k) {
			v.Vals[i] = val
			return
		}
	}
	at := len(v.Keys)
	if n, ok := arrayIndex(k); ok {
		at = 0
		for at < len(v.Keys) {
			m, ok := arrayIndex(v.Keys[at])
			if !ok || m > n {
				break
			}
			at++
		}
	}
	v.Keys = append(v.Keys, nil)
	v.Vals = append(v.Vals, nil)
	copy(v.Keys[at+1:], v.Keys[at:])
	copy(v.Vals[at+1:], v.Vals[at:])
	v.Keys[at], v.Vals[at] = k, val
}

// Delete removes an object's member.
func (v *Value) Delete(key string) {
	k := FromString(key)
	for i, have := range v.Keys {
		if equal(have, k) {
			v.Keys = append(v.Keys[:i], v.Keys[i+1:]...)
			v.Vals = append(v.Vals[:i], v.Vals[i+1:]...)
			return
		}
	}
}

// Clone is a deep copy of v.
func (v *Value) Clone() *Value {
	if v == nil {
		return nil
	}
	out := &Value{Kind: v.Kind, Bool: v.Bool, Num: v.Num, Str: v.Str}
	if v.Items != nil {
		out.Items = make([]*Value, len(v.Items))
		for i, it := range v.Items {
			out.Items[i] = it.Clone()
		}
	}
	if v.Keys != nil {
		out.Keys = append([]String(nil), v.Keys...)
		out.Vals = make([]*Value, len(v.Vals))
		for i, it := range v.Vals {
			out.Vals[i] = it.Clone()
		}
	}
	return out
}

func equal(a, b String) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// unitKey is a key's code units as a map key.
func unitKey(k String) string {
	b := make([]byte, 2*len(k))
	for i, c := range k {
		b[2*i], b[2*i+1] = byte(c>>8), byte(c)
	}
	return string(b)
}

// orderIndexes moves an object's array-index keys first, in ascending
// order, as JavaScript orders them; the other keys keep their order.
func (v *Value) orderIndexes() {
	type member struct {
		n   uint64
		k   String
		val *Value
	}
	var idx []member
	var keys []String
	var vals []*Value
	for i, k := range v.Keys {
		if n, ok := arrayIndex(k); ok {
			idx = append(idx, member{n, k, v.Vals[i]})
			continue
		}
		keys = append(keys, k)
		vals = append(vals, v.Vals[i])
	}
	sort.Slice(idx, func(i, j int) bool { return idx[i].n < idx[j].n })
	v.Keys, v.Vals = v.Keys[:0], v.Vals[:0]
	for _, m := range idx {
		v.Keys = append(v.Keys, m.k)
		v.Vals = append(v.Vals, m.val)
	}
	v.Keys = append(v.Keys, keys...)
	v.Vals = append(v.Vals, vals...)
}

// arrayIndex reports whether k is an array index, a canonical decimal
// integer below 2^32 - 1, which JavaScript orders before other keys.
func arrayIndex(k String) (uint64, bool) {
	if len(k) == 0 || len(k) > 10 || len(k) > 1 && k[0] == '0' {
		return 0, false
	}
	var n uint64
	for _, c := range k {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	return n, n < 1<<32-1
}

// MaxDepth bounds how deep Parse follows nesting, as encoding/json
// does.
const MaxDepth = 10000

// ErrDepth is returned for a value nested past MaxDepth.
var ErrDepth = errors.New("jsjson: nesting too deep")

// Parse reads one JSON value, as JSON.parse does after a UTF-8 decode:
// an invalid byte becomes U+FFFD.
func Parse(data []byte) (*Value, error) {
	p := &parser{data: data}
	p.space()
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.data) {
		return nil, errors.New("jsjson: bytes after the value")
	}
	return v, nil
}

type parser struct {
	data []byte
	i    int
}

var errSyntax = errors.New("jsjson: not JSON")

func (p *parser) space() {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case ' ', '\t', '\r', '\n':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value(depth int) (*Value, error) {
	if depth > MaxDepth {
		return nil, ErrDepth
	}
	if p.i >= len(p.data) {
		return nil, errSyntax
	}
	switch c := p.data[p.i]; {
	case c == '{':
		p.i++
		v := &Value{Kind: Object, Keys: []String{}, Vals: []*Value{}}
		p.space()
		if p.i < len(p.data) && p.data[p.i] == '}' {
			p.i++
			return v, nil
		}
		// The members as written, a repeated one at its first place;
		// the array-index keys are moved first when the object ends.
		at := map[string]int{}
		indexed := false
		for {
			p.space()
			if p.i >= len(p.data) || p.data[p.i] != '"' {
				return nil, errSyntax
			}
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			p.space()
			if p.i >= len(p.data) || p.data[p.i] != ':' {
				return nil, errSyntax
			}
			p.i++
			p.space()
			val, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			if i, ok := at[unitKey(k)]; ok {
				v.Vals[i] = val
			} else {
				at[unitKey(k)] = len(v.Keys)
				v.Keys = append(v.Keys, k)
				v.Vals = append(v.Vals, val)
				if _, ok := arrayIndex(k); ok {
					indexed = true
				}
			}
			p.space()
			if p.i >= len(p.data) {
				return nil, errSyntax
			}
			if p.data[p.i] == ',' {
				p.i++
				continue
			}
			if p.data[p.i] == '}' {
				p.i++
				if indexed {
					v.orderIndexes()
				}
				return v, nil
			}
			return nil, errSyntax
		}
	case c == '[':
		p.i++
		v := &Value{Kind: Array, Items: []*Value{}}
		p.space()
		if p.i < len(p.data) && p.data[p.i] == ']' {
			p.i++
			return v, nil
		}
		for {
			p.space()
			it, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			v.Items = append(v.Items, it)
			p.space()
			if p.i >= len(p.data) {
				return nil, errSyntax
			}
			if p.data[p.i] == ',' {
				p.i++
				continue
			}
			if p.data[p.i] == ']' {
				p.i++
				return v, nil
			}
			return nil, errSyntax
		}
	case c == '"':
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		return &Value{Kind: Str, Str: s}, nil
	case c == 't' && bytes.HasPrefix(p.data[p.i:], []byte("true")):
		p.i += 4
		return &Value{Kind: Bool, Bool: true}, nil
	case c == 'f' && bytes.HasPrefix(p.data[p.i:], []byte("false")):
		p.i += 5
		return &Value{Kind: Bool}, nil
	case c == 'n' && bytes.HasPrefix(p.data[p.i:], []byte("null")):
		p.i += 4
		return &Value{Kind: Null}, nil
	case c == '-' || c >= '0' && c <= '9':
		start := p.i
		if p.data[p.i] == '-' {
			p.i++
		}
		digits := func() int {
			n := 0
			for p.i < len(p.data) && p.data[p.i] >= '0' && p.data[p.i] <= '9' {
				p.i++
				n++
			}
			return n
		}
		if p.i < len(p.data) && p.data[p.i] == '0' {
			p.i++
		} else if digits() == 0 {
			return nil, errSyntax
		}
		if p.i < len(p.data) && p.data[p.i] == '.' {
			p.i++
			if digits() == 0 {
				return nil, errSyntax
			}
		}
		if p.i < len(p.data) && (p.data[p.i] == 'e' || p.data[p.i] == 'E') {
			p.i++
			if p.i < len(p.data) && (p.data[p.i] == '+' || p.data[p.i] == '-') {
				p.i++
			}
			if digits() == 0 {
				return nil, errSyntax
			}
		}
		f, err := strconv.ParseFloat(string(p.data[start:p.i]), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, errSyntax
		}
		return &Value{Kind: Number, Num: f}, nil
	}
	return nil, errSyntax
}

// str reads a string at p.i, its opening quote included.
func (p *parser) str() (String, error) {
	p.i++
	var out String
	for p.i < len(p.data) {
		c := p.data[p.i]
		switch {
		case c == '"':
			p.i++
			if out == nil {
				out = String{}
			}
			return out, nil
		case c == '\\':
			if p.i+1 >= len(p.data) {
				return nil, errSyntax
			}
			e := p.data[p.i+1]
			p.i += 2
			switch e {
			case '"', '\\', '/':
				out = append(out, uint16(e))
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				if p.i+4 > len(p.data) {
					return nil, errSyntax
				}
				n, err := strconv.ParseUint(string(p.data[p.i:p.i+4]), 16, 16)
				if err != nil {
					return nil, errSyntax
				}
				out = append(out, uint16(n))
				p.i += 4
			default:
				return nil, errSyntax
			}
		case c < 0x20:
			return nil, errSyntax
		case c < utf8.RuneSelf:
			out = append(out, uint16(c))
			p.i++
		default:
			r, n := utf8.DecodeRune(p.data[p.i:])
			out = utf16.AppendRune(out, r)
			p.i += n
		}
	}
	return nil, errSyntax
}

// Stringify writes v as JSON.stringify(v) does, with no indentation.
func (v *Value) Stringify() []byte {
	var b bytes.Buffer
	v.write(&b)
	return b.Bytes()
}

func (v *Value) write(b *bytes.Buffer) {
	if v == nil {
		b.WriteString("null")
		return
	}
	switch v.Kind {
	case Null:
		b.WriteString("null")
	case Bool:
		if v.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case Number:
		b.WriteString(Number2String(v.Num))
	case Str:
		Quote(b, v.Str)
	case Array:
		b.WriteByte('[')
		for i, it := range v.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			it.write(b)
		}
		b.WriteByte(']')
	case Object:
		b.WriteByte('{')
		for i, k := range v.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			Quote(b, k)
			b.WriteByte(':')
			v.Vals[i].write(b)
		}
		b.WriteByte('}')
	}
}

const hexDigits = "0123456789abcdef"

// Quote writes s as JSON.stringify writes a string: '"' and '\\'
// escaped, the control characters as \b, \f, \n, \r, \t or \u00xx, a
// lone surrogate as \udxxx, everything else as itself.
func Quote(b *bytes.Buffer, s String) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&0xF])
		case c >= 0xD800 && c < 0xDC00 && i+1 < len(s) && s[i+1] >= 0xDC00 && s[i+1] < 0xE000:
			b.WriteRune(utf16.DecodeRune(rune(c), rune(s[i+1])))
			i++
		case c >= 0xD800 && c < 0xE000:
			b.WriteString(`\u`)
			for shift := 12; shift >= 0; shift -= 4 {
				b.WriteByte(hexDigits[(c>>shift)&0xF])
			}
		default:
			b.WriteRune(rune(c))
		}
	}
	b.WriteByte('"')
}

// QuoteString is Quote of a Go string.
func QuoteString(s string) string {
	var b bytes.Buffer
	Quote(&b, FromString(s))
	return b.String()
}

// Number2String writes f as Number.prototype.toString does, and as
// JSON.stringify does: the shortest digits that read back as f, in
// fixed notation from 1e-6 up to 1e21 and in exponent notation outside
// it; NaN and the infinities, which JSON cannot hold, as null.
func Number2String(f float64) string {
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0):
		return "null"
	case f == 0:
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	// d.ddde±x: the digits and the exponent of the leading one.
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	k, n := len(digits), x+1
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		es := "+"
		if n-1 < 0 {
			es = "-"
		}
		ev := n - 1
		if ev < 0 {
			ev = -ev
		}
		if k == 1 {
			out = digits + "e" + es + strconv.Itoa(ev)
		} else {
			out = digits[:1] + "." + digits[1:] + "e" + es + strconv.Itoa(ev)
		}
	}
	return sign + out
}
