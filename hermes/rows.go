package hermes

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// Column is one column of a database row: its name and its value as
// database/sql scans it, an int64, float64, string, []byte or nil.
type Column struct {
	Name  string
	Value any
}

// Columns is a row of a table, in the table's column order.
type Columns []Column

// Get returns a column's value; false when the row has no such column.
func (cs Columns) Get(name string) (any, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c.Value, true
		}
	}
	return nil, false
}

// Has reports whether the row has a column, null or not.
func (cs Columns) Has(name string) bool {
	_, ok := cs.Get(name)
	return ok
}

// String returns a text column; empty when it is absent, null or not
// text.
func (cs Columns) String(name string) string {
	v, _ := cs.Get(name)
	switch v := v.(type) {
	case string:
		return v
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
	}
	return ""
}

// Text returns a text column and whether it is text, not null and not
// absent: Hermes tells an empty string from NULL in some columns.
func (cs Columns) Text(name string) (string, bool) {
	v, _ := cs.Get(name)
	switch v := v.(type) {
	case string:
		return v, true
	case []byte:
		if utf8.Valid(v) {
			return string(v), true
		}
	}
	return "", false
}

// Int returns an integer column.
func (cs Columns) Int(name string) (int64, bool) {
	v, _ := cs.Get(name)
	switch v := v.(type) {
	case int64:
		return v, true
	case float64:
		if v == math.Trunc(v) && math.Abs(v) <= 1<<53 {
			return int64(v), true
		}
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// Float returns a numeric column. Text that spells a number is not
// one: SQLite would store a REAL value as such.
func (cs Columns) Float(name string) (float64, bool) {
	v, _ := cs.Get(name)
	switch v := v.(type) {
	case int64:
		return float64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// Flag returns an integer column as a flag: true when it is non-zero,
// and def when the column is absent or null.
func (cs Columns) Flag(name string, def bool) bool {
	if n, ok := cs.Int(name); ok {
		return n != 0
	}
	return def
}

// jsonColumns are the columns Hermes writes JSON text into.
var jsonColumns = map[string]bool{
	"tool_calls": true, "reasoning_details": true, "codex_reasoning_items": true, "codex_message_items": true,
	"display_metadata": true, "model_config": true, "origin_json": true, "tool_names": true,
	"absorbed_message_uids": true, "tool_call_uids": true, "task_json": true, "event_json": true,
	"result_json": true,
}

// JSON writes the row as one JSON object, a member per column in the
// table's order: a JSON text column as the value it holds when it holds
// one, text as a string, a number as a number, null as null, and a
// blob as base64 text.
func (cs Columns) JSON() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, c := range cs {
		if i > 0 {
			b.WriteByte(',')
		}
		name, _ := json.Marshal(c.Name)
		b.Write(name)
		b.WriteByte(':')
		b.Write(columnJSON(c))
	}
	b.WriteByte('}')
	return b.Bytes()
}

// Without returns the row without the columns named.
func (cs Columns) Without(names ...string) Columns {
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	var out Columns
	for _, c := range cs {
		if !drop[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

func columnJSON(c Column) []byte {
	switch v := c.Value.(type) {
	case nil:
		return []byte("null")
	case int64:
		return strconv.AppendInt(nil, v, 10)
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return []byte("null")
		}
		out, _ := json.Marshal(v)
		return out
	case []byte:
		if !utf8.Valid(v) {
			out, _ := json.Marshal(base64.StdEncoding.EncodeToString(v))
			return out
		}
		return columnJSON(Column{Name: c.Name, Value: string(v)})
	case string:
		if jsonColumns[c.Name] && json.Valid([]byte(v)) {
			var buf bytes.Buffer
			if json.Compact(&buf, []byte(v)) == nil {
				return buf.Bytes()
			}
		}
		out, _ := json.Marshal(v)
		return out
	case bool:
		if v {
			return []byte("true")
		}
		return []byte("false")
	}
	out, err := json.Marshal(fmt.Sprint(c.Value))
	if err != nil {
		return []byte("null")
	}
	return out
}
