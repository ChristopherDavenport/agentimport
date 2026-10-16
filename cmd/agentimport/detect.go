package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	atifconvert "github.com/ChristopherDavenport/agentimport/atif/convert"
	"github.com/ChristopherDavenport/agentimport/claudecode"
	ccconvert "github.com/ChristopherDavenport/agentimport/claudecode/convert"
	"github.com/ChristopherDavenport/agentimport/cline"
	clineconvert "github.com/ChristopherDavenport/agentimport/cline/convert"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	sdkconvert "github.com/ChristopherDavenport/agentimport/clinesdk/convert"
	sdksqlite "github.com/ChristopherDavenport/agentimport/clinesdk/sqlite"
	"github.com/ChristopherDavenport/agentimport/codex"
	codexconvert "github.com/ChristopherDavenport/agentimport/codex/convert"
	"github.com/ChristopherDavenport/agentimport/opencode"
	occonvert "github.com/ChristopherDavenport/agentimport/opencode/convert"
	"github.com/ChristopherDavenport/agentimport/opencode/sqlite"
	"github.com/ChristopherDavenport/agentimport/openhands"
	ohconvert "github.com/ChristopherDavenport/agentimport/openhands/convert"
	"github.com/ChristopherDavenport/agentimport/pi"
	piconvert "github.com/ChristopherDavenport/agentimport/pi/convert"
)

// source is one native format the command converts.
type source struct {
	name string
	// find returns the sessions under a directory, or the file itself.
	find    func(root string) ([]string, error)
	convert func(path string, r *runner) (*agentimport.Result, error)
}

var sources = []source{
	{
		name: ccconvert.Source,
		find: claudecode.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return ccconvert.ConvertFile(path, ccconvert.Options{Slim: r.slim})
		},
	},
	{
		name: piconvert.Source,
		find: pi.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return piconvert.ConvertFile(path, piconvert.Options{Slim: r.slim})
		},
	},
	{
		name: codexconvert.Source,
		find: codex.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return r.convertCodex(path)
		},
	},
	{
		name: atifconvert.Source,
		find: atif.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return atifconvert.ConvertFile(path, atifconvert.Options{Slim: r.slim, Converted: r.atifDone})
		},
	},
	{
		name: clineconvert.Source,
		find: cline.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return clineconvert.ConvertFile(path, clineconvert.Options{Slim: r.slim, Converted: r.clineDone})
		},
	},
	{
		name: sdkconvert.Source,
		find: clinesdk.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return sdkconvert.ConvertFile(path, sdkconvert.Options{Slim: r.slim, OpenIndex: sdksqlite.OpenIndex})
		},
	},
	{
		name: ohconvert.Source,
		find: openhands.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return ohconvert.ConvertFile(path, ohconvert.Options{Slim: r.slim, Converted: r.openhandsDone})
		},
	},
	{
		name: occonvert.Source,
		find: opencode.FindSessions,
		convert: func(path string, r *runner) (*agentimport.Result, error) {
			return occonvert.ConvertFile(path, occonvert.Options{Slim: r.slim, OpenDatabase: sqlite.OpenStore})
		},
	},
}

func sourceNamed(name string) (source, bool) {
	for _, s := range sources {
		if s.name == name {
			return s, true
		}
	}
	return source{}, false
}

// fromValues is what -from takes.
func fromValues() string {
	names := []string{"auto"}
	for _, s := range sources {
		names = append(names, s.name)
	}
	return strings.Join(names, "|")
}

// input is one session to convert.
type input struct {
	path   string
	source source
}

// skipped is a file found in a directory that is not a session to
// convert, and why.
type skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// errUnknown is a file whose source could not be told.
var errUnknown = errors.New("not a session of any source this command reads; name one with -from")

