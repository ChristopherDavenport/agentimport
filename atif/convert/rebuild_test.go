package convert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/agentsession/export"
	"github.com/ChristopherDavenport/openresponses"
)

// historySession is a session whose entries carry the members RFC 0001
// sorts into meaning members (failed, trust, one this converter does not
// know) and history members (legacy_id, normalised).
func historySession(t *testing.T) *agentsession.Session {
	t.Helper()
	at := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	s := agentsession.New(agentsession.Header{ID: "rb-main", CreatedAt: at, Harness: &agentsession.Harness{Name: "rb", Version: "1"}})
	add := func(e agentsession.Entry) string {
		t.Helper()
		at = at.Add(time.Second)
		e.Base().Timestamp = at
		id, err := s.Append(e)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	add(&agentsession.ConfigEntry{Model: "m-1"})
	env := agentsession.NewEnvEntry("/work/rb")
	env.Unknown = map[string]json.RawMessage{"x:inode": json.RawMessage(`9007199254740993`)}
	add(env)
	if len(env.Normalised) == 0 {
		t.Fatal("the env entry was written without a normalised record")
	}
	user := agentsession.NewItemEntry(openresponses.UserText("Read it."))
	user.LegacyID = "native-1"
	user.Unknown = map[string]json.RawMessage{"x:probe": json.RawMessage(`"kept"`)}
	add(user)
	call := &agentsession.ItemEntry{Item: &openresponses.FunctionCall{CallID: "call_1", Name: "read", Arguments: `{}`}, ResponseID: "resp_1"}
	callID := add(call)
	add(&agentsession.ResponseEntry{ResponseID: "resp_1", Model: "m-1", Status: openresponses.ResponseStatusCompleted})
	add(agentsession.NewDispatch("call_1", callID))
	add(&agentsession.ItemEntry{Item: openresponses.NewFunctionCallOutput("call_1", "no such file"), Failed: true,
		Trust: &agentsession.Trust{Reads: []string{agentsession.TrustUntrusted}}})
	return s
}

// exportDoc writes the main trajectory of s as an ATIF document and
// returns its path.
func exportDoc(t *testing.T, s *agentsession.Session, edit func(map[string]any)) string {
	t.Helper()
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
	path := filepath.Join(t.TempDir(), "agent", "trajectory.json")
	if err := export.WriteDocument(path, doc); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatal(err)
		}
		edit(m)
		if raw, err = json.Marshal(m); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// TestRebuildToIDs imports a document agentsession's exporter wrote and
// rebuilds every entry to the id the document names (RFC 0001,
// Projections/ATIF, 0.17): meaning members are core members again, an
// unknown member is kept as written, and history members come back on
// the entries that match.
func TestRebuildToIDs(t *testing.T) {
	s := historySession(t)
	res := convertFixture(t, exportDoc(t, s, nil), Options{})
	got := reread(t, res.Sessions[0])
	for _, e := range s.Path(s.Leaf()) {
		g, ok := got.Entry(e.Base().ID)
		if !ok {
			t.Errorf("%T %s is not rebuilt to its id", e, e.Base().ID)
			continue
		}
		if g.Base().LegacyID != e.Base().LegacyID || len(g.Base().Normalised) != len(e.Base().Normalised) {
			t.Errorf("%s: legacy_id %q normalised %v; want %q %v", e.Base().ID, g.Base().LegacyID, g.Base().Normalised, e.Base().LegacyID, e.Base().Normalised)
		}
		if ie, ok := g.(*agentsession.ItemEntry); ok {
			want := e.(*agentsession.ItemEntry)
			if ie.Failed != want.Failed || (ie.Trust == nil) != (want.Trust == nil) || string(ie.Unknown["x:probe"]) != string(want.Unknown["x:probe"]) {
				t.Errorf("%s: failed %v trust %v probe %s", ie.ID, ie.Failed, ie.Trust, ie.Unknown["x:probe"])
			}
		}
	}
	for _, f := range res.Report.Findings {
		if f.Code == "atif/entry-not-rebuilt" || f.Code == "atif/not-rebuilt" {
			t.Errorf("%+v", f)
		}
	}
}

// TestRebuildEdited imports a document edited after the export: the
// entry whose text changed, and every entry after it, are new entries,
// each listed, carrying no history member as its own.
func TestRebuildEdited(t *testing.T) {
	s := historySession(t)
	for _, slim := range []bool{false, true} {
		path := exportDoc(t, s, func(m map[string]any) {
			for _, sv := range m["steps"].([]any) {
				step := sv.(map[string]any)
				if step["message"] == "Read it." {
					step["message"] = "Read that."
					or := step["extra"].(map[string]any)["openresponses"].(map[string]any)
					item := or["item"].(map[string]any)
					item["content"] = "Read that."
				}
			}
		})
		res := convertFixture(t, path, Options{Slim: slim})
		got := reread(t, res.Sessions[0])
		listed := 0
		for _, f := range res.Report.Findings {
			if f.Code == "atif/entry-not-rebuilt" {
				listed++
			}
		}
		// The user item and the four entries after it.
		if listed != 5 {
			t.Errorf("slim %v: %d entries listed as not rebuilt, want 5", slim, listed)
		}
		var legacy bool
		for _, e := range got.Entries() {
			if e.Base().LegacyID != "" {
				t.Errorf("slim %v: %s carries legacy_id as its own", slim, e.Base().ID)
			}
			if _, ok := e.Base().Unknown[Source+":legacy_id"]; ok {
				legacy = true
			}
		}
		if legacy == slim {
			t.Errorf("slim %v: the legacy_id kept as %s:legacy_id = %v", slim, Source, legacy)
		}
		path2 := s.Path(s.Leaf())
		for _, e := range path2[:2] {
			if _, ok := got.Entry(e.Base().ID); !ok {
				t.Errorf("slim %v: %s before the edit is not rebuilt to its id", slim, e.Base().ID)
			}
		}
	}
}

// TestRebuildBlocked: a document that embeds sub-agents is mapped from
// its steps, and says that none of its entries keeps its id.
func TestRebuildBlocked(t *testing.T) {
	s, child := session(t)
	var main export.Trajectory
	for tr, err := range export.Trajectories(s) {
		if err != nil {
			t.Fatal(err)
		}
		if tr.Main {
			main = tr
		}
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
	path := filepath.Join(t.TempDir(), "agent", "trajectory.json")
	if err := export.WriteDocument(path, doc); err != nil {
		t.Fatal(err)
	}
	res := convertFixture(t, path, Options{})
	if !slices.Contains(res.Report.Codes(), "atif/not-rebuilt") {
		t.Errorf("no atif/not-rebuilt: %v", res.Report.Codes())
	}
}

// redactedHistory is historySession redacted, as a redactor writes one,
// so its document carries the header marks the rebuild restores.
func redactedHistory(t *testing.T) *agentsession.Session {
	t.Helper()
	at := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)
	s := agentsession.New(agentsession.Header{ID: "rb-red", CreatedAt: at, Redacted: true,
		RedactedFrom: &agentsession.RedactedFrom{Session: "rb-original"}})
	for i, e := range []agentsession.Entry{
		&agentsession.ConfigEntry{Model: "m-1"},
		agentsession.NewItemEntry(openresponses.UserText("Hi [REDACTED].")),
		agentsession.NewItemEntry(openresponses.UserText("Go on.")),
	} {
		if err := agentsession.SetRedacted(e, []agentsession.Span{{From: i + 1, To: i + 1}}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Second)
		e.Base().Timestamp = at
		if _, err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// TestRebuildVisibleAndLeaf: since format 0.18 an entry matches only
// when its step shows what it projects, and the whole path reaches the
// leaf with every step naming an entry. A document edited in a step's
// visible message alone, or with a step appended after the export, is
// not restored as redacted, and says why; nor is one whose root says
// redacted false.
func TestRebuildVisibleAndLeaf(t *testing.T) {
	s := redactedHistory(t)
	if res := convertFixture(t, exportDoc(t, s, nil), Options{}); !reread(t, res.Sessions[0]).Header().Redacted {
		t.Fatal("the unedited document is not restored as redacted")
	}
	for name, edit := range map[string]func(map[string]any){
		"visible message": func(m map[string]any) {
			for _, sv := range m["steps"].([]any) {
				if step := sv.(map[string]any); step["message"] == "Hi [REDACTED]." {
					step["message"] = "Hi Sam."
				}
			}
		},
		"appended step": func(m map[string]any) {
			steps := m["steps"].([]any)
			m["steps"] = append(steps, map[string]any{"step_id": len(steps) + 1, "timestamp": "2026-09-03T10:00:09Z", "source": "user", "message": "Added later."})
		},
		"redacted false": func(m map[string]any) {
			as := m["extra"].(map[string]any)["agentsession"].(map[string]any)
			as["redacted"] = false
			delete(as, "redacted_from")
		},
	} {
		res := convertFixture(t, exportDoc(t, s, edit), Options{})
		if reread(t, res.Sessions[0]).Header().Redacted {
			t.Errorf("%s: the header is restored as redacted", name)
		}
		codes := res.Report.Codes()
		switch name {
		case "visible message":
			if !slices.Contains(codes, "atif/entry-not-rebuilt") || !slices.Contains(codes, "atif/redaction-not-restored") {
				t.Errorf("%s: %v", name, codes)
			}
		case "appended step":
			if !slices.Contains(codes, "atif/step-not-rebuilt") || !slices.Contains(codes, "atif/redaction-not-restored") {
				t.Errorf("%s: %v", name, codes)
			}
		}
	}
}
