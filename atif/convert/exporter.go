package convert

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// A document agentsession's exporter wrote (agentsession/export ToATIF)
// carries, in each step's extra, the raw Open Responses items the step
// was built from and the entries' members. Such a step is rebuilt from
// them rather than mapped from its declared fields, and a step without
// them falls back to the mapping of any other producer.

// exporterRestored are the step extra members the rebuild takes back
// into entries: they are not kept in atif:extra.
var exporterRestored = []string{
	"openresponses", "agentsession", "visible", "source", "queued_from", "omitted",
	"item_entry_ids", "role", "item_type", "orphan_call_id", "phases", "response_id",
	"first_kept", "tokens_before", "usage", "branch_from", "context_management",
	"status", "error", "incomplete", "latency_ms",
}

// exporterRecords are the members the exporter writes for entries that
// are not items (records, labels, config and the like), which the
// rebuild keeps in atif:extra.
var exporterRecords = []string{
	"labels", "info", "custom", "config_entries", "config_extensions", "tool_changes",
	"environment", "extensions", "calls", "run", "queued", "outcome", "cost_usd", "links",
	"preferred_over", "abandoned_at",
}

// exporterExtra splits a step's extra: members of entries the exporter
// merged in, which carry a namespace, go back on the entry; what is
// neither restored nor such a member is kept in atif:extra.
func (c *docConv) exporterExtra(i int, ext map[string]json.RawMessage) (members, kept map[string]json.RawMessage) {
	members, kept = map[string]json.RawMessage{}, map[string]json.RawMessage{}
	var records []string
	for k, v := range ext {
		switch {
		case slices.Contains(exporterRestored, k):
		case slices.Contains(exporterRecords, k):
			kept[k] = v
			records = append(records, k)
		case strings.Contains(k, ":"):
			members[k] = v
		default:
			kept[k] = v
		}
	}
	if len(records) > 0 {
		slices.Sort(records)
		kind, verb := agentimport.Kept, "kept in "+Source+":extra"
		if c.fam.opts.Slim {
			kind, verb = agentimport.Dropped, "left out"
		}
		c.addf(kind, "atif/exporter-records", i+1,
			"the exporter's records of entries that are not items (%s) are %s; records stays empty", strings.Join(records, ", "), verb)
	}
	return members, kept
}

// exporterStep rebuilds a step from the raw items its extra carries. ok
// is false for a step that carries none. n is how many steps after it
// the rebuild took with it: a compaction's kept window.
func (c *docConv) exporterStep(i int) (n int, ok bool, err error) {
	s := c.t.Steps[i]
	ext := object(s.Ext)
	or := object(ext["openresponses"])
	if or == nil {
		return 0, false, nil
	}
	switch {
	case or["items"] != nil:
		return 0, true, c.exporterAgent(i, s, ext, or)
	case or["item"] != nil && ext["context_management"] != nil:
		n, err := c.exporterCompaction(i, s, ext, or)
		return n, true, err
	case or["item"] != nil && ext["branch_from"] != nil:
		return 0, true, c.exporterBranchSummary(i, s, ext, or)
	case or["item"] != nil:
		return 0, true, c.exporterItem(i, s, ext, or)
	}
	return 0, false, nil
}

// decodeItem decodes a raw item and reports nothing, so a step's items
// can all be checked before any of them changes the conversion's state.
func decodeItem(raw json.RawMessage) (openresponses.Item, error) {
	clean, _, err := agentimport.CleanJSON(raw)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(clean), []byte("{")) {
		return nil, errors.New("the raw item is not an object with a type")
	}
	item, err := openresponses.UnmarshalItem(clean)
	if err != nil {
		return nil, err
	}
	if item == nil || item.ItemType() == "" {
		return nil, errors.New("the raw item is not an object with a type")
	}
	return item, nil
}

// rawItem decodes a raw item, reporting what cleaning it changed, and
// inlines an image the exporter wrote to a file beside the document.
func (c *docConv) rawItem(i int, raw json.RawMessage) (openresponses.Item, error) {
	item, err := decodeItem(raw)
	if err != nil {
		return nil, err
	}
	if _, err := c.clean(i+1, raw); err != nil {
		return nil, err
	}
	switch v := item.(type) {
	case *openresponses.Message:
		c.inlineImages(i, v.Content)
	case *openresponses.FunctionCallOutput:
		c.inlineImages(i, v.Output.Parts)
	}
	return item, nil
}