// collect turns the arguments into the sessions to convert. With a
// source named, a directory is searched with that source's finder and a
// file is taken as that source's. With auto, a directory is searched
// with every finder and each file found is detected; a Claude Code
// sub-agent transcript, part of its main session, is skipped.
func collect(args []string, from string) ([]input, []skipped, error) {
	var (
		ins  []input
		skip []skipped
		seen = map[string]bool{}
	)
	add := func(path string, s source) {
		if s.name == occonvert.Source {
			// An OpenCode store is converted whole, whichever of its
			// files names it.
			if root, ok := opencode.StoreOf(path); ok {
				path = root
			}
		}
		if s.name == sdkconvert.Source {
			// A Cline SDK session is a directory, its children's
			// histories in it, whichever of its files names it.
			if main := clinesdk.MainFile(path); main != "" {
				path = main
			}
		}
		if s.name == ohconvert.Source {
			// An OpenHands conversation is a directory, whichever of
			// its files names it.
			if dir, err := openhands.ConversationDir(path); err == nil {
				path = dir
			}
		}
		if !seen[path] {
			seen[path] = true
			ins = append(ins, input{path, s})
		}
	}
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, nil, err
		}
		if from != "auto" {
			s, _ := sourceNamed(from)
			paths, err := s.find(arg)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range paths {
				add(p, s)
			}
			continue
		}
		if !info.IsDir() {
			s, err := detect(arg)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: %w", arg, err)
			}
			add(arg, s)
			continue
		}
		// Every finder's candidates, each detected.
		var candidates []string
		for _, s := range sources {
			paths, err := s.find(arg)
			if err != nil {
				return nil, nil, err
			}
			candidates = append(candidates, paths...)
		}
		slices.Sort(candidates)
		for _, p := range slices.Compact(candidates) {
			if seen[p] {
				continue
			}
			s, err := detect(p)
			switch {
			case err != nil:
				skip = append(skip, skipped{p, err.Error()})
			case s.name == ccconvert.Source && isSubagent(p):
				skip = append(skip, skipped{p, "a Claude Code sub-agent transcript, converted with its main session"})
			default:
				add(p, s)
			}
		}
	}
	return ins, skip, nil
}

// isSubagent reports whether a path is a Claude Code sub-agent's
// transcript, <session>/subagents/agent-<id>.jsonl, which is converted
// with its main session.
func isSubagent(p string) bool {
	return filepath.Base(filepath.Dir(p)) == "subagents" && strings.HasPrefix(filepath.Base(p), "agent-")
}

// probeLines is how many lines detection reads; probeLineSize the
// longest line it parses. Claude Code writes side records with no
// session envelope first, and lines of megabytes.
const (
	probeLines    = 20
	probeLineSize = 8 << 20
)

