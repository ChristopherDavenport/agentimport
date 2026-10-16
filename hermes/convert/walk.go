package convert

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// A row's place in the record, by its flags: in the context Hermes
// resumes with (active), summarised by an in-place compaction
// (compacted), or out of context otherwise: undone by a rewind, a
// compaction's superseded original of a row it carried, or deactivated.
const (
	live     = 'A'
	folded   = 'C'
	inactive = 'R'
)

// walk converts a session's rows, in id order, the order Hermes reads
// them in.
type walk struct {
	sc *sconv
	// rows are the rows that read, and class each one's place.
	rows  []*hermes.Message
	class []byte
	// start marks the rows a generation of in-place compaction starts
	// at, and summary is each generation's summary row, -1 when it has
	// none; copied marks the rows Hermes copied rather than received.
	start   map[int]bool
	summary map[int]int
	copied  map[int]bool
	// mainAt is the end of the main line, the path to the context
	// Hermes resumes with; a run of inactive rows hangs off it as a
	// branch of its own.
	mainAt string
	// pending is a compaction waiting for its summary, the next item on
	// the main line; inRun says the walk is on a branch of rows out of
	// context.
	pending *pendingCompaction
	inRun   bool
	// harness is the state of the background-review harness strip on
	// the line being walked.
	harness harnessState
	// used lists the columns the row being converted maps beyond the
	// base ones, and rowKept says its members are written.
	used    []string
	rowKept bool
	// keepContent says the row's text item is not its stored content,
	// which its columns then keep.
	keepContent bool
	// unitOf names the row each item entry came from.
	unitOf map[string]string
	// idents caches the rows' hashed identities.
	idents map[int][32]byte
	// callAliases and outAliases hold the aliases a call entry is named
	// by and an output entry names its call by.
	callAliases, outAliases map[string][]string
	// indexed says a row's display index was reported left out.
	indexed bool
}

// isNullColumn reports a column that is absent or null.
func isNullColumn(cols hermes.Columns, name string) bool {
	v, ok := cols.Get(name)
	return !ok || v == nil
}

type pendingCompaction struct {
	row     *hermes.Message
	summary *hermes.Message
}

func newWalk(sc *sconv, msgs []*hermes.Message) *walk {
	w := &walk{sc: sc, start: map[int]bool{}, summary: map[int]int{}, copied: map[int]bool{}, unitOf: map[string]string{}, idents: map[int][32]byte{}, callAliases: map[string][]string{}, outAliases: map[string][]string{}}
	for _, m := range msgs {
		if m.Err != nil {
			sc.mark(m.Unit, agentimport.LineMalformed)
			sc.addf(agentimport.Repaired, "hermes/malformed", m.Unit, "a message row that cannot be read is skipped: %s", m.Err)
			continue
		}
		w.rows = append(w.rows, m)
		switch {
		case m.Active:
			w.class = append(w.class, live)
		case m.Compacted:
			w.class = append(w.class, folded)
		default:
			w.class = append(w.class, inactive)
		}
	}
	return w
}

// isSummary reports a compaction summary row: one flagged
// _compressed_summary, or one whose content is a summary, as
// _is_context_summary_message tells it; a row written before the column
// existed reads 0 in it.
func isSummary(m *hermes.Message) bool {
	if m.Role != hermes.RoleUser && m.Role != hermes.RoleAssistant {
		return false
	}
	return m.Summary || isSummaryText(m.Content.String())
}

// identity is what tells a row Hermes copied from the row it copied:
// role, content, call ID and calls (SessionMessagesMixin._row_identity).
func identity(m *hermes.Message) string {
	content := m.Content.Text
	if m.Content.Structured {
		content = string(rawJSON(m.Content.Parts))
	}
	return m.Role + "\x00" + content + "\x00" + m.ToolCallID + "\x00" + m.Cols.String("tool_calls")
}

