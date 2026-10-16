package cline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// MaxSize bounds a file the reader reads. A larger file is refused
// rather than read into memory, and reported as malformed.
const MaxSize = 1 << 30

// File names in a task directory.
const (
	FileAPI            = "api_conversation_history.json"
	FileUI             = "ui_messages.json"
	FileLegacy         = "claude_messages.json"
	FileMetadata       = "task_metadata.json"
	FileContextHistory = "context_history.json"
	FileSettings       = "settings.json"
	// FileHistoryItem is Roo's per-task index entry.
	FileHistoryItem = "history_item.json"
	// FileTaskHistory is classic Cline's index, state/taskHistory.json
	// under the storage directory.
	FileTaskHistory = "taskHistory.json"
)

// Storage directory names, the VS Code extension IDs, lowercased as VS
// Code writes them.
const (
	StorageClassic = "saoudrizwan.claude-dev"
	StorageRoo     = "rooveterinaryinc.roo-cline"
)

// Variant is which agent wrote a task directory.
type Variant string

const (
	// Classic is Cline before 4.0, the VS Code extension's task
	// directories, and the standalone hosts that kept the layout.
	Classic Variant = "classic"
	// Roo is Roo Code, forked from classic Cline.
	Roo Variant = "roo"
)

// Array is one of a task's JSON array files, read element by element.
type Array[T any] struct {
	// Name is the file's name in the task directory; empty when the
	// task has no such file.
	Name string
	// Elems are the elements read, in order.
	Elems []T
	// Err is set when the file could not be read whole: it does not
	// parse, it is not an array, or it is cut short. Elems then holds
	// the elements that end before the fault. The file counts one
	// malformed unit for it.
	Err error
}

// Present reports whether the task has the file.
func (a *Array[T]) Present() bool { return a.Name != "" }

// Units is the number of units the file accounts for: each element,
// and one for a fault.
func (a *Array[T]) Units() int {
	n := len(a.Elems)
	if a.Err != nil {
		n++
	}
	return n
}

// Task is one task directory as read.
type Task struct {
	// Dir is the task directory, and ID its name, the task's ID.
	Dir string
	ID  string
	// Variant is which agent wrote the task; VariantBy says how it was
	// told, and Guessed that it was told by shape rather than by the
	// storage directory's name.
	Variant   Variant
	VariantBy string
	Guessed   bool

	API Array[*Message]
	UI  Array[*UIMessage]
	// Context holds context_history.json's updates, each a unit.
	Context Array[*ContextUpdate]

	// Metadata is task_metadata.json; MetadataErr why it did not read.
	Metadata    *TaskMetadata
	MetadataRaw json.RawMessage
	MetadataErr error
	HasMetadata bool

	// Settings is settings.json as read, a per-task settings snapshot
	// some classic releases write; SettingsErr why it did not parse.
	Settings    json.RawMessage
	SettingsErr error
	HasSettings bool

	// History is the task's index entry: Roo's history_item.json, or
	// the element of classic's state/taskHistory.json naming this task.
	// HistoryFile is where it was read from, relative to the storage
	// directory; HistoryErr why it did not read. A history_item.json is
	// a unit of the task; an entry of taskHistory.json is one when it
	// was found.
	History     *HistoryItem
	HistoryFile string
	HistoryErr  error
	HistoryUnit bool

	// Others lists the other files in the task directory, each a unit
	// the converter does not read: a hook's leftover transcript, a
	// temporary file, a newer release's. Dirs lists its directories.
	Others []string
	Dirs   []string
}

// Units is the number of units the task accounts for.
func (t *Task) Units() int {
	n := t.API.Units() + t.UI.Units() + t.Context.Units() + len(t.Others)
	if t.HasMetadata {
		n++
	}
	if t.HasSettings {
		n++
	}
	if t.HistoryUnit {
		n++
	}
	return n
}

