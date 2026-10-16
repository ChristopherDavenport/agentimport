// Strings in this file are copied from cline/cline
// (https://github.com/cline/cline) at c8f808b5, Copyright 2026 Cline Bot
// Inc., licensed under the Apache License 2.0, so that the output
// matches what Cline sends. See THIRD_PARTY_NOTICES.md.

package convert

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// Texts Cline writes into a history.
const (
	// legacyWarning is the user message the VS Code extension appends
	// to a classic task's history when it carries it on as a session
	// (apps/vscode/src/sdk/legacy-task-handling.ts).
	legacyWarning = "Warning: this is a legacy conversation, which means tool names may have changed. Please use the most up-to-date tools you are aware of."
	// rejectionSuffix ends the reason of every refused approval
	// (TOOL_REJECTION_SUFFIX).
	rejectionSuffix = "NOT a tool or system failure. Clarify with user before proceeding."
	// emptyContent is what Cline sends for a message with no content
	// (EMPTY_CONTENT_TEXT).
	emptyContent = "ERROR: EMPTY CONTENT"
)

// errSkip ends the conversion of a history that converts to no session.
var errSkip = errors.New("skip")

// sconv converts one history into one session.
type sconv struct {
	fam    *family
	h      *hist
	tgt    target
	depth  int
	b      *agentimport.Builder
	report *agentimport.Report
	disp   map[unit]agentimport.Disposition
	out    *agentsession.Session
	// children are the conversions of the children this history ran,
	// in the order their calls were met.
	children []*sconv

	tail    string
	last    time.Time
	created time.Time
	model   string
	// system is the system prompt a child's spawning call gave it.
	system string

	// copyEnd is the first message that is the session's own: those
	// before it are a copy of another history's, whose usage the
	// response entries do not carry; copyWhy says whose.
	copyEnd int
	copyWhy string
	legacy  bool
	fork    *clinesdk.Fork
	// forkSID is the session the fork's source converts to.
	forkSID string
	// skipWhy is set when the history converts to no session.
	skipWhy string

	// first is the first entry each message wrote; rested marks the
	// messages whose own members an entry carries.
	first  map[int]string
	rested map[int]bool
	// calls are the calls written, by native ID, the latest of each;
	// pending the last assistant message's, in order; answered the
	// native IDs a result in the history answers.
	calls    map[string]*call
	pending  []*call
	answered map[string]bool
	respN    int
	respIDs  map[string]bool

	comp *compPlan
	// omits are the item entries config entries left out of context.
	omits []string
	// ctx is the context the next request sends, for the projection;
	// rd its index of reads; bytes its text's size.
	ctx      []*clinesdk.Message
	rd       *reads
	bytes    int
	budgeted bool
	// imaged says an image was reported.
	imaged bool
}

// call is a function call a message made.
type call struct {
	native string
	id     string
	name   string
	entry  string
	output bool
	msg    int
}

func newSconv(fam *family, h *hist, tgt target, depth int) *sconv {
	return &sconv{fam: fam, h: h, tgt: tgt, depth: depth, system: tgt.system, report: agentimport.NewReport(Source), disp: map[unit]agentimport.Disposition{},
		first: map[int]string{}, rested: map[int]bool{}, calls: map[string]*call{}, answered: map[string]bool{}, respIDs: map[string]bool{}, rd: newReads()}
}

// sessions is the session, then its children's, each after its own.
func (sc *sconv) sessions() []*agentsession.Session {
	out := []*agentsession.Session{sc.out}
	for _, ch := range sc.children {
		out = append(out, ch.sessions()...)
	}
	return out
}

func (sc *sconv) root() bool { return sc.h.i == 0 }

func (sc *sconv) name() string { return sc.h.f.Name }

// addf records a finding against a file at a 1-based element, or 0.
func (sc *sconv) addf(kind agentimport.Kind, code, file string, line int, format string, args ...any) {
	if file == "" {
		file = sc.name()
	}
	sc.report.Addf(kind, code, file, line, format, args...)
}

