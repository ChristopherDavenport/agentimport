package convert

import (
	"encoding/json"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/agentsession"
)

// Harbor's Claude Code adapter flattens a session and its sub-agents
// into one trajectory, sorted by time: a sub-agent's steps carry
// extra.is_sidechain and extra.agent_id (claude_code.py). They are not
// the main model's context, so the conversion takes them out of the
// main session, one session per agent: a subsession of the Task call
// whose result names the agent (tool_result_metadata.tool_use_result
// .agentId), linked as the module's Claude Code converter links it, or
// a session with parent_session alone.

// sidechain is one sub-agent's steps.
type sidechain struct {
	id    string
	steps []int
	done  bool
}

// splitSidechains takes a claude-code document's sidechain steps out of
// the main conversion, by agent, and finds the calls that started them.
func (c *docConv) splitSidechains() {
	// A sub-agent's own document is its sidechain steps; splitting it
	// again would convert it into itself without end.
	if c.agentName() != "claude-code" || c.in.synthetic {
		return
	}
	for i, s := range c.t.Steps {
		if s == nil || s.Err != nil {
			continue
		}
		ext := object(s.Ext)
		var side bool
		if json.Unmarshal(ext["is_sidechain"], &side) != nil || !side {
			continue
		}
		id := str(ext["agent_id"])
		sc := c.sidechains[id]
		if sc == nil {
			sc = &sidechain{id: id}
			if c.sidechains == nil {
				c.sidechains = map[string]*sidechain{}
			}
			c.sidechains[id] = sc
			c.sidechainOrder = append(c.sidechainOrder, sc)
		}
		sc.steps = append(sc.steps, i)
		if c.moved == nil {
			c.moved = map[int]bool{}
		}
		c.moved[i] = true
	}
	if len(c.sidechains) == 0 {
		return
	}
	c.spawnedBy = map[string]spawnRecord{}
	for i, s := range c.t.Steps {
		if s == nil || s.Err != nil || c.moved[i] || s.Observation == nil || s.Observation.Err != nil {
			continue
		}
		for _, r := range s.Observation.Results {
			if r == nil || r.Err != nil || r.SourceCallID == "" {
				continue
			}
			res := object(object(object(r.Ext)["tool_result_metadata"])["tool_use_result"])
			id := str(res["agentId"])
			if id == "" {
				id = str(res["agent_id"])
			}
			if c.sidechains[id] != nil {
				status := str(res["status"])
				c.spawnedBy[r.SourceCallID] = spawnRecord{agent: id, waited: status == "" || status == "completed"}
			}
		}
	}
}

// spawnRecord is the agent a call's result names, and whether the call
// waited for it.
type spawnRecord struct {
	agent  string
	waited bool
}

// sidechainDoc is a sub-agent's steps as a document of their own.
func (c *docConv) sidechainDoc(sc *sidechain) docInput {
	t := &atif.Trajectory{SchemaVersion: c.t.SchemaVersion, Agent: c.t.Agent}
	for _, i := range sc.steps {
		t.Steps = append(t.Steps, c.t.Steps[i])
	}
	name := "sidechain"
	if sc.id != "" {
		name += "-" + sc.id
	}
	return docInput{t: t, name: c.in.name + "#" + name, dir: c.in.dir, synthetic: true, pos: sc.steps}
}

// sidechainChild converts a sub-agent's steps into its session: a
// subsession of callID, a peer of it when the call did not wait, or,
// with none, a session with parent_session alone. It returns the
// session's ID and leaf.
func (c *docConv) sidechainChild(sc *sidechain, callID string, peer bool, created time.Time) (string, string, bool) {
	sc.done = true
	in := c.sidechainDoc(sc)
	t := target{parent: c.tgt.id, created: created}
	switch {
	case callID != "" && !peer:
		t.id, t.spawnedBy = agentsession.SubsessionID(c.tgt.id, callID), callID
		c.fam.ids[t.id] = true
	case callID != "":
		t.id = agentsession.SubsessionID(c.tgt.id, callID)
		c.fam.ids[t.id] = true
	default:
		t.id, _ = c.fam.claim("", c.tgt.id, "sidechain/"+sc.id)
	}
	how := "a session with parent_session alone: no call's result names the agent"
	switch {
	case callID != "" && !peer:
		how = "a subsession of call " + callID + ", whose result names the agent"
	case callID != "":
		how = "a peer of call " + callID + ", which started the agent and did not wait for it"
	}
	c.addf(agentimport.Lossy, "atif/sidechain", sc.steps[0]+1,
		"Harbor's Claude Code adapter interleaves sub-agent %s's %d steps with the main session's by time; they are not the main model's context and are session %s, %s", sc.id, len(sc.steps), t.id, how)
	sessions, leaf, err := c.fam.child(in, t, c.report, c.in.name)
	if err != nil {
		return "", "", false
	}
	c.children = append(c.children, sessions...)
	return t.id, leaf, true
}

// unlinkedSidechains converts the sub-agents no call's result named.
func (c *docConv) unlinkedSidechains() {
	for _, sc := range c.sidechainOrder {
		if !sc.done {
			c.sidechainChild(sc, "", false, c.last)
		}
	}
}
