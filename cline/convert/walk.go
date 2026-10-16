package convert

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/cline"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// Unit kinds: the elements of the task's arrays, and its single files.
const (
	uAPI      = "api"
	uUI       = "ui"
	uContext  = "context"
	uMetadata = "metadata"
	uSettings = "settings"
	uHistory  = "history"
	uOther    = "other"
)

// unit is one unit of the task: an element of an array file (i is its
// index, -1 for the file's own fault), a single file (i is 0), or an
// other file (i is its index in Task.Others).
type unit struct {
	kind string
	i    int
}

// conv converts one task.
type conv struct {
	fam    *family
	t      *cline.Task
	tgt    target
	prefix string
	depth  int
	b      *agentimport.Builder
	report *agentimport.Report
	roo    bool

	tail    string
	last    time.Time
	created time.Time
	disp    map[unit]agentimport.Disposition

	// Per API element: the items it put in context, in order, its first
	// entry, and its output entries.
	items   [][]openresponses.Item
	first   []string
	outputs [][]string
	// special marks a Roo summary or truncation marker, written into
	// the compaction its event makes rather than where it stands.
	special      map[int]bool
	specialItems map[int][]openresponses.Item

	// pending holds the calls of the last assistant message, by native
	// ID, and calls the same in order; pendingFrom is that message.
	pending     map[string]*call
	calls       []*call
	pendingFrom int
	model       string
	respIDs     map[string]bool
	compacts    int

	events []*event
	side   []*sideRecord
	sideAt int
	reqs   map[int]*uiReq
	// failed marks the request records that answered no response.
	failed  map[int]bool
	edits   map[[2]int][]*cline.ContextUpdate
	nextEvt int
	// cut is, for classic, the element whose tool results the range in
	// force strips from requests, the first after it; -1 for none.
	cut     int
	cutDone bool
	// omitted maps each entry a config entry omits to its element, so
	// a compaction carries only those it keeps.
	omitted map[string]int

	children [][]*agentsession.Session
	childIDs map[string]bool
}

// call is a function call an assistant message made.
type call struct {
	native string
	id     string
	name   string
	entry  string
	output bool
}

func newConv(fam *family, t *cline.Task, tgt target, prefix string, depth int, report *agentimport.Report) *conv {
	n := len(t.API.Elems)
	return &conv{
		fam: fam, t: t, tgt: tgt, prefix: prefix, depth: depth, report: report,
		roo:          t.Variant == cline.Roo,
		disp:         map[unit]agentimport.Disposition{},
		items:        make([][]openresponses.Item, n),
		first:        make([]string, n),
		outputs:      make([][]string, n),
		special:      map[int]bool{},
		specialItems: map[int][]openresponses.Item{},
		pending:      map[string]*call{},
		pendingFrom:  -2,
		respIDs:      map[string]bool{},
		reqs:         map[int]*uiReq{},
		failed:       map[int]bool{},
		edits:        map[[2]int][]*cline.ContextUpdate{},
		cut:          -1,
		omitted:      map[string]int{},
		childIDs:     map[string]bool{},
	}
}

// addf records a finding against a file of the task, at an element's
// 1-based position, or 0.
func (c *conv) addf(kind agentimport.Kind, code, file string, line int, format string, args ...any) {
	if file != "" {
		file = c.prefix + file
	} else if c.prefix != "" {
		file = strings.TrimSuffix(c.prefix, "/")
	}
	c.report.Addf(kind, code, file, line, format, args...)
}

// apiAt records a finding against API element k.
func (c *conv) apiAt(kind agentimport.Kind, code string, k int, format string, args ...any) {
	c.addf(kind, code, c.t.API.Name, k+1, format, args...)
}

// uiAt records a finding against UI element i.
func (c *conv) uiAt(kind agentimport.Kind, code string, i int, format string, args ...any) {
	c.addf(kind, code, c.t.UI.Name, i+1, format, args...)
}

// mark records where a unit ended up; the first mark stands.
func (c *conv) mark(u unit, d agentimport.Disposition) {
	if _, ok := c.disp[u]; !ok {
		c.disp[u] = d
	}
}

