package jsonline

import (
	"encoding/json"
	"reflect"
	"testing"
)

type shape struct {
	Name  string  `json:"name"`
	Count int     `json:"count"`
	Tag   *string `json:"tag"`

	Members
}

func (s *shape) decode(data []byte, lenient bool) error {
	type plain shape
	if lenient {
		return DecodeLenient(data, (*plain)(s), &s.Members)
	}
	return Decode(data, (*plain)(s), &s.Members)
}

func (s shape) encode() ([]byte, error) {
	type plain shape
	return Encode(plain(s), &s.Members)
}

func TestRoundTrip(t *testing.T) {
	for _, in := range []string{
		`{"name":"a","count":0,"tag":null,"other":[1]}`,
		`{"name":"a"}`,
		`{}`,
	} {
		var s shape
		if err := s.decode([]byte(in), false); err != nil {
			t.Fatal(err)
		}
		out, err := s.encode()
		if err != nil {
			t.Fatal(err)
		}
		if !same(in, string(out)) {
			t.Errorf("%s wrote %s", in, out)
		}
	}
}

func TestDecodeLenient(t *testing.T) {
	in := `{"name":{"not":"a string"},"count":3,"tag":"x","other":1}`
	var s shape
	if err := s.decode([]byte(in), false); err == nil {
		t.Fatal("a mistyped member decodes strictly")
	}
	s = shape{}
	if err := s.decode([]byte(in), true); err != nil {
		t.Fatal(err)
	}
	if s.Name != "" || s.Count != 3 || s.Tag == nil || *s.Tag != "x" || !reflect.DeepEqual(s.Mistyped(), []string{"name"}) {
		t.Errorf("decoded %+v, mistyped %v", s, s.Mistyped())
	}
	out, err := s.encode()
	if err != nil {
		t.Fatal(err)
	}
	if !same(in, string(out)) {
		t.Errorf("wrote %s", out)
	}
	if err := s.decode([]byte(`[1]`), true); err == nil {
		t.Error("an array decodes as an object")
	}
}

func same(a, b string) bool {
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