// TaskDir returns the task directory a path names: the path itself
// when it is a directory, else the directory of the file.
func TaskDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return path, nil
	}
	return filepath.Dir(path), nil
}

// ReadTask reads the task directory path names, or the directory of the
// file it names. A file that cannot be read or parsed is recorded on
// the task, never fatal: ReadTask fails only when the directory cannot
// be listed or holds neither an API history nor UI messages.
func ReadTask(path string) (*Task, error) {
	dir, err := TaskDir(path)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	errs := map[string]error{}
	var others, dirs []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			dirs = append(dirs, name)
			continue
		}
		switch name {
		case FileAPI, FileUI, FileLegacy, FileMetadata, FileContextHistory, FileSettings, FileHistoryItem:
			data, err := readLimited(filepath.Join(dir, name))
			if err != nil {
				errs[name] = err
				continue
			}
			files[name] = data
		default:
			others = append(others, name)
		}
	}
	if files[FileAPI] == nil && files[FileUI] == nil && files[FileLegacy] == nil &&
		errs[FileAPI] == nil && errs[FileUI] == nil && errs[FileLegacy] == nil {
		return nil, fmt.Errorf("%s: not a Cline or Roo task directory: no %s or %s", dir, FileAPI, FileUI)
	}
	t := ParseTask(filepath.Base(dir), files)
	t.Dir = dir
	t.Others, t.Dirs = append(others, t.Others...), dirs
	for name, err := range errs {
		t.fileError(name, err)
	}
	t.readIndex()
	t.detect()
	return t, nil
}

// readLimited reads a file of at most MaxSize bytes; the error names no
// path.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("the file cannot be opened")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize+1))
	if err != nil {
		return nil, errors.New("the file cannot be read")
	}
	if len(data) > MaxSize {
		return nil, fmt.Errorf("the file is larger than %d bytes", MaxSize)
	}
	return data, nil
}

// fileError records that a known file could not be read.
func (t *Task) fileError(name string, err error) {
	switch name {
	case FileAPI:
		t.API = Array[*Message]{Name: name, Err: err}
	case FileUI, FileLegacy:
		if !t.UI.Present() {
			t.UI = Array[*UIMessage]{Name: name, Err: err}
		}
	case FileContextHistory:
		t.Context = Array[*ContextUpdate]{Name: name, Err: err}
	case FileMetadata:
		t.HasMetadata, t.MetadataErr = true, err
	case FileSettings:
		t.HasSettings, t.SettingsErr = true, err
	case FileHistoryItem:
		t.HistoryUnit, t.HistoryFile, t.HistoryErr = true, name, err
	}
}

// ParseTask reads a task from its files' contents, by name, without
// touching the file system: the task's index entry beside the
// directory and the variant by the storage directory are left to
// [ReadTask], so the variant is told by shape. A claude_messages.json,
// the name the earliest releases gave a file, is read as UI messages,
// or as the API history when its elements have a role.
func ParseTask(id string, files map[string][]byte) *Task {
	t := &Task{ID: id}
	if data, ok := files[FileAPI]; ok {
		t.API = parseMessages(FileAPI, data)
	}
	if data, ok := files[FileUI]; ok {
		t.UI = parseUI(FileUI, data)
	}
	if data, ok := files[FileLegacy]; ok {
		switch {
		case !t.API.Present() && legacyIsAPI(data):
			t.API = parseMessages(FileLegacy, data)
		case !t.UI.Present():
			t.UI = parseUI(FileLegacy, data)
		default:
			t.Others = append(t.Others, FileLegacy)
		}
	}
	if data, ok := files[FileContextHistory]; ok {
		t.Context = ParseContextHistory(data)
		t.Context.Name = FileContextHistory
	}
	if data, ok := files[FileMetadata]; ok {
		t.HasMetadata = true
		t.MetadataRaw = bytes.TrimSpace(data)
		var m TaskMetadata
		switch {
		case !json.Valid(data):
			t.MetadataErr = errors.New("the file is not JSON")
		case json.Unmarshal(data, &m) != nil:
			t.MetadataErr = errors.New("the file is not a JSON object")
		default:
			t.Metadata = &m
		}
	}
	if data, ok := files[FileSettings]; ok {
		t.HasSettings = true
		if json.Valid(data) {
			t.Settings = bytes.TrimSpace(data)
		} else {
			t.SettingsErr = errors.New("the file is not JSON")
		}
	}
	if data, ok := files[FileHistoryItem]; ok {
		t.HistoryUnit, t.HistoryFile = true, FileHistoryItem
		var h HistoryItem
		if err := json.Unmarshal(data, &h); err != nil || !json.Valid(data) {
			t.HistoryErr = errors.New("the file is not a JSON object")
		} else {
			t.History = &h
		}
	}
	t.detect()
	return t
}

