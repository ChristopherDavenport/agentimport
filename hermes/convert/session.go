package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentsession"
)

// sconv converts one session: its row and its messages' into one
// session of the Agent Session Format.
type sconv struct {
	fam   *family
	rec   *record
	tgt   target
	depth int
	// report holds this session's tally and findings, merged into into
	// when the session converts.
	report *agentimport.Report
	into   *agentimport.Report
	b      *agentimport.Builder
	out    *agentsession.Session
	slim   bool

	// units lists the rows read, in order, and disp where each ended
	// up.
	units []string
	disp  map[string]agentimport.Disposition

	// at is the entry the next one follows.
	at string
	// pairing pairs a tool row with a call, and callEntry names each
	// call's entry.
	pairing   *pairing
	callEntry map[string]string

	kids     *kids
	children []*sconv
	// failed are the children that could not be converted, whose rows
	// this session's report counts as dropped.
	failed []*record
}

func newSconv(fam *family, rec *record, tgt target, depth int) *sconv {
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	return &sconv{fam: fam, rec: rec, tgt: tgt, depth: depth, report: r, slim: fam.opts.Slim,
		disp: map[string]agentimport.Disposition{}, pairing: newPairing(), callEntry: map[string]string{}}
}

// sessions returns the session, then its children's, each after its
// parent.
func (sc *sconv) sessions() []*agentsession.Session {
	out := []*agentsession.Session{sc.out}
	for _, ch := range sc.children {
		out = append(out, ch.sessions()...)
	}
	return out
}

// read records that the converter read a unit.
func (sc *sconv) read(unit string) {
	if _, ok := sc.disp[unit]; ok {
		return
	}
	sc.units = append(sc.units, unit)
	sc.disp[unit] = ""
}

// mark records where a unit ended up. A unit that ends in two places is
// counted in the one that keeps the most: mapped, then kept, then
// dropped.
func (sc *sconv) mark(unit string, d agentimport.Disposition) {
	sc.read(unit)
	if rank(d) > rank(sc.disp[unit]) {
		sc.disp[unit] = d
	}
}

func rank(d agentimport.Disposition) int {
	switch d {
	case agentimport.LineMapped:
		return 4
	case agentimport.LineKept:
		return 3
	case agentimport.LineDropped:
		return 2
	case agentimport.LineMalformed:
		return 1
	}
	return 0
}

// addf records a finding against a unit, or the session's row.
func (sc *sconv) addf(kind agentimport.Kind, code, unit string, format string, args ...any) {
	if unit == "" {
		unit = sc.rec.s.Unit
	}
	sc.report.Addf(kind, code, unit, 0, format, args...)
}

// tally counts every unit read, and merges the report into into.
func (sc *sconv) tally() {
	for _, u := range sc.units {
		d := sc.disp[u]
		if d == "" {
			d = agentimport.LineDropped
			sc.addf(agentimport.Dropped, "hermes/unreached", u, "the row was read and nothing made of it")
		}
		sc.report.Line(d)
	}
	sc.into.Merge(sc.report)
}

// abandon counts every unit the session read as dropped, when the
// session could not be converted, and merges the findings that say why.
// Its children's sessions, converted with it, are let go: each is
// converted again on its own, after the roots.
func (sc *sconv) abandon() {
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	for range sc.units {
		r.Line(agentimport.LineDropped)
	}
	// Each child converted with it, and each that failed with it, is
	// converted again on its own; the report built here counts this
	// session's rows alone, so none is counted twice or lost.
	var release func(s *sconv)
	release = func(s *sconv) {
		for _, rec := range s.failed {
			delete(sc.fam.done, rec.s.ID)
		}
		for _, ch := range s.children {
			delete(sc.fam.done, ch.rec.s.ID)
			if ch.tgt.id != ch.rec.own.id {
				delete(sc.fam.ids, ch.tgt.id)
			}
			ch.rec.sid = ""
			release(ch)
		}
	}
	release(sc)
	for _, f := range sc.report.Findings {
		if f.Code == "hermes/session-failed" {
			r.Add(f)
		}
	}
	sc.into.Merge(r)
}