// generations finds where each generation of in-place compaction
// starts. Hermes archives every live row when it compacts in place
// (archive_and_compact) and writes the compacted context after them as
// new rows: the head it protects, copied, the summary, and the tail,
// copied. The last generation is told by the flags, the first live row
// after the last compacted one; an earlier one by its summary row, less
// the rows before it that copy the head of the generation before. A
// continuation's or a branch's first summary is the one its parent's
// compaction wrote, part of its first generation.
func (w *walk) generations() {
	var main []int
	for i, c := range w.class {
		if c != inactive {
			main = append(main, i)
		}
	}
	lastFolded := -1
	for k, i := range main {
		if w.class[i] == folded {
			lastFolded = k
		}
	}
	if lastFolded < 0 {
		if s := w.firstSummary(main, 0); s >= 0 && w.sc.rec.rel == hermes.Continuation && w.inherited(main, s) {
			w.copies(main, 0, s)
		}
		return
	}
	final := -1
	for k := lastFolded + 1; k < len(main); k++ {
		if w.class[main[k]] == live {
			final = k
			break
		}
	}
	prev := 0
	end := len(main)
	if final >= 0 {
		end = final
	}
	firstSeen := false
	for k := 0; k < end; k++ {
		if !isSummary(w.rows[main[k]]) {
			continue
		}
		if !firstSeen {
			firstSeen = true
			if w.inherited(main, k) {
				if w.sc.rec.rel == hermes.Continuation {
					w.copies(main, prev, k)
				}
				continue
			}
		}
		h := w.head(main, prev, k)
		if k-h <= prev {
			continue
		}
		w.start[main[k-h]] = true
		w.summary[main[k-h]] = main[k]
		w.copies(main, k-h, k)
		prev = k - h
	}
	if final >= 0 {
		w.start[main[final]] = true
		s := w.firstSummary(main, final)
		w.summary[main[final]] = -1
		if s >= 0 {
			w.summary[main[final]] = main[s]
		}
		w.copies(main, final, s)
	} else if s := w.firstSummary(main, 0); !firstSeen && s >= 0 && w.sc.rec.rel == hermes.Continuation && w.inherited(main, s) {
		w.copies(main, 0, s)
	}
}

// inherited reports whether the summary at main[k], the session's
// first, is one its parent's compaction wrote rather than its own: in
// a branch, a row of the prefix it copied; in a continuation, a summary
// of the handoff, which every row before it copies, each stamped
// before the child started (_carry_parent_timestamps).
func (w *walk) inherited(main []int, k int) bool {
	switch w.sc.rec.rel {
	case hermes.Branch:
		return w.copied[main[k]]
	case hermes.Continuation:
		start := w.sc.rec.s.Started
		if start.IsZero() {
			return true
		}
		for _, i := range main[:k] {
			if t := w.rows[i].Time; !t.IsZero() && t.After(start) {
				return false
			}
		}
		return true
	}
	return false
}

// firstSummary is the position in main of the first summary row from
// from on, -1 for none.
func (w *walk) firstSummary(main []int, from int) int {
	for k := from; k < len(main); k++ {
		if isSummary(w.rows[main[k]]) {
			return k
		}
	}
	return -1
}

// maxHead bounds the head a compaction protects: the system prompt and
// protect_first_n rows, three by default, which decays to none after
// the first compaction (_protect_head_size).
const maxHead = 16

// head counts the rows just before the summary at main[s] that copy,
// in order, the rows the generation before starts with, at main[prev].
func (w *walk) head(main []int, prev, s int) int {
	for h := min((s-prev)/2, maxHead); h > 0; h-- {
		ok := true
		for j := 0; j < h; j++ {
			if w.ident(main[s-h+j]) != w.ident(main[prev+j]) {
				ok = false
				break
			}
		}
		if ok {
			return h
		}
	}
	return 0
}

// ident is a row's identity, hashed once.
func (w *walk) ident(i int) [32]byte {
	if h, ok := w.idents[i]; ok {
		return h
	}
	h := sha256.Sum256([]byte(identity(w.rows[i])))
	w.idents[i] = h
	return h
}

// copies marks the rows of a generation that Hermes copied: those from
// its start up to its summary, the head, and those after the summary
// stamped earlier than it, the tail, whose rows keep the time of the
// rows they copy (_carry_parent_timestamps). s is the summary's
// position in main, -1 for none, when nothing is marked.
func (w *walk) copies(main []int, from, s int) {
	if s < 0 {
		return
	}
	for k := from; k < s; k++ {
		w.copied[main[k]] = true
	}
	sum := w.rows[main[s]]
	for k := s + 1; k < len(main); k++ {
		m := w.rows[main[k]]
		if sum.Time.IsZero() || m.Time.IsZero() || !m.Time.Before(sum.Time) {
			break
		}
		w.copied[main[k]] = true
	}
}

