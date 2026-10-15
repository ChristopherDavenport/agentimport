// Package convert converts Claude Code sessions into the Agent Session
// Format. docs/formats/claudecode.md is the mapping and lists every
// finding code it reports.
package convert

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "claudecode"

// Harness is the harness name converted sessions record.
const Harness = "claude-code"

// Options tune a conversion. The zero value keeps everything.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members and
	// custom entries: the toolUseResult sidecar, line members the
	// converter does not map, attachment payloads beside their
	// rendering, the request log and file-history checkpoints. Each
	// drop is still reported.
	Slim bool
}

// ConvertFile reads the session whose main transcript is path, with its
// sub-agents, and converts it.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	s, err := claudecode.Open(path)
	if err != nil {
		return nil, err
	}
	return Convert(s, opts)
}

// Convert converts a session: the main transcript becomes the first
// session of the result, and each sub-agent a session of its own after
// the session that spawned it.
func Convert(s *claudecode.Session, opts Options) (*agentimport.Result, error) {
	cv := &conversion{opts: opts, report: agentimport.NewReport(Source), session: s}
	cv.children = map[string][]*claudecode.Agent{}
	cv.done = map[string]bool{}
	for _, a := range s.Agents {
		if a.Transcript == nil {
			cv.report.Addf(agentimport.Dropped, "claudecode/agent-meta-only", agentFile(a.ID), 0,
				"sub-agent %s has a meta file and no transcript", a.ID)
			continue
		}
		parent := ""
		if a.Meta != nil {
			parent = a.Meta.ParentAgentID
		}
		cv.children[parent] = append(cv.children[parent], a)
	}
	cv.sessionOf = map[string]string{}
	cv.transcripts = map[string]*claudecode.Transcript{}
	for _, a := range s.Agents {
		if a.Transcript != nil {
			cv.transcripts[a.ID] = a.Transcript
		}
	}
	sessions, _, err := cv.transcript(s.Main, "", target{id: s.ID})
	if err != nil {
		return nil, err
	}
	// An agent no converted transcript linked to a call is a session of
	// its own, provenance alone naming the session it came from. One
	// whose parent agent is waiting goes after it, so the parent can
	// link it.
	for {
		progress := false
		for _, a := range s.Agents {
			if a.Transcript == nil || cv.done[a.ID] {
				continue
			}
			if p := parentOf(a); p != "" && cv.waiting(p) {
				continue
			}
			// The session it came from: its parent agent's, when that
			// was converted, else the main one.
			parentID, parentTr := s.ID, s.Main
			if p := parentOf(a); p != "" && cv.sessionOf[p] != "" {
				parentID, parentTr = cv.sessionOf[p], cv.transcripts[p]
			}
			cv.report.Addf(agentimport.Lossy, "claudecode/agent-unlinked", agentFile(a.ID), 0,
				"no call in the transcripts spawned sub-agent %s; it is a session with parent_session alone", a.ID)
			more, _, err := cv.transcript(a.Transcript, agentFile(a.ID), target{
				id: agentsession.SubsessionID(parentID, "agent-"+a.ID), parent: parentID, agent: a, parentTr: parentTr,
			})
			if err != nil {
				return nil, err
			}
			sessions = append(sessions, more...)
			progress = true
		}
		if !progress {
			break
		}
	}
	return &agentimport.Result{Report: cv.report, Sessions: sessions}, nil
}

func parentOf(a *claudecode.Agent) string {
	if a.Meta == nil {
		return ""
	}
	return a.Meta.ParentAgentID
}

// waiting reports whether agent id has a transcript not yet converted,
// whose conversion may link its children.
func (cv *conversion) waiting(id string) bool {
	for _, a := range cv.session.Agents {
		if a.ID == id {
			return a.Transcript != nil && !cv.done[a.ID]
		}
	}
	return false
}

// conversion is one Convert call across the main transcript and its
// agents.
type conversion struct {
	opts    Options
	report  *agentimport.Report
	session *claudecode.Session
	// children lists the agents by the agent that spawned them, "" for
	// the main transcript.
	children map[string][]*claudecode.Agent
	done     map[string]bool
	// sessionOf maps an agent to the session it became, and
	// transcripts to its transcript.
	sessionOf   map[string]string
	transcripts map[string]*claudecode.Transcript
}

// target is the session a transcript becomes.
type target struct {
	id string
	// parent and spawnedBy are set for an agent's session.
	parent    string
	spawnedBy string
	agent     *claudecode.Agent
	// parentTr is the transcript of the session that spawned the
	// agent, whose responses a fork's transcript opens with a copy of.
	parentTr *claudecode.Transcript
}

func agentFile(id string) string { return "subagents/agent-" + id + ".jsonl" }

