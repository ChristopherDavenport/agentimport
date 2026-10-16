// Package convert converts OpenHands SDK conversation directories into
// the Agent Session Format. docs/formats/openhands.md is the mapping
// and lists every finding code it reports.
package convert

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/openhands"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes.
const Source = "openhands"

// Harness is the harness name the sessions carry.
const Harness = "openhands"

// Unit names what a report's tally counts: a conversation is a
// directory of JSON files, one event per file, so each file is a unit.
const Unit = "files"

// maxDepth bounds how deep sub-agents are followed.
const maxDepth = 16

// Options tune a conversion. The zero value keeps everything.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: the
	// state snapshot, events' members the conversion does not map, the
	// structured observations beside their rendered outputs, and the
	// agent server's state updates, token IDs, completion logs and
	// stream fragments. Each drop is still reported.
	Slim bool
	// Location is the zone of OpenHands' timestamps, which are local
	// time written with no zone; nil reads them as UTC. Either is
	// reported.
	Location *time.Location
	// CreatedAt is the session's created_at when a conversation holds
	// no timestamp at all. Zero leaves it the epoch; either is reported.
	CreatedAt time.Time
	// Alone converts a conversation without its sub-agents, which are
	// otherwise read from its subagents directory.
	Alone bool
	// Converted, when set, records the conversation directories each
	// conversion converted, so a caller converting many inputs can skip
	// a sub-agent its parent's conversion already holds.
	Converted *Converted
}

// Converted is the set of conversation directories conversions have
// converted, by their real path. It is safe for concurrent use.
type Converted struct {
	mu   sync.Mutex
	dirs map[string]bool
}

// Has reports whether a conversion converted the conversation whose
// directory, or a file in it, path names.
func (c *Converted) Has(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dirs[conversationReal(path)]
}

func (c *Converted) add(dirs []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dirs == nil {
		c.dirs = map[string]bool{}
	}
	for _, d := range dirs {
		c.dirs[d] = true
	}
}

// conversationReal is the real path of the conversation directory path
// names.
func conversationReal(path string) string {
	if dir, err := openhands.ConversationDir(path); err == nil {
		path = dir
	}
	return realPath(path)
}

// ConvertFile reads the conversation whose directory, or a file in it,
// path names, and converts it with its sub-agents.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	c, err := openhands.ReadConversation(path)
	if err != nil {
		return nil, err
	}
	return Convert(c, opts)
}

// Convert converts a conversation into a session, and each sub-agent
// it ran into one of its own after it. One report covers them all; a
// finding in a sub-agent names its directory. Only the main
// conversation's failure fails the conversion: a sub-agent that cannot
// be read or converted is a finding.
func Convert(c *openhands.Conversation, opts Options) (*agentimport.Result, error) {
	if c == nil {
		return nil, errors.New("openhands: no conversation")
	}
	if opts.Location == nil {
		opts.Location = time.UTC
	}
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	fam := &family{opts: opts, active: map[string]bool{}, ids: map[string]bool{}}
	if c.Dir != "" {
		real := realPath(c.Dir)
		fam.active[real] = true
		fam.held = append(fam.held, real)
	}
	native := nativeID(c)
	id, derived := fam.sessionID(native)
	tgt := target{id: id, native: native, derived: derived}
	if c.Dir != "" {
		if parent, ok := openhands.SubagentParent(c.Dir); ok {
			tgt.alone = parentID(parent)
		}
	}
	cv, err := fam.convert(c, tgt, "", 0, r)
	if err != nil {
		return nil, err
	}
	if opts.Converted != nil {
		opts.Converted.add(fam.held)
	}
	return &agentimport.Result{Report: r, Sessions: cv.sessions()}, nil
}

// family converts a conversation and its sub-agents.
type family struct {
	opts Options
	// active holds the conversation directories being converted, by
	// real path, so a sub-agent naming an ancestor ends; held lists
	// those converted.
	active map[string]bool
	held   []string
	// ids are the session IDs given out.
	ids map[string]bool
}

// target is the session a conversation becomes.
type target struct {
	id string
	// native is the conversation's own ID; derived says why the
	// session ID is not it, empty when it is.
	native, derived string
	// parent and spawnedBy are set for a sub-agent's session; alone is
	// the parent of a sub-agent converted on its own.
	parent, spawnedBy, alone string
}

// convert converts one conversation into its session, and its
// sub-agents into theirs. prefix names the conversation's directory in
// findings: empty for the main one. Its units and findings go in
// report.
func (fam *family) convert(c *openhands.Conversation, tgt target, prefix string, depth int, report *agentimport.Report) (*conv, error) {
	cv := newConv(fam, c, tgt, prefix, depth, report)
	if err := cv.run(); err != nil {
		return nil, fmt.Errorf("%s: %w", displayConv(prefix), err)
	}
	read, err := agentimport.Finish(cv.b.S)
	if err != nil {
		return nil, fmt.Errorf("%s: converted session does not verify: %w", displayConv(prefix), err)
	}
	cv.out = read
	return cv, nil
}

func displayConv(prefix string) string {
	if prefix == "" {
		return "conversation"
	}
	return strings.TrimSuffix(prefix, "/")
}

// nativeID is a conversation's ID: base_state's, else its directory's
// name as a UUID.
func nativeID(c *openhands.Conversation) string {
	if c.State != nil && c.State.ID != "" {
		return c.State.ID
	}
	return dashed(c.Name)
}

// parentID is the ID of the conversation in dir, read from its
// base_state.json, else its directory's name.
func parentID(dir string) string {
	if id := openhands.StateID(dir); id != "" {
		return id
	}
	return dashed(filepath.Base(dir))
}

// dashed writes 32 hex digits as a UUID with dashes, as base_state
// does; anything else is returned as it is.
func dashed(s string) string {
	if len(s) != 32 {
		return s
	}
	if _, err := hex.DecodeString(s); err != nil {
		return s
	}
	s = strings.ToLower(s)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

// sessionID is a session's ID: the conversation's when a session store
// takes it and no other session has it, else one derived from it.
func (fam *family) sessionID(native string) (id, derived string) {
	if storeName(native) && !fam.ids[native] {
		fam.ids[native] = true
		return native, ""
	}
	sum := sha256.Sum256([]byte(native))
	key := "conversation/" + hex.EncodeToString(sum[:])
	id = agentsession.SubsessionID(Source, key)
	for n := 2; fam.ids[id]; n++ {
		id = agentsession.SubsessionID(Source, fmt.Sprintf("%s/%d", key, n))
	}
	fam.ids[id] = true
	if storeName(native) {
		return id, "another session of this conversion has the conversation's ID; the session ID is derived from it"
	}
	return id, "the conversation ID is not a name a session store takes (" + storeRule + "); the session ID is derived from it"
}

// storeRule is the rule storeName keeps, as findings state it.
const storeRule = "letters, digits, '.', '_' and '-', at most 200"

// storeName reports whether a session store takes id as a session's
// name.
func storeName(id string) bool {
	if id == "" || id == "." || id == ".." || len(id) > 200 {
		return false
	}
	for _, c := range id {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
		default:
			return false
		}
	}
	return true
}

// realPath is p made absolute with its symbolic links resolved, or p
// cleaned when it does not exist.
func realPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// readChild reads a sub-agent's directory; its errors name no path.
func readChild(dir string) (*openhands.Conversation, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, errors.New("no such conversation directory")
	}
	c, err := openhands.ReadConversation(dir)
	if err != nil {
		return nil, errors.New("the directory holds no conversation")
	}
	return c, nil
}