// at records a finding against message k.
func (sc *sconv) at(kind agentimport.Kind, code string, k int, format string, args ...any) {
	sc.addf(kind, code, sc.name(), k+1, format, args...)
}

func (sc *sconv) mark(u unit, d agentimport.Disposition) {
	if _, ok := sc.disp[u]; !ok {
		sc.disp[u] = d
	}
}

func (sc *sconv) msgUnit(k int) unit { return unit{uMsg, sc.h.i, k} }

// keeping says what Slim makes of something kept: the kind to report
// and the disposition.
func (sc *sconv) keeping() (agentimport.Kind, agentimport.Disposition) {
	if sc.fam.opts.Slim {
		return agentimport.Dropped, agentimport.LineDropped
	}
	return agentimport.Kept, agentimport.LineKept
}

// clean makes raw I-JSON, reporting what it changed.
func (sc *sconv) clean(line int, raw json.RawMessage) (json.RawMessage, error) {
	if agentimport.RepeatedMembers(raw) {
		sc.addf(agentimport.Repaired, "agentimport/repeated-member", "", line, "an object named a member twice, which I-JSON forbids; the last is kept")
	}
	if agentimport.InvalidText(raw) {
		sc.addf(agentimport.Repaired, "agentimport/invalid-text", "", line, "text that was not valid Unicode was replaced with U+FFFD")
	}
	out, _, err := agentimport.CleanJSON(raw)
	return out, err
}

// appendEntry appends e after the tail, with the last timestamp when it
// has none.
func (sc *sconv) appendEntry(e agentsession.Entry) (string, error) {
	base := e.Base()
	if base.Timestamp.IsZero() {
		base.Timestamp = sc.last
		if base.Timestamp.IsZero() {
			base.Timestamp = sc.created
		}
	}
	id, err := sc.b.Append(sc.tail, e)
	if err != nil {
		return "", err
	}
	sc.tail = id
	if base.Timestamp.After(sc.last) {
		sc.last = base.Timestamp
	}
	return id, nil
}

// written records the first entry message k wrote.
func (sc *sconv) written(k int, id string) {
	if _, ok := sc.first[k]; !ok && k >= 0 {
		sc.first[k] = id
	}
}

// run converts the history and accounts for its units.
func (sc *sconv) run() error {
	sc.prepare()
	if sc.skipWhy != "" {
		return errSkip
	}
	h, err := sc.header()
	if err != nil {
		return err
	}
	sc.b = agentimport.NewBuilder(h, Source, sc.report)
	sc.b.File = sc.name()
	if err := sc.start(); err != nil {
		return err
	}
	msgs := sc.h.f.Messages
	for k := 0; k <= len(msgs); k++ {
		if sc.comp != nil && sc.comp.apply && k == sc.comp.at {
			if err := sc.writeCompaction(); err != nil {
				return err
			}
		}
		if k == len(msgs) {
			break
		}
		if err := sc.message(k, msgs[k]); err != nil {
			return fmt.Errorf("message %d: %w", k+1, err)
		}
	}
	return sc.end()
}

