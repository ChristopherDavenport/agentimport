package claudecode

import (
	"path/filepath"
	"strings"
	"testing"
)

func walk(u *Unit, depth int, b *strings.Builder) {
	b.WriteString(strings.Repeat("  ", depth))
	if u.Response != nil {
		b.WriteString("response " + u.Response.MessageID)
		for _, c := range u.Response.Calls {
			b.WriteString(" " + c.Use.ID)
			if c.Result != nil {
				b.WriteString("=" + c.Result.UUID)
			}
		}
	} else {
		b.WriteString(u.Line.Type + " " + u.Line.UUID)
	}
	if u.Logical {
		b.WriteString(" (logical)")
	}
	b.WriteString("\n")
	for _, c := range u.Children {
		walk(c, depth+1, b)
	}
}

func TestBuildTree(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		want    string
	}{
		// The results of parallel calls are in their response, so the
		// next response continues from it and no result is a dead end.
		{"parallel", `user u1
  response msg_02 toolu_A=r1 toolu_B=r2
    response msg_03 toolu_C=r3
      response msg_04 toolu_D=r4
        response msg_05
`},
		// The attachment and the meta line between the two blocks are in
		// the response, so nothing hangs off the path.
		{"between", `user u1
  response msg_70 toolu_P=r1 toolu_Q=r2
    response msg_71
`},
		// The boundary hangs from its logical parent.
		{"compaction", `user u1
  response msg_20
    user u2
      response msg_21
        system c1 (logical)
          user u3
            user u4
              response msg_22
`},
		// A child written before its parent, and a line whose parent is
		// missing, which is a root.
		{"damaged", `user u1
  response msg_50
    user u3
      hologram h1
user u5
`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			m, _ := filepath.Glob(filepath.Join("convert", "testdata", tc.fixture, "*.jsonl"))
			tr, err := ReadFile(m[0])
			if err != nil {
				t.Fatal(err)
			}
			tree := BuildTree(tr)
			var b strings.Builder
			for _, r := range tree.Roots {
				walk(r, 0, &b)
			}
			if b.String() != tc.want {
				t.Errorf("tree:\n%s\nwant:\n%s", b.String(), tc.want)
			}
		})
	}
}
