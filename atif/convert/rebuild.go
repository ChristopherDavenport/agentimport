package convert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/agentsession/export"
	"github.com/ChristopherDavenport/openresponses"
)

// A document agentsession's exporter wrote from format 0.17 on carries,
// under extra.agentsession.entries, each entry's envelope, its history
// members and every body member its step does not carry. RFC 0001
// (Projections/ATIF) asks a converter reading such a document back to
// rebuild each entry to the id the document names, to check every
// rebuilt id, to keep history members only on an entry that matches, to
// restore the header's redacted and redacted_from exactly when every
// entry matches and the document holds the whole path, and to list
// every entry that does not match. rebuildEntries does that, in place
// of mapping the steps.

// entryRecords returns the document's extra.agentsession.entries.
func (c *docConv) entryRecords() map[string]json.RawMessage {
	return object(object(object(c.t.Ext)["agentsession"])["entries"])
}

// rebuildBlocked says why a document carrying entries is mapped from its
// steps rather than rebuilt from them, or "" when it is rebuilt.
func (c *docConv) rebuildBlocked() string {
	switch {
	case len(c.t.Subagents) > 0 || c.t.SubagentsMistyped:
		return "it embeds sub-agent trajectories, which this converter links to their calls from the steps"
	case c.t.ContinuedRef != "":
		return "it continues in another document"
	case c.in.file != nil && c.in.file.Cut != nil:
		return "it is cut short"
	}
	return ""
}

// rebuilt is an entry put back together from a document.
type rebuilt struct {
	id   string
	line map[string]json.RawMessage
	// verified: the line hashes to the id the document names, and its
	// step shows what it projects; visible says the step did not.
	verified, visible bool
	parent            string
	// step is the step, by index, that carried the entry's body, or -1.
	step int
}