// prepare reads what the walk needs before it starts: whose copy the
// history starts with, the compaction, the calls the history answers.
func (sc *sconv) prepare() {
	f := sc.h.f
	if sc.h.kind == clinesdk.AgentTeammate && sc.h.own > 0 {
		sc.copyEnd, sc.copyWhy = sc.h.own, "team"
	}
	for _, m := range f.Messages {
		if m.Err != nil {
			continue
		}
		for _, b := range m.Blocks() {
			if b.Err == nil && b.Type == clinesdk.BlockToolResult {
				sc.answered[b.ToolUseID] = true
			}
		}
	}
	if !sc.root() {
		return
	}
	if m := sc.fam.s.Manifest; m != nil {
		if fk := m.Fork(); fk != nil {
			// A fork copies its source's history and metadata, a
			// carried-on classic task's warning and flags among them:
			// it is the source's fork, whatever it copied.
			sc.fork = fk
			sc.copyEnd, sc.forkSID = sc.forkCopy(fk)
			sc.copyWhy = "fork"
		}
	}
	if w := legacyCopy(sc.fam.s); w >= 0 {
		sc.legacy, sc.copyEnd, sc.copyWhy = true, w+1, "legacy"
		own := false
		for _, m := range f.Messages[w+1:] {
			if m.Err == nil && !m.DisplayOnly() {
				own = true
			}
		}
		if !own {
			sc.skipWhy = fmt.Sprintf("the VS Code extension carried classic task %s on as this session and nothing was written after its copy of the task's history; the session is the classic task's, which the cline source converts, and the copy (%d messages) is left out", sc.fam.rootID(), len(f.Messages))
			return
		}
	} else if sc.fork == nil {
		if m := sc.fam.s.Manifest; m != nil && m.LegacyTask() {
			sc.addf(agentimport.Lossy, "clinesdk/legacy-flag", sc.fam.s.ManifestName(), 0, "the manifest marks the session as a classic task carried on, and its history holds no copy of one; it is converted as a session of its own")
		} else if w := legacyAt(f.Messages); w >= 0 {
			sc.at(agentimport.Lossy, "clinesdk/legacy-flag", w, "the history holds the warning the VS Code extension appends to a classic task it carries on, but the session is no classic task's: its manifest does not mark it so and its ID is not a task's; it is converted as a session of its own")
		}
	}
	if c := sc.fam.s.Compaction; c != nil {
		sc.comp = sc.planCompaction(c)
	}
}

// legacyCopy is the index of the legacy warning in a session's history
// when the history is the VS Code extension's copy of a classic task
// carried on: the session is no fork, its history holds the warning,
// and its manifest marks it a classic task or its ID is a task's, the
// digits of a time. -1 otherwise.
func legacyCopy(s *clinesdk.Session) int {
	if s == nil || s.Messages == nil {
		return -1
	}
	m := s.Manifest
	if m != nil && m.Fork() != nil {
		return -1
	}
	w := legacyAt(s.Messages.Messages)
	if w < 0 {
		return -1
	}
	if m != nil && m.LegacyTask() || classicID(nativeID(s)) {
		return w
	}
	return -1
}

// classicID reports whether id is a classic task's: Date.now() as
// digits.
func classicID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// nativeID is a session's ID: its manifest's, else its directory's
// name.
func nativeID(s *clinesdk.Session) string {
	if m := s.Manifest; m != nil && m.SessionID != "" {
		return m.SessionID
	}
	return s.ID
}

// legacyAt is the index of the user message holding the legacy
// warning, -1 when there is none.
func legacyAt(msgs []*clinesdk.Message) int {
	for k, m := range msgs {
		if m.Err != nil || m.Role != clinesdk.RoleUser {
			continue
		}
		for _, b := range m.Blocks() {
			if b.Err == nil && b.Type == clinesdk.BlockText && strings.Contains(b.Text, legacyWarning) {
				return k
			}
		}
	}
	return -1
}

// skipped accounts for a history that converts to no session.
func (sc *sconv) skipped() {
	sc.fam.report.Addf(agentimport.Dropped, "clinesdk/duplicate-store", sc.name(), 0, "%s", sc.skipWhy)
	sc.fam.remark(unit{uFile, sc.h.i, 0}, agentimport.LineDropped)
	for k := range sc.h.f.Messages {
		sc.fam.remark(sc.msgUnit(k), agentimport.LineDropped)
	}
	if sc.root() {
		sc.fam.remark(unit{uManifest, 0, 0}, agentimport.LineDropped)
		if c := sc.fam.s.Compaction; c != nil {
			sc.fam.remark(unit{uComp, 0, 0}, agentimport.LineDropped)
			for k := range c.Messages {
				sc.fam.remark(unit{uCompMsg, 0, k}, agentimport.LineDropped)
			}
		}
	}
}

