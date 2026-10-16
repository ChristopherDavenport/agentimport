package convert

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// usageRef is one call's token usage from base_state's metrics.
type usageRef struct {
	usageID string
	u       *openhands.TokenUsage
	used    bool
}

// usageIndex holds base_state's per-call usage by response ID, and
// each response's latency.
type usageIndex struct {
	all     []*usageRef
	byResp  map[string][]*usageRef
	latency map[string]json.RawMessage
}

// childMetrics reports whether a usage ID is a sub-agent's, whose
// metrics the parent holds a copy of.
func childMetrics(usageID string) bool {
	return strings.HasPrefix(usageID, "task:") || strings.HasPrefix(usageID, "delegate:")
}

// indexUsage reads base_state's metrics. The sub-agents' copies are
// left out: each sub-agent's session carries its own.
func (c *conv) indexUsage() {
	c.usage = &usageIndex{byResp: map[string][]*usageRef{}, latency: map[string]json.RawMessage{}}
	s := c.c.State
	if s == nil || s.Stats == nil {
		return
	}
	children := 0
	for _, key := range s.Stats.UsageToMetrics.Keys() {
		m := s.Stats.UsageToMetrics[key]
		if childMetrics(key) {
			children++
			continue
		}
		if m.Err != nil {
			c.addf(agentimport.Dropped, "openhands/bad-metrics", openhands.FileBaseState, 0, "the metrics of %q are not an object and are left out", key)
			continue
		}
		for _, u := range m.TokenUsages {
			if u == nil {
				continue
			}
			ref := &usageRef{usageID: key, u: u}
			c.usage.all = append(c.usage.all, ref)
			if u.ResponseID != "" {
				c.usage.byResp[u.ResponseID] = append(c.usage.byResp[u.ResponseID], ref)
			}
		}
		for _, l := range m.ResponseLatencies {
			if l != nil && l.ResponseID != "" {
				if _, ok := c.usage.latency[l.ResponseID]; !ok {
					c.usage.latency[l.ResponseID] = l.Latency
				}
			}
		}
	}
	if children > 0 {
		kind, _ := c.keeping()
		c.addf(kind, "openhands/child-usage", openhands.FileBaseState, 0, "%d sub-agents' metrics, under task: and delegate: usage IDs, are copies of the sub-agents' own, which their sessions carry; they are not counted here", children)
	}
}

// take returns the first usage of a response not taken yet.
func (x *usageIndex) take(resp string) *usageRef {
	for _, r := range x.byResp[resp] {
		if !r.used {
			r.used = true
			return r
		}
	}
	return nil
}

// usageOf maps a token usage to Responses usage: prompt tokens are
// litellm's, which count cache reads and writes, or for an ACPAgent
// count them apart, so they are folded in as providers/anthropic does.
// The counts Responses usage has no place for are returned apart.
func (c *conv) usageOf(u *openhands.TokenUsage) (*openresponses.Usage, map[string]int) {
	in := openhands.Count(u.PromptTokens)
	read := openhands.Count(u.CacheReadTokens)
	write := openhands.Count(u.CacheWriteTokens)
	if c.acp {
		in += read + write
	}
	out := openhands.Count(u.CompletionTokens)
	use := &openresponses.Usage{
		InputTokens:         in,
		OutputTokens:        out,
		TotalTokens:         in + out,
		InputTokensDetails:  openresponses.InputTokensDetails{CachedTokens: read},
		OutputTokensDetails: openresponses.OutputTokensDetails{ReasoningTokens: openhands.Count(u.ReasoningTokens)},
	}
	extra := map[string]int{}
	for name, raw := range map[string]json.RawMessage{"cache_write_tokens": u.CacheWriteTokens, "context_window": u.ContextWindow, "per_turn_token": u.PerTurnToken} {
		if n := openhands.Count(raw); n > 0 {
			extra[name] = n
		}
	}
	if len(extra) == 0 {
		extra = nil
	}
	return use, extra
}