// rebuildEntries makes the session from the document's entries. Every
// step's content is in them, so each step is mapped.
func (c *docConv) rebuildEntries(h agentsession.Header, recs map[string]json.RawMessage) (*agentsession.Session, error) {
	c.report.Line(agentimport.LineMapped)
	bodies, steps := c.stepBodies()
	ents := map[string]*rebuilt{}
	for id, raw := range recs {
		rec := object(raw)
		line := map[string]json.RawMessage{"id": jsonString(id)}
		for k, v := range rec {
			if k == "body" {
				for bk, bv := range object(v) {
					line[bk] = bv
				}
				continue
			}
			line[k] = v
		}
		step := -1
		if b, ok := bodies[id]; ok {
			step = steps[id]
			for k, v := range b {
				if _, set := line[k]; !set {
					line[k] = v
				}
			}
		}
		r := &rebuilt{id: id, line: line, parent: str(line["parent"]), step: step}
		if data, err := json.Marshal(line); err == nil {
			got, _, err := agentsession.EntryHashes(data)
			r.verified = err == nil && got == id
		}
		ents[id] = r
	}
	order, whole := rebuildOrder(ents)
	// The whole path reaches the leaf the document names, every entry
	// from it up held (RFC 0001, Projections/ATIF, 0.18).
	leaf := c.docLeaf()
	for id, n := leaf, 0; ; n++ {
		r, ok := ents[id]
		if !ok || n > len(ents) {
			whole = false
			break
		}
		if r.parent == "" {
			break
		}
		id = r.parent
	}
	// Every step names an entry of the document: one that names none,
	// such as a step appended after the export, is not of the path, and
	// what it holds is in no entry.
	for i, st := range c.t.Steps {
		switch {
		case st == nil || st.Err != nil:
			c.report.Line(agentimport.LineMalformed)
			c.addf(agentimport.Repaired, "atif/step-malformed", i+1, "a step that is not an object is skipped; the entries carry what the exporter wrote")
			whole = false
		case ents[stepID(st)] == nil:
			c.report.Line(agentimport.LineDropped)
			c.addf(agentimport.Dropped, "atif/step-not-rebuilt", i+1,
				"the step names no entry the document carries, as one appended after the export does not; it is not of the exported path, and is left out")
			whole = false
		default:
			c.report.Line(agentimport.LineMapped)
		}
	}
	// An entry matches only when its step shows what it projects (0.18):
	// the id covers the raw item, not what a reader of the step sees.
	c.checkVisible(ents, order, leaf)

	s := agentsession.New(h)
	newID := map[string]string{}
	unrebuilt := 0
	for _, r := range order {
		why := ""
		switch {
		case r.visible:
			why = "its step shows other text than it projects, as an edit of the document's visible fields alone leaves it"
		case !r.verified:
			why = "its members hash to another id, as an edit after the export, or a document stripped of a body, leaves them"
		case r.parent != "" && ents[r.parent] == nil:
			why = "its parent is not in the document"
		case r.parent != "" && newID[r.parent] != r.parent:
			why = "its parent did not rebuild to its id"
		}
		if why == "" {
			e, err := c.lineEntry(r.line)
			if err == nil {
				if _, err = s.Append(e); err == nil {
					newID[r.id] = r.id
					continue
				}
			}
			why = "the entry it rebuilds to is refused (" + agentimport.ErrorText(err) + ")"
		}
		id, err := c.appendNew(s, r, newID)
		if err != nil {
			return nil, err
		}
		newID[r.id] = id
		unrebuilt++
		c.addf(agentimport.Lossy, "atif/entry-not-rebuilt", r.step+1,
			"entry %s does not rebuild to the document's id: %s; it is entry %s, and carries no history member as its own", r.id, why, id)
	}
	as := object(object(c.t.Ext)["agentsession"])
	restore := false
	if isTrue(as["redacted"]) || object(as["redacted_from"]) != nil {
		switch {
		case unrebuilt > 0:
			c.addf(agentimport.Lossy, "atif/redaction-not-restored", 0,
				"the document is of a redacted session, but %d of its %d entries do not rebuild to their ids; the header's redacted and redacted_from are kept in %s:extra, not restored", unrebuilt, len(order), Source)
		case !whole:
			c.addf(agentimport.Lossy, "atif/redaction-not-restored", 0,
				"the document is of a redacted session, but it does not hold the whole path, as one built after a compaction does not; the header's redacted and redacted_from are kept in %s:extra, not restored", Source)
		default:
			restore = true
		}
	}
	h, err := c.rebuiltHeader(h, as, restore)
	if err != nil {
		return nil, err
	}
	// The header is the session's, so the entries move to a session
	// that has it; each keeps its id.
	out := agentsession.New(h)
	for _, e := range s.Entries() {
		if e.Base().Parent == "" {
			out.ResetLeaf()
		}
		if _, err := out.Append(e); err != nil {
			return nil, fmt.Errorf("entry %s: %w", e.Base().ID, err)
		}
	}
	return out, nil
}

// rebuiltHeader is h with the document's extra kept without the entries,
// which the session now holds, and, when restore says every entry
// rebuilt to its id over the whole path, with the redacted session's
// redacted and redacted_from, which then leave the kept extra.
func (c *docConv) rebuiltHeader(h agentsession.Header, as map[string]json.RawMessage, restore bool) (agentsession.Header, error) {
	if restore {
		var from *agentsession.RedactedFrom
		if raw := as["redacted_from"]; object(raw) != nil {
			from = &agentsession.RedactedFrom{}
			if err := json.Unmarshal(raw, from); err != nil {
				c.addf(agentimport.Lossy, "atif/redaction-not-restored", 0,
					"the document's redacted_from does not read (%s); the header's redacted and redacted_from are kept in %s:extra, not restored", agentimport.ErrorText(err), Source)
				restore = false
			}
		}
		if restore {
			h.Redacted, h.RedactedFrom = true, from
		}
	}
	key := Source + ":extra"
	ext := object(h.Extra[key])
	if ext == nil {
		return h, nil
	}
	kept := object(ext["agentsession"])
	delete(kept, "entries")
	if restore {
		delete(kept, "redacted")
		delete(kept, "redacted_from")
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		return h, err
	}
	ext["agentsession"] = raw
	if raw, err = json.Marshal(ext); err != nil {
		return h, err
	}
	h.Extra = maps.Clone(h.Extra)
	h.Extra[key] = raw
	return h, nil
}

