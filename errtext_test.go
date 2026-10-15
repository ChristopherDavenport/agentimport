package agentimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestErrorText(t *testing.T) {
	type inner struct {
		Status string `json:"status"`
	}
	type outer struct {
		inner
		N int `json:"n"`
	}
	decode := func(s string) error { return json.Unmarshal([]byte(s), &outer{}) }
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{errors.New("plain"), "plain"},
		{decode(`{"status":1}`), `json: member "status" is a JSON number, not string`},
		{decode(`{"n":"x"}`), `json: member "n" is a JSON string, not int`},
		{fmt.Errorf("item %q: %w", "message", decode(`{"status":1}`)), `item "message": json: member "status" is a JSON number, not string`},
	} {
		if got := ErrorText(tc.err); got != tc.want {
			t.Errorf("ErrorText(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