// branchCopies marks the rows a branch copied from its parent: the
// rows from its first that match, in order, rows of the parent, by
// identity and time, as _clone_message_rows copies them.
func (w *walk) branchCopies() (int, error) {
	sc := w.sc
	if sc.rec.rel != hermes.Branch || sc.rec.parent == nil {
		return 0, nil
	}
	pmsgs, err := sc.fam.peek(sc.rec.parent)
	if err != nil {
		return 0, err
	}
	// The rows the branch copied were the parent's live rows then, a
	// subsequence of its rows in id order whatever their flags are now:
	// the parent may have compacted or rewound since.
	n := 0
	for _, b := range pmsgs {
		if n >= len(w.rows) {
			break
		}
		a := w.rows[n]
		if b.Err != nil || identity(a) != identity(b) || !a.Time.Equal(b.Time) {
			continue
		}
		w.copied[n] = true
		n++
	}
	return n, nil
}

// run walks the rows.
func (w *walk) run() error {
	sc := w.sc
	nb, err := w.branchCopies()
	if err != nil {
		return err
	}
	w.generations()
	if nb > 0 {
		sc.addf(agentimport.Lossy, "hermes/branch-copy", "", "the branch starts with %d rows Hermes copied from %s; they are this session's own entries, their responses marked %s:copied, and parent_session and a fork_of link name the origin", nb, sc.rec.parent.s.ID, Source)
	}
	w.mainAt = sc.at
	inRun := false
	var runAt string
	var saved *pairing
	var savedHarness harnessState
	runs := 0
	for i, m := range w.rows {
		w.inRun = w.class[i] == inactive
		if w.class[i] == inactive {
			if !inRun {
				inRun, runAt, saved = true, w.mainAt, sc.pairing.clone()
				savedHarness, w.harness = w.harness, harnessState{}
				runs++
			}
			sc.at = runAt
			if err := w.row(i); err != nil {
				return err
			}
			runAt = sc.at
			continue
		}
		if inRun {
			inRun = false
			sc.pairing = saved
			w.harness = savedHarness
		}
		sc.at = w.mainAt
		if w.start[i] {
			w.pending = &pendingCompaction{row: m}
			if s := w.summary[i]; s >= 0 {
				w.pending.summary = w.rows[s]
			}
		}
		if err := w.row(i); err != nil {
			return err
		}
		w.mainAt = sc.at
	}
	if inRun {
		sc.pairing = saved
	}
	w.inRun = false
	sc.at = w.mainAt
	if w.pending != nil {
		sc.addf(agentimport.Lossy, "hermes/compaction-empty", w.pending.row.Unit, "a compaction's rows hold no item to start its context with; the rows before it stay in context")
		w.pending = nil
	}
	if runs > 0 {
		sc.addf(agentimport.Lossy, "hermes/inactive-rows", "", "%d runs of rows Hermes took out of context (a rewind, an undo, or the originals of rows a compaction carried) are each a branch from the row before them; rows of two rewinds in a row read as one branch", runs)
	}
	return w.repair()
}

// row converts one row.
func (w *walk) row(i int) error {
	m := w.rows[i]
	sc := w.sc
	w.used, w.rowKept, w.keepContent = nil, false, false
	for _, p := range m.Problems {
		sc.addf(agentimport.Repaired, "hermes/mistyped", m.Unit, "column %s does not hold what Hermes writes; it is kept as written and read as Hermes reads it", p)
	}
	if m.Role != hermes.RoleSessionMeta {
		switch w.harness.next(m) {
		case harnessPrompt:
			return sc.custom(m.Time, m.Unit, "hermes/review-harness", "a background-review harness prompt an older Hermes wrote into the session (a resume strips it)", rowData(m))
		case harnessReply:
			return sc.custom(m.Time, m.Unit, "hermes/review-harness", "the curator's reply to a background-review harness prompt (a resume strips it with the prompt)", rowData(m))
		}
	}
	switch m.Role {
	case hermes.RoleUser:
		return w.user(i)
	case hermes.RoleAssistant:
		return w.assistant(i)
	case hermes.RoleTool:
		return w.tool(i)
	case hermes.RoleSystem:
		return w.system(i)
	case hermes.RoleSessionMeta:
		return sc.custom(m.Time, m.Unit, "hermes/session-meta", "the gateway's session_meta row, bookkeeping", rowData(m))
	}
	return sc.custom(m.Time, m.Unit, "hermes/unknown-role", fmt.Sprintf("a row of role %q, one Hermes' readers do not send", m.Role), rowData(m))
}