// keeping says what Slim makes of something kept: the kind to report
// and the verb.
func (c *conv) keeping() (agentimport.Kind, agentimport.Disposition) {
	if c.fam.opts.Slim {
		return agentimport.Dropped, agentimport.LineDropped
	}
	return agentimport.Kept, agentimport.LineKept
}

// clean makes raw I-JSON, reporting what it changed.
func (c *conv) clean(file string, line int, raw json.RawMessage) (json.RawMessage, error) {
	if agentimport.RepeatedMembers(raw) {
		c.addf(agentimport.Repaired, "agentimport/repeated-member", file, line, "an object named a member twice, which I-JSON forbids; the last is kept")
	}
	if agentimport.InvalidText(raw) {
		c.addf(agentimport.Repaired, "agentimport/invalid-text", file, line, "text that was not valid Unicode was replaced with U+FFFD")
	}
	out, _, err := agentimport.CleanJSON(raw)
	return out, err
}

// appendEntry appends e after the tail, with the last timestamp when it
// has none.
func (c *conv) appendEntry(e agentsession.Entry) (string, error) {
	base := e.Base()
	if base.Timestamp.IsZero() {
		base.Timestamp = c.last
		if base.Timestamp.IsZero() {
			base.Timestamp = c.created
		}
	}
	id, err := c.b.Append(c.tail, e)
	if err != nil {
		return "", err
	}
	c.tail = id
	if base.Timestamp.After(c.last) {
		c.last = base.Timestamp
	}
	return id, nil
}

// run converts the task and accounts for every unit.
func (c *conv) run() error {
	h, err := c.header()
	if err != nil {
		return err
	}
	c.b = agentimport.NewBuilder(h, Source, c.report)
	c.b.File = strings.TrimSuffix(c.prefix, "/")
	c.files()
	if err := c.root(); err != nil {
		return err
	}
	c.prepare()
	n := len(c.t.API.Elems)
	for k := 0; k <= n; k++ {
		if err := c.flushSide(k); err != nil {
			return err
		}
		if err := c.applyEvents(k); err != nil {
			return err
		}
		if k == n {
			break
		}
		if err := c.element(k); err != nil {
			return err
		}
	}
	if err := c.flushSide(n + 1); err != nil {
		return err
	}
	c.unanswered()
	c.tally()
	return nil
}

// files accounts for the units that are files: a fault reading an
// array, the metadata, settings and index entry, and the files the
// converter does not read.
func (c *conv) files() {
	t := c.t
	if t.API.Err != nil {
		c.addf(agentimport.Repaired, "cline/malformed-file", t.API.Name, 0, "%s; the %d elements before the fault are read", t.API.Err, len(t.API.Elems))
		c.mark(unit{uAPI, -1}, agentimport.LineMalformed)
	}
	if t.UI.Err != nil {
		c.addf(agentimport.Repaired, "cline/malformed-file", t.UI.Name, 0, "%s; the %d elements before the fault are read", t.UI.Err, len(t.UI.Elems))
		c.mark(unit{uUI, -1}, agentimport.LineMalformed)
	}
	if t.Context.Err != nil {
		c.addf(agentimport.Repaired, "cline/malformed-file", t.Context.Name, 0, "%s; the %d updates before the fault are read", t.Context.Err, len(t.Context.Elems))
		c.mark(unit{uContext, -1}, agentimport.LineMalformed)
	}
	if !t.API.Present() {
		c.addf(agentimport.Lossy, "cline/no-api-history", "", 0, "the task has no %s; the session holds what the UI messages record alone, which is not the model's context", cline.FileAPI)
	}
	if t.API.Name == cline.FileLegacy || t.UI.Name == cline.FileLegacy {
		c.addf(agentimport.Lossy, "cline/legacy-file-name", cline.FileLegacy, 0, "the file has the name the earliest releases gave it; it is read as %s", map[bool]string{true: "the API history", false: "the UI messages"}[t.API.Name == cline.FileLegacy])
	}
	for i, name := range t.Others {
		c.addf(agentimport.Dropped, "cline/file-unread", name, 0, "a file of the task directory the converter does not read")
		c.mark(unit{uOther, i}, agentimport.LineDropped)
	}
	for _, d := range t.Dirs {
		if d == "checkpoints" {
			c.addf(agentimport.Dropped, "cline/checkpoints", d, 0, "the task's checkpoint repository is not read; the UI records name its commits")
		}
	}
	if t.HasMetadata && t.MetadataErr != nil {
		c.addf(agentimport.Repaired, "cline/malformed-file", cline.FileMetadata, 0, "%s; it is skipped", t.MetadataErr)
		c.mark(unit{uMetadata, 0}, agentimport.LineMalformed)
	}
	if t.HasSettings && t.SettingsErr != nil {
		c.addf(agentimport.Repaired, "cline/malformed-file", cline.FileSettings, 0, "%s; it is skipped", t.SettingsErr)
		c.mark(unit{uSettings, 0}, agentimport.LineMalformed)
	}
	if t.HistoryUnit && t.HistoryErr != nil {
		c.addf(agentimport.Repaired, "cline/malformed-file", t.HistoryFile, 0, "the task's index entry: %s; it is skipped", t.HistoryErr)
		c.mark(unit{uHistory, 0}, agentimport.LineMalformed)
	}
}