// inlineImages reads back an image the exporter spilled to a file, a
// relative path in the raw item, as the data URL it was.
func (c *docConv) inlineImages(i int, parts openresponses.Contents) {
	for _, p := range parts {
		img, ok := p.(*openresponses.InputImage)
		if !ok || img.ImageURL == "" || strings.Contains(img.ImageURL, ":") {
			continue
		}
		data := c.image(i, &atif.Part{Type: atif.PartImage, Source: &atif.Source{Path: img.ImageURL, MediaType: imageType(img.ImageURL)}})
		if in, ok := data.(*openresponses.InputImage); ok {
			img.ImageURL = in.ImageURL
		}
	}
}

// itemEntry rebuilds an item entry: the item and the flags the
// exporter wrote beside it.
func (c *docConv) itemEntry(i int, raw json.RawMessage, ext map[string]json.RawMessage) (*agentsession.ItemEntry, error) {
	item, err := c.rawItem(i, raw)
	if err != nil {
		return nil, err
	}
	e := agentsession.NewItemEntry(item)
	if v, ok := ext["visible"]; ok {
		var b bool
		if json.Unmarshal(v, &b) == nil && !b {
			e.Visible = &b
		}
	}
	if v, ok := ext["source"]; ok {
		var t agentsession.Trigger
		if json.Unmarshal(v, &t) == nil {
			e.Source = &t
		}
	}
	e.QueuedFrom = str(ext["queued_from"])
	return e, nil
}

// leftOut notes the entries a step's extra says the request at the
// leaf leaves out: native lists the exporter's entry IDs in the step,
// ids the entries they became.
func (c *docConv) leftOut(ext map[string]json.RawMessage, native, ids []string) {
	var list []struct {
		EntryID string `json:"entry_id"`
	}
	if json.Unmarshal(ext["omitted"], &list) != nil {
		return
	}
	for _, o := range list {
		if k := slices.Index(native, o.EntryID); k >= 0 && k < len(ids) {
			c.omitted = append(c.omitted, ids[k])
		}
	}
}