// rowData is a row as a custom entry holds it.
func rowData(m *hermes.Message) json.RawMessage { return m.Cols.JSON() }

// baseColumns are the columns every row maps, or Hermes derives for
// its display.
var baseColumns = []string{"id", "session_id", "role", "content", "timestamp", "active", "compacted", "display_order", "display_identity"}

// rowMembers keeps on e the row's ID and its columns no entry maps.
func (w *walk) rowMembers(e agentsession.Entry, m *hermes.Message, used ...string) error {
	sc := w.sc
	if err := sc.b.Member(e, "row", m.ID); err != nil {
		return err
	}
	drop := append(append([]string(nil), baseColumns...), used...)
	if w.keepContent {
		// The content Hermes stores, which the item's text is not.
		drop = slices.DeleteFunc(drop, func(c string) bool { return c == "content" })
	}
	if !w.indexed && (!isNullColumn(m.Cols, "display_identity") || !isNullColumn(m.Cols, "display_order")) {
		w.indexed = true
		sc.addf(agentimport.Dropped, "hermes/display-index", m.Unit, "display_identity and display_order, the display index Hermes derives from a row's other columns, are not kept")
	}
	rest := m.Cols.Without(drop...)
	var keep hermes.Columns
	for _, c := range rest {
		switch v := c.Value.(type) {
		case nil:
			continue
		case string:
			if v == "" {
				continue
			}
		case int64:
			if v == 0 {
				continue
			}
		}
		keep = append(keep, c)
	}
	if len(keep) == 0 {
		return nil
	}
	return sc.member(e, "columns", keep.JSON(), m.Unit)
}

// item appends an item entry, or, when a compaction is pending, makes
// the item the compaction's summary. It returns the entry that holds
// the item.
func (w *walk) item(it openresponses.Item, m *hermes.Message, respID string, vis *bool, prep func(e *agentsession.ItemEntry) error) (string, *agentsession.ItemEntry, error) {
	sc := w.sc
	if w.pending != nil && !w.inRun {
		switch it.(type) {
		case *openresponses.Message, *openresponses.Compaction:
			id, err := w.compaction(it, m)
			return id, nil, err
		}
		// No summary can be made of a call or an output: the rows
		// before the generation are left out by omit instead.
		if err := w.fold(it, m); err != nil {
			return "", nil, err
		}
	}
	e := agentsession.NewItemEntry(it)
	e.Timestamp = m.Time
	e.ResponseID = respID
	e.Visible = vis
	if prep != nil {
		if err := prep(e); err != nil {
			return "", nil, err
		}
	}
	if !w.rowKept {
		if err := w.rowMembers(e, m, w.used...); err != nil {
			return "", nil, err
		}
		w.rowKept = true
	}
	id, err := sc.append(e)
	w.unitOf[id] = m.Unit
	return id, e, err
}