// tally counts every unit of the task by where it ended up. A unit the
// conversion did not reach is dropped and reported.
func (c *conv) tally() {
	t := c.t
	count := func(u unit, file string, line int) {
		d, ok := c.disp[u]
		if !ok {
			d = agentimport.LineDropped
			c.addf(agentimport.Dropped, "cline/unreached", file, line, "the converter did not reach this unit; a bug if it occurs")
		}
		c.report.Line(d)
	}
	for k := range t.API.Elems {
		count(unit{uAPI, k}, t.API.Name, k+1)
	}
	if t.API.Err != nil {
		count(unit{uAPI, -1}, t.API.Name, 0)
	}
	for i := range t.UI.Elems {
		count(unit{uUI, i}, t.UI.Name, i+1)
	}
	if t.UI.Err != nil {
		count(unit{uUI, -1}, t.UI.Name, 0)
	}
	for i := range t.Context.Elems {
		count(unit{uContext, i}, t.Context.Name, i+1)
	}
	if t.Context.Err != nil {
		count(unit{uContext, -1}, t.Context.Name, 0)
	}
	if t.HasMetadata {
		count(unit{uMetadata, 0}, cline.FileMetadata, 0)
	}
	if t.HasSettings {
		count(unit{uSettings, 0}, cline.FileSettings, 0)
	}
	if t.HistoryUnit {
		count(unit{uHistory, 0}, t.HistoryFile, 0)
	}
	for i, name := range t.Others {
		count(unit{uOther, i}, name, 0)
	}
}

