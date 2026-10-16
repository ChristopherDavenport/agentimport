package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/opencode/sqlite"
	"github.com/ChristopherDavenport/agentsession"
)

// fixtureRoots are the converters' fixture directories, whose inputs
// the command reads; their golden outputs are agentsession files, which
// it must skip.
var fixtureRoots = []string{
	"../../claudecode/convert/testdata",
	"../../pi/convert/testdata",
	"../../codex/convert/testdata",
	"../../atif/convert/testdata",
	"../../cline/convert/testdata",
	"../../openhands/convert/testdata",
	"../../opencode/convert/testdata",
}

func runCmd(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRun(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		out  string
	}{
		{nil, 2, ""},
		{[]string{"help"}, 0, "usage: agentimport"},
		{[]string{"version"}, 0, "agentimport "},
		{[]string{"nope"}, 2, ""},
		{[]string{"convert", "x"}, 2, ""},                   // no -o
		{[]string{"convert", "-o", t.TempDir()}, 2, ""},     // no input
		{[]string{"report", "-from", "gemini", "x"}, 2, ""}, // unknown source
		{[]string{"convert", "-report", "yaml", "-o", "o", "x"}, 2, ""},
		{[]string{"report"}, 2, ""},
	} {
		code, out, _ := runCmd(t, tc.args...)
		if code != tc.code || !strings.Contains(out, tc.out) {
			t.Errorf("%v: exit %d, out %q; want %d containing %q", tc.args, code, out, tc.code, tc.out)
		}
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		path, want, err string
	}{
		{"../../claudecode/convert/testdata/plain/11111111-1111-4111-8111-000000000001.jsonl", "claudecode", ""},
		{"../../pi/convert/testdata/anthropic/2026-09-01T10-00-00-000Z_01a00001-0000-7000-8000-000000000001.jsonl", "pi", ""},
		{"../../pi/convert/testdata/v1/" + firstJSONL(t, "../../pi/convert/testdata/v1"), "pi", ""},
		{"../../codex/convert/testdata/plain/rollout-2026-10-01T10-00-00-01990000-0000-7000-8000-000000000001.jsonl", "codex", ""},
		// Codex's 2025 bare layout, whatever the file is called.
		{write("bare.jsonl", `{"id":"0198","timestamp":"2025-08-01T00:00:00Z","instructions":null}`+"\n"+`{"record_type":"state"}`+"\n"), "codex", ""},
		{write("envelope.jsonl", `{"timestamp":"2026-10-01T00:00:00Z","type":"session_meta","payload":{"id":"x"}}`+"\n"), "codex", ""},
		// Claude Code side records come first and carry only a sessionId.
		{write("side.jsonl", `{"type":"queue-operation","operation":"enqueue","sessionId":"s"}`+"\n"), "claudecode", ""},
		{"../../claudecode/convert/testdata/plain/golden/0.jsonl", "", "already an agentsession file"},
		{write("notes.jsonl", `{"hello":"world"}`+"\n"), "", "-from"},
		{"../../atif/convert/testdata/minimal/trajectory.json", "atif", ""},
		{"../../atif/convert/testdata/truncated/trajectory.json", "atif", ""},
		{write("config.json", `{"schema_version":"2.0"}`), "", "-from"},
		{write("empty.jsonl", ""), "", "-from"},
		{"../../cline/convert/testdata/classic-native/saoudrizwan.claude-dev/tasks/1760000000000/api_conversation_history.json", "cline", ""},
		{"../../cline/convert/testdata/classic-xml/saoudrizwan.claude-dev/tasks/1700000000000/claude_messages.json", "cline", ""},
		{write("api_conversation_history.json", "[]"), "", "-from"},
		{"../../openhands/convert/testdata/linear-chat/conversations/00000001000040009000000000000001", "openhands", ""},
		{"../../openhands/convert/testdata/linear-chat/conversations/00000001000040009000000000000001/base_state.json", "openhands", ""},
		{"../../openhands/convert/testdata/linear-chat/conversations/00000001000040009000000000000001/events/event-00001-00000001-0001-4000-8000-000000000002.json", "openhands", ""},
		{write("base_state.json", "{}"), "", "-from"},
		{"../../opencode/convert/testdata/g1-basic", "opencode", ""},
		{"../../opencode/convert/testdata/g1-basic/storage/message/ses_g1demo/msg_0001.json", "opencode", ""},
		{"../../openhands/convert/testdata/linear-chat", "", "-from"},
	} {
		s, err := detect(tc.path)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("detect(%s) = %s, %v; want error containing %q", filepath.Base(tc.path), s.name, err, tc.err)
			}
			continue
		}
		if err != nil || s.name != tc.want {
			t.Errorf("detect(%s) = %s, %v; want %s", filepath.Base(tc.path), s.name, err, tc.want)
		}
	}
}