// restore sets members back on an entry, and keeps the rest of the
// extra in atif:extra.
func (c *docConv) restore(e agentsession.Entry, i int, members, kept map[string]json.RawMessage) error {
	for k, v := range members {
		clean, err := c.clean(i+1, v)
		if err != nil {
			return err
		}
		base := e.Base()
		if base.Unknown == nil {
			base.Unknown = map[string]json.RawMessage{}
		}
		base.Unknown[k] = clean
	}
	if len(kept) == 0 {
		return nil
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return c.keepExtra(e, i, raw)
}

// exporterItem rebuilds a step built from one item entry: a user or
// system message, an extension item, or an output whose call is not on
// the path.
func (c *docConv) exporterItem(i int, s *atif.Step, ext, or map[string]json.RawMessage) error {
	e, err := c.itemEntry(i, or["item"], ext)
	if err != nil {
		return c.exporterFallback(i, s, err)
	}
	if out, ok := e.Item.(*openresponses.FunctionCallOutput); ok {
		if cl := c.findCall(out.CallID, i, nil); cl != nil && !cl.answered {
			cl.answered = true
			out.CallID = cl.id
		}
	}
	members, kept := c.exporterExtra(i, ext)
	if err := c.restore(e, i, members, kept); err != nil {
		return err
	}
	ids, err := c.commitEntries(i, s, []agentsession.Entry{e}, nil, false)
	if err != nil {
		return err
	}
	c.leftOut(ext, []string{str(object(ext["agentsession"])["entry_id"])}, ids)
	c.mark(i, agentimport.LineMapped)
	return nil
}

// exporterFallback maps a step whose raw items do not decode from its
// declared fields, and reports it.
func (c *docConv) exporterFallback(i int, s *atif.Step, err error) error {
	c.addf(agentimport.Lossy, "atif/raw-items", i+1,
		"the step's raw items do not decode (%s); it is mapped from its declared fields", agentimport.ErrorText(err))
	switch s.Source {
	case atif.SourceUser:
		return c.userStep(i, s)
	case atif.SourceAgent:
		return c.agentStep(i, s)
	}
	_, err = c.systemStep(i, s)
	return err
}

// exporterAgent rebuilds an agent step: its config delta, its items in
// order, the response entry the exporter carried whole, and the outputs
// its results carry.
func (c *docConv) exporterAgent(i int, s *atif.Step, ext, or map[string]json.RawMessage) error {
	line := i + 1
	var raws []json.RawMessage
	if err := json.Unmarshal(or["items"], &raws); err != nil {
		return c.exporterFallback(i, s, err)
	}
	// Every item and the response decode before anything is recorded:
	// a step that falls back is mapped as if the rebuild never started.
	for _, raw := range raws {
		if _, err := decodeItem(raw); err != nil {
			return c.exporterFallback(i, s, err)
		}
	}
	body := or["response"]
	if !atif.IsNull(body) {
		if _, err := parseResponseBody(body); err != nil {
			return c.exporterFallback(i, s, err)
		}
	}
	var resp *agentsession.ResponseEntry
	if !atif.IsNull(body) {
		r, err := c.responseBody(i, body)
		if err != nil {
			return err
		}
		resp = r
	}
	var entries []agentsession.Entry
	if delta := c.stepConfig(i, s); delta != nil {
		entries = append(entries, delta)
	}
	local := map[string][]*call{}
	var calls []string
	for _, raw := range raws {
		e, err := c.itemEntry(i, raw, nil)
		if err != nil {
			return err
		}
		if resp != nil {
			e.ResponseID = resp.ResponseID
		} else {
			e.ResponseID = str(ext["response_id"])
		}
		if fc, ok := e.Item.(*openresponses.FunctionCall); ok {
			native := fc.CallID
			id, renamed := c.b.Calls.Assign(native)
			if renamed {
				fc.CallID = id
				if err := c.b.Member(e, "call_id", native); err != nil {
					return err
				}
				c.addf(agentimport.Repaired, "atif/call-id", line,
					"tool call id %q is empty, repeats another call's in the session, or holds characters providers refuse; it is %s, the native id in %s:call_id", native, id, Source)
			}
			cl := &call{id: id, native: native, step: i}
			c.calls = append(c.calls, cl)
			local[native] = append(local[native], cl)
			calls = append(calls, native)
		}
		entries = append(entries, e)
	}
	c.rawMismatch(i, s, calls, entries)
	members, kept := c.exporterExtra(i, ext)
	if resp != nil {
		entries = append(entries, resp)
	} else {
		c.addf(agentimport.Lossy, "atif/no-response", line, "the exporter's step carries no response entry; its items stand without one")
	}
	if len(entries) > 0 {
		if err := c.restore(entries[len(entries)-1], i, members, kept); err != nil {
			return err
		}
	}
	nItems := len(entries)
	outs, omitted, err := c.exporterResults(i, s, local)
	if err != nil {
		return err
	}
	entries = append(entries, outs...)
	ids, err := c.commitEntries(i, s, entries, nil, false)
	if err != nil {
		return err
	}
	var itemIDs []string
	for k, e := range entries {
		if _, ok := e.(*agentsession.ItemEntry); ok && k < nItems {
			itemIDs = append(itemIDs, ids[k])
		}
		if omitted[e] {
			c.omitted = append(c.omitted, ids[k])
		}
	}
	var native []string
	_ = json.Unmarshal(ext["item_entry_ids"], &native)
	c.leftOut(ext, native, itemIDs)
	c.mark(i, agentimport.LineMapped)
	return nil
}

// rawMismatch reports a step whose declared fields disagree with its
// raw items, which an edit after export leaves: the raw items are
// trusted, and the declared ones kept.
func (c *docConv) rawMismatch(i int, s *atif.Step, calls []string, entries []agentsession.Entry) {
	var declared []string
	for _, tc := range s.ToolCalls {
		if tc != nil && tc.Err == nil {
			declared = append(declared, tc.ToolCallID)
		}
	}
	var texts []string
	for _, e := range entries {
		if it, ok := e.(*agentsession.ItemEntry); ok {
			if m, ok := it.Item.(*openresponses.Message); ok {
				texts = append(texts, m.Text())
			}
		}
	}
	if slices.Equal(declared, calls) && (s.Message.Parts != nil || s.Message.Text == strings.Join(texts, "\n")) {
		return
	}
	c.addf(agentimport.Lossy, "atif/raw-mismatch", i+1,
		"the step's declared message or tool calls disagree with the raw items it carries, as an edit after export leaves them; the raw items are converted")
}

// exporterResults rebuilds the outputs an agent step's results carry,
// each after the link to the sub-agent that answered it, and says which
// the request at the leaf leaves out.
func (c *docConv) exporterResults(i int, s *atif.Step, local map[string][]*call) ([]agentsession.Entry, map[agentsession.Entry]bool, error) {
	o := s.Observation
	if o == nil || o.Err != nil {
		return nil, nil, nil
	}
	var out []agentsession.Entry
	omitted := map[agentsession.Entry]bool{}
	for j, r := range o.Results {
		if r == nil || r.Err != nil {
			c.addf(agentimport.Dropped, "atif/result-malformed", i+1, "result %d is not an object and is left out", j)
			continue
		}
		rext := object(r.Ext)
		ror := object(rext["openresponses"])
		raw, has := ror["item"]
		if has {
			if _, err := decodeItem(raw); err != nil {
				c.addf(agentimport.Lossy, "atif/raw-items", i+1,
					"result %d's raw item does not decode (%s); it is mapped from its declared fields", j, agentimport.ErrorText(err))
				has = false
			}
		}
		if !has {
			// A result the exporter wrote no item for, a subsession
			// reference attached to a call with no output on the path,
			// or one whose item does not decode.
			ents, err := c.results(i, &atif.Step{Observation: &atif.Observation{Results: []*atif.Result{r}}}, local, nil)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, ents...)
			continue
		}
		e, err := c.itemEntry(i, ror["item"], rext)
		if err != nil {
			return nil, nil, err
		}
		if fo, ok := e.Item.(*openresponses.FunctionCallOutput); ok {
			if cl := c.findCall(fo.CallID, i, local); cl != nil {
				cl.answered = true
				fo.CallID = cl.id
				for _, ref := range r.Refs.List {
					child, leaf, ok, err := c.spawn(i, s, ref, cl.id)
					if err != nil {
						return nil, nil, err
					}
					if !ok {
						if err := c.keepRef(e, i, ref); err != nil {
							return nil, nil, err
						}
						continue
					}
					link := agentsession.NewSubsessionLink(child, cl.id)
					if err := c.refMembers(link, i, ref); err != nil {
						return nil, nil, err
					}
					out = append(out, link)
					if leaf != "" {
						e.Parents = append(e.Parents, agentsession.EntryRef{Session: child, Entry: leaf})
					}
				}
			}
		}
		members, kept := c.exporterExtra(i, rext)
		if err := c.restore(e, i, members, kept); err != nil {
			return nil, nil, err
		}
		out = append(out, e)
		if rext["omitted"] != nil {
			omitted[e] = true
		}
	}
	return out, omitted, nil
}