// detect tells a file's source from its name and first lines:
//   - a Codex rollout by its rollout-*.jsonl name, a first line of type
//     session_meta, a record_type state line, or the 2025 bare layout's
//     first line, an object with an id and a timestamp and no type;
//   - a pi session by a first line of type session that is not an
//     agentsession header;
//   - a Claude Code transcript by a sessionId member on one of its
//     first lines;
//   - an ATIF document by a .json name and a schema_version starting
//     "ATIF-v", which a document of any layout carries before its
//     steps or after them;
//   - a classic Cline or Roo Code task by the name of its API history
//     or UI messages in a task directory, tasks/<id>/;
//   - a Cline SDK session by its directory, holding <name>.messages.json
//     or a manifest naming it, or by one of its files, a child's
//     history among them;
//   - an OpenHands conversation by its directory, holding a
//     base_state.json or an event file, or by one of those files;
//   - an OpenCode store by its data directory, holding opencode.db or a
//     storage/ tree of sessions, by a file in one, or by a database
//     file, each holding OpenCode's tables: Crush's .opencode/opencode.db
//     is another program's.
//
// An agentsession file, which is already converted, is an error.
func detect(path string) (source, error) {
	if root, ok := opencode.StoreOf(path); ok && isOpenCode(root) {
		s, _ := sourceNamed(occonvert.Source)
		return s, nil
	}
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		if openhands.IsConversationDir(path) {
			s, _ := sourceNamed(ohconvert.Source)
			return s, nil
		}
		if clinesdk.MainFile(path) != "" {
			s, _ := sourceNamed(sdkconvert.Source)
			return s, nil
		}
		return source{}, errUnknown
	}
	if clinesdk.IsSessionFile(path) && clinesdk.MainFile(path) != "" {
		s, _ := sourceNamed(sdkconvert.Source)
		return s, nil
	}
	if openhands.IsConversationFile(path) {
		if dir, err := openhands.ConversationDir(path); err == nil && openhands.IsConversationDir(dir) {
			s, _ := sourceNamed(ohconvert.Source)
			return s, nil
		}
	}
	cx, _ := sourceNamed(codexconvert.Source)
	if codex.IDFromFileName(path) != "" {
		return cx, nil
	}
	if cline.IsTaskFile(path) {
		s, _ := sourceNamed(clineconvert.Source)
		return s, nil
	}
	if strings.HasSuffix(path, ".json") {
		if _, ok := atif.Probe(path); ok {
			s, _ := sourceNamed(atifconvert.Source)
			return s, nil
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return source{}, err
	}
	defer f.Close()
	br := bufio.NewReaderSize(f, 1<<16)
	first := true
	for range probeLines {
		line, err := readProbe(br)
		if obj := parseObject(line); obj != nil {
			typ := stringMember(obj, "type")
			if first {
				first = false
				switch {
				case typ == "session" && strings.HasPrefix(stringMember(obj, "format"), "agentsession/"):
					return source{}, errors.New("already an agentsession file")
				case typ == "session":
					s, _ := sourceNamed(piconvert.Source)
					return s, nil
				case typ == "session_meta" && obj["payload"] != nil:
					return cx, nil
				case typ == "" && obj["id"] != nil && obj["timestamp"] != nil && obj["sessionId"] == nil:
					return cx, nil
				}
			}
			if obj["record_type"] != nil {
				return cx, nil
			}
			if obj["sessionId"] != nil {
				s, _ := sourceNamed(ccconvert.Source)
				return s, nil
			}
		}
		if err != nil {
			break
		}
	}
	return source{}, errUnknown
}

// isOpenCode reports whether a store StoreOf names holds OpenCode's
// sessions: a database of its tables, or a data directory holding one
// or a storage/ tree of sessions. The answer is kept per store, since a
// directory search detects every storage file of one.
func isOpenCode(root string) bool {
	openCodeStores.Lock()
	defer openCodeStores.Unlock()
	if ok, seen := openCodeStores.m[root]; seen {
		return ok
	}
	ok := probeOpenCode(root)
	openCodeStores.m[root] = ok
	return ok
}

var openCodeStores = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

func probeOpenCode(root string) bool {
	info, err := os.Stat(root)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return probeDatabase(root)
	}
	db := filepath.Join(root, opencode.DatabaseName)
	if _, err := os.Stat(db); err == nil && probeDatabase(db) {
		return true
	}
	info, err = os.Stat(filepath.Join(root, opencode.DirStorage, opencode.DirSession))
	return err == nil && info.IsDir()
}

// probeDatabase reads a database's schema from the checkpointed file;
// a database whose schema is still in its write-ahead log, as one a
// running OpenCode created may be, is read from a copy instead.
func probeDatabase(path string) bool {
	if sqlite.Probe(path) {
		return true
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		return false
	}
	db, err := sqlite.Open(path)
	if err != nil {
		return false
	}
	db.Close()
	return true
}

// readProbe reads one line, giving nil for a line over probeLineSize.
func readProbe(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	over := false
	for {
		frag, err := br.ReadSlice('\n')
		if !over {
			if len(buf)+len(frag) > probeLineSize {
				over, buf = true, nil
			} else {
				buf = append(buf, frag...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}

func parseObject(line []byte) map[string]json.RawMessage {
	line = bytes.TrimSpace(bytes.ReplaceAll(line, []byte{0}, nil))
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(line, &obj) != nil {
		return nil
	}
	return obj
}

func stringMember(obj map[string]json.RawMessage, name string) string {
	var s string
	_ = json.Unmarshal(obj[name], &s)
	return s
}

// displayPath shortens a path under the working directory for output.
func displayPath(p string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}