func firstJSONL(t *testing.T, dir string) string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return e.Name()
		}
	}
	t.Fatalf("no .jsonl in %s", dir)
	return ""
}

// convertResult is convert -report json's output.
type convertResult struct {
	Sessions []struct {
		Path    string
		Source  string
		Written []written
		Kept    []string
		Error   string
	}
	Aggregate map[string]summary
	Skipped   []skipped
}

// TestConvertFixtures converts every fixture into one store and checks
// that each file written reads back as the session the converter made:
// the same header, the same entries in the same order, the same leaf,
// and that it verifies.
func TestConvertFixtures(t *testing.T) {
	store := t.TempDir()
	args := append([]string{"convert", "-o", store, "-report", "json"}, fixtureRoots...)
	code, out, errOut := runCmd(t, args...)
	if code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
	var res convertResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	bySource := map[string]int{}
	for _, in := range res.Sessions {
		if in.Error != "" {
			t.Errorf("%s: %s", in.Path, in.Error)
			continue
		}
		bySource[in.Source]++
		s, _ := sourceNamed(in.Source)
		var ignored []skipped
		want, err := s.convert(in.Path, newRunner(false, &ignored))
		if err != nil {
			t.Fatalf("%s: %v", in.Path, err)
		}
		if len(in.Written) != len(want.Sessions) {
			t.Errorf("%s: wrote %d sessions, the conversion made %d", in.Path, len(in.Written), len(want.Sessions))
			continue
		}
		for i, w := range in.Written {
			if !strings.HasPrefix(w.File, store+string(filepath.Separator)) {
				t.Errorf("%s: wrote %s outside the store", in.Path, w.File)
			}
			got := readSession(t, w.File)
			sameSession(t, w.File, got, want.Sessions[i])
		}
	}
	for _, name := range []string{"claudecode", "pi", "codex", "atif", "cline", "openhands", "opencode"} {
		if bySource[name] == 0 {
			t.Errorf("no %s fixture was converted", name)
		}
		if res.Aggregate[name].Lines == 0 {
			t.Errorf("no aggregate for %s", name)
		}
	}
	for _, s := range res.Skipped {
		if !strings.Contains(s.Path, "golden") && !strings.Contains(s.Path, "/subagents/agent-") &&
			s.Reason != "a Codex sub-agent's rollout, converted with its parent" &&
			s.Reason != "an ATIF continuation or sub-agent file, converted with the document naming it" &&
			s.Reason != "a Roo sub-task, converted with the task that delegated to it" &&
			s.Reason != "an OpenHands sub-agent's conversation, converted with its parent" {
			t.Errorf("skipped %s: %s", s.Path, s.Reason)
		}
	}
}

func readSession(t *testing.T, path string) *agentsession.Session {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	s, err := agentsession.Read(f)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if err := agentimport.Verify(s); err != nil {
		t.Errorf("%s: %v", path, err)
	}
	return s
}

func sameSession(t *testing.T, name string, got, want *agentsession.Session) {
	t.Helper()
	gh, wh := got.Header(), want.Header()
	if gh.ID != wh.ID || gh.ParentSession != wh.ParentSession || gh.SpawnedBy != wh.SpawnedBy || !gh.CreatedAt.Equal(wh.CreatedAt) {
		t.Errorf("%s: header %+v, want %+v", name, gh, wh)
	}
	ids := func(s *agentsession.Session) []string {
		var out []string
		for _, e := range s.Entries() {
			out = append(out, e.Base().ID)
		}
		return out
	}
	if !slices.Equal(ids(got), ids(want)) {
		t.Errorf("%s: entries differ from the conversion's", name)
	}
	var gb, wb bytes.Buffer
	if err := agentsession.Write(&gb, got); err != nil {
		t.Fatal(err)
	}
	if err := agentsession.Write(&wb, want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gb.Bytes(), wb.Bytes()) {
		t.Errorf("%s: the stored session writes differently from the conversion", name)
	}
	// The leaf a reader of the file resumes from, which is what the
	// converter sets; the converter's session in memory follows its
	// appends instead.
	back, err := agentsession.Read(&wb)
	if err != nil {
		t.Fatal(err)
	}
	if got.Leaf() != back.Leaf() {
		t.Errorf("%s: leaf %s, want %s", name, got.Leaf(), back.Leaf())
	}
}