// header builds the session header: the earliest timestamp, the
// working directory and the task's index entry, the variant, and the
// files the model never saw, kept as members.
func (c *conv) header() (agentsession.Header, error) {
	t := c.t
	h := agentsession.Header{ID: c.tgt.id, ParentSession: c.tgt.parent, SpawnedBy: c.tgt.spawnedBy}
	if hi := t.History; c.tgt.parent == "" && hi != nil && hi.ParentTaskID != "" {
		if storeName(hi.ParentTaskID) {
			h.ParentSession = hi.ParentTaskID
		}
		c.addf(agentimport.Lossy, "cline/roo-subtask-alone", t.HistoryFile, 0,
			"the task is a sub-task of %s, converted on its own: parent_session names the parent task, and no link in the parent's session names this one", hi.ParentTaskID)
	}
	var created time.Time
	consider := func(ts time.Time, ok bool) {
		if ok && (created.IsZero() || ts.Before(created)) {
			created = ts
		}
	}
	for _, m := range t.API.Elems {
		if m.Err == nil {
			consider(m.Time())
		}
	}
	for _, m := range t.UI.Elems {
		if m.Err == nil {
			consider(m.Time())
		}
	}
	if created.IsZero() && t.History != nil {
		consider(cline.Millis(t.History.TS))
	}
	if created.IsZero() {
		created = time.Unix(0, 0).UTC()
		if !c.fam.opts.CreatedAt.IsZero() {
			created = c.fam.opts.CreatedAt
		}
		c.addf(agentimport.Lossy, "cline/no-timestamp", "", 0, "the task holds no timestamp; created_at is %s", created.Format(time.RFC3339))
	}
	h.CreatedAt = created
	c.created = created
	if hi := t.History; hi != nil {
		switch {
		case c.roo:
			h.CWD = hi.Workspace
		case hi.CWDOnTaskInitialization != "":
			h.CWD = hi.CWDOnTaskInitialization
		default:
			h.CWD = hi.ShadowGitConfigWorkTree
		}
	}
	if c.roo {
		h.Harness = &agentsession.Harness{Name: HarnessRoo}
	} else {
		h.Harness = &agentsession.Harness{Name: HarnessClassic, Version: c.clineVersion()}
	}
	src := agentimport.Source{Format: Source}
	if c.tgt.id != t.ID {
		src.NativeID = t.ID
	}
	if err := agentimport.SetSource(&h, src); err != nil {
		return h, err
	}
	c.addf(agentimport.Gap, "agentimport/provenance", "", 0, "the import is recorded in the header member %s", agentimport.SourceMember)
	if c.tgt.derived != "" {
		c.addf(agentimport.Lossy, "cline/session-id", "", 0, "%s", c.tgt.derived)
	}
	era := c.toolEra()
	variant := map[string]any{"variant": t.Variant, "by": t.VariantBy, "tool_calls": era}
	if err := c.headerMember(&h, "variant", variant); err != nil {
		return h, err
	}
	switch era {
	case "xml", "mixed":
		c.addf(agentimport.Lossy, "cline/tool-era", "", 0, "the task's tool calls are %s, told by shape: no version is recorded; XML calls stay the text the model wrote", map[string]string{"xml": "XML in the assistant's text", "mixed": "XML in some responses and native tool_use in others"}[era])
	}
	if t.Guessed {
		c.addf(agentimport.Lossy, "cline/variant-guessed", "", 0, "no storage directory names the agent; %s by %s", t.Variant, t.VariantBy)
	}
	if hi := t.History; hi != nil {
		c.mark(unit{uHistory, 0}, agentimport.LineMapped)
		if c.fam.opts.Slim {
			c.addf(agentimport.Dropped, "cline/history-item", t.HistoryFile, 0, "the task's index entry is left out beyond the working directory, name and ranges it gives")
		} else {
			raw, err := c.clean(t.HistoryFile, 0, hi.Raw)
			if err != nil {
				return h, err
			}
			h.Extra[Source+":history_item"] = raw
			c.addf(agentimport.Kept, "cline/history-item", t.HistoryFile, 0, "the task's index entry is the header member %s:history_item", Source)
		}
	} else {
		c.addf(agentimport.Lossy, "cline/missing-index", "", 0, "%s", c.missingIndex())
	}
	if t.HasMetadata && t.MetadataErr == nil {
		if err := c.keepFile(&h, uMetadata, cline.FileMetadata, "task_metadata", t.MetadataRaw, "the task's metadata (files in context, model switches, environments)"); err != nil {
			return h, err
		}
	}
	if t.HasSettings && t.SettingsErr == nil {
		if err := c.keepFile(&h, uSettings, cline.FileSettings, "settings", t.Settings, "the task's settings snapshot"); err != nil {
			return h, err
		}
	}
	return h, nil
}

// toolEra tells how the task's responses call tools, by shape: "native"
// tool_use blocks, "xml" in the text, "mixed", or "none".
func (c *conv) toolEra() string {
	native, xml := false, false
	for _, m := range c.t.API.Elems {
		if m.Err != nil || m.Role != cline.RoleAssistant {
			continue
		}
		if len(m.ToolUses()) > 0 {
			native = true
		}
		if m.Content.IsText && len(cline.ParseXMLTools(m.Content.Text)) > 0 {
			xml = true
		}
		for _, b := range m.Content.Blocks {
			if b.Err == nil && b.Type == cline.BlockText && len(cline.ParseXMLTools(b.Text)) > 0 {
				xml = true
			}
		}
	}
	switch {
	case native && xml:
		return "mixed"
	case native:
		return "native"
	case xml:
		return "xml"
	}
	return "none"
}