// header builds the session header.
func (sc *sconv) header() (agentsession.Header, error) {
	f := sc.h.f
	s := sc.fam.s
	h := agentsession.Header{ID: sc.tgt.id, ParentSession: sc.tgt.parent, SpawnedBy: sc.tgt.spawnedBy}
	if sc.root() {
		switch {
		case sc.legacy:
			h.ParentSession = ClassicSessionID(sc.fam.rootID())
		case sc.fork != nil:
			h.ParentSession = sc.forkSID
		}
	}
	var created time.Time
	ok := false
	if sc.root() && s.Manifest != nil {
		created, ok = s.Manifest.Started()
	}
	if !ok && sc.h.row != nil {
		created, ok = clinesdk.ISOTime(sc.h.row.StartedAt)
	}
	if !ok {
		for _, m := range f.Messages {
			if m.Err == nil {
				if created, ok = m.Time(); ok {
					break
				}
			}
		}
	}
	if !ok {
		created, ok = clinesdk.ISOTime(f.UpdatedAt)
	}
	if !ok {
		created = time.Unix(0, 0).UTC()
		sc.addf(agentimport.Lossy, "clinesdk/no-timestamp", "", 0, "the session records no time; created_at is the epoch")
	}
	h.CreatedAt = created
	sc.created = created
	if m := s.Manifest; m != nil {
		h.CWD = m.CWD
	}
	version := ""
	if f.Origin != nil {
		version = f.Origin.Version
	}
	if version == "" && s.Messages != nil && s.Messages.Origin != nil {
		version = s.Messages.Origin.Version
	}
	h.Harness = &agentsession.Harness{Name: Harness, Version: version}
	src := agentimport.Source{Format: Source}
	if sc.tgt.id != sc.tgt.native {
		src.NativeID = sc.tgt.native
	}
	if err := agentimport.SetSource(&h, src); err != nil {
		return h, err
	}
	sc.addf(agentimport.Gap, "agentimport/provenance", "", 0, "the import is recorded in the header member %s", agentimport.SourceMember)
	if sc.tgt.derived != "" {
		sc.addf(agentimport.Lossy, "clinesdk/session-id", "", 0, "%s", sc.tgt.derived)
	}
	if sc.root() && s.Manifest != nil {
		if raw := s.Manifest.ImportedFrom(); raw != nil {
			raw, err := sc.clean(0, raw)
			if err != nil {
				return h, err
			}
			setHeader(&h, "imported_from", raw)
			sc.addf(agentimport.Lossy, "clinesdk/imported-session", s.ManifestName(), 0, "Cline imported the session from another agent's (header member %s:imported_from); its history is Cline's re-encoding of that session, which Cline summarised once on its first resume", Source)
		}
	}
	kind, _ := sc.keeping()
	if sc.root() && s.HasManifest {
		if s.Manifest == nil {
			sc.addf(agentimport.Repaired, "clinesdk/malformed-file", s.ManifestName(), 0, "%s; it is skipped", agentimport.ErrorText(s.ManifestErr))
			sc.mark(unit{uManifest, 0, 0}, agentimport.LineMalformed)
		} else if sc.fam.opts.Slim {
			sc.addf(agentimport.Dropped, "clinesdk/manifest", s.ManifestName(), 0, "the manifest's members the header has no place for are left out")
			sc.mark(unit{uManifest, 0, 0}, agentimport.LineMapped)
		} else {
			raw, err := json.Marshal(s.Manifest)
			if err != nil {
				return h, err
			}
			if raw, err = sc.clean(0, raw); err != nil {
				return h, err
			}
			setHeader(&h, "manifest", raw)
			sc.addf(kind, "clinesdk/manifest", s.ManifestName(), 0, "the manifest is the header member %s:manifest", Source)
			sc.mark(unit{uManifest, 0, 0}, agentimport.LineMapped)
		}
	}
	if f.Err != nil {
		sc.addf(agentimport.Repaired, "clinesdk/malformed-file", "", 0, "%s; the %d messages before the fault are read", agentimport.ErrorText(f.Err), len(f.Messages))
		sc.mark(unit{uFile, sc.h.i, 0}, agentimport.LineMalformed)
	} else {
		sc.mark(unit{uFile, sc.h.i, 0}, agentimport.LineMapped)
	}
	if f.Bare {
		sc.addf(agentimport.Lossy, "clinesdk/version", "", 0, "the file is a bare array of messages, as Cline wrote before its messages file had a version")
	} else if n, ok := f.VersionNumber(); f.Err == nil && (!ok || n != 1) {
		sc.addf(agentimport.Lossy, "clinesdk/version", "", 0, "the messages file's version is %s, not 1; it is read as version 1", strings.TrimSpace(string(f.Version)))
	}
	if !f.Bare && f.Err == nil && !f.HasMessages {
		sc.addf(agentimport.Lossy, "clinesdk/no-messages", "", 0, "the messages file has no messages array; Cline reads it as an empty history")
	}
	env, err := envelopeRest(f)
	if err != nil {
		return h, err
	}
	if len(env) > 0 && !sc.fam.opts.Slim {
		raw, err := json.Marshal(env)
		if err != nil {
			return h, err
		}
		if raw, err = sc.clean(0, raw); err != nil {
			return h, err
		}
		setHeader(&h, "file", raw)
	}
	if r := sc.h.row; r != nil && len(r.Raw) > 0 {
		if sc.fam.opts.Slim {
			sc.addf(agentimport.Dropped, "clinesdk/index-row", "", 0, "the session's row in Cline's index is left out")
		} else {
			raw, err := sc.clean(0, r.Raw)
			if err != nil {
				return h, err
			}
			setHeader(&h, "index_row", raw)
			sc.addf(agentimport.Kept, "clinesdk/index-row", "", 0, "the session's row in Cline's index is the header member %s:index_row", Source)
		}
	}
	return h, nil
}