// stepBodies gathers what the steps carry of each entry: an item's item,
// from its step's items by its position in item_entry_ids, or the item
// of a step or an observation result, and a response's body.
func (c *docConv) stepBodies() (map[string]map[string]json.RawMessage, map[string]int) {
	bodies := map[string]map[string]json.RawMessage{}
	steps := map[string]int{}
	put := func(id string, i int, body map[string]json.RawMessage) {
		if id == "" {
			return
		}
		bodies[id], steps[id] = body, i
	}
	for i, s := range c.t.Steps {
		if s == nil || s.Err != nil {
			continue
		}
		ext := object(s.Ext)
		or := object(ext["openresponses"])
		if items, ok := or["items"]; ok {
			var raws []json.RawMessage
			var ids []string
			_ = json.Unmarshal(items, &raws)
			_ = json.Unmarshal(ext["item_entry_ids"], &ids)
			if rb := object(or["response"]); rb != nil {
				body := map[string]json.RawMessage{}
				for k, v := range rb {
					if k != "entry_id" {
						body[k] = v
					}
				}
				put(str(rb["entry_id"]), i, body)
			}
			for k, raw := range raws {
				if k < len(ids) {
					put(ids[k], i, map[string]json.RawMessage{"item": c.inlineRaw(i, raw)})
				}
			}
		} else if item, ok := or["item"]; ok {
			put(str(object(ext["agentsession"])["entry_id"]), i, map[string]json.RawMessage{"item": c.inlineRaw(i, item)})
		}
		if o := s.Observation; o != nil && o.Err == nil {
			for _, r := range o.Results {
				if r == nil || r.Err != nil {
					continue
				}
				rext := object(r.Ext)
				if item, ok := object(rext["openresponses"])["item"]; ok {
					put(str(object(rext["agentsession"])["entry_id"]), i, map[string]json.RawMessage{"item": c.inlineRaw(i, item)})
				}
			}
		}
	}
	return bodies, steps
}