// missingIndex says what a task with no index entry lacks.
func (c *conv) missingIndex() string {
	if c.roo {
		return "the task has no " + cline.FileHistoryItem + ", which older Roo releases kept in VS Code's state database; the working directory, mode and provider profile are unknown, and sub-tasks cannot be linked"
	}
	return "the task has no entry in state/taskHistory.json, which older releases kept in VS Code's state database; the working directory is unknown and the deleted range is taken from the UI messages alone"
}

// clineVersion is the Cline version the task's metadata records first.
func (c *conv) clineVersion() string {
	if m := c.t.Metadata; m != nil {
		for _, e := range m.EnvironmentHistory {
			if e != nil && e.ClineVersion != "" {
				return e.ClineVersion
			}
		}
	}
	return ""
}

// keepFile keeps a file the model never saw as a header member.
func (c *conv) keepFile(h *agentsession.Header, kind, file, member string, raw json.RawMessage, what string) error {
	k, d := c.keeping()
	c.mark(unit{kind, 0}, d)
	if c.fam.opts.Slim {
		c.addf(k, "cline/task-file", file, 0, "%s is left out", what)
		return nil
	}
	clean, err := c.clean(file, 0, raw)
	if err != nil {
		return err
	}
	h.Extra[Source+":"+member] = clean
	c.addf(k, "cline/task-file", file, 0, "%s is the header member %s:%s", what, Source, member)
	return nil
}

// headerMember sets a header member.
func (c *conv) headerMember(h *agentsession.Header, name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if h.Extra == nil {
		h.Extra = map[string]json.RawMessage{}
	}
	h.Extra[Source+":"+name] = raw
	return nil
}

// root writes the session's first entries: the settings, with the
// model of the first response, and the task's name.
func (c *conv) root() error {
	cfg := &agentsession.ConfigEntry{}
	cfg.Timestamp = c.created
	if !c.roo {
		c.model = c.firstModel()
		cfg.Model = c.model
		if c.model == "" {
			c.addf(agentimport.Lossy, "cline/no-model", "", 0, "no message, index entry or metadata names the model; the settings have none")
		}
	} else {
		c.addf(agentimport.Lossy, "cline/roo-no-model", "", 0, "Roo records no model ID; the settings have none, and the provider profile and mode are members of the first config entry")
		if hi := c.t.History; hi != nil {
			if err := c.b.Member(cfg, "api_config_name", hi.APIConfigName); err != nil {
				return err
			}
			if err := c.b.Member(cfg, "mode", hi.Mode); err != nil {
				return err
			}
		}
	}
	if _, err := c.appendEntry(cfg); err != nil {
		return err
	}
	if name := c.taskName(); name != "" {
		info := &agentsession.InfoEntry{Name: name}
		info.Timestamp = c.created
		if _, err := c.appendEntry(info); err != nil {
			return err
		}
	}
	return nil
}

// firstModel is the model of the first response, else the one the
// index entry or the metadata names.
func (c *conv) firstModel() string {
	for _, m := range c.t.API.Elems {
		if m.Err == nil && m.Role == cline.RoleAssistant && m.ModelInfo != nil && m.ModelInfo.ModelID != "" {
			return m.ModelInfo.ModelID
		}
	}
	if hi := c.t.History; hi != nil && hi.ModelID != "" {
		return hi.ModelID
	}
	if md := c.t.Metadata; md != nil {
		for _, u := range md.ModelUsage {
			if u != nil && u.ModelID != "" {
				return u.ModelID
			}
		}
	}
	return ""
}

// taskName is the task's first line, from its index entry or its first
// UI message, at most 200 characters.
func (c *conv) taskName() string {
	text := ""
	if hi := c.t.History; hi != nil {
		text = hi.Task
	}
	if text == "" {
		for _, m := range c.t.UI.Elems {
			if m.Err == nil && m.Type == "say" && m.Say == "task" {
				text = m.Text
				break
			}
		}
	}
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	if r := []rune(text); len(r) > 200 {
		text = string(r[:200])
	}
	return text
}