// transcript converts one transcript into the session t names, and
// each agent its calls spawned. It returns the session and then every
// session it spawned, each before its own descendants, and the entry
// the session's leaf is.
func (cv *conversion) transcript(tr *claudecode.Transcript, file string, t target) ([]*agentsession.Session, string, error) {
	if t.agent != nil {
		cv.done[t.agent.ID] = true
		cv.sessionOf[t.agent.ID] = t.id
	}
	h, err := cv.header(tr, file, t)
	if err != nil {
		return nil, "", err
	}
	b := agentimport.NewBuilder(h, Source, cv.report)
	b.File = file
	c := newConverter(cv, tr, file, t, b)
	if err := c.run(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", displayFile(file), err)
	}
	if err := agentimport.Verify(b.S); err != nil {
		return nil, "", fmt.Errorf("%s: converted session does not verify: %w", displayFile(file), err)
	}
	out := []*agentsession.Session{b.S}
	for _, ch := range c.spawned {
		if ch.sessions == nil {
			more, _, err := cv.transcript(ch.agent.Transcript, agentFile(ch.agent.ID), target{
				id: ch.id, parent: t.id, agent: ch.agent, parentTr: tr,
			})
			if err != nil {
				return nil, "", err
			}
			ch.sessions = more
		}
		out = append(out, ch.sessions...)
	}
	return out, b.Resume(), nil
}

func displayFile(file string) string {
	if file == "" {
		return "main transcript"
	}
	return file
}

// header builds a session header from a transcript: the earliest
// timestamp, the first working directory and version, the source.
func (cv *conversion) header(tr *claudecode.Transcript, file string, t target) (agentsession.Header, error) {
	h := agentsession.Header{ID: t.id, ParentSession: t.parent, SpawnedBy: t.spawnedBy}
	var created time.Time
	for _, l := range tr.Lines {
		if ts := l.Time(); !ts.IsZero() && (created.IsZero() || ts.Before(created)) {
			created = ts
		}
		if h.CWD == "" && l.CWD != "" {
			h.CWD = l.CWD
		}
		if h.Harness == nil && l.Version != "" {
			h.Harness = &agentsession.Harness{Name: Harness, Version: l.Version}
		}
	}
	if created.IsZero() {
		// A transcript with no timestamp at all: the session format
		// needs one, and the epoch says it was not known.
		created = time.Unix(0, 0).UTC()
		cv.report.Addf(agentimport.Lossy, "claudecode/no-timestamp", file, 0, "the transcript has no timestamps; created_at is the epoch")
	}
	h.CreatedAt = created
	if h.Harness == nil {
		h.Harness = &agentsession.Harness{Name: Harness}
	}
	src := agentimport.Source{Format: Source}
	if t.agent != nil {
		src.NativeID = t.agent.ID
	}
	if err := agentimport.SetSource(&h, src); err != nil {
		return h, err
	}
	cv.report.Addf(agentimport.Gap, "agentimport/provenance", file, 0,
		"the import is recorded in the header member %s", agentimport.SourceMember)
	if e, ok := firstEnvelope(tr); ok && (e.Entrypoint != "" || e.UserType != "") {
		env := map[string]string{}
		if e.Entrypoint != "" {
			env["entrypoint"] = e.Entrypoint
		}
		if e.UserType != "" {
			env["userType"] = e.UserType
		}
		raw, err := json.Marshal(env)
		if err != nil {
			return h, err
		}
		h.Extra[Source+":envelope"] = raw
	}
	if t.agent != nil && t.agent.Meta == nil && t.agent.MetaErr != nil {
		if json.Valid(t.agent.MetaRaw) && !cv.opts.Slim {
			raw, _, err := agentimport.CleanJSON(t.agent.MetaRaw)
			if err != nil {
				return h, err
			}
			h.Extra[Source+":agent_meta"] = raw
			cv.report.Addf(agentimport.Kept, "claudecode/agent-meta-undecodable", file, 0,
				"the sub-agent's meta file does not decode (%v); it is the header member %s:agent_meta, and the agent is linked as if it had none", t.agent.MetaErr, Source)
		} else {
			cv.report.Addf(agentimport.Dropped, "claudecode/agent-meta-undecodable", file, 0,
				"the sub-agent's meta file does not decode (%v) and is left out", t.agent.MetaErr)
		}
	}
	if t.agent != nil && t.agent.Meta != nil {
		if cv.opts.Slim {
			cv.report.Addf(agentimport.Dropped, "claudecode/agent-meta", file, 0, "the sub-agent's meta file (type, description, model) is left out")
		} else {
			raw, _, err := agentimport.CleanJSON(t.agent.Meta.Raw)
			if err != nil {
				return h, err
			}
			h.Extra[Source+":agent_meta"] = raw
			cv.report.Addf(agentimport.Kept, "claudecode/agent-meta", file, 0, "the sub-agent's meta file is the header member %s:agent_meta", Source)
		}
	}
	return h, nil
}