// testHookRun, when set, runs at the end of each session's walk; a test
// makes a session fail with it.
var testHookRun func(sc *sconv) error

// convertInto converts a session into tgt, its tally and findings merged
// into into.
func (fam *family) convertInto(rec *record, tgt target, depth int, into *agentimport.Report) *sconv {
	fam.done[rec.s.ID] = true
	fam.active[rec.s.ID] = true
	defer delete(fam.active, rec.s.ID)
	sc := newSconv(fam, rec, tgt, depth)
	sc.into = into
	if err := sc.run(); err != nil {
		sc.report.Addf(agentimport.Dropped, "hermes/session-failed", rec.s.Unit, 0, "the session could not be converted: %s", agentimport.ErrorText(err))
		sc.abandon()
		return nil
	}
	read, err := agentimport.Finish(sc.b.S)
	if err != nil {
		sc.report = agentimport.NewReport(Source)
		sc.report.Addf(agentimport.Dropped, "hermes/session-failed", rec.s.Unit, 0, "the converted session does not verify: %s", agentimport.ErrorText(err))
		sc.abandon()
		return nil
	}
	sc.out = read
	rec.sid = tgt.id
	sc.tally()
	return sc
}

// run reads the session's rows, writes its header and walks them.
func (sc *sconv) run() error {
	sc.mark(sc.rec.s.Unit, agentimport.LineMapped)
	msgs, err := sc.fam.messages(sc.rec)
	if err != nil {
		return fmt.Errorf("read the messages: %w", err)
	}
	for _, m := range msgs {
		sc.read(m.Unit)
	}
	h, err := sc.header(msgs)
	if err != nil {
		return err
	}
	sc.b = agentimport.NewBuilder(h, Source, sc.report)
	if err := sc.start(); err != nil {
		return err
	}
	sc.kids = sc.findKids(msgs)
	w := newWalk(sc, msgs)
	if err := w.run(); err != nil {
		return err
	}
	if err := sc.unlinkedKids(); err != nil {
		return err
	}
	if err := sc.continuations(); err != nil {
		return err
	}
	if err := sc.b.Leaf(sc.at); err != nil {
		return err
	}
	sc.rec.leaf = sc.b.Resume()
	if testHookRun != nil {
		return testHookRun(sc)
	}
	return nil
}

// header writes the session's header.
func (sc *sconv) header(msgs []*hermes.Message) (agentsession.Header, error) {
	s := sc.rec.s
	h := agentsession.Header{
		ID:        sc.tgt.id,
		CreatedAt: s.Started,
		Harness:   &agentsession.Harness{Name: Harness},
		CWD:       s.CWD,
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Unix(0, 0).UTC()
		for _, m := range msgs {
			if !m.Time.IsZero() {
				h.CreatedAt = m.Time
				break
			}
		}
		sc.addf(agentimport.Lossy, "hermes/created-at", "", "the session row holds no start time Hermes accepts; created_at is its first message's")
	}
	if sc.tgt.parent != "" {
		h.ParentSession = sc.tgt.parent
		h.SpawnedBy = sc.tgt.spawnedBy
	}
	if sc.tgt.derived != "" {
		sc.addf(agentimport.Lossy, "hermes/session-id", "", "%s", sc.tgt.derived)
	}
	native := ""
	if sc.tgt.id != s.ID {
		native = s.ID
	}
	if err := agentimport.SetSource(&h, agentimport.Source{Format: Source, NativeID: native}); err != nil {
		return h, err
	}
	sc.addf(agentimport.Gap, "agentimport/provenance", "", "the import is recorded in the header's %s member", agentimport.SourceMember)
	extra := map[string]json.RawMessage{}
	if native != "" {
		extra[Source+":session_id"] = rawJSON(native)
	}
	if v := sc.fam.version; sc.fam.hasVersion {
		extra[Source+":schema_version"] = rawJSON(v)
	}
	for _, p := range s.Problems {
		sc.addf(agentimport.Repaired, "hermes/mistyped", "", "column %s does not hold what Hermes writes; it is kept as written and read as absent", p)
	}
	usage, err := sc.fam.st.ModelUsage(s.ID)
	if err != nil {
		return h, err
	}
	if !sc.slim {
		rest := s.Cols.Without(sessionMapped...)
		raw, err := cleanRaw(rest.JSON())
		if err != nil {
			return h, err
		}
		extra[Source+":session"] = raw
		sc.addf(agentimport.Kept, "hermes/session-row", "", "the session row's columns the header has no place for are in the header's %s:session member", Source)
		if len(usage) > 0 {
			var rows []json.RawMessage
			for _, u := range usage {
				rows = append(rows, u.Without("session_id").JSON())
			}
			raw, err := cleanRaw(rawJSON(rows))
			if err != nil {
				return h, err
			}
			extra[Source+":model_usage"] = raw
		}
	} else {
		sc.addf(agentimport.Dropped, "hermes/session-row", "", "the session row's columns the header has no place for are left out")
	}
	sc.usageNote(usage)
	for k, v := range extra {
		if h.Extra == nil {
			h.Extra = map[string]json.RawMessage{}
		}
		h.Extra[k] = v
	}
	return h, nil
}