// envelopeRest is a messages file's envelope without the members the
// conversion maps: its messages and its system prompt.
func envelopeRest(f *clinesdk.MessagesFile) (map[string]json.RawMessage, error) {
	if f.Bare {
		return nil, nil
	}
	g := *f
	g.HasMessages = false
	g.Messages = nil
	g.SystemPrompt = nil
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	delete(all, "messages")
	delete(all, "system_prompt")
	return all, nil
}

func setHeader(h *agentsession.Header, name string, raw json.RawMessage) {
	if h.Extra == nil {
		h.Extra = map[string]json.RawMessage{}
	}
	h.Extra[Source+":"+name] = raw
}

// sessionID is the session ID a session directory's own history
// converts to, as rootTarget and ownID give it: derived for a classic
// task carried on, or for an ID a store does not take.
func sessionID(s *clinesdk.Session) string {
	native := nativeID(s)
	if legacyCopy(s) >= 0 {
		return agentsession.SubsessionID(native, Source)
	}
	return nativeSID(native)
}

// nativeSID is the session ID a native ID converts to when nothing
// more is known of its session.
func nativeSID(native string) string {
	if storeName(native) {
		return native
	}
	return agentsession.SubsessionID(Source, "session/"+native)
}

// start writes the entries every session opens with: the link to the
// history it copies, the first settings, the title.
func (sc *sconv) start() error {
	switch {
	case sc.legacy:
		link := agentsession.NewLinkEntry(agentsession.RelForkOf, ClassicSessionID(sc.fam.rootID()))
		link.Timestamp = sc.created
		if err := sc.b.Member(link, "migrated_from", sc.fam.rootID()); err != nil {
			return err
		}
		if _, err := sc.appendEntry(link); err != nil {
			return err
		}
		sc.addf(agentimport.Lossy, "clinesdk/legacy-copy", "", 0, "the VS Code extension carried classic task %[1]s on as this session: its first %[2]d messages are the extension's copy of the task's history after its deleted range, which the cline source converts as session %[3]s, named in parent_session and by a fork_of link; the copy is converted as this session's own entries, its usage kept off the responses. The copy is lossy: thinking without its signature, redacted thinking, documents and reasoning details left out, tool results made text, and the whole task's usage put on its last assistant message", sc.fam.rootID(), sc.copyEnd, ClassicSessionID(sc.fam.rootID()))
	case sc.fork != nil:
		link := agentsession.NewLinkEntry(agentsession.RelForkOf, sc.forkSID)
		link.Timestamp = sc.created
		raw, err := sc.clean(0, sc.fork.Raw)
		if err != nil {
			return err
		}
		if err := sc.b.Member(link, "fork", raw); err != nil {
			return err
		}
		if _, err := sc.appendEntry(link); err != nil {
			return err
		}
	}
	model, provider := sc.firstModel()
	sc.model = model
	cfg := &agentsession.ConfigEntry{Model: model}
	cfg.Timestamp = sc.created
	if sp := sc.h.f.SystemPrompt; sp != nil {
		text := *sp
		cfg.Instructions = &text
		sc.addf(agentimport.Lossy, "clinesdk/system-prompt-latest", "", 0, "the system prompt is the one the file holds, the latest Cline sent; earlier requests may have sent another")
	} else if sc.system != "" {
		text := sc.system
		cfg.Instructions = &text
		sc.addf(agentimport.Lossy, "clinesdk/system-prompt-from-call", "", 0, "the history holds no system prompt; the instructions are the systemPrompt of the spawn_agent call that ran it")
	} else {
		sc.addf(agentimport.Lossy, "clinesdk/no-system-prompt", "", 0, "the history holds no system prompt; the settings carry the model alone")
	}
	if provider != "" {
		if err := sc.b.Member(cfg, "provider", provider); err != nil {
			return err
		}
	}
	if _, err := sc.appendEntry(cfg); err != nil {
		return err
	}
	if sc.root() && sc.fam.s.Manifest != nil {
		if title := sc.fam.s.Manifest.Title(); title != "" {
			info := &agentsession.InfoEntry{Name: title}
			info.Timestamp = sc.created
			if _, err := sc.appendEntry(info); err != nil {
				return err
			}
		}
	}
	sc.addf(agentimport.Lossy, "clinesdk/no-stop-reason", "", 0, "Cline records no stop reason; every response is completed")
	sc.teamCopy()
	return nil
}

