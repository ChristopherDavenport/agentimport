package convert

import "maps"

// pairing pairs tool rows with calls as Hermes pairs them before it
// sends (agent/agent_runtime_helpers.py _drop_stray_tool_results): a
// result belongs to a call of the assistant turn just before it, named
// by any of the call's aliases, each call answered once; an assistant
// or a user turn starts a new run. A row that pairs with no call of its
// run still names a call when an earlier one has its alias, so the
// requests before a resume read it as Hermes sent it; Hermes drops it
// only when it rebuilds the history, which repair reproduces.
type pairing struct {
	// known maps an alias of a call of the current run to the call's
	// group, matched marks the groups answered, and calls holds each
	// group's call ID.
	known   map[string]int
	matched map[int]bool
	calls   []string
	// latest maps an alias to the call ID of the latest call of it.
	latest map[string]string
}

func newPairing() *pairing {
	return &pairing{known: map[string]int{}, matched: map[int]bool{}, latest: map[string]string{}}
}

// clone copies the state, for a branch of inactive rows to work on.
func (p *pairing) clone() *pairing {
	return &pairing{known: maps.Clone(p.known), matched: maps.Clone(p.matched), calls: append([]string(nil), p.calls...), latest: maps.Clone(p.latest)}
}

// turn starts a new run: an assistant or a user turn.
func (p *pairing) turn() {
	p.known, p.matched, p.calls = map[string]int{}, map[int]bool{}, nil
}

// call records a call of the current assistant turn by its aliases.
func (p *pairing) call(callID string, aliases []string) {
	g := len(p.calls)
	p.calls = append(p.calls, callID)
	for _, a := range aliases {
		if _, ok := p.known[a]; !ok {
			p.known[a] = g
		}
		p.latest[a] = callID
	}
}

// result pairs a tool row's tool_call_id: the call of the current run
// it names, unanswered so far, the first such; inRun is false when it
// names none, and callID is then the latest call of an alias it names,
// if any.
func (p *pairing) result(aliases []string) (callID string, inRun bool) {
	best := -1
	for _, a := range aliases {
		if g, ok := p.known[a]; ok && !p.matched[g] && (best < 0 || g < best) {
			best = g
		}
	}
	if best >= 0 {
		p.matched[best] = true
		return p.calls[best], true
	}
	for _, a := range aliases {
		if id, ok := p.latest[a]; ok {
			return id, false
		}
	}
	return "", false
}