// legacyIsAPI reports whether a claude_messages.json holds API
// messages: its first object element has a role.
func legacyIsAPI(data []byte) bool {
	var elems []json.RawMessage
	if json.Unmarshal(data, &elems) != nil {
		return false
	}
	for _, e := range elems {
		var head struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(e, &head) == nil {
			return head.Role != ""
		}
	}
	return false
}

func parseMessages(name string, data []byte) Array[*Message] {
	a := Array[*Message]{Name: name}
	raws, err := elements(data)
	a.Err = err
	for _, raw := range raws {
		m := &Message{}
		_ = m.UnmarshalJSON(raw)
		a.Elems = append(a.Elems, m)
	}
	return a
}

func parseUI(name string, data []byte) Array[*UIMessage] {
	a := Array[*UIMessage]{Name: name}
	raws, err := elements(data)
	a.Err = err
	for _, raw := range raws {
		m := &UIMessage{}
		_ = m.UnmarshalJSON(raw)
		a.Elems = append(a.Elems, m)
	}
	return a
}

// ParseMessages reads an api_conversation_history.json from its bytes.
func ParseMessages(data []byte) Array[*Message] { return parseMessages(FileAPI, data) }

// ParseUI reads a ui_messages.json from its bytes.
func ParseUI(data []byte) Array[*UIMessage] { return parseUI(FileUI, data) }

// elements reads a JSON array's elements up to the first that does not
// parse. err says why the array was not read to its end: it is not an
// array, or it is cut short or broken.
func elements(data []byte) ([]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		if err == io.EOF {
			return nil, errors.New("the file is empty")
		}
		return nil, errors.New("the file is not JSON")
	}
	if tok != json.Delim('[') {
		return nil, errors.New("the file is not a JSON array")
	}
	var out []json.RawMessage
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return out, errors.New("the array is cut short or broken after its last whole element")
		}
		out = append(out, raw)
	}
	if _, err := dec.Token(); err != nil {
		return out, errors.New("the array is cut short or broken after its last whole element")
	}
	if rest := bytes.TrimSpace(data[dec.InputOffset():]); len(rest) > 0 {
		return out, errors.New("the array is followed by bytes that are not part of it")
	}
	return out, nil
}