// sessionMapped are the columns of a session row the header and the
// first entries map.
var sessionMapped = []string{"id", "started_at", "cwd", "model", "system_prompt", "title"}

// usageNote reports where the session's usage is: Hermes counts tokens
// and cost per session, and per model of a session, never per response.
func (sc *sconv) usageNote(usage []hermes.Columns) {
	s := sc.rec.s
	any := len(usage) > 0
	for _, c := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "estimated_cost_usd", "actual_cost_usd"} {
		if f, ok := s.Cols.Float(c); ok && f != 0 {
			any = true
		}
	}
	if !any {
		return
	}
	where := "the header's " + Source + ":session member (its token and cost columns) and " + Source + ":model_usage (session_model_usage's rows)"
	if sc.slim {
		where = "nowhere: Slim leaves the session row out"
	}
	sc.addf(agentimport.Gap, "hermes/session-usage", "", "Hermes records usage per session and per model of a session, not per response, so no response entry carries usage; it is in %s", where)
	ran := false
	for _, id := range sc.fam.children[s.ID] {
		if r := sc.fam.recs[id].rel; r == hermes.Delegate || r == hermes.Tool {
			ran = true
		}
	}
	if ran {
		sc.addf(agentimport.Lossy, "hermes/usage-rollup", "", "the session ran children; Hermes adds a sub-agent's estimated cost to its parent's, so the parent's cost columns count it again")
	}
}

// rawJSON is v as JSON, or nil.
func rawJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

// cleanRaw returns raw as I-JSON.
func cleanRaw(raw json.RawMessage) (json.RawMessage, error) {
	out, _, err := agentimport.CleanJSON(raw)
	return out, err
}

