package claudecode

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// Members is embedded in every object this package decodes. It keeps
// the members the Go type does not declare, and remembers which
// declared members the input carried and which were null, so an object
// decoded and encoded again is the same JSON value: a member absent
// from the input stays absent, an explicit false or zero stays, and a
// null stays null.
type Members struct {
	// Extra holds the members the type does not declare, verbatim.
	Extra map[string]json.RawMessage `json:"-"`

	read    bool
	present map[string]bool
	null    map[string]bool
}

// Has reports whether the input carried the declared member name,
// null or not. An object built in Go rather than decoded reports false.
func (m *Members) Has(name string) bool { return m.present[name] }

// IsNull reports whether the input carried name as null.
func (m *Members) IsNull(name string) bool { return m.null[name] }

// fieldNames caches the JSON names a struct type declares.
var fieldNames sync.Map // reflect.Type -> map[string]bool

func declared(t reflect.Type) map[string]bool {
	if v, ok := fieldNames.Load(t); ok {
		return v.(map[string]bool)
	}
	names := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if name, ok := jsonName(f); ok {
			names[name] = true
		}
	}
	fieldNames.Store(t, names)
	return names
}

// jsonName returns the member name a struct field encodes as; false
// for a field that encodes none: unexported, embedded or tagged "-".
func jsonName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" || !f.IsExported() || f.Anonymous {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}
	return name, true
}

// decode unmarshals data into v, a pointer to a struct type without
// its own UnmarshalJSON, and fills m from the members data carries.
func decode(data []byte, v any, m *Members) error {
	if err := json.Unmarshal(data, v); err != nil {
		return err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	names := declared(reflect.TypeOf(v).Elem())
	// encoding/json fills a field from a key that matches its name in
	// any case. A key that is not the declared name exactly is kept in
	// Extra, so decode again from the declared keys alone, or both
	// would be written back.
	folded := false
	for k := range all {
		if !names[k] && foldsToDeclared(k, names) {
			folded = true
			break
		}
	}
	if folded {
		exact := map[string]json.RawMessage{}
		for k, raw := range all {
			if names[k] {
				exact[k] = raw
			}
		}
		data, err := json.Marshal(exact)
		if err != nil {
			return err
		}
		rv := reflect.ValueOf(v).Elem()
		rv.Set(reflect.Zero(rv.Type()))
		if err := json.Unmarshal(data, v); err != nil {
			return err
		}
	}
	m.read = true
	m.present, m.null, m.Extra = nil, nil, nil
	for k, raw := range all {
		if !names[k] {
			if m.Extra == nil {
				m.Extra = map[string]json.RawMessage{}
			}
			m.Extra[k] = raw
			continue
		}
		if m.present == nil {
			m.present = map[string]bool{}
		}
		m.present[k] = true
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if m.null == nil {
				m.null = map[string]bool{}
			}
			m.null[k] = true
		}
	}
	return nil
}

// encode marshals v, a struct value of a type without its own
// MarshalJSON, as m says: a decoded object writes back the declared
// members its input carried, null where they were null, and an object
// built in Go writes its non-zero members. Extra is written beside
// them.
func encode(v any, m *Members) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, err
	}
	rv := reflect.ValueOf(v)
	rt := rv.Type()
	out := make(map[string]json.RawMessage, len(all)+len(m.Extra))
	for i := 0; i < rt.NumField(); i++ {
		name, ok := jsonName(rt.Field(i))
		if !ok {
			continue
		}
		raw, ok := all[name]
		if !ok {
			continue
		}
		fv := rv.Field(i)
		switch {
		case m.read && !m.present[name]:
			// Absent from the input stays absent, unless it was set
			// since.
			if fv.IsZero() {
				continue
			}
		case m.read && m.null[name] && fv.IsZero():
			raw = json.RawMessage("null")
		case !m.read && fv.IsZero():
			continue
		}
		out[name] = raw
	}
	for k, raw := range m.Extra {
		if _, ok := out[k]; !ok {
			out[k] = raw
		}
	}
	return json.Marshal(out)
}

func foldsToDeclared(k string, names map[string]bool) bool {
	for n := range names {
		if strings.EqualFold(k, n) {
			return true
		}
	}
	return false
}