// firstModel is the model of the history's first assistant message,
// else the manifest's.
func (sc *sconv) firstModel() (model, provider string) {
	for _, m := range sc.h.f.Messages {
		if m.Err == nil && m.Role == clinesdk.RoleAssistant && !m.DisplayOnly() && m.ModelInfo != nil && m.ModelInfo.ID != "" {
			return m.ModelInfo.ID, m.ModelInfo.Provider
		}
	}
	if m := sc.fam.s.Manifest; m != nil {
		return m.Model, m.Provider
	}
	return "", ""
}

// message converts message k.
func (sc *sconv) message(k int, m *clinesdk.Message) error {
	ts, ok := m.Time()
	if !ok {
		ts = time.Time{}
	}
	switch {
	case m.Err != nil:
		sc.at(agentimport.Repaired, "clinesdk/malformed", k, "an element that is not a message is skipped: %s", agentimport.ErrorText(m.Err))
		sc.mark(sc.msgUnit(k), agentimport.LineMalformed)
		return nil
	case m.DisplayOnly():
		return sc.custom(k, m, ts, "display_only", "clinesdk/display-only", "a display-only message, which the chat view shows and no request sends,")
	case m.Content.Err != nil:
		return sc.custom(k, m, ts, "malformed_content", "clinesdk/content-malformed", "a message whose content is neither text nor blocks, which Cline cannot send,")
	}
	if len(m.Mistyped()) > 0 {
		sc.at(agentimport.Lossy, "clinesdk/mistyped-member", k, "the message's members %s are not of their type; they are kept in %s:message and read as empty", strings.Join(m.Mistyped(), ", "), Source)
	}
	var err error
	switch m.Role {
	case clinesdk.RoleUser:
		err = sc.user(k, m, ts)
	case clinesdk.RoleAssistant:
		err = sc.assistant(k, m, ts)
	default:
		return sc.custom(k, m, ts, "unknown_role", "clinesdk/unknown-role", fmt.Sprintf("a message of role %q, which Cline does not send,", m.Role))
	}
	if err != nil {
		return err
	}
	if m.IsCompactionSummary() {
		sc.at(agentimport.Lossy, "clinesdk/compaction-inline", k, "a compaction summary in the history: Cline before 4.1 replaced the history it summarised with it, so the file holds nothing before the compaction it made")
	}
	sc.mark(sc.msgUnit(k), agentimport.LineMapped)
	sc.push(m)
	return nil
}