// joinUsage puts on a response entry the usage, model and latency
// base_state records for its native response ID. A response a fork
// copied keeps its usage in a member, so the source's count is not
// counted again.
func (c *conv) joinUsage(i int, e *agentsession.ResponseEntry, native string) error {
	if l, ok := c.usage.latency[native]; ok {
		if f, ok := openhands.Float(l); ok && f >= 0 && f < 1e9 {
			e.LatencyMS = int64(f*1000 + 0.5)
		}
	}
	ref := c.usage.take(native)
	if ref == nil {
		if c.c.State != nil {
			c.at(agentimport.Lossy, "openhands/usage-joined", i, "base_state records no usage for response %q; the response has none", native)
		}
		return nil
	}
	use, extra := c.usageOf(ref.u)
	if i < c.copied {
		if err := c.b.Member(e, "copied_usage", map[string]any{"usage": use, "model": ref.u.Model, "usage_id": ref.usageID}); err != nil {
			return err
		}
		c.at(agentimport.Kept, "openhands/fork-usage", i, "the response is a copy a fork made, and its usage, which the fork's metrics hold again, is kept in %s:copied_usage", Source)
		return nil
	}
	e.Usage = use
	e.Model = ref.u.Model
	if extra != nil {
		if err := c.b.Member(e, "usage", extra); err != nil {
			return err
		}
		if extra["cache_write_tokens"] > 0 {
			c.at(agentimport.Gap, "agentimport/usage-detail", i, "cache writes have no place in Responses usage; OpenHands counts them in prompt tokens, and the count is in %s:usage", Source)
		} else {
			c.at(agentimport.Kept, "openhands/usage-member", i, "the context window and the per-turn tokens OpenHands records with the usage are in %s:usage", Source)
		}
	}
	return nil
}

// joinCompaction puts the condenser's usage for a condensation on its
// compaction.
func (c *conv) joinCompaction(i int, e *agentsession.CompactionEntry, native string) error {
	ref := c.usage.take(native)
	if ref == nil || i < c.copied {
		return nil
	}
	use, extra := c.usageOf(ref.u)
	e.Usage = use
	if ref.u.Model != "" || extra != nil {
		m := map[string]any{}
		if ref.u.Model != "" {
			m["model"] = ref.u.Model
		}
		for k, v := range extra {
			m[k] = v
		}
		if err := c.b.Member(e, "usage", m); err != nil {
			return err
		}
	}
	return nil
}

// statsMember is base_state's stats without the per-call usages and
// latencies, which the response entries carry: the totals and the
// costs, which name no response.
func (c *conv) statsMember(s *openhands.State) (json.RawMessage, error) {
	if s.Stats == nil {
		return nil, nil
	}
	out := map[string]json.RawMessage{}
	for _, key := range s.Stats.UsageToMetrics.Keys() {
		m := s.Stats.UsageToMetrics[key]
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		if m.Err == nil && !childMetrics(key) {
			var obj map[string]json.RawMessage
			if json.Unmarshal(raw, &obj) == nil {
				delete(obj, "token_usages")
				delete(obj, "response_latencies")
				if raw, err = json.Marshal(obj); err != nil {
					return nil, err
				}
			}
		}
		out[key] = raw
	}
	all := map[string]json.RawMessage{}
	raw, err := json.Marshal(s.Stats)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	m, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	all["usage_to_metrics"] = m
	return json.Marshal(all)
}

