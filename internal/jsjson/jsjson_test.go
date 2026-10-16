package jsjson

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// The wants were printed by node 25's JSON.stringify(JSON.parse(in)).
func TestStringify(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"b":1,"a":2,"10":3,"2":4,"01":5,"4294967295":6,"4294967294":7}`, `{"2":4,"10":3,"4294967294":7,"b":1,"a":2,"01":5,"4294967295":6}`},
		{`{"a":1,"b":2,"a":3}`, `{"a":3,"b":2}`},
		{`[1e21,1e-7,123456789012345680000,0.000001,1.5e300,-0,-1.25e-10,100,0.1,5e-324,1e400,12.0,2e21,123e-20]`,
			`[1e+21,1e-7,123456789012345680000,0.000001,1.5e+300,0,-1.25e-10,100,0.1,5e-324,null,12,2e+21,1.23e-18]`},
		{`"\u0001\u001f\b\f\n\r\t\"\\ \/ \u2028 \ud800 \udc00x \ud83d\ude00 é <>&"`,
			"\"\\u0001\\u001f\\b\\f\\n\\r\\t\\\"\\\\ / \u2028 \\ud800 \\udc00x 😀 é <>&\""},
		{` { "x" : [ { "y" : null , "z" : true } ] , "" : false } `, `{"x":[{"y":null,"z":true}],"":false}`},
		{"\"\xff\"", "\"\uFFFD\""},
	} {
		v, err := Parse([]byte(tc.in))
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		if got := string(v.Stringify()); got != tc.want {
			t.Errorf("%s:\n got %s\nwant %s", tc.in, got, tc.want)
		}
	}
}

func TestParseRefuses(t *testing.T) {
	for _, in := range []string{``, `{`, `[1,]`, `{"a"}`, `01`, `1.`, `"\x"`, "\"\x01\"", `tru`, `1 2`, `-`, `"\u12"`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%q parsed", in)
		}
	}
	deep := strings.Repeat("[", MaxDepth+2) + strings.Repeat("]", MaxDepth+2)
	if _, err := Parse([]byte(deep)); err != ErrDepth {
		t.Errorf("deep nesting: %v", err)
	}
}

func TestNumber2String(t *testing.T) {
	for _, tc := range []struct {
		f    float64
		want string
	}{
		{1, "1"}, {-1.5, "-1.5"}, {1e20, "100000000000000000000"}, {1e21, "1e+21"}, {1.5e-6, "0.0000015"},
		{1e-6, "0.000001"}, {1e-7, "1e-7"}, {123.456, "123.456"}, {math.Inf(1), "null"}, {math.Copysign(0, -1), "0"},
		{1.7976931348623157e308, "1.7976931348623157e+308"},
	} {
		if got := Number2String(tc.f); got != tc.want {
			t.Errorf("%v: got %s, want %s", tc.f, got, tc.want)
		}
	}
}

func TestSet(t *testing.T) {
	v, _ := Parse([]byte(`{"b":1,"3":2}`))
	v.Set("a", NewString("x"))
	v.Set("1", NewString("y"))
	v.Set("b", NewString("z"))
	if got, want := string(v.Stringify()), `{"1":"y","3":2,"b":"z","a":"x"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	v.Delete("3")
	if got, want := string(v.Stringify()), `{"1":"y","b":"z","a":"x"}`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestLone(t *testing.T) {
	for _, tc := range []struct {
		in   String
		want bool
	}{
		{String{'a'}, false}, {String{0xD83D, 0xDE00}, false}, {String{0xD800}, true}, {String{0xDC00, 'a'}, true}, {String{0xD800, 'a'}, true},
	} {
		if got := tc.in.Lone(); got != tc.want {
			t.Errorf("%v: got %v", tc.in, got)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{`{"a":[1,"b",null,true]}`, `"\ud800"`, `1e400`, `{"1":1,"a":2,"0":3}`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := Parse(data)
		if err != nil {
			return
		}
		out := v.Stringify()
		w, err := Parse(out)
		if err != nil {
			t.Fatalf("stringified value does not parse: %s", out)
		}
		if again := w.Stringify(); string(again) != string(out) {
			t.Fatalf("not stable: %s then %s", out, again)
		}
	})
}

// TestManyKeys reads an object of many members in time linear in it:
// a repeated member is found without scanning the others. The best of
// three runs of four times the keys against once.
func TestManyKeys(t *testing.T) {
	object := func(n int) []byte {
		var b strings.Builder
		b.WriteString("{")
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `"k%d":%d`, i, i)
		}
		b.WriteString(`,"5":0,"k0":1}`)
		return []byte(b.String())
	}
	best := func(data []byte) time.Duration {
		var d time.Duration
		for i := 0; i < 3; i++ {
			start := time.Now()
			v, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if e := time.Since(start); i == 0 || e < d {
				d = e
			}
			if k, _ := v.Keys[0], 0; k.String() != "5" || v.Get("k0").Num != 1 {
				t.Fatalf("keys start %s, k0 %v", v.Keys[0].String(), v.Get("k0").Num)
			}
		}
		return d
	}
	a, b := best(object(20_000)), best(object(80_000))
	if b > 8*a {
		t.Errorf("%v for 20000 keys, %v for 80000: not linear", a, b)
	}
}
