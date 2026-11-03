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
	// verified: the line hashes to the id the document names.
	verified bool
	parent   string
	// step is the step, by index, that carried the entry's body, or -1.
	step int
}

// rebuildEntries makes the session from the document's entries. Every
// step's content is in them, so each step is mapped.
func (c *docConv) rebuildEntries(h agentsession.Header, recs map[string]json.RawMessage) (*agentsession.Session, error) {
	c.report.Line(agentimport.LineMapped)
	bodies, steps := c.stepBodies()
	for i, s := range c.t.Steps {
		if s == nil || s.Err != nil {
			c.report.Line(agentimport.LineMalformed)
			c.addf(agentimport.Repaired, "atif/step-malformed", i+1, "a step that is not an object is skipped; the entries carry what the exporter wrote")
			continue
		}
		c.report.Line(agentimport.LineMapped)
	}
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

	s := agentsession.New(h)
	newID := map[string]string{}
	unrebuilt := 0
	for _, r := range order {
		why := ""
		switch {
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
	if as["redacted"] != nil || as["redacted_from"] != nil {
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
		if raw := as["redacted_from"]; raw != nil {
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
