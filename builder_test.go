package agentimport

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

func TestBuilder(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	h := agentsession.Header{ID: "11111111-2222-4333-8444-555555555555", CreatedAt: t0}
	if err := SetSource(&h, Source{Format: "test", Converter: "test@v0"}); err != nil {
		t.Fatal(err)
	}
	r := NewReport("test")
	b := NewBuilder(h, "test", r)

	item := func(m *openresponses.Message, ts time.Time) agentsession.Entry {
		e := agentsession.NewItemEntry(m)
		e.Timestamp = ts
		return e
	}
	// The native file writes the reply before the prompt it answers;
	// the converter walks its tree, so the builder sees parent first.
	prompt := item(openresponses.UserText("hi"), t0)
	if err := b.Member(prompt, "uuid", "u1"); err != nil {
		t.Fatal(err)
	}
	p, err := b.Append("", prompt)
	if err != nil {
		t.Fatal(err)
	}
	b.Bind("u1", p)
	a1, err := b.Append(p, item(openresponses.AssistantText("hello"), t0.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	// A rewind: a second reply to the same prompt.
	a2, err := b.Append(p, item(openresponses.AssistantText("hello again"), t0.Add(2*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	// Two identical entries at one instant are two native lines; the
	// second is moved, not folded.
	same := func() agentsession.Entry { return item(openresponses.UserText("again"), t0.Add(3*time.Second)) }
	s1, err := b.Append(a1, same())
	if err != nil {
		t.Fatal(err)
	}
	s2, err := b.Append(a1, same())
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 {
		t.Fatal("identical entries were folded into one")
	}
	if !r.Has(Repaired) {
		t.Error("the move was not reported")
	}
	// A side record with no time sorts where the file put it.
	info := &agentsession.InfoEntry{Name: "title"}
	if _, err := b.Append(s2, info); err != nil {
		t.Fatal(err)
	}
	if !info.Timestamp.Equal(t0.Add(3*time.Second + time.Nanosecond)) {
		t.Errorf("info timestamp = %v", info.Timestamp)
	}
	if err := b.Leaf(a2); err != nil {
		t.Fatal(err)
	}
	if b.S.Leaf() != a2 {
		t.Errorf("leaf = %s, want %s", b.S.Leaf(), a2)
	}
	if id, ok := b.ID("u1"); !ok || id != p {
		t.Errorf("ID(u1) = %s, %v", id, ok)
	}
	if err := Verify(b.S); err != nil {
		t.Fatal(err)
	}

	got, _ := b.S.Entry(p)
	if string(got.Base().Unknown["test:uuid"]) != `"u1"` {
		t.Errorf("native member = %s", got.Base().Unknown["test:uuid"])
	}
	var src Source
	if err := json.Unmarshal(b.S.Header().Extra[SourceMember], &src); err != nil || src.Format != "test" {
		t.Errorf("source = %+v, %v", src, err)
	}
}
