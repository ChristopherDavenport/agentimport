package agentimport

import (
	"encoding/json"
	"testing"
)

func TestCleanJSON(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		changed  bool
	}{
		{`{"a":1}`, `{"a":1}`, false},
		{`{"a":"😀"}`, `{"a":"😀"}`, false},
		{`{"a\ud800b":1}`, `{"a` + "�" + `b":1}`, true},
		{"{\"a\xffb\":\"x\xfe\"}", `{"a` + "�" + `b":"x` + "�" + `"}`, true},
		{`{"n":12345678901234567890}`, `{"n":12345678901234567890}`, false},
		{`{"s":"\\ud800"}`, `{"s":"\\ud800"}`, false},
		{`{"":0,"":{}}`, `{"":{}}`, true},
		{`{"a":[{"b":1,"b":2}],"c":{"b":3}}`, `{"a":[{"b":2}],"c":{"b":3}}`, true},
		{`{"a":{"b":1},"b":{"a":2}}`, `{"a":{"b":1},"b":{"a":2}}`, false},
	} {
		got, changed, err := CleanJSON(json.RawMessage(tc.in))
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if string(got) != tc.want || changed != tc.changed {
			t.Errorf("CleanJSON(%q) = %s, %v; want %s, %v", tc.in, got, changed, tc.want, tc.changed)
		}
	}
}