// element converts API element k.
func (c *conv) element(k int) error {
	m := c.t.API.Elems[k]
	u := unit{uAPI, k}
	switch {
	case m.Err != nil:
		c.apiAt(agentimport.Repaired, "cline/malformed-element", k, "%s; it is skipped", agentimport.ErrorText(m.Err))
		c.mark(u, agentimport.LineMalformed)
		return nil
	case c.special[k]:
		// Written by its event's compaction.
		return nil
	case m.IsStandaloneReasoning():
		return c.standaloneReasoning(k, m)
	case m.Role == cline.RoleUser:
		return c.user(k, m)
	case m.Role == cline.RoleAssistant:
		return c.assistant(k, m)
	}
	_, err := c.customAPI(k, m, "cline/unknown-role", fmt.Sprintf("an element with role %q is a custom entry", m.Role))
	return err
}

// customAPI keeps API element k as a custom entry.
func (c *conv) customAPI(k int, m *cline.Message, code, detail string) (string, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	if raw, err = c.clean(c.t.API.Name, k+1, raw); err != nil {
		return "", err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"api_message": raw})
	if err != nil {
		return "", err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	e.Timestamp, _ = m.Time()
	id, err := c.appendEntry(e)
	if err != nil {
		return "", err
	}
	c.apiAt(agentimport.Kept, code, k, "%s", detail)
	c.mark(unit{uAPI, k}, agentimport.LineKept)
	return id, nil
}

// sideRecord is a record placed between the API elements: a UI message
// or a context edit, after element pos.
type sideRecord struct {
	pos  int
	ts   time.Time
	ui   int
	edit int
}

// prepare sorts out, before the walk, what goes where: the Roo
// summaries and markers and every compaction, the requests' usage, and
// the UI records and context edits kept between the API elements.
func (c *conv) prepare() {
	c.matchRequests()
	c.prepareEvents()
	c.prepareSide()
}

// posByTime is the index of the last API element written at or before
// ts, -1 when none was.
func (c *conv) posByTime(ts time.Time) int {
	pos := -1
	for k, m := range c.t.API.Elems {
		if m.Err != nil {
			continue
		}
		if mt, ok := m.Time(); ok {
			if mt.After(ts) {
				break
			}
			pos = k
		}
	}
	return pos
}

// prepareSide classifies the UI messages the requests and events did
// not take, and the context edits, and places each after an API
// element.
func (c *conv) prepareSide() {
	n := len(c.t.API.Elems)
	for i, m := range c.t.UI.Elems {
		u := unit{uUI, i}
		if _, done := c.disp[u]; done {
			continue
		}
		if m.Err != nil {
			c.uiAt(agentimport.Repaired, "cline/malformed-element", i, "%s; it is skipped", agentimport.ErrorText(m.Err))
			c.mark(u, agentimport.LineMalformed)
			continue
		}
		if m.Partial {
			c.uiAt(agentimport.Dropped, "cline/ui-partial", i, "a UI message still streaming when the file was written is left out")
			c.mark(u, agentimport.LineDropped)
			continue
		}
		if restated(m) && cline.IsNull(m.Extra["lastCheckpointHash"]) && cline.IsNull(m.Extra["checkpoint"]) {
			c.uiAt(agentimport.Dropped, "cline/ui-restated", i, "a %s %s UI message restates the API history, or is a prompt carrying nothing", m.Type, m.Kind())
			c.mark(u, agentimport.LineDropped)
			continue
		}
		ts, _ := m.Time()
		pos := -2
		if !c.roo {
			if hi, ok := m.HistoryIndex(); ok {
				pos = min(hi, n-1)
			}
		}
		if pos == -2 {
			pos = c.posByTime(ts)
		}
		c.side = append(c.side, &sideRecord{pos: pos, ts: ts, ui: i, edit: -1})
	}
	for i, e := range c.t.Context.Elems {
		if e.Err != nil {
			c.addf(agentimport.Repaired, "cline/malformed-element", c.t.Context.Name, i+1, "%s; it is skipped", agentimport.ErrorText(e.Err))
			c.mark(unit{uContext, i}, agentimport.LineMalformed)
			continue
		}
		key := [2]int{e.Message, e.Block}
		c.edits[key] = append(c.edits[key], e)
		ts, _ := e.Time()
		c.side = append(c.side, &sideRecord{pos: c.posByTime(ts), ts: ts, ui: -1, edit: i})
	}
	sort.SliceStable(c.side, func(a, b int) bool { return c.side[a].pos < c.side[b].pos })
}