// ParseContextHistory reads a context_history.json: a serialised
// Map<messageIndex, [editType, Map<blockIndex, ContextUpdate[]>]>, as
// nested arrays. Each update is a unit; a message or block entry that
// does not read is one unit with Err set.
func ParseContextHistory(data []byte) Array[*ContextUpdate] {
	var a Array[*ContextUpdate]
	raws, err := elements(data)
	a.Err = err
	bad := func(raw json.RawMessage, msg, block int, why string) {
		a.Elems = append(a.Elems, &ContextUpdate{Message: msg, Block: block, Raw: bytes.Clone(raw), Err: errors.New(why)})
	}
	for _, raw := range raws {
		var pair []json.RawMessage
		if json.Unmarshal(raw, &pair) != nil || len(pair) != 2 {
			bad(raw, -1, -1, "a message entry that is not [index, [editType, blocks]]")
			continue
		}
		msg, exact, ok := Int(pair[0])
		var inner []json.RawMessage
		if !ok || !exact || msg < 0 || msg > 1<<31 || json.Unmarshal(pair[1], &inner) != nil || len(inner) != 2 {
			bad(raw, -1, -1, "a message entry that is not [index, [editType, blocks]]")
			continue
		}
		var blocks []json.RawMessage
		if json.Unmarshal(inner[1], &blocks) != nil {
			bad(raw, int(msg), -1, "a message entry whose blocks are not an array")
			continue
		}
		for _, braw := range blocks {
			var bpair []json.RawMessage
			var updates []json.RawMessage
			if json.Unmarshal(braw, &bpair) != nil || len(bpair) != 2 || json.Unmarshal(bpair[1], &updates) != nil {
				bad(braw, int(msg), -1, "a block entry that is not [index, updates]")
				continue
			}
			block, exact, ok := Int(bpair[0])
			if !ok || !exact || block < 0 || block > 1<<31 {
				bad(braw, int(msg), -1, "a block entry whose index is not a count")
				continue
			}
			for _, uraw := range updates {
				u := &ContextUpdate{Message: int(msg), EditType: bytes.Clone(inner[0]), Block: int(block), Raw: bytes.Clone(uraw)}
				var parts []json.RawMessage
				if json.Unmarshal(uraw, &parts) != nil || len(parts) < 3 {
					u.Err = errors.New("an update that is not [ts, type, [text], metadata]")
					a.Elems = append(a.Elems, u)
					continue
				}
				u.TS = parts[0]
				if json.Unmarshal(parts[1], &u.Kind) != nil || json.Unmarshal(parts[2], &u.Text) != nil {
					u.Err = errors.New("an update whose type or text is not a string")
				}
				if len(parts) > 3 {
					u.Metadata = parts[3]
				}
				a.Elems = append(a.Elems, u)
			}
		}
	}
	return a
}

// storageDir returns the storage directory a task directory is in,
// <storage>/tasks/<id>, and whether the layout holds.
func storageDir(taskDir string) (string, bool) {
	tasks := filepath.Dir(taskDir)
	if filepath.Base(tasks) != "tasks" {
		return "", false
	}
	return filepath.Dir(tasks), true
}

// readIndex reads the task's entry of classic's state/taskHistory.json
// when the task has no history_item.json of its own.
func (t *Task) readIndex() {
	if t.HistoryFile != "" {
		return
	}
	storage, ok := storageDir(t.Dir)
	if !ok {
		return
	}
	data, err := readLimited(filepath.Join(storage, "state", FileTaskHistory))
	if err != nil {
		return
	}
	h, found, err := FindHistory(data, t.ID)
	if !found {
		return
	}
	t.HistoryFile, t.HistoryUnit = filepath.Join("state", FileTaskHistory), true
	t.History, t.HistoryErr = h, err
}

// FindHistory returns the element of a taskHistory.json whose id is
// id. found is false when no element names it; err is set when the one
// that does is not an object of the item's members.
func FindHistory(data []byte, id string) (h *HistoryItem, found bool, err error) {
	raws, _ := elements(data)
	for _, raw := range raws {
		var head map[string]json.RawMessage
		var hid string
		if json.Unmarshal(raw, &head) != nil || json.Unmarshal(head["id"], &hid) != nil || hid != id {
			continue
		}
		var item HistoryItem
		if json.Unmarshal(raw, &item) != nil {
			return nil, true, errors.New("the task's entry does not decode")
		}
		return &item, true, nil
	}
	return nil, false, nil
}

