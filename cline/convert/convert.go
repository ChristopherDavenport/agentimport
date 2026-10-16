// Package convert converts classic Cline and Roo Code task directories
// into the Agent Session Format. docs/formats/cline.md is the mapping
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
	"github.com/ChristopherDavenport/agentimport/cline"
	"github.com/ChristopherDavenport/agentsession"
)

// Source is the format name and the namespace of every extension
// member and custom entry this converter writes, for both variants.
const Source = "cline"

// Harness names, per variant.
const (
	HarnessClassic = "cline"
	HarnessRoo     = "roo-code"
)

// Unit names what a report's tally counts: a task is JSON files, not
// lines, so each element of its arrays and each other file is a unit.
const Unit = "elements and files"

// maxDepth bounds how deep sub-tasks are followed.
const maxDepth = 32

// Options tune a conversion. The zero value keeps everything.
type Options struct {
	// Slim drops what the model never saw and the session format has
	// no member for, rather than keeping it in namespaced members: the
	// task's index entry, metadata and settings, members the conversion
	// does not map, and UI records the API history does not hold. Each
	// drop is still reported. Context edits, which the model saw, are
	// kept.
	Slim bool
	// CreatedAt is the session's created_at when a task holds no
	// timestamp at all. Zero leaves it the epoch; either is reported.
	CreatedAt time.Time
	// Alone converts a task without the sub-tasks it delegated to,
	// which are otherwise read from the task directories beside it.
	Alone bool
	// Converted, when set, records the task directories each conversion
	// converted, so a caller converting many inputs can skip a sub-task
	// its parent's conversion already holds.
	Converted *Converted
}

// Converted is the set of task directories conversions have converted,
// by their real path. It is safe for concurrent use.
type Converted struct {
	mu   sync.Mutex
	dirs map[string]bool
}

// Has reports whether a conversion converted the task whose directory,
// or a file in it, path names.
func (c *Converted) Has(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dirs[taskReal(path)]
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

// taskReal is the real path of the task directory path names.
func taskReal(path string) string {
	if dir, err := cline.TaskDir(path); err == nil {
		path = dir
	}
	return realPath(path)
}

// ConvertFile reads the task whose directory, or a file in it, path
// names, and converts it with the sub-tasks it delegated to.
func ConvertFile(path string, opts Options) (*agentimport.Result, error) {
	t, err := cline.ReadTask(path)
	if err != nil {
		return nil, err
	}
	return Convert(t, opts)
}

// Convert converts a task into a session, and each sub-task it waited
// for into one of its own after it. One report covers them all; a
// finding in a sub-task names its directory. Only the main task's
// failure fails the conversion: a sub-task that cannot be read or
// converted is a finding.
func Convert(t *cline.Task, opts Options) (*agentimport.Result, error) {
	if t == nil {
		return nil, errors.New("cline: no task")
	}
	r := agentimport.NewReport(Source)
	r.Unit = Unit
	fam := &family{opts: opts, active: map[string]bool{}, ids: map[string]bool{}}
	if t.Dir != "" {
		real := realPath(t.Dir)
		fam.active[real] = true
		fam.held = append(fam.held, real)
	}
	id, derived := fam.mainID(t.ID)
	sessions, _, err := fam.task(t, target{id: id, derived: derived}, "", 0, r)
	if err != nil {
		return nil, err
	}
	if opts.Converted != nil {
		opts.Converted.add(fam.held)
	}
	return &agentimport.Result{Report: r, Sessions: sessions}, nil
}

// family converts a task and the sub-tasks it waited for.
type family struct {
	opts Options
	// active holds the task directories being converted, by real path,
	// so a sub-task naming an ancestor ends; held lists those converted.
	active map[string]bool
	held   []string
	// ids are the session IDs given out.
	ids map[string]bool
}

// target is the session a task becomes.
type target struct {
	id string
	// derived says why the session ID is not the task's, empty when it
	// is.
	derived string
	// parent and spawnedBy are set for a sub-task's session.
	parent, spawnedBy string
}

// task converts one task into its session, and the sub-tasks it waited
// for into theirs. It returns the session, then the sub-tasks', and the
// entry the session's leaf is. prefix names the task's directory in
// findings: empty for the main task. Its units and findings go in
// report.
func (fam *family) task(t *cline.Task, tgt target, prefix string, depth int, report *agentimport.Report) ([]*agentsession.Session, string, error) {
	c := newConv(fam, t, tgt, prefix, depth, report)
	if err := c.run(); err != nil {
		return nil, "", fmt.Errorf("%s: %w", displayTask(prefix), err)
	}
	read, err := agentimport.Finish(c.b.S)
	if err != nil {
		return nil, "", fmt.Errorf("%s: converted session does not verify: %w", displayTask(prefix), err)
	}
	out := []*agentsession.Session{read}
	for _, ch := range c.children {
		out = append(out, ch...)
	}
	return out, c.b.Resume(), nil
}

func displayTask(prefix string) string {
	if prefix == "" {
		return "task"
	}
	return strings.TrimSuffix(prefix, "/")
}

// mainID is the main session's ID: the task's ID when a session store
// takes it, else one derived from it.
func (fam *family) mainID(native string) (id, derived string) {
	if storeName(native) {
		fam.ids[native] = true
		return native, ""
	}
	key := "task/" + native
	if native == "" {
		sum := sha256.Sum256([]byte(native))
		key = "task/" + hex.EncodeToString(sum[:])
	}
	id = agentsession.SubsessionID(Source, key)
	fam.ids[id] = true
	return id, "the task ID is not a name a session store takes (" + storeRule + "); the session ID is derived from it"
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

// childDir returns the directory of the task a sub-task ID names: a
// sibling of the task directory, read only when the ID is one path
// element.
func childDir(taskDir, id string) (string, bool) {
	if taskDir == "" || id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) || filepath.Base(id) != id {
		return "", false
	}
	return filepath.Join(filepath.Dir(taskDir), id), true
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

// readChild reads a sub-task's directory; its errors name no path.
func readChild(dir string) (*cline.Task, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, errors.New("no such task directory")
	}
	t, err := cline.ReadTask(dir)
	if err != nil {
		return nil, errors.New("the directory holds no task")
	}
	return t, nil
}