func TestConvertOverwrite(t *testing.T) {
	store := t.TempDir()
	in := "../../claudecode/convert/testdata/plain"
	if code, out, errOut := runCmd(t, "convert", "-o", store, in); code != 0 {
		t.Fatalf("first convert: exit %d\n%s%s", code, out, errOut)
	}
	// A session the store holds is kept and listed, and is no failure:
	// converting a tree again adds what is new.
	code, out, _ := runCmd(t, "convert", "-o", store, in)
	if code != 0 || !strings.Contains(out, "already in the store; -force replaces it") || strings.Contains(out, "wrote ") {
		t.Errorf("second convert: exit %d\n%s", code, out)
	}
	code, out, _ = runCmd(t, "convert", "-force", "-o", store, in)
	if code != 0 || !strings.Contains(out, "wrote ") {
		t.Errorf("-force: exit %d\n%s", code, out)
	}
	files, _ := filepath.Glob(filepath.Join(store, "*", "*.jsonl"))
	if len(files) != 1 {
		t.Errorf("store holds %v", files)
	}
}

func TestStrict(t *testing.T) {
	// Every conversion reports the provenance gap, so -strict refuses
	// any; without it the same run passes.
	in := "../../pi/convert/testdata/anthropic"
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"report", in}, 0},
		{[]string{"report", "-strict", in}, 1},
		{[]string{"convert", "-o", t.TempDir(), in}, 0},
		{[]string{"convert", "-strict", "-o", t.TempDir(), in}, 1},
	} {
		if code, out, errOut := runCmd(t, tc.args...); code != tc.code {
			t.Errorf("%v: exit %d, want %d\n%s%s", tc.args, code, tc.code, out, errOut)
		}
	}
}