// compaction writes the pending compaction, its summary the first item
// of the generation it starts. Hermes sends the generation's rows in
// order, its head first when it kept one; the summary is that first
// item, so the context is the one Hermes sends, and a head before the
// summary row is a gap of the session format, which puts a
// compaction's summary first.
func (w *walk) compaction(summary openresponses.Item, m *hermes.Message) (string, error) {
	sc := w.sc
	p := w.pending
	w.pending = nil
	first, err := w.keepNone(m.Time)
	if err != nil {
		return "", err
	}
	ctx, err := sc.b.S.ContextAt(sc.at)
	if err != nil {
		return "", err
	}
	settings := ctx.Settings
	settings.Omit.Items = nil
	e := &agentsession.CompactionEntry{FirstKept: first, Summary: summary, Config: settings}
	e.Timestamp = m.Time
	if p.summary != nil {
		if err := sc.b.Member(e, "summary_row", p.summary.ID); err != nil {
			return "", err
		}
	}
	if err := w.rowMembers(e, m, w.used...); err != nil {
		return "", err
	}
	w.rowKept = true
	id, err := sc.append(e)
	if err != nil {
		return "", err
	}
	switch {
	case p.summary == nil:
		sc.addf(agentimport.Lossy, "hermes/compaction", p.row.Unit, "an in-place compaction: Hermes resumes from the rows it wrote from here on, told by their flags; no summary row is among them, and the first item they hold is the compaction's summary")
	case p.summary == p.row:
		sc.addf(agentimport.Lossy, "hermes/compaction", p.row.Unit, "an in-place compaction: Hermes archived the rows before it and resumes from its summary and the rows after it, which the compaction keeps; the archived rows stay on the path")
	default:
		sc.addf(agentimport.Lossy, "hermes/compaction", p.row.Unit, "an in-place compaction: Hermes archived the rows before it and resumes from the head it protected, copied, the summary and the rows after it")
		sc.addf(agentimport.Gap, "agentimport/fold-head", p.row.Unit, "Hermes' compacted context starts with the head it protected, copied before the summary: the compaction's summary is the head's first item, verbatim, and the summary row follows it")
	}
	return id, nil
}

// fold starts a generation whose first item is no message: every item
// in context is left out by omit, and the item follows as an entry.
func (w *walk) fold(it openresponses.Item, m *hermes.Message) error {
	sc := w.sc
	p := w.pending
	w.pending = nil
	ctx, err := sc.b.S.ContextAt(sc.at)
	if err != nil {
		return err
	}
	var ids []string
	seen := map[string]bool{}
	held := false
	for _, e := range ctx.ItemEntries {
		ie, ok := e.(*agentsession.ItemEntry)
		if !ok {
			held = true
			continue
		}
		if !seen[ie.ID] {
			seen[ie.ID] = true
			ids = append(ids, ie.ID)
		}
	}
	if len(ids) > 0 {
		cfg := &agentsession.ConfigEntry{Omit: &agentsession.Omit{Items: ids}}
		cfg.Timestamp = m.Time
		if _, err := sc.append(cfg); err != nil {
			return err
		}
	}
	what := "an in-place compaction whose first item is a " + it.ItemType() + ", of which no summary can be made: the items before it are left out by omit"
	if held {
		what += "; an earlier compaction's summary, which omit cannot reach, stays in context"
	}
	sc.addf(agentimport.Lossy, "hermes/compaction", p.row.Unit, "%s", what)
	return nil
}

// keepNone returns the entry a compaction that keeps nothing before it
// names as first_kept: the current one when it puts nothing in context,
// else a custom marker written for it.
func (w *walk) keepNone(ts time.Time) (string, error) {
	sc := w.sc
	if e, ok := sc.b.S.Entry(sc.at); ok {
		switch e.(type) {
		case *agentsession.ItemEntry, *agentsession.CompactionEntry, *agentsession.BranchSummaryEntry:
		default:
			return sc.at, nil
		}
	}
	marker := &agentsession.CustomEntry{NS: Source, Data: json.RawMessage(`{"keeps_nothing":true}`)}
	marker.Timestamp = ts
	return sc.append(marker)
}

// visible is false for a row Hermes sends the model and never shows: a
// display_kind of hidden, or display metadata marking it model-only.
func visible(m *hermes.Message) *bool {
	hidden := m.DisplayKind == "hidden"
	var meta struct {
		ModelOnly any `json:"model_only"`
	}
	if json.Unmarshal(m.DisplayMetadata, &meta) == nil {
		switch v := meta.ModelOnly.(type) {
		case bool:
			hidden = hidden || v
		case float64:
			hidden = hidden || v != 0
		}
	}
	if !hidden {
		return nil
	}
	f := false
	return &f
}