// finish writes the usage no event names as custom entries at the end
// of the leaf's path, as paid calls with no response, and makes the
// leaf the entry OpenHands resumes from.
func (c *conv) finish() error {
	// A call is pending on a path that runs from it to a leaf with no
	// answer: walk up from each leaf, noting the calls answered on the
	// way.
	hasChild := make([]bool, len(c.c.Events))
	for i := range c.c.Events {
		if p := c.parent[i]; p >= 0 && !c.treeArtifact(i) {
			hasChild[p] = true
		}
	}
	reported := map[*call]bool{}
	for leaf := range c.c.Events {
		if hasChild[leaf] || c.treeArtifact(leaf) {
			continue
		}
		answered := map[*call]bool{}
		for j := leaf; j >= 0; j = c.parent[j] {
			if openhands.IsObservationLike(c.info[j].kind) {
				if cl := c.callFor(c.info[j].toolCallID, j); cl != nil {
					answered[cl] = true
				}
			}
			cl := c.callAt[j]
			if cl == nil || answered[cl] || reported[cl] {
				continue
			}
			reported[cl] = true
			c.at(agentimport.Lossy, "openhands/pending-call", j, "no event on a path from it answers call %s: OpenHands sends no call without its answer, and runs or rejects a pending one when the conversation resumes; the session reads it as pending", cl.id)
		}
	}
	leaf := c.b.Last()
	if c.leaf >= 0 && c.tail[c.leaf] != "" {
		leaf = c.tail[c.leaf]
	}
	source, known := c.sourceUsages()
	for _, ref := range c.usage.all {
		if ref.used {
			continue
		}
		use, extra := c.usageOf(ref.u)
		// A fork made with reset_metrics=False holds the source's
		// metrics whole: a call the source made is the source's to
		// count, so it is kept under a member a reader does not count.
		usageKey := "usage"
		if c.forked() {
			copied := !known || source[usageIdentity(ref.u)]
			if copied {
				usageKey = "copied_usage"
				if known {
					c.addf(agentimport.Kept, "openhands/fork-usage", openhands.FileBaseState, 0, "a call of %q in the fork's metrics is one its source made, copied with them; it is kept in a custom entry's copied_usage, not counted", ref.usageID)
				} else {
					c.addf(agentimport.Lossy, "openhands/fork-usage", openhands.FileBaseState, 0, "a call of %q no event names, in a fork whose source's metrics cannot be read, may be a copy of the source's; it is kept in a custom entry's copied_usage, not counted", ref.usageID)
				}
			}
		}
		data := map[string]any{usageKey: use, "usage_id": ref.usageID}
		if ref.u.Model != "" {
			data["model"] = ref.u.Model
		}
		if ref.u.ResponseID != "" {
			data["response_id"] = ref.u.ResponseID
		}
		for k, v := range extra {
			data[k] = v
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		e := &agentsession.CustomEntry{NS: Source, Data: raw}
		id, err := c.add(-1, leaf, e)
		if err != nil {
			return err
		}
		leaf = id
		if usageKey != "usage" {
			continue
		}
		c.addf(agentimport.Kept, "openhands/unmatched-usage", openhands.FileBaseState, 0, "a call of %q that no event names (a title, a refused condensation, an ACP turn) is a custom entry with its usage at the end of the leaf's path", ref.usageID)
	}
	return c.b.Leaf(leaf)
}

// forked reports whether the conversation is a fork, as meta.json says.
func (c *conv) forked() bool {
	return c.c.Meta != nil && c.c.Meta.ForkedFromConversationID != ""
}

// sourceUsages returns the token usages of a fork's source, read from
// its base_state.json beside the fork, by identity; known is false when
// the source cannot be read.
func (c *conv) sourceUsages() (map[string]bool, bool) {
	if !c.forked() || c.c.Dir == "" {
		return nil, false
	}
	dir := filepath.Join(filepath.Dir(c.c.Dir), openhands.HexID(c.c.Meta.ForkedFromConversationID))
	src, err := openhands.ReadState(dir)
	if err != nil || src.Stats == nil {
		return nil, false
	}
	out := map[string]bool{}
	for _, key := range src.Stats.UsageToMetrics.Keys() {
		m := src.Stats.UsageToMetrics[key]
		if m.Err != nil {
			continue
		}
		for _, u := range m.TokenUsages {
			if u != nil {
				out[usageIdentity(u)] = true
			}
		}
	}
	return out, true
}

// usageIdentity is a token usage's members as JSON, which a copy
// repeats.
func usageIdentity(u *openhands.TokenUsage) string {
	raw, _ := json.Marshal(u)
	return string(raw)
}