// start writes the entries every session opens with: a branch's link to
// its origin, the first settings, the title and the repository.
func (sc *sconv) start() error {
	s := sc.rec.s
	ts := sc.b.S.Header().CreatedAt
	if sc.rec.rel == hermes.Branch && sc.rec.parent != nil && !sc.rec.noParent {
		link := agentsession.NewLinkEntry(agentsession.RelForkOf, sc.rec.parent.sidOrID())
		link.Timestamp = ts
		id, err := sc.b.Append("", link)
		if err != nil {
			return err
		}
		sc.at = id
	}
	cfg := &agentsession.ConfigEntry{Model: s.Model}
	cfg.Timestamp = ts
	prompt, from := sc.systemPrompt()
	if prompt != "" {
		cfg.Instructions = &prompt
	}
	switch {
	case prompt == "":
		sc.addf(agentimport.Dropped, "hermes/system-prompt", "", "the session row holds no system prompt; the settings carry the model alone")
	default:
		sc.addf(agentimport.Lossy, "hermes/system-prompt", "", "the instructions are the system prompt Hermes stored for the session (%s); what it adds per request, the ephemeral prompt, prefill messages and per-turn notes, is not stored", from)
	}
	if mc := s.ModelConfig; len(mc) > 0 {
		if err := sc.member(cfg, "model_config", mc, ""); err != nil {
			return err
		}
	}
	if tn := s.Cols.String("tool_names"); tn != "" {
		sc.addf(agentimport.Dropped, "hermes/tools", "", "Hermes stores the names of the session's tools, kept with the session row, not their definitions; the settings carry no tools")
	} else {
		sc.addf(agentimport.Dropped, "hermes/tools", "", "Hermes does not store the session's tool definitions; the settings carry no tools")
	}
	id, err := sc.b.Append(sc.at, cfg)
	if err != nil {
		return err
	}
	sc.at = id
	if s.Title != "" {
		info := &agentsession.InfoEntry{Name: s.Title}
		info.Timestamp = ts
		if sc.at, err = sc.b.Append(sc.at, info); err != nil {
			return err
		}
	}
	if s.GitRepoRoot != "" || s.GitBranch != "" {
		env := agentsession.NewEnvEntry(s.CWD)
		env.VCS = &agentsession.VCS{System: "git"}
		env.Timestamp = ts
		if s.GitBranch != "" {
			if err := env.VCS.SetMember(Source+":branch", s.GitBranch); err != nil {
				return err
			}
			sc.addf(agentimport.Gap, "agentimport/vcs-branch", "", "the session's git branch is in the vcs member %s:branch", Source)
		}
		if err := sc.b.Member(env, "repo_root", s.GitRepoRoot); err != nil {
			return err
		}
		if sc.at, err = sc.b.Append(sc.at, env); err != nil {
			return err
		}
	}
	return nil
}

// systemPrompt is the session's system prompt: system_prompts' row the
// hash names, from schema version 25, else the row's own column, which
// Hermes keeps as the fallback.
func (sc *sconv) systemPrompt() (prompt, from string) {
	s := sc.rec.s
	if s.SystemPromptHash != "" {
		if p, ok := sc.fam.st.SystemPrompt(s.SystemPromptHash); ok {
			return p, "system_prompts, by the row's system_prompt_hash"
		}
		if s.SystemPrompt == "" {
			sc.addf(agentimport.Lossy, "hermes/system-prompt-missing", "", "the session names system prompt %s, which system_prompts does not hold", s.SystemPromptHash)
		}
	}
	return s.SystemPrompt, "the row's system_prompt column"
}

// append adds an entry after the current one and makes it current.
func (sc *sconv) append(e agentsession.Entry) (string, error) {
	id, err := sc.b.Append(sc.at, e)
	if err != nil {
		return "", err
	}
	sc.at = id
	return id, nil
}

// member sets a namespaced member from a native value, or drops it
// under Slim and says so. unit names the row it came from.
func (sc *sconv) member(e agentsession.Entry, name string, v any, unit string) error {
	if isEmpty(v) {
		return nil
	}
	if sc.slim {
		sc.addf(agentimport.Dropped, "hermes/members", unit, "%s is left out", name)
		return nil
	}
	sc.addf(agentimport.Kept, "hermes/members", unit, "kept in %s:%s", Source, name)
	return sc.b.Member(e, name, v)
}

// isEmpty reports a value that says nothing.
func isEmpty(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case json.RawMessage:
		t := bytes.TrimSpace(v)
		return len(t) == 0 || string(t) == "null" || string(t) == "{}" || string(t) == "[]"
	case []json.RawMessage:
		return len(v) == 0
	}
	return false
}

// custom writes a custom entry holding a native row, which the model
// never saw, or drops it under Slim; the unit is tallied either way.
func (sc *sconv) custom(ts time.Time, unit, code, what string, data any) error {
	if sc.slim {
		sc.mark(unit, agentimport.LineDropped)
		sc.addf(agentimport.Dropped, code, unit, "%s, which is not context, is left out", what)
		return nil
	}
	raw, err := cleanRaw(rawJSON(data))
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: raw}
	e.Timestamp = ts
	if _, err := sc.append(e); err != nil {
		return err
	}
	sc.mark(unit, agentimport.LineKept)
	sc.addf(agentimport.Kept, code, unit, "%s, which is not context, is a custom entry", what)
	return nil
}
