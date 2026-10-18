package convert

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentimport/internal/doccheck"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/agentsession/export"
	"github.com/ChristopherDavenport/openresponses"
)

// png is a 1x1 image, so the exporter has something to spill.
var png = "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nnot really a png"))

// session builds a session in agentsession with what the exporter
// writes extras for: a reasoning item, calls and their outputs, an
// image, a subsession, a decision, a label, a compaction with a pinned
// item and a kept window, and turns after it.
func session(t *testing.T) (*agentsession.Session, *agentsession.Session) {
	t.Helper()
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	tick := func() time.Time { at = at.Add(time.Second); return at }
	s := agentsession.New(agentsession.Header{ID: "rt-main", CreatedAt: at, CWD: "/repo",
		Harness: &agentsession.Harness{Name: "rt", Version: "1"}})
	add := func(s *agentsession.Session, e agentsession.Entry) string {
		t.Helper()
		e.Base().Timestamp = tick()
		id, err := s.Append(e)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	instructions := "Be useful."
	add(s, &agentsession.ConfigEntry{Model: "m-1", Instructions: &instructions,
		ToolsAdded: openresponses.Tools{openresponses.NewFunctionTool("read", "Read a file.", json.RawMessage(`{"type":"object"}`))}})
	reply := func(s *agentsession.Session, id string, items ...openresponses.Item) {
		for _, it := range items {
			e := agentsession.NewItemEntry(it)
			e.ResponseID = id
			add(s, e)
		}
		add(s, &agentsession.ResponseEntry{ResponseID: id, Model: "m-1", Status: openresponses.ResponseStatusCompleted,
			Usage: &openresponses.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}})
	}
	add(s, agentsession.NewItemEntry(openresponses.UserText("Hi.")))
	reply(s, "resp_0", openresponses.AssistantText("Hello."))
	add(s, agentsession.NewItemEntry(openresponses.UserText("Ready?")))
	reply(s, "resp_00", openresponses.AssistantText("Ready."))
	// The fold keeps the last exchange, which the exporter writes after
	// it as copied steps.
	c, err := s.CompactKeeping(2, openresponses.UserText("Summary: greeted."))
	if err != nil {
		t.Fatal(err)
	}
	c.Pinned = openresponses.Items{openresponses.UserText("Pinned: the repo is Go.")}
	c.TokensBefore = 900
	add(s, c)
	add(s, agentsession.NewItemEntry(openresponses.UserMessage(&openresponses.InputText{Text: "Look at this."}, &openresponses.InputImage{ImageURL: png})))
	reply(s, "resp_1",
		&openresponses.ReasoningItem{Summary: openresponses.Contents{&openresponses.SummaryText{Text: "Read it."}}, EncryptedContent: "sig"},
		openresponses.AssistantText("Reading."),
		&openresponses.FunctionCall{CallID: "call_1", Name: "read", Arguments: `{"path":"a.go"}`},
		&openresponses.FunctionCall{CallID: "call_2", Name: "task", Arguments: `{"prompt":"check"}`})
	out := agentsession.NewItemEntry(&openresponses.FunctionCallOutput{CallID: "call_1",
		Output: openresponses.FunctionCallOutputData{Parts: openresponses.Contents{&openresponses.InputText{Text: "package a"}, &openresponses.InputImage{ImageURL: png}}}})
	out.Unknown = map[string]json.RawMessage{"rt:note": json.RawMessage(`"kept"`)}
	add(s, out)

	child := agentsession.New(agentsession.Header{ID: agentsession.SubsessionID("rt-main", "call_2"), CreatedAt: tick(),
		ParentSession: "rt-main", SpawnedBy: "call_2", Harness: &agentsession.Harness{Name: "rt", Version: "1"}})
	add(child, &agentsession.ConfigEntry{Model: "m-2"})
	add(child, agentsession.NewItemEntry(openresponses.UserText("check")))
	reply(child, "resp_c1", openresponses.AssistantText("All good."))
	add(s, agentsession.NewSubsessionLink(child.ID(), "call_2"))
	answer := agentsession.NewItemEntry(openresponses.NewFunctionCallOutput("call_2", "All good."))
	answer.Parents = []agentsession.EntryRef{{Session: child.ID(), Entry: child.Leaf()}}
	add(s, answer)

	reply(s, "resp_2", openresponses.AssistantText("Both done."),
		&openresponses.FunctionCall{CallID: "call_3", Name: "read", Arguments: `{"path":"b.go"}`})
	target := s.Leaf()
	for _, e := range s.Path(s.Leaf()) {
		if it, ok := e.(*agentsession.ItemEntry); ok {
			if fc, ok := it.Item.(*openresponses.FunctionCall); ok && fc.CallID == "call_3" {
				target = it.ID
			}
		}
	}
	add(s, agentsession.NewDecision("call_3", target, agentsession.VerdictReject, "user").WithReason("not that file"))
	add(s, openresponsesOutput("call_3", "rejected"))
	label := add(s, agentsession.NewItemEntry(openresponses.UserText("Summarise and go on.")))
	add(s, agentsession.NewLabelEntry(label, "checkpoint"))
	reply(s, "resp_3", openresponses.AssistantText("Going on."))

	add(s, agentsession.NewItemEntry(openresponses.UserText("What next?")))
	reply(s, "resp_4", openresponses.AssistantText("Tests."))
	return s, child
}

