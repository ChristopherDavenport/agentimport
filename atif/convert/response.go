package convert

import (
	"encoding/json"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// response builds an agent step's response entry: its model, its usage
// from the step's metrics, and the number of model calls it stands for.
func (c *docConv) response(i int, s *atif.Step, respID string, calls int64) (*agentsession.ResponseEntry, error) {
	line := i + 1
	e := &agentsession.ResponseEntry{ResponseID: respID, Model: s.ModelName}
	if e.Model == "" {
		e.Model = c.settings.Model
	}
	c.count("atif/response-status", line)
	if calls > 1 {
		e.Attempts = int(calls)
		c.addf(agentimport.Lossy, "atif/llm-call-count", line,
			"the step stands for %d model calls; the response's attempts is %d, though attempts counts retries of one call and llm_call_count calls that may each have produced part of the step", calls, calls)
	}
	if m := s.Metrics; m != nil {
		if m.Err != nil {
			if !c.fam.opts.Slim {
				if err := c.b.Member(e, "metrics", m.Raw); err != nil {
					return nil, err
				}
			}
			c.addf(agentimport.Kept, "atif/mistyped-member", line, "the metrics are not an object; they are kept in %s:metrics", Source)
			return e, nil
		}
		u := c.usage(line, m)
		if s.Copied() && u != nil {
			if err := c.b.Member(e, "usage", u); err != nil {
				return nil, err
			}
		} else {
			e.Usage = u
		}
		if err := c.metricsMember(e, line, m); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// keepMetrics keeps the metrics of a step that writes no response on
// its last item.
func (c *docConv) keepMetrics(entries []agentsession.Entry, i int, s *atif.Step) error {
	if len(entries) == 0 {
		c.addf(agentimport.Dropped, "atif/metrics", i+1, "the metrics of a step with nothing else are left out")
		return nil
	}
	if s.Metrics.Err != nil {
		return c.b.Member(entries[len(entries)-1], "metrics", s.Metrics.Raw)
	}
	raw, err := json.Marshal(s.Metrics)
	if err != nil {
		return err
	}
	if c.fam.opts.Slim {
		c.addf(agentimport.Dropped, "atif/metrics", i+1, "the metrics of a step with no response are left out")
		return nil
	}
	c.addf(agentimport.Kept, "atif/metrics", i+1, "the metrics of a step with no response are in %s:metrics", Source)
	return c.b.Member(entries[len(entries)-1], "metrics", json.RawMessage(raw))
}

// usage converts metrics to Responses usage: prompt_tokens counts
// cached tokens already, completion_tokens reasoning tokens; reasoning
// tokens are read from the extra keys producers write them under.
func (c *docConv) usage(line int, m *atif.Metrics) *openresponses.Usage {
	prompt, pok := c.count64(line, "prompt_tokens", m.PromptTokens)
	completion, cok := c.count64(line, "completion_tokens", m.CompletionTokens)
	cached, _ := c.count64(line, "cached_tokens", m.CachedTokens)
	if !pok && !cok {
		return nil
	}
	u := &openresponses.Usage{InputTokens: int(prompt), OutputTokens: int(completion)}
	u.InputTokensDetails.CachedTokens = int(cached)
	ext := object(m.Ext)
	for _, raw := range []json.RawMessage{ext["reasoning_tokens"], ext["reasoning_output_tokens"], object(ext["completion_tokens_details"])["reasoning_tokens"]} {
		if n, _, ok := atif.Int(raw); ok {
			u.OutputTokensDetails.ReasoningTokens = int(n)
			break
		}
	}
	u.TotalTokens = u.InputTokens + u.OutputTokens
	return u
}

// count64 reads a token count, reporting one that is not a plain
// integer.
func (c *docConv) count64(line int, name string, raw json.RawMessage) (int64, bool) {
	if atif.IsNull(raw) {
		return 0, false
	}
	n, plain, ok := atif.Int(raw)
	switch {
	case !ok:
		c.addf(agentimport.Lossy, "atif/metrics-invalid", line, "%s is %s, not a count; it is not in the usage, and is kept in %s:metrics", name, string(raw), Source)
	case !plain:
		c.addf(agentimport.Repaired, "atif/metrics-count", line, "%s is written %s; it is read as %d, as Harbor's models read it", name, string(raw), n)
	}
	return n, ok
}

// metricsMember keeps what the metrics hold beside the three counts
// read: the cost, the extra, the token ids and logprobs, members ATIF
// does not declare, and a count that is not one. With Slim they are
// dropped.
func (c *docConv) metricsMember(e agentsession.Entry, line int, m *atif.Metrics) error {
	rest := map[string]json.RawMessage{}
	for k, v := range map[string]json.RawMessage{
		"cost_usd": m.CostUSD, "extra": m.Ext, "prompt_token_ids": m.PromptTokenIDs,
		"completion_token_ids": m.CompletionTokenIDs, "logprobs": m.Logprobs,
	} {
		if !atif.IsNull(v) {
			rest[k] = v
		}
	}
	for k, v := range m.Members.Extra {
		rest[k] = v
	}
	// A count that is no count is kept as written.
	for k, v := range map[string]json.RawMessage{"prompt_tokens": m.PromptTokens, "completion_tokens": m.CompletionTokens, "cached_tokens": m.CachedTokens} {
		if _, _, ok := atif.Int(v); !ok && !atif.IsNull(v) {
			rest[k] = v
		}
	}
	if len(rest) == 0 {
		return nil
	}
	if _, ok := rest["cost_usd"]; ok || rest["extra"] != nil {
		c.addf(agentimport.Gap, "agentimport/usage-detail", line, "Responses usage has no cost and no place for the producer's extra counts; they are in %s:metrics", Source)
	}
	if c.fam.opts.Slim {
		c.addf(agentimport.Dropped, "atif/metrics", line, "the metrics beside the token counts (cost, extra, token ids, logprobs) are left out")
		return nil
	}
	if len(m.Members.Extra) > 0 {
		c.reportMembers(line, "metrics", m.Members.Extra, m.Mistyped())
	}
	c.addf(agentimport.Kept, "atif/metrics", line, "the metrics beside the token counts are in %s:metrics", Source)
	return c.b.Member(e, "metrics", rest)
}