// custom writes message k as a custom entry, out of context.
func (sc *sconv) custom(k int, m *clinesdk.Message, ts time.Time, kind, code, what string) error {
	fkind, d := sc.keeping()
	if sc.fam.opts.Slim {
		sc.at(fkind, code, k, "%s is left out", what)
		sc.mark(sc.msgUnit(k), d)
		return nil
	}
	raw, err := sc.clean(k+1, m.Elem)
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"kind": rawJSON(kind), "message": raw})
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	e.Timestamp = ts
	id, err := sc.appendEntry(e)
	if err != nil {
		return err
	}
	sc.written(k, id)
	sc.at(fkind, code, k, "%s is a custom entry", what)
	sc.mark(sc.msgUnit(k), d)
	return nil
}

// messageRest is a message without the members the conversion maps.
func messageRest(m *clinesdk.Message, mapped ...string) (map[string]json.RawMessage, error) {
	v, err := jsjson.Parse(m.Elem)
	if err != nil || v.Kind != jsjson.Object {
		return nil, err
	}
	for _, k := range append([]string{"role", "content", "ts"}, mapped...) {
		v.Delete(k)
	}
	if len(v.Keys) == 0 {
		return nil, nil
	}
	out := map[string]json.RawMessage{}
	for i, k := range v.Keys {
		out[k.String()] = v.Vals[i].Stringify()
	}
	return out, nil
}

// keepMembers sets the members of something the session has no place
// for as a namespaced member, or reports them dropped with Slim.
func (sc *sconv) keepMembers(e agentsession.Entry, k int, name string, rest map[string]json.RawMessage, what string) error {
	if len(rest) == 0 {
		return nil
	}
	if sc.fam.opts.Slim {
		sc.at(agentimport.Dropped, "clinesdk/members", k, "%s members the session has no place for are left out", what)
		return nil
	}
	raw, err := json.Marshal(rest)
	if err != nil {
		return err
	}
	if raw, err = sc.clean(k+1, raw); err != nil {
		return err
	}
	if err := sc.b.Member(e, name, json.RawMessage(raw)); err != nil {
		return err
	}
	sc.at(agentimport.Kept, "clinesdk/members", k, "%s members the session has no place for are %s:%s", what, Source, name)
	return nil
}

// blockRest is a block without the members the conversion maps.
func blockRest(b *clinesdk.Block, mapped ...string) map[string]json.RawMessage {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(raw, &all) != nil {
		return nil
	}
	delete(all, "type")
	for _, k := range mapped {
		delete(all, k)
	}
	if len(all) == 0 {
		return nil
	}
	return all
}

// end writes what follows the last message: a stale sidecar, and the
// notes on calls left without a result.
func (sc *sconv) end() error {
	if sc.comp != nil && !sc.comp.apply {
		if err := sc.staleCompaction(); err != nil {
			return err
		}
	}
	for _, p := range sc.pending {
		if !p.output && !sc.answered[p.native] {
			sc.at(agentimport.Lossy, "clinesdk/pending-call", p.msg, "call %s (%s) has no result and the history ends; the session reads it as pending", p.id, p.name)
		}
	}
	return nil
}

// itemEntry is an item entry at a time.
func itemEntry(item openresponses.Item, ts time.Time) *agentsession.ItemEntry {
	e := agentsession.NewItemEntry(item)
	e.Timestamp = ts
	return e
}