// user converts a user row.
func (w *walk) user(i int) error {
	sc := w.sc
	m := w.rows[i]
	sc.pairing.turn()
	used := []string{}
	var msg *openresponses.Message
	if m.Content.Structured && !m.HasAPIContent {
		cs, kept := parts(m.Content.Parts, false)
		if kept > 0 {
			sc.addf(agentimport.Kept, "hermes/content-part", m.Unit, "%d content parts Open Responses does not define are carried as extension parts", kept)
		}
		msg = &openresponses.Message{Role: openresponses.RoleUser, Content: cs}
	} else {
		text, sidecar := sentText(m)
		if sidecar {
			used = append(used, "api_content")
		}
		w.sentDiffers(m, text, sidecar)
		msg = openresponses.UserText(text)
	}
	if isSummary(m) {
		used = append(used, "_compressed_summary")
	}
	w.used = used
	if !m.Content.Structured && msg.Text() == "" && !m.HasAPIContent {
		sc.addf(agentimport.Lossy, "hermes/empty-turn", m.Unit, "an empty user turn: once a message follows it, Hermes sends %q in its place in each request's copy (repair_empty_non_final_messages); the item stays empty", emptyPlaceholder)
	}
	if _, _, err := w.item(msg, m, "", visible(m), nil); err != nil {
		return err
	}
	sc.mark(m.Unit, agentimport.LineMapped)
	if m.Observed {
		sc.addf(agentimport.Lossy, "hermes/observed", m.Unit, "an observed group-chat message; a Telegram channel that gathers observed messages into one block sends them that way, which is not reproduced")
	}
	if did := delegationID(m); did != "" {
		sc.addf(agentimport.Kept, "hermes/delegation-delivery", m.Unit, "a background delegation's result, %s, delivered as a user message", did)
	}
	return nil
}

// delegationID reads the delegation a delivered result belongs to.
func delegationID(m *hermes.Message) string {
	var meta struct {
		DelegationID string `json:"delegation_id"`
	}
	if json.Unmarshal(m.DisplayMetadata, &meta) != nil {
		return ""
	}
	return meta.DelegationID
}

// system converts a system row. A resume sends it where it stands; the
// gateway's replay drops it.
func (w *walk) system(i int) error {
	sc := w.sc
	m := w.rows[i]
	text, sidecar := sentText(m)
	if m.Content.Structured {
		text = strip(m.Content.String())
	} else {
		w.sentDiffers(m, text, sidecar)
	}
	w.used = nil
	if _, _, err := w.item(openresponses.SystemText(text), m, "", nil, nil); err != nil {
		return err
	}
	sc.mark(m.Unit, agentimport.LineMapped)
	sc.addf(agentimport.Lossy, "hermes/system-row", m.Unit, "a system row is in context where it stands, as a resume of the CLI sends it; the gateway's replay leaves it out")
	return nil
}

// harnessState is _strip_background_review_harness's state over the
// rows of one line.
type harnessState struct {
	skip, prev bool
}

const (
	harnessNone = iota
	harnessPrompt
	harnessReply
)

// next takes a row: a harness prompt, the reply right after one, or
// neither. A prompt arms the drop of the next message when that is an
// assistant's, unless it follows another prompt, which holds the slot
// already; any other message disarms it.
func (h *harnessState) next(m *hermes.Message) int {
	if m.Role == hermes.RoleUser || m.Role == hermes.RoleSystem {
		text := m.Content.Text
		if m.Role == hermes.RoleUser {
			// As Hermes loads it: _loaded_view_content.
			text = strip(sanitizeContext(text))
		}
		if !m.Content.Structured && isReviewHarness(m.Role, text) {
			h.skip = !h.prev
			h.prev = true
			return harnessPrompt
		}
	}
	if h.skip {
		h.skip = false
		if m.Role == hermes.RoleAssistant {
			h.prev = false
			return harnessReply
		}
	}
	h.prev = false
	return harnessNone
}

// sentDiffers notes a row whose sent text is not the content Hermes
// stores: api_content sent in its place, or the content as the loader
// and the request's closing pass give it (sanitize_context, strip, a
// stale marker blanked). The stored content is then kept in the row's
// columns.
func (w *walk) sentDiffers(m *hermes.Message, text string, sidecar bool) {
	if text == m.Content.Text {
		return
	}
	w.keepContent = true
	if sidecar {
		if m.Content.Text != m.APIContent {
			w.sc.addf(agentimport.Lossy, "hermes/api-content", m.Unit, "the row's api_content, what Hermes sent, is the item; the content it shows is kept in the row's columns")
		}
		return
	}
	w.sc.addf(agentimport.Lossy, "hermes/sanitized-content", m.Unit, "the item is the text Hermes sends, the stored content as its loader and the request's closing pass give it (memory blocks taken out, white space stripped, a stale marker blanked); the stored content is kept in the row's columns")
}