func TestReport(t *testing.T) {
	code, out, errOut := runCmd(t, append([]string{"report"}, fixtureRoots...)...)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	for _, want := range []string{"converted: claudecode ", "claudecode: ", "pi: ", "codex: ", "atif: ", "roots and steps:", "gap      agentimport/provenance"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}

	code, out, _ = runCmd(t, append([]string{"report", "-json", "-slim"}, fixtureRoots...)...)
	if code != 0 {
		t.Fatalf("-json: exit %d", code)
	}
	var res struct {
		Files []struct {
			Path, Source string
			Sessions     int
			Report       *summary
			Error        string
		}
		Aggregate map[string]summary
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	lines := map[string]int{}
	for _, f := range res.Files {
		if f.Error != "" || f.Report == nil || f.Sessions == 0 {
			t.Errorf("%s: %+v", f.Path, f)
			continue
		}
		lines[f.Source] += f.Report.Lines
	}
	for name, agg := range res.Aggregate {
		if agg.Lines != lines[name] {
			t.Errorf("%s: aggregate %d lines, files add up to %d", name, agg.Lines, lines[name])
		}
		n := 0
		for _, c := range agg.Tally {
			n += c
		}
		if n != agg.Lines {
			t.Errorf("%s: tally %v does not add up to %d", name, agg.Tally, agg.Lines)
		}
	}
}

// TestNoEscape converts sessions whose native IDs and working
// directories would put a file outside the store: the store refuses
// such an ID (jsonl.ErrBadName, agentsession v0.0.26) and files a
// working directory of "/.." under "default", so nothing is written
// beside the store.
func TestNoEscape(t *testing.T) {
	for _, tc := range []struct {
		name, id, cwd string
		ok            bool
	}{
		{"id", "../../../escaped", "/work", false},
		{"absolute id", "/tmp/escaped", "/work", false},
		{"cwd", "01a00099-0000-7000-8000-000000000099", "/..", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			in := filepath.Join(dir, "in")
			store := filepath.Join(dir, "deep", "store")
			if err := os.MkdirAll(in, 0o755); err != nil {
				t.Fatal(err)
			}
			hdr, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": tc.id, "timestamp": "2026-09-01T10:00:00.000Z", "cwd": tc.cwd})
			body := string(hdr) + "\n" +
				`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-09-01T10:00:01.000Z","message":{"role":"user","content":"hi","timestamp":1788256801000}}` + "\n"
			path := filepath.Join(in, "session.jsonl")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			code, out, _ := runCmd(t, "convert", "-o", store, path)
			switch {
			case tc.ok && code != 0:
				t.Errorf("exit %d, want 0:\n%s", code, out)
			case !tc.ok && (code != 1 || !strings.Contains(out, "failed: ")):
				t.Errorf("exit %d, want 1 with a failure:\n%s", code, out)
			}
			err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && p != path && !strings.HasPrefix(p, store+string(filepath.Separator)) {
					t.Errorf("wrote %s", p)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestCodexFamily checks a Codex sub-agent's rollout converts with its
// ancestor when both are inputs, and on its own when no ancestor is.
func TestCodexFamily(t *testing.T) {
	root := "../../codex/convert/testdata/family/sessions"
	run := func(args ...string) (converted []string, kids int) {
		t.Helper()
		ins, skip, err := collect(args, "auto")
		if err != nil {
			t.Fatal(err)
		}
		newRunner(false, &skip).each(ins, func(o outcome) {
			if o.err != nil {
				t.Errorf("%s: %v", o.in.path, o.err)
				return
			}
			converted = append(converted, filepath.Base(o.in.path))
		})
		for _, s := range skip {
			if s.Reason == "a Codex sub-agent's rollout, converted with its parent" {
				kids++
			}
		}
		return converted, kids
	}
	// The whole tree: the parent converts its family; its three
	// descendants, a grandchild among them, are not converted again.
	if got, kids := run(root); len(got) != 1 || !strings.HasSuffix(got[0], "0000000000f0.jsonl") || kids != 3 {
		t.Errorf("tree: converted %v, %d skipped", got, kids)
	}
	// One day: the children filed on it convert on their own, their
	// parent not being an input.
	if got, kids := run(filepath.Join(root, "2026", "10", "03")); len(got) != 2 || kids != 0 {
		t.Errorf("day: converted %v, %d skipped", got, kids)
	}
	// The grandparent and the grandchild, not its parent: the grandchild
	// waits for its grandparent, whose family holds it.
	gp := filepath.Join(root, "2026", "10", "02", "rollout-2026-10-02T10-00-00-01990000-0000-7000-8000-0000000000f0.jsonl")
	gc := filepath.Join(root, "2026", "10", "03", "rollout-2026-10-02T10-00-40-01990000-0000-7000-8000-0000000000f3.jsonl")
	if got, kids := run(gc, gp); len(got) != 1 || kids != 1 {
		t.Errorf("grandparent and grandchild: converted %v, %d skipped", got, kids)
	}

	// Converting again writes nothing new and fails nothing.
	store := t.TempDir()
	for i := range 2 {
		code, out, errOut := runCmd(t, "convert", "-o", store, "-report", "json", root)
		if code != 0 {
			t.Fatalf("run %d: exit %d: %s\n%s", i, code, errOut, out)
		}
		var res convertResult
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatal(err)
		}
		if len(res.Sessions) != 1 {
			t.Fatalf("run %d: %+v", i, res.Sessions)
		}
		if w, k := len(res.Sessions[0].Written), len(res.Sessions[0].Kept); i == 0 && (w != 4 || k != 0) || i == 1 && (w != 0 || k != 4) {
			t.Errorf("run %d: wrote %d, kept %d", i, w, k)
		}
	}
}

// TestCodexCycle checks a rollout naming itself as its parent, and two
// naming each other, each convert once and are never lost.
func TestCodexCycle(t *testing.T) {
	dir := t.TempDir()
	meta := func(id, parent string) string {
		return `{"timestamp":"2026-10-02T10:00:00.000Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-10-02T10:00:00.000Z","cwd":"/w","cli_version":"0.160.0","source":{"subagent":{"thread_spawn":{"parent_thread_id":"` + parent + `","depth":1}}}}}` + "\n" +
			`{"timestamp":"2026-10-02T10:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Hi."}]}}` + "\n"
	}
	const self, a, b = "01990000-0000-7000-8000-0000000000c0", "01990000-0000-7000-8000-0000000000c1", "01990000-0000-7000-8000-0000000000c2"
	for id, parent := range map[string]string{self: self, a: b, b: a} {
		if err := os.WriteFile(filepath.Join(dir, "rollout-2026-10-02T10-00-00-"+id+".jsonl"), []byte(meta(id, parent)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ins, skip, err := collect([]string{dir}, "auto")
	if err != nil {
		t.Fatal(err)
	}
	sessions := map[string]int{}
	newRunner(false, &skip).each(ins, func(o outcome) {
		if o.err != nil {
			t.Fatalf("%s: %v", o.in.path, o.err)
		}
		for _, s := range o.res.Sessions {
			sessions[s.ID()]++
		}
	})
	if len(sessions) != 3 || sessions[self] != 1 || sessions[a] != 1 || sessions[b] != 1 {
		t.Errorf("sessions = %v", sessions)
	}
}

// TestATIFFamily checks an ATIF document's continuations and sub-agent
// files convert with it and are skipped as inputs, and that a file
// another names converts on its own when named alone.
func TestATIFFamily(t *testing.T) {
	run := func(args ...string) (converted []string, skippedRelated int) {
		t.Helper()
		ins, skip, err := collect(args, "auto")
		if err != nil {
			t.Fatal(err)
		}
		newRunner(false, &skip).each(ins, func(o outcome) {
			if o.err != nil {
				t.Errorf("%s: %v", o.in.path, o.err)
				return
			}
			converted = append(converted, filepath.Base(o.in.path))
		})
		for _, s := range skip {
			if strings.HasPrefix(s.Reason, "an ATIF continuation") {
				skippedRelated++
			}
		}
		return converted, skippedRelated
	}
	refs := "../../atif/convert/testdata/file-refs"
	if got, n := run(refs); len(got) != 1 || got[0] != "trajectory.json" || n != 3 {
		t.Errorf("file-refs: converted %v, %d skipped", got, n)
	}
	if got, n := run(filepath.Join(refs, "trajectory.summarization-1-questions.json")); len(got) != 1 || n != 0 {
		t.Errorf("a sub-agent file alone: converted %v, %d skipped", got, n)
	}
	// A cycle: the child names its parent; each converts once.
	if got, n := run("../../atif/convert/testdata/cycle"); len(got) != 1 || n != 2 {
		t.Errorf("cycle: converted %v, %d skipped", got, n)
	}
}

// TestClineFamily converts a Roo task that delegated to a sub-task: the
// sub-task, an input too, converts with its parent and is skipped; on
// its own it converts.
func TestClineFamily(t *testing.T) {
	run := func(args ...string) (converted []string, skippedChildren int) {
		t.Helper()
		ins, skip, err := collect(args, "auto")
		if err != nil {
			t.Fatal(err)
		}
		newRunner(false, &skip).each(ins, func(o outcome) {
			if o.err != nil {
				t.Errorf("%s: %v", o.in.path, o.err)
				return
			}
			converted = append(converted, filepath.Base(filepath.Dir(o.in.path)))
		})
		for _, s := range skip {
			if strings.HasPrefix(s.Reason, "a Roo sub-task") {
				skippedChildren++
			}
		}
		return converted, skippedChildren
	}
	root := "../../cline/convert/testdata/roo-delegation"
	if got, n := run(root); len(got) != 1 || got[0] != "4f1b2c3d-0000-4000-8000-000000000006" || n != 1 {
		t.Errorf("delegation: converted %v, %d skipped", got, n)
	}
	child := filepath.Join(root, "rooveterinaryinc.roo-cline/tasks/4f1b2c3d-0000-4000-8000-0000000000c6")
	if got, n := run(child); len(got) != 1 || n != 0 {
		t.Errorf("the sub-task alone: converted %v, %d skipped", got, n)
	}
}

// TestOpenHandsFamily converts an OpenHands conversation with a
// sub-agent: the sub-agent's directory, named as an input too, converts
// with its parent and is skipped; on its own it converts.
func TestOpenHandsFamily(t *testing.T) {
	run := func(args ...string) (converted []string, skippedChildren int) {
		t.Helper()
		ins, skip, err := collect(args, "auto")
		if err != nil {
			t.Fatal(err)
		}
		newRunner(false, &skip).each(ins, func(o outcome) {
			if o.err != nil {
				t.Errorf("%s: %v", o.in.path, o.err)
				return
			}
			converted = append(converted, filepath.Base(o.in.path))
		})
		for _, s := range skip {
			if strings.HasPrefix(s.Reason, "an OpenHands sub-agent") {
				skippedChildren++
			}
		}
		return converted, skippedChildren
	}
	parent := "../../openhands/convert/testdata/subagent-task/conversations/0000000b000040009000000000000001"
	child := filepath.Join(parent, "subagents/0000000b000040009000000000000002")
	if got, n := run(parent, child); len(got) != 1 || got[0] != filepath.Base(parent) || n != 1 {
		t.Errorf("parent and child: converted %v, %d skipped", got, n)
	}
	if got, n := run(child); len(got) != 1 || n != 0 {
		t.Errorf("the sub-agent alone: converted %v, %d skipped", got, n)
	}
	// An event file names its conversation, once.
	ev := filepath.Join(parent, "events/event-00000-0000000b-0001-4000-8000-000000000001.json")
	if got, _ := run(ev, parent); len(got) != 1 {
		t.Errorf("an event file and its directory: converted %v", got)
	}
}

// TestOpenCodeStore detects an OpenCode database and its data
// directory, refuses Crush's database of the same name, and converts a
// store once whichever of its paths name it.
func TestOpenCodeStore(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "opencode")
	crush := filepath.Join(root, "project", ".opencode")
	for _, d := range []string{data, crush} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	schema := func(names ...string) string {
		var out string
		for _, n := range names {
			b, err := os.ReadFile("../../opencode/testdata/schema-" + n + ".sql")
			if err != nil {
				t.Fatal(err)
			}
			out += string(b)
		}
		return out
	}
	db := filepath.Join(data, "opencode.db")
	w, err := sqlite.Fixture(db, schema("project", "v1")+`
INSERT INTO project VALUES ('p', '/w', NULL, NULL, NULL, NULL, 1, 1, NULL, '[]', NULL);
INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated) VALUES ('ses_a', 'p', 's', '/w', 't', '1.4.0', 1760000000000, 1760000000000);
INSERT INTO message VALUES ('msg_a', 'ses_a', 1760000000001, 1760000000001, '{"role":"user","time":{"created":1760000000001},"agent":"build","model":{"providerID":"x","modelID":"y"}}');
INSERT INTO part VALUES ('prt_a', 'msg_a', 'ses_a', 1, 1, '{"type":"text","text":"hi"}');`)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	cw, err := sqlite.Fixture(filepath.Join(crush, "opencode.db"), "CREATE TABLE sessions (id TEXT PRIMARY KEY); CREATE TABLE messages (id TEXT PRIMARY KEY, parts TEXT);")
	if err != nil {
		t.Fatal(err)
	}
	cw.Close()
	for _, p := range []string{data, db} {
		if s, err := detect(p); err != nil || s.name != "opencode" {
			t.Errorf("detect(%s) = %s, %v", p, s.name, err)
		}
	}
	if s, err := detect(filepath.Join(crush, "opencode.db")); err == nil {
		t.Errorf("Crush's database is detected as %s", s.name)
	}
	// A database whose schema is still in its write-ahead log, as a
	// running OpenCode's may be, is read from a copy to tell.
	live := filepath.Join(root, "live", "opencode")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	lw, err := sqlite.Fixture(filepath.Join(live, "opencode.db"), schema("project", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	defer lw.Close()
	if s, err := detect(live); err != nil || s.name != "opencode" {
		t.Errorf("a database with its schema in the log: %s, %v", s.name, err)
	}
	ins, skip, err := collect([]string{data, db, filepath.Join(root, "project")}, "auto")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, in := range ins {
		if in.source.name == "opencode" {
			n++
			if in.path != data {
				t.Errorf("input %s, want the data directory", in.path)
			}
		}
	}
	if n != 1 {
		t.Errorf("%d OpenCode inputs, want 1; skipped %v", n, skip)
	}
	store := t.TempDir()
	if code, out, errOut := runCmd(t, "convert", "-o", store, db); code != 0 {
		t.Fatalf("exit %d: %s\n%s", code, errOut, out)
	}
}