// inlineRaw puts back, in a raw item, the data URL of an image the
// exporter spilled to a file beside the document, which the entry held.
func (c *docConv) inlineRaw(i int, raw json.RawMessage) json.RawMessage {
	if !bytes.Contains(raw, []byte(`"image_url"`)) {
		return raw
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return raw
	}
	changed := false
	var walk func(any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if u, ok := v["image_url"].(string); ok && v["type"] == "input_image" && u != "" && !strings.Contains(u, ":") {
				img := c.image(i, &atif.Part{Type: atif.PartImage, Source: &atif.Source{Path: u, MediaType: imageType(u)}})
				if in, ok := img.(*openresponses.InputImage); ok {
					v["image_url"], changed = in.ImageURL, true
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(v)
	if !changed {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// rebuildOrder orders the entries parent before child, those with the
// same parent by time and then id, and says whether the document holds
// the whole path: every parent an entry of the document or null.
func rebuildOrder(ents map[string]*rebuilt) ([]*rebuilt, bool) {
	ids := make([]string, 0, len(ents))
	for id := range ents {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		if c := strings.Compare(str(ents[a].line["ts"]), str(ents[b].line["ts"])); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	whole := len(ents) > 0
	done := map[string]bool{}
	var order []*rebuilt
	for len(order) < len(ids) {
		progressed := false
		for _, id := range ids {
			r := ents[id]
			if done[id] {
				continue
			}
			if r.parent == "" || done[r.parent] || ents[r.parent] == nil {
				if r.parent != "" && ents[r.parent] == nil {
					whole = false
				}
				done[id] = true
				order = append(order, r)
				progressed = true
			}
		}
		if !progressed {
			// A cycle, which no exporter writes: the rest go in time
			// order, each a new entry.
			whole = false
			for _, id := range ids {
				if !done[id] {
					done[id] = true
					ents[id].verified = false
					order = append(order, ents[id])
				}
			}
		}
	}
	return order, whole
}

// lineEntry decodes a rebuilt line as an entry.
func (c *docConv) lineEntry(line map[string]json.RawMessage) (agentsession.Entry, error) {
	data, err := json.Marshal(line)
	if err != nil {
		return nil, err
	}
	return agentsession.UnmarshalEntry(data)
}

// appendNew appends an entry that did not rebuild to its id as a new
// one: under the entry its parent became, its history members, which
// are not true of a new entry, kept as atif:<name> or left out under
// Slim.
func (c *docConv) appendNew(s *agentsession.Session, r *rebuilt, newID map[string]string) (string, error) {
	line := make(map[string]json.RawMessage, len(r.line))
	for k, v := range r.line {
		line[k] = v
	}
	// The id the document names stays for the decode, which wants one,
	// and is cleared before the append computes the entry's own.
	for k, v := range r.line {
		if !agentsession.IsHistoryMember(k) {
			continue
		}
		delete(line, k)
		if c.fam.opts.Slim {
			c.addf(agentimport.Dropped, "atif/history-member", r.step+1, "the %s of entry %s, which did not rebuild to its id, is left out", k, r.id)
			continue
		}
		line[Source+":"+k] = v
		c.addf(agentimport.Kept, "atif/history-member", r.step+1, "the %s of entry %s, which did not rebuild to its id, is %s:%s", k, r.id, Source, k)
	}
	// An entry named in the body, a dispatch's or a decision's target, a
	// fold's first kept entry, an omit's items, is the entry it became.
	for k, v := range line {
		if k != "id" && k != "parent" {
			line[k] = remapIDs(v, newID)
		}
	}
	parent := ""
	if r.parent != "" {
		parent = newID[r.parent]
	}
	if parent == "" {
		delete(line, "parent")
	} else {
		line["parent"] = jsonString(parent)
	}
	e, err := c.lineEntry(line)
	if err != nil {
		return "", fmt.Errorf("entry %s: %w", r.id, err)
	}
	if parent == "" {
		s.ResetLeaf()
	}
	e.Base().ID = ""
	e.Base().Parent = parent
	id, err := s.Append(e)
	if err != nil {
		return "", fmt.Errorf("entry %s: %w", r.id, err)
	}
	return id, nil
}

// remapIDs replaces, in a JSON value, every string that is the id of an
// entry rebuilt under another id with that id.
func remapIDs(raw json.RawMessage, newID map[string]string) json.RawMessage {
	if !bytes.Contains(raw, []byte("sha256:")) {
		return raw
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return raw
	}
	changed := false
	var walk func(any) any
	walk = func(v any) any {
		switch v := v.(type) {
		case string:
			if n, ok := newID[v]; ok && n != v {
				changed = true
				return n
			}
		case map[string]any:
			for k, x := range v {
				v[k] = walk(x)
			}
		case []any:
			for i, x := range v {
				v[i] = walk(x)
			}
		}
		return v
	}
	v = walk(v)
	if !changed {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// isTrue reports a JSON true.
func isTrue(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "true"
}

// docLeaf is the entry the document was built at: its trajectory_id,
// which agentsession's exporter sets to the leaf, or the leaf its root
// extra names.
func (c *docConv) docLeaf() string {
	if leaf := str(object(object(c.t.Ext)["agentsession"])["leaf"]); leaf != "" {
		return leaf
	}
	return c.t.TrajectoryID
}

// stepID is the entry a step names under agentsession in its extra.
func stepID(s *atif.Step) string {
	return str(object(object(s.Ext)["agentsession"])["entry_id"])
}

// stepEntries are the ids of the entries a step is made from: its own,
// its items' and response's, and its observation results'.
func stepEntries(s *atif.Step) []string {
	ext := object(s.Ext)
	var ids []string
	add := func(id string) {
		if id != "" {
			ids = append(ids, id)
		}
	}
	add(stepID(s))
	var items []string
	_ = json.Unmarshal(ext["item_entry_ids"], &items)
	for _, id := range items {
		add(id)
	}
	add(str(object(object(ext["openresponses"])["response"])["entry_id"]))
	if o := s.Observation; o != nil && o.Err == nil {
		for _, r := range o.Results {
			if r != nil && r.Err == nil {
				add(str(object(object(r.Ext)["agentsession"])["entry_id"]))
			}
		}
	}
	return ids
}

// visibleFields is what a reader of a step sees that the projection
// fills from its entries: message, reasoning_content, tool_calls and each
// observation result's source_call_id and content, as canonical JSON.
func visibleFields(step map[string]any) string {
	v := map[string]any{"message": step["message"], "reasoning_content": step["reasoning_content"], "tool_calls": step["tool_calls"]}
	if o, ok := step["observation"].(map[string]any); ok {
		var results []any
		rs, _ := o["results"].([]any)
		for _, rv := range rs {
			res, _ := rv.(map[string]any)
			results = append(results, map[string]any{"source_call_id": res["source_call_id"], "content": res["content"]})
		}
		v["observation"] = results
	}
	data, _ := json.Marshal(v)
	return string(data)
}

// decodeAny decodes JSON keeping numbers as written.
func decodeAny(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	return d.Decode(v)
}

// checkVisible projects the entries that rebuilt to their ids, those
// whose parents did too, again at the document's leaf, and unmarks the
// entries of every step whose visible fields differ from what the
// projection shows. When the leaf did not rebuild no step is confirmed,
// and every entry of a step is unmarked.
func (c *docConv) checkVisible(ents map[string]*rebuilt, order []*rebuilt, leaf string) {
	projected := c.reproject(ents, order, leaf)
	for _, st := range c.t.Steps {
		if st == nil || st.Err != nil {
			continue
		}
		raw, err := json.Marshal(st)
		var step map[string]any
		if err == nil {
			err = decodeAny(raw, &step)
		}
		if want, ok := projected[stepID(st)]; err == nil && ok && want == visibleFields(step) {
			continue
		}
		for _, id := range stepEntries(st) {
			if r := ents[id]; r != nil && r.verified {
				r.verified, r.visible = false, true
			}
		}
	}
}

// reproject is each step's visible fields, by the entry it names, in the
// document agentsession's exporter writes from the entries that rebuilt
// to their ids at leaf; nil when the leaf did not rebuild.
func (c *docConv) reproject(ents map[string]*rebuilt, order []*rebuilt, leaf string) map[string]string {
	closed := map[string]bool{}
	as := object(object(c.t.Ext)["agentsession"])
	header := map[string]any{"type": "session", "format": agentsession.Format, "id": c.t.SessionID,
		"created_at": "2026-01-01T00:00:00Z", "payload": agentsession.Payload}
	if isTrue(as["redacted"]) && object(as["redacted_from"]) != nil {
		header["redacted"], header["redacted_from"] = true, as["redacted_from"]
	}
	if c.t.SessionID == "" {
		header["id"] = "atif"
	}
	var buf bytes.Buffer
	write := func(v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return false
		}
		buf.Write(append(data, '\n'))
		return true
	}
	if !write(header) {
		return nil
	}
	for _, r := range order {
		if r.verified && (r.parent == "" || closed[r.parent]) {
			if write(r.line) {
				closed[r.id] = true
			}
		}
	}
	if !closed[leaf] {
		return nil
	}
	s, err := agentsession.Read(&buf)
	if err != nil {
		return nil
	}
	tr, err := export.At(s, leaf)
	if err != nil {
		return nil
	}
	doc, err := export.ToATIF(tr, export.Options{})
	if err != nil {
		return nil
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return nil
	}
	var again map[string]any
	if decodeAny(data, &again) != nil {
		return nil
	}
	out := map[string]string{}
	steps, _ := again["steps"].([]any)
	for _, sv := range steps {
		step, _ := sv.(map[string]any)
		extra, _ := step["extra"].(map[string]any)
		as, _ := extra["agentsession"].(map[string]any)
		id, _ := as["entry_id"].(string)
		out[id] = visibleFields(step)
	}
	return out
}