func openresponsesOutput(callID, text string) *agentsession.ItemEntry {
	return agentsession.NewItemEntry(openresponses.NewFunctionCallOutput(callID, text))
}

// TestExporterRoundTrip exports a session with agentsession's exporter
// and imports the document: the context at the imported session's leaf
// is the item list export.Items reads from the document, and the
// subsession comes back as a session of its own.
func TestExporterRoundTrip(t *testing.T) {
	s, child := session(t)
	var main export.Trajectory
	found := false
	for tr, err := range export.Trajectories(s) {
		if err != nil {
			t.Fatal(err)
		}
		if tr.Main {
			main, found = tr, true
		}
	}
	if !found {
		t.Fatal("no main trajectory")
	}
	doc, err := export.ToATIF(main, export.Options{Subsessions: func(id string) (*agentsession.Session, error) {
		if id == child.ID() {
			return child, nil
		}
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	want, err := export.Items(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent", "trajectory.json")
	if err := export.WriteDocument(path, doc); err != nil {
		t.Fatal(err)
	}
	res := convertFixture(t, path, Options{})
	if len(res.Sessions) != 2 {
		t.Fatalf("%d sessions, want the main one and its subsession", len(res.Sessions))
	}
	got := reread(t, res.Sessions[0])
	ctx, err := got.Context()
	if err != nil {
		t.Fatal(err)
	}
	if a, b := itemsJSON(t, ctx.Items), itemsJSON(t, want); !slices.Equal(a, b) {
		t.Errorf("context at the leaf:\n%s\nwant (export.Items):\n%s", strings.Join(a, "\n"), strings.Join(b, "\n"))
	}
	if h := res.Sessions[1].Header(); h.ParentSession != got.ID() || h.SpawnedBy != "call_2" {
		t.Errorf("subsession header: parent %q spawned_by %q", h.ParentSession, h.SpawnedBy)
	}
	if got.Header().CWD != "/repo" {
		t.Errorf("cwd = %q", got.Header().CWD)
	}
	// The member the exporter merged into the step's extra is back on
	// the output entry.
	foundNote := false
	for _, e := range got.Entries() {
		if string(e.Base().Unknown["rt:note"]) == `"kept"` {
			foundNote = true
		}
	}
	if !foundNote {
		t.Error("the output's rt:note member is not on an entry")
	}
	for _, code := range []string{"atif/folded-history", "atif/exporter-records"} {
		if !slices.Contains(res.Report.Codes(), code) {
			t.Errorf("no %s finding: the document does not exercise it", code)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "images", "*")); len(matches) == 0 {
		t.Error("the exporter spilled no image: the document does not exercise reading one back")
	}
	if testing.Verbose() {
		var b strings.Builder
		_ = res.Report.WriteText(&b)
		t.Log(b.String())
	}
	doccheck.Check(t, Source, res.Report)
}

func itemsJSON(t *testing.T, items openresponses.Items) []string {
	t.Helper()
	out := make([]string, 0, len(items))
	for _, it := range items {
		raw, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	return out
}

// TestExporterRedacted exports a redacted session and imports the
// document: the exporter carries each redacted entry's redacted member,
// in the response body and in the steps' extra.agentsession, and no
// entry of the imported session, which is not a redaction, may carry
// it. The marks are kept namespaced and reported.
func TestExporterRedacted(t *testing.T) {
	at := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	s := agentsession.New(agentsession.Header{ID: "rt-redacted", CreatedAt: at, Redacted: true,
		RedactedFrom: &agentsession.RedactedFrom{Session: "rt-original"},
		Harness:      &agentsession.Harness{Name: "rt", Version: "1"}})
	n := 0
	add := func(e agentsession.Entry) {
		t.Helper()
		n++
		if err := agentsession.SetRedacted(e, []agentsession.Span{{From: n, To: n}}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Second)
		e.Base().Timestamp = at
		if _, err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	add(&agentsession.ConfigEntry{Model: "m-1"})
	add(agentsession.NewItemEntry(openresponses.UserText("Hi [REDACTED].")))
	reply := agentsession.NewItemEntry(openresponses.AssistantText("Hello."))
	reply.ResponseID = "resp_1"
	add(reply)
	add(&agentsession.ResponseEntry{ResponseID: "resp_1", Model: "m-1", Status: openresponses.ResponseStatusCompleted,
		Usage: &openresponses.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}})
	var main export.Trajectory
	for tr, err := range export.Trajectories(s) {
		if err != nil {
			t.Fatal(err)
		}
		if tr.Main {
			main = tr
		}
	}
	doc, err := export.ToATIF(main, export.Options{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"redacted_entries"`) || !strings.Contains(string(raw), `"redacted":[4]`) {
		t.Fatalf("the exporter wrote no redaction marks to exercise:\n%s", raw)
	}
	for _, slim := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "agent", "trajectory.json")
		if err := export.WriteDocument(path, doc); err != nil {
			t.Fatal(err)
		}
		res := convertFixture(t, path, Options{Slim: slim})
		got := reread(t, res.Sessions[0])
		if got.Header().Redacted || got.Header().RedactedFrom != nil {
			t.Errorf("slim %v: the imported session is marked redacted", slim)
		}
		kept, marks := false, false
		for _, e := range got.Entries() {
			if strings.Contains(string(e.Base().Unknown[Source+":extra"]), `"redacted_entries"`) {
				marks = true
			}
			if _, ok := agentsession.RedactedOf(e); ok {
				t.Errorf("slim %v: entry %s is redacted", slim, e.Base().ID)
			}
			if _, ok := e.Base().Unknown["redacted"]; ok {
				t.Errorf("slim %v: entry %s carries a redacted member", slim, e.Base().ID)
			}
			if r, ok := e.(*agentsession.ResponseEntry); ok && string(r.Unknown[Source+":redacted"]) == "[4]" {
				kept = true
			}
		}
		if kept == slim {
			t.Errorf("slim %v: the response's %s:redacted kept = %v", slim, Source, kept)
		}
		if marks == slim {
			t.Errorf("slim %v: the steps' redaction marks kept in %s:extra = %v", slim, Source, marks)
		}
		if !slices.Contains(res.Report.Codes(), "atif/redacted") {
			t.Errorf("slim %v: no atif/redacted finding: %v", slim, res.Report.Codes())
		}
		doccheck.Check(t, Source, res.Report)
	}
}