// responseBody rebuilds a response entry from the body the exporter
// carried: the entry without its envelope, its ID as entry_id.
func (c *docConv) responseBody(i int, body json.RawMessage) (*agentsession.ResponseEntry, error) {
	if _, err := c.clean(i+1, body); err != nil {
		return nil, err
	}
	return parseResponseBody(body)
}

// parseResponseBody is responseBody reporting nothing.
func parseResponseBody(body json.RawMessage) (*agentsession.ResponseEntry, error) {
	clean, _, err := agentimport.CleanJSON(body)
	if err != nil {
		return nil, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(clean, &all); err != nil {
		return nil, err
	}
	var r struct {
		ResponseID  string                           `json:"response_id"`
		Model       string                           `json:"model"`
		Status      openresponses.ResponseStatus     `json:"status"`
		Usage       *openresponses.Usage             `json:"usage"`
		Incomplete  *openresponses.IncompleteDetails `json:"incomplete"`
		Error       *openresponses.ErrorPayload      `json:"error"`
		RequestHash string                           `json:"request_hash"`
		LatencyMS   int64                            `json:"latency_ms"`
		Attempts    int                              `json:"attempts"`
		TS          string                           `json:"ts"`
	}
	if err := json.Unmarshal(clean, &r); err != nil {
		return nil, err
	}
	e := &agentsession.ResponseEntry{
		ResponseID: r.ResponseID, Model: r.Model, Status: r.Status, Usage: r.Usage,
		Incomplete: r.Incomplete, Error: r.Error, RequestHash: r.RequestHash, LatencyMS: r.LatencyMS,
	}
	if r.Attempts > 0 {
		e.Attempts = r.Attempts
	}
	if ts, ok := agentsession.ParseCanonicalTime(r.TS); ok {
		e.Timestamp = ts
	}
	for _, k := range []string{"response_id", "model", "status", "usage", "incomplete", "error", "request_hash",
		"latency_ms", "attempts", "ts", "entry_id", "content", "parents", "type", "id", "parent"} {
		delete(all, k)
	}
	if len(all) > 0 {
		e.Unknown = all
	}
	if e.ResponseID == "" {
		return nil, fmt.Errorf("the response body has no response_id")
	}
	return e, nil
}

// exporterCompaction rebuilds a compaction: the steps after it marked
// copied context are its kept window, which precedes it on the path the
// document was exported from, so they are converted first and the
// compaction keeps from the first of them.
func (c *docConv) exporterCompaction(i int, s *atif.Step, ext, or map[string]json.RawMessage) (int, error) {
	var raws []json.RawMessage
	if raw := or["pinned"]; raw != nil {
		if err := json.Unmarshal(raw, &raws); err != nil {
			return 0, c.exporterFallback(i, s, err)
		}
	}
	for _, raw := range append([]json.RawMessage{or["item"]}, raws...) {
		if _, err := decodeItem(raw); err != nil {
			return 0, c.exporterFallback(i, s, err)
		}
	}
	summary, err := c.rawItem(i, or["item"])
	if err != nil {
		return 0, err
	}
	var pinned openresponses.Items
	for _, r := range raws {
		item, err := c.rawItem(i, r)
		if err != nil {
			return 0, err
		}
		pinned = append(pinned, item)
	}
	// The window: the copied steps after the compaction.
	n := 0
	before := c.tail
	for k := i + 1; k < len(c.t.Steps); k++ {
		w := c.t.Steps[k]
		if w == nil || w.Err != nil || !w.Copied() {
			break
		}
		if wor := object(object(w.Ext)["openresponses"]); wor == nil || wor["item"] != nil && object(w.Ext)["context_management"] != nil {
			break
		}
		if _, ok, err := c.exporterStep(k); err != nil {
			return 0, err
		} else if !ok {
			break
		}
		n++
	}
	firstKept := ""
	if n > 0 {
		for _, e := range c.b.S.Path(c.tail) {
			if e.Base().Parent == before {
				firstKept = e.Base().ID
				break
			}
		}
		c.addf(agentimport.Lossy, "atif/folded-history", i+1,
			"the %d copied steps after the compaction are the window it kept, which the exporter writes after it; they are put back before it", n)
	}
	if firstKept == "" {
		if firstKept, err = c.keepNone(i, s); err != nil {
			return 0, err
		}
	}
	settings := c.settings
	if c.tail != "" {
		ctx, err := c.b.S.ContextAt(c.tail)
		if err != nil {
			return 0, err
		}
		settings = ctx.Settings
	}
	e := &agentsession.CompactionEntry{FirstKept: firstKept, Summary: summary, Pinned: pinned, Config: settings}
	if n, _, ok := atif.Int(ext["tokens_before"]); ok {
		e.TokensBefore = int(n)
	}
	if u := ext["usage"]; !atif.IsNull(u) {
		var usage openresponses.Usage
		if json.Unmarshal(u, &usage) == nil {
			e.Usage = &usage
		}
	}
	c.addf(agentimport.Lossy, "atif/exporter-config", i+1,
		"the exporter does not write a compaction's settings checkpoint; the compaction's config is the settings in force where it is rebuilt")
	members, kept := c.exporterExtra(i, ext)
	if err := c.restore(e, i, members, kept); err != nil {
		return 0, err
	}
	if _, err := c.commitEntries(i, s, []agentsession.Entry{e}, nil, false); err != nil {
		return 0, err
	}
	c.mark(i, agentimport.LineMapped)
	return n, nil
}

// exporterBranchSummary rebuilds a branch summary.
func (c *docConv) exporterBranchSummary(i int, s *atif.Step, ext, or map[string]json.RawMessage) error {
	summary, err := c.rawItem(i, or["item"])
	if err != nil {
		return c.exporterFallback(i, s, err)
	}
	e := &agentsession.BranchSummaryEntry{From: str(ext["branch_from"]), Summary: summary}
	if u := ext["usage"]; !atif.IsNull(u) {
		var usage openresponses.Usage
		if json.Unmarshal(u, &usage) == nil {
			e.Usage = &usage
		}
	}
	c.addf(agentimport.Lossy, "atif/branch-summary", i+1,
		"a branch summary's from names an entry of the exported session, which this one does not hold")
	members, kept := c.exporterExtra(i, ext)
	if err := c.restore(e, i, members, kept); err != nil {
		return err
	}
	if _, err := c.commitEntries(i, s, []agentsession.Entry{e}, nil, false); err != nil {
		return err
	}
	c.mark(i, agentimport.LineMapped)
	return nil
}