// restated reports whether a UI message carries nothing the API
// history does not: the chat's rendering of a message, a tool or a
// command, or a prompt with no content.
func restated(m *cline.UIMessage) bool {
	if m.Type == "ask" {
		switch m.Ask {
		case "followup", "plan_mode_respond", "act_mode_respond", "command", "command_output",
			"completion_result", "tool", "browser_action_launch", "use_mcp_server", "new_task",
			"condense", "summarize_task", "report_bug", "use_subagents", "resume_task",
			"resume_completed_task":
			return true
		}
		return false
	}
	switch m.Say {
	case "task", "text", "reasoning", "completion_result", "user_feedback", "command",
		"command_output", "tool", "browser_action", "browser_action_launch",
		"browser_action_result", "mcp_server_request_started", "mcp_server_response",
		"use_mcp_server", "subtask_result", "codebase_search_result", "api_req_finished":
		return true
	}
	return false
}

// flushSide writes the side records placed before element k.
func (c *conv) flushSide(k int) error {
	for c.sideAt < len(c.side) && c.side[c.sideAt].pos < k {
		r := c.side[c.sideAt]
		c.sideAt++
		var err error
		if r.ui >= 0 {
			err = c.uiRecord(r.ui)
		} else {
			err = c.contextEdit(r.edit)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// uiRecord keeps a UI message the API history does not restate as a
// custom entry.
func (c *conv) uiRecord(i int) error {
	m := c.t.UI.Elems[i]
	kind, d := c.keeping()
	note := ""
	switch m.Kind() {
	case "deleted_api_reqs", "api_req_deleted":
		c.uiAt(agentimport.Lossy, "cline/rewound", i, "the user restored a checkpoint here: the API and UI messages after it were deleted from the files, and only their usage is recorded; nothing of them can be recovered")
	case "use_subagents", "subagent", "subagent_usage":
		note = "; the sub-agents ran in memory and wrote no transcript"
	case "command_permission_denied", "clineignore_error", "rooignore_error":
		note = "; the refusal is a decision on the call when the result names it"
	}
	paid := c.paidUsage(i, m)
	fields := map[string]json.RawMessage{}
	if c.fam.opts.Slim {
		c.uiAt(kind, "cline/ui-record", i, "a %s %s UI message is left out%s", m.Type, m.Kind(), note)
		if paid == nil {
			c.mark(unit{uUI, i}, d)
			return nil
		}
		// The usage stays, so the path's cost does.
		kind, d = agentimport.Kept, agentimport.LineKept
	} else {
		raw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		if raw, err = c.clean(c.t.UI.Name, i+1, raw); err != nil {
			return err
		}
		fields["ui_message"] = raw
	}
	if u := paid; u != nil {
		ur, err := json.Marshal(u)
		if err != nil {
			return err
		}
		fields["usage"] = ur
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	e.Timestamp, _ = m.Time()
	if _, err := c.appendEntry(e); err != nil {
		return err
	}
	if !c.fam.opts.Slim {
		c.uiAt(kind, "cline/ui-record", i, "a %s %s UI message is a custom entry%s", m.Type, m.Kind(), note)
	}
	c.mark(unit{uUI, i}, d)
	return nil
}

// paidUsage is the usage of model calls a UI record tells of that no
// response entry holds: a request that produced no response, the
// requests a checkpoint restore deleted, sub-agents that ran in
// memory. A custom entry carrying it as top-level usage is counted by
// a reader as a call the path paid for (RFC 0001, custom). nil when the
// record tells of none.
func (c *conv) paidUsage(i int, m *cline.UIMessage) *openresponses.Usage {
	switch {
	case m.Say == "api_req_started" && c.failed[i]:
	case m.Say == "deleted_api_reqs", m.Say == "api_req_deleted", m.Say == "subagent_usage":
	default:
		return nil
	}
	info := m.ReqInfo()
	if info == nil || !info.HasUsage() {
		return nil
	}
	ts, ok := m.Time()
	incl, why := c.inclusive("", ts, ok, info)
	if why != "" {
		c.uiAt(agentimport.Lossy, "cline/usage-semantics", i, "%s", why)
	}
	if !inRange(info.TokensIn, info.TokensOut, info.CacheReads, info.CacheWrites) {
		c.uiAt(agentimport.Lossy, "cline/usage-out-of-range", i, "a count of the record is negative or above 2^40; it is read as 0")
	}
	c.uiAt(agentimport.Kept, "cline/usage-without-response", i, "the usage this %s record holds, paid for with no response entry, is the custom entry's usage, which readers count", m.Kind())
	return recordUsage(info, incl)
}

// contextEdit keeps a classic context edit as a custom entry where it
// was made, and says what the model saw instead: the session format
// cannot replace an item in place.
func (c *conv) contextEdit(i int) error {
	e := c.t.Context.Elems[i]
	data, err := json.Marshal(map[string]any{"context_edit": map[string]any{
		"message": e.Message, "block": e.Block, "edit_type": e.EditType, "update": e.Raw,
	}})
	if err != nil {
		return err
	}
	if data, err = c.clean(c.t.Context.Name, i+1, data); err != nil {
		return err
	}
	ce := &agentsession.CustomEntry{NS: Source, Data: data}
	ce.Timestamp, _ = e.Time()
	if _, err := c.appendEntry(ce); err != nil {
		return err
	}
	c.mark(unit{uContext, i}, agentimport.LineKept)
	c.addf(agentimport.Gap, "agentimport/context-replace", c.t.Context.Name, i+1,
		"from this point Cline sends %s with its text replaced by %s; the session keeps the original, and the edit is a custom entry",
		c.describeBlock(e.Message, e.Block), describeText(e.NewText()))
	return nil
}

// describeBlock names block b of API message k, as findings say it.
func (c *conv) describeBlock(k, b int) string {
	what := "a block"
	if k >= 0 && k < len(c.t.API.Elems) {
		m := c.t.API.Elems[k]
		if m.Err == nil && b >= 0 && b < len(m.Content.Blocks) && m.Content.Blocks[b].Err == nil {
			what = "the " + m.Content.Blocks[b].Type + " block"
		}
	}
	return fmt.Sprintf("%s %d of API message %d", what, b, k+1)
}

// The texts Cline replaces context with.
const (
	truncationNotice  = "[NOTE] Some previous conversation history with the user has been removed to maintain optimal context window length."
	continueNotice    = "[Continue assisting the user!]"
	duplicateRead     = "[[NOTE] This file read has been removed to save space in the context window."
	resultMissingText = "result missing"
)

// describeText names a replacement text without quoting the user's
// content.
func describeText(s string) string {
	switch {
	case strings.HasPrefix(s, truncationNotice):
		return "the truncation notice"
	case s == continueNotice:
		return fmt.Sprintf("%q", continueNotice)
	case strings.HasPrefix(s, duplicateRead):
		return "the duplicate-file-read notice"
	}
	return fmt.Sprintf("other text (%d bytes)", len(s))
}

// unanswered reports the calls no output answered at the end.
func (c *conv) unanswered() {
	var names []string
	for _, p := range c.pending {
		if !p.output {
			names = append(names, p.native)
		}
	}
	if len(names) > 0 {
		slices.Sort(names)
		c.addf(agentimport.Lossy, "cline/call-without-result", c.t.API.Name, 0,
			"the last response's calls %s have no result: the task ended before they were answered", strings.Join(names, ", "))
	}
}
