package agentimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrorText renders an error for a finding's detail so it reads the
// same on every Go release. encoding/json's type errors name the field
// by a path whose form changed between releases (an embedded struct's
// name was dropped from it in Go 1.27), so they are rewritten to the
// member's own name, the JSON kind it held and the type it needed;
// anything wrapping such an error keeps its own text.
func ErrorText(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) {
		return text
	}
	field := te.Field
	if i := strings.LastIndex(field, "."); i >= 0 {
		field = field[i+1:]
	}
	stable := fmt.Sprintf("json: member %q is a JSON %s, not %s", field, te.Value, te.Type)
	if field == "" {
		stable = fmt.Sprintf("json: a JSON %s is not %s", te.Value, te.Type)
	}
	return strings.Replace(text, te.Error(), stable, 1)
}