// detect tells the variant: by the storage directory's name when the
// task is under one, else by shape, and says which.
func (t *Task) detect() {
	if t.Dir != "" {
		for d := t.Dir; ; {
			parent := filepath.Dir(d)
			switch strings.ToLower(filepath.Base(d)) {
			case StorageClassic:
				t.Variant, t.VariantBy, t.Guessed = Classic, "the storage directory "+StorageClassic, false
				return
			case StorageRoo:
				t.Variant, t.VariantBy, t.Guessed = Roo, "the storage directory "+StorageRoo, false
				return
			}
			if parent == d {
				break
			}
			d = parent
		}
	}
	t.Guessed = true
	if why := t.rooShape(); why != "" {
		t.Variant, t.VariantBy = Roo, why
		return
	}
	if why := t.classicShape(); why != "" {
		t.Variant, t.VariantBy = Classic, why
		return
	}
	t.Variant, t.VariantBy = Classic, "no shape of either; classic assumed"
}

// rooShape names a shape only Roo writes, or "".
func (t *Task) rooShape() string {
	if t.HistoryFile == FileHistoryItem {
		return "a " + FileHistoryItem
	}
	for _, m := range t.API.Elems {
		if m.Err != nil {
			continue
		}
		switch {
		case m.IsSummary || m.CondenseID != "" || m.CondenseParent != "":
			return "a condensed message"
		case m.IsTruncationMarker || m.TruncationID != "" || m.TruncationParent != "":
			return "a sliding-window truncation"
		case m.IsStandaloneReasoning():
			return "a reasoning item on its own"
		}
		for _, b := range m.Content.Blocks {
			if b.Err == nil && (b.Type == BlockReasoning || b.Type == BlockThoughtSignature) {
				return "a " + b.Type + " block"
			}
		}
	}
	for _, m := range t.UI.Elems {
		if m.Err == nil && (m.APIProtocol != "" || !IsNull(m.ContextCondense) || !IsNull(m.ContextTruncation)) {
			return "a UI message with Roo's members"
		}
	}
	return ""
}

// classicShape names a shape only classic Cline writes, or "".
func (t *Task) classicShape() string {
	if t.Context.Present() {
		return "a " + FileContextHistory
	}
	if t.Metadata != nil && (len(t.Metadata.ModelUsage) > 0 || len(t.Metadata.EnvironmentHistory) > 0) {
		return "task metadata with model usage or environments"
	}
	for _, m := range t.API.Elems {
		if m.Err == nil && (m.ModelInfo != nil || m.Metrics != nil) {
			return "a message with modelInfo or metrics"
		}
	}
	return ""
}

// ParentTask reads the parent task ID a Roo task's history_item.json
// names; empty when there is none.
func ParentTask(taskDir string) string {
	data, err := readLimited(filepath.Join(taskDir, FileHistoryItem))
	if err != nil {
		return ""
	}
	var h HistoryItem
	if json.Unmarshal(data, &h) != nil {
		return ""
	}
	return h.ParentTaskID
}

// FindSessions returns the task directories under root as the path of
// each one's main file: its API history, or its UI messages when it has
// none, sorted. root may be a storage directory, its tasks directory,
// one task directory, or a file in one, which is returned as it is.
// Checkpoint repositories are not walked, nor is a task directory below
// its own level; a directory that cannot be read is skipped.
func FindSessions(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case "checkpoints", ".git", "node_modules":
			if p != root {
				return filepath.SkipDir
			}
		}
		if main := mainFile(p); main != "" {
			out = append(out, main)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	return slices.Compact(out), err
}

// mainFile returns the file a task directory is found by, or "" when
// dir is not a task directory: one inside a tasks directory holding an
// API history or UI messages.
func mainFile(dir string) string {
	if filepath.Base(filepath.Dir(dir)) != "tasks" {
		return ""
	}
	for _, name := range []string{FileAPI, FileUI, FileLegacy} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	return ""
}

// IsTaskFile reports whether path is a file a task directory is found
// by, in a task directory inside a tasks directory.
func IsTaskFile(path string) bool {
	switch filepath.Base(path) {
	case FileAPI, FileUI, FileLegacy:
		return filepath.Base(filepath.Dir(filepath.Dir(path))) == "tasks"
	}
	return false
}
