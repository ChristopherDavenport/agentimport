package convert

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
)

// The request-time projection is a port of MessageBuilder.buildForApi
// (core/src/session/services/message-builder.ts, read at v4.0.0 and
// v4.1.24), which rewrites the copy of the history each request sends
// and persists none of it. What it does to one block whatever the
// request is written into the block's item: user text normalised, a
// tool result's text cut to 8,000 characters in the middle, a file
// block to 50,000, a call with no result answered. What it does from
// one request on is a context edit the session format cannot write in
// place: an outdated read replaced by a notice, an assistant text cut,
// the 6 MB budget. Lengths are UTF-16 code units, as JavaScript counts
// them.

const (
	maxToolResultChars          = 8_000
	maxFileContentChars         = 50_000
	maxTotalTextBytes           = 6_000_000
	maxAssistantTextChars       = 200_000
	maxAssistantToolMarkupChars = 12_000
	minOutdatedRewriteBytes     = 65_536
	repeatedToolCallMarkup      = 8

	outdatedFileContent   = "[outdated - see the latest file content]"
	missingToolResultText = "Tool execution was interrupted before a result was produced."
)

func truncatedMarker(n int) string { return fmt.Sprintf("\n\n...[truncated %d chars]...\n\n", n) }
func assistantMarker(n int) string {
	return fmt.Sprintf("\n\n...[assistant text truncated: omitted %d chars]...\n\n", n)
}
func markupMarker(n int) string {
	return fmt.Sprintf("\n\n...[assistant text truncated: omitted %d chars due to repeated tool-call markup]...\n\n", n)
}

// toolCallMarkup is MessageBuilder's TOOL_CALL_MARKUP_PATTERN.
var toolCallMarkup = regexp.MustCompile(`(?i)<\s*(?:[|\x{FF5C}]\s*)?DSML\s*(?:[|\x{FF5C}]\s*)?(?:tool_calls|invoke)\b[^>]*>|<\s*/?\s*(?:tool_calls?|tool_call|function_calls?|function_call|invoke)\b[^>]*>`)

// utf16Len is a string's length as JavaScript counts it.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// truncateMiddle is truncateMiddleByChars: text longer than max UTF-16
// units keeps its head and tail around a marker naming how many units
// it dropped.
func truncateMiddle(text string, max int, marker func(int) string) string {
	if len(text) <= max {
		return text
	}
	s := jsjson.FromString(text)
	if len(s) <= max {
		return text
	}
	keep := (max - len(marker(len(s)-max))) / 2
	if keep < 0 {
		keep = 0
	}
	removed := len(s) - keep*2
	if removed < 0 {
		removed = 0
	}
	m := marker(removed)
	keep = (max - len(m)) / 2
	if keep < 0 {
		keep = 0
	}
	out := s[:keep].String() + m
	if keep > 0 {
		out += s[len(s)-keep:].String()
	}
	return out
}

// truncateAssistantText is MessageBuilder's cap on an assistant's text:
// 12,000 units when it repeats tool-call markup eight times, else
// 200,000.
func truncateAssistantText(text string) string {
	if utf16Len(text) > maxAssistantToolMarkupChars && len(toolCallMarkup.FindAllStringIndex(text, repeatedToolCallMarkup)) >= repeatedToolCallMarkup {
		return truncateMiddle(text, maxAssistantToolMarkupChars, markupMarker)
	}
	return truncateMiddle(text, maxAssistantTextChars, assistantMarker)
}

// isImageWithData reports whether a value is an image block with its
// data, which MessageBuilder leaves whole.
func isImageWithData(v *jsjson.Value) bool {
	if v == nil || v.Kind != jsjson.Object {
		return false
	}
	t, _ := v.Get("type").Text()
	_, data := v.Get("data").Text()
	_, mt := v.Get("mediaType").Text()
	return t == "image" && data && mt
}

// isStructured reports whether a tool result entry is a structured
// result, an object that is no text, image or file block.
func isStructured(v *jsjson.Value) bool {
	if v == nil || v.Kind != jsjson.Object {
		return false
	}
	t, _ := v.Get("type").Text()
	if v.Get("type") != nil && v.Get("type").Kind != jsjson.Str {
		return true
	}
	return t != "text" && t != "image" && t != "file"
}

// truncateNested cuts every string a structured result holds, at any
// depth but inside an image; v itself when nothing changed.
func truncateNested(v *jsjson.Value) *jsjson.Value {
	switch {
	case v == nil:
		return v
	case v.Kind == jsjson.Str:
		s := v.Str.String()
		if t := truncateMiddle(s, maxToolResultChars, truncatedMarker); t != s {
			return jsjson.NewString(t)
		}
		return v
	case v.Kind == jsjson.Array:
		var out *jsjson.Value
		for i, it := range v.Items {
			if nt := truncateNested(it); nt != it {
				if out == nil {
					out = &jsjson.Value{Kind: jsjson.Array, Items: append([]*jsjson.Value(nil), v.Items...)}
				}
				out.Items[i] = nt
			}
		}
		if out != nil {
			return out
		}
		return v
	case v.Kind == jsjson.Object:
		if isImageWithData(v) {
			return v
		}
		var out *jsjson.Value
		for i, it := range v.Vals {
			if nt := truncateNested(it); nt != it {
				if out == nil {
					out = &jsjson.Value{Kind: jsjson.Object, Keys: append([]jsjson.String(nil), v.Keys...), Vals: append([]*jsjson.Value(nil), v.Vals...)}
				}
				out.Vals[i] = nt
			}
		}
		if out != nil {
			return out
		}
	}
	return v
}

// withText returns an object with one string member replaced.
func withText(v *jsjson.Value, key, text string) *jsjson.Value {
	out := &jsjson.Value{Kind: jsjson.Object, Keys: append([]jsjson.String(nil), v.Keys...), Vals: append([]*jsjson.Value(nil), v.Vals...)}
	out.Set(key, jsjson.NewString(text))
	return out
}

// truncateResult is truncateToolResultContent: a tool result's text,
// its text and file entries, and every string of its structured
// entries, cut to 8,000 units; content itself when nothing changed.
func truncateResult(content *jsjson.Value) *jsjson.Value {
	switch {
	case content == nil:
		return content
	case content.Kind == jsjson.Str:
		s := content.Str.String()
		if t := truncateMiddle(s, maxToolResultChars, truncatedMarker); t != s {
			return jsjson.NewString(t)
		}
		return content
	case content.Kind != jsjson.Array:
		return content
	}
	var out *jsjson.Value
	for i, e := range content.Items {
		ne := e
		t, _ := e.Get("type").Text()
		switch {
		case e.Kind == jsjson.Object && t == "file":
			if s, ok := e.Get("content").Text(); ok {
				if cut := truncateMiddle(s, maxToolResultChars, truncatedMarker); cut != s {
					ne = withText(e, "content", cut)
				}
			}
		case e.Kind == jsjson.Object && t == "text" && e.Get("type").Kind == jsjson.Str:
			if s, ok := e.Get("text").Text(); ok {
				if cut := truncateMiddle(s, maxToolResultChars, truncatedMarker); cut != s {
					ne = withText(e, "text", cut)
				}
			}
		case isStructured(e):
			ne = truncateNested(e)
		}
		if ne != e {
			if out == nil {
				out = &jsjson.Value{Kind: jsjson.Array, Items: append([]*jsjson.Value(nil), content.Items...)}
			}
			out.Items[i] = ne
		}
	}
	if out != nil {
		return out
	}
	return content
}

// locator is a read's ReadLocator: a path, and its line range when the
// read was of part of the file.
type locator struct {
	path       string
	start, end *float64
}

func (l locator) full() bool { return l.start == nil && l.end == nil }

// key is toReadLocatorKey.
func (l locator) key() string {
	if l.full() {
		return l.path
	}
	s, e := "1", "EOF"
	if l.start != nil {
		s = jsjson.Number2String(*l.start)
	}
	if l.end != nil {
		e = jsjson.Number2String(*l.end)
	}
	return l.path + ":" + s + "-" + e
}

var readQuery = regexp.MustCompile(`^(.*):(\d+)-(EOF|\d+)$`)

// readTools are the tools whose results MessageBuilder replaces when a
// later read supersedes them.
var readTools = map[string]bool{"read": true, "read_files": true}

func pathOf(v *jsjson.Value) string {
	for _, k := range []string{"path", "file_path", "filePath"} {
		if s, ok := v.Get(k).Text(); ok && s != "" {
			return s
		}
	}
	return ""
}

func lineOf(v *jsjson.Value) *float64 {
	if v == nil || v.Kind != jsjson.Number || v.Num != float64(int64(v.Num)) {
		return nil
	}
	n := v.Num
	return &n
}

// requestLocator is extractLocatorFromReadRequest.
func requestLocator(v *jsjson.Value) (locator, bool) {
	if v == nil || v.Kind != jsjson.Object {
		return locator{}, false
	}
	p := pathOf(v)
	if p == "" {
		return locator{}, false
	}
	return locator{path: p, start: lineOf(v.Get("start_line")), end: lineOf(v.Get("end_line"))}, true
}

// entryLocator is extractLocatorFromResultEntry.
func entryLocator(v *jsjson.Value) (locator, bool) {
	if v == nil || v.Kind != jsjson.Object {
		return locator{}, false
	}
	if l, ok := requestLocator(v); ok {
		return l, true
	}
	q, ok := v.Get("query").Text()
	if !ok || q == "" {
		return locator{}, false
	}
	m := readQuery.FindStringSubmatch(q)
	if m == nil {
		return locator{path: q}, true
	}
	l := locator{path: m[1]}
	if s, err := strconv.ParseFloat(m[2], 64); err == nil {
		l.start = &s
	}
	if m[3] != "EOF" {
		if e, err := strconv.ParseFloat(m[3], 64); err == nil {
			l.end = &e
		}
	}
	return l, true
}

func dedupe(ls []locator) []locator {
	var out []locator
	at := map[string]int{}
	for _, l := range ls {
		if i, ok := at[l.key()]; ok {
			out[i] = l
			continue
		}
		at[l.key()] = len(out)
		out = append(out, l)
	}
	return out
}

// inputLocators is extractLocatorsFromReadToolInput.
func inputLocators(input *jsjson.Value) []locator {
	if input == nil || input.Kind != jsjson.Object {
		return nil
	}
	var out []locator
	if l, ok := requestLocator(input); ok {
		out = append(out, l)
	}
	if fs := input.Get("files"); fs != nil && fs.Kind == jsjson.Array {
		for _, f := range fs.Items {
			if l, ok := requestLocator(f); ok {
				out = append(out, l)
			}
		}
	}
	if fs := input.Get("file_paths"); fs != nil && fs.Kind == jsjson.Array {
		for _, f := range fs.Items {
			if s, ok := f.Text(); ok && s != "" {
				out = append(out, locator{path: s})
			}
		}
	}
	return dedupe(out)
}

// parsedLocators is tryParseReadLocators of a text.
func parsedLocators(text string) []locator {
	v, err := jsjson.Parse([]byte(text))
	if err != nil {
		return nil
	}
	if v.Kind == jsjson.Array {
		var out []locator
		for _, it := range v.Items {
			if l, ok := entryLocator(it); ok {
				out = append(out, l)
			}
		}
		return dedupe(out)
	}
	if l, ok := entryLocator(v); ok {
		return []locator{l}
	}
	return nil
}

// resultLocators is extractReadLocatorsFromToolResultContent.
func resultLocators(content *jsjson.Value) []locator {
	if content == nil {
		return nil
	}
	if s, ok := content.Text(); ok {
		return parsedLocators(s)
	}
	if content.Kind != jsjson.Array {
		return nil
	}
	var out []locator
	for _, e := range content.Items {
		if t, _ := e.Get("type").Text(); e.Kind == jsjson.Object && t == "text" && e.Get("type").Kind == jsjson.Str {
			if s, ok := e.Get("text").Text(); ok {
				out = append(out, parsedLocators(s)...)
			}
			continue
		}
		if isStructured(e) {
			if l, ok := entryLocator(e); ok {
				out = append(out, l)
			}
		}
	}
	return dedupe(out)
}

// replaceOutdatedEntry is replaceOutdatedReadEntry.
func replaceOutdatedEntry(v *jsjson.Value, keys map[string]bool) *jsjson.Value {
	l, ok := entryLocator(v)
	if !ok || !keys[l.key()] {
		return v
	}
	out := v.Clone()
	switch {
	case v.Get("result") != nil && v.Get("result").Kind == jsjson.Str:
		out.Set("result", jsjson.NewString(outdatedFileContent))
	case v.Get("content") != nil && v.Get("content").Kind == jsjson.Str:
		out.Set("content", jsjson.NewString(outdatedFileContent))
	default:
		out.Set("result", jsjson.NewString(outdatedFileContent))
	}
	return out
}

// replaceInString is replaceOutdatedInString: ok is false when text is
// not JSON.
func replaceInString(text string, keys map[string]bool) (string, bool) {
	v, err := jsjson.Parse([]byte(text))
	if err != nil {
		return "", false
	}
	if v.Kind == jsjson.Array {
		out := &jsjson.Value{Kind: jsjson.Array}
		for _, it := range v.Items {
			out.Items = append(out.Items, replaceOutdatedEntry(it, keys))
		}
		return string(out.Stringify()), true
	}
	return string(replaceOutdatedEntry(v, keys).Stringify()), true
}

// outdatedImages is countOutdatedImageEntries.
func outdatedImages(text string, keys map[string]bool) int {
	v, err := jsjson.Parse([]byte(text))
	if err != nil {
		return 0
	}
	entries := []*jsjson.Value{v}
	if v.Kind == jsjson.Array {
		entries = v.Items
	}
	n := 0
	for _, e := range entries {
		l, ok := entryLocator(e)
		if !ok || !keys[l.key()] {
			continue
		}
		r, _ := e.Get("result").Text()
		c, _ := e.Get("content").Text()
		if r == "Successfully read image" || c == "Successfully read image" {
			n++
		}
	}
	return n
}

// replaceOutdated is replaceOutdatedReadContent.
func replaceOutdated(content *jsjson.Value, outdated []locator) *jsjson.Value {
	keys := map[string]bool{}
	paths := map[string]bool{}
	for _, l := range outdated {
		keys[l.key()] = true
		paths[l.path] = true
	}
	if s, ok := content.Text(); ok {
		if r, ok := replaceInString(s, keys); ok {
			return jsjson.NewString(r)
		}
		return jsjson.NewString(outdatedFileContent)
	}
	if content.Kind != jsjson.Array {
		return content
	}
	images := 0
	for _, e := range content.Items {
		if t, _ := e.Get("type").Text(); t == "text" {
			if s, ok := e.Get("text").Text(); ok {
				images += outdatedImages(s, keys)
			}
		}
	}
	out := &jsjson.Value{Kind: jsjson.Array}
	for _, e := range content.Items {
		t, _ := e.Get("type").Text()
		switch {
		case e.Kind == jsjson.Object && t == "file":
			if paths[stringOrEmpty(e.Get("path"))] {
				out.Items = append(out.Items, withText(e, "content", outdatedFileContent))
				continue
			}
		case e.Kind == jsjson.Object && t == "image":
			if images > 0 {
				images--
				out.Items = append(out.Items, &jsjson.Value{Kind: jsjson.Object, Keys: []jsjson.String{jsjson.FromString("type"), jsjson.FromString("text")},
					Vals: []*jsjson.Value{jsjson.NewString("text"), jsjson.NewString(outdatedFileContent)}})
				continue
			}
		case isStructured(e):
			out.Items = append(out.Items, replaceOutdatedEntry(e, keys))
			continue
		case e.Kind == jsjson.Object && t == "text":
			s, _ := e.Get("text").Text()
			r, ok := replaceInString(s, keys)
			if !ok {
				out.Items = append(out.Items, withText(e, "text", outdatedFileContent))
				continue
			}
			if r != s {
				out.Items = append(out.Items, withText(e, "text", r))
				continue
			}
		}
		out.Items = append(out.Items, e)
	}
	return out
}

func stringOrEmpty(v *jsjson.Value) string {
	s, _ := v.Text()
	return s
}

// reclaimBytes is estimateOutdatedReclaimBytes: what replacing the
// newly outdated locators of one result would save a request.
func reclaimBytes(content *jsjson.Value, all []locator, keys map[string]bool) int {
	fully := len(all) > 0
	for _, l := range all {
		if !keys[l.key()] {
			fully = false
		}
	}
	attribute := func(text string) int {
		v, err := jsjson.Parse([]byte(text))
		if err != nil {
			if fully || len(all) == 0 {
				return len(truncateMiddle(text, maxToolResultChars, truncatedMarker))
			}
			return 0
		}
		entries := []*jsjson.Value{v}
		if v.Kind == jsjson.Array {
			entries = v.Items
		}
		total := 0
		for _, e := range entries {
			if l, ok := entryLocator(e); ok && keys[l.key()] {
				total += len(truncateNested(e).Stringify())
			}
		}
		return total
	}
	if s, ok := content.Text(); ok {
		return attribute(s)
	}
	if content.Kind != jsjson.Array {
		return 0
	}
	images := 0
	for _, e := range content.Items {
		if t, _ := e.Get("type").Text(); t == "text" {
			if s, ok := e.Get("text").Text(); ok {
				images += outdatedImages(s, keys)
			}
		}
	}
	total := 0
	for _, e := range content.Items {
		t, _ := e.Get("type").Text()
		switch {
		case e.Kind == jsjson.Object && t == "text":
			if s, ok := e.Get("text").Text(); ok {
				total += attribute(s)
			}
		case e.Kind == jsjson.Object && t == "image":
			if images > 0 {
				images--
				s, _ := e.Get("data").Text()
				total += len(s)
			}
		case isStructured(e):
			if l, ok := entryLocator(e); ok && keys[l.key()] {
				total += len(truncateNested(e).Stringify())
			}
		case e.Kind == jsjson.Object && t == "file":
			if keys[stringOrEmpty(e.Get("path"))] {
				s, _ := e.Get("content").Text()
				total += len(truncateMiddle(s, maxToolResultChars, truncatedMarker))
			}
		}
	}
	return total
}

// textBytes counts the UTF-8 bytes of the text a block sends, as
// countMessageTextBytes does, for the budget.
func textBytes(b *clinesdk.Block, content *jsjson.Value) int {
	switch b.Type {
	case clinesdk.BlockText:
		return len(b.Text)
	case clinesdk.BlockThinking:
		return len(b.Thinking)
	case clinesdk.BlockFile:
		s, _ := b.FileText()
		return len(s)
	case clinesdk.BlockToolUse:
		v, err := jsjson.Parse(b.Input)
		if err != nil {
			return 0
		}
		return nestedBytes(v)
	case clinesdk.BlockToolResult:
		if content == nil {
			return 0
		}
		if s, ok := content.Text(); ok {
			return len(s)
		}
		n := 0
		for _, e := range content.Items {
			t, _ := e.Get("type").Text()
			switch {
			case e.Kind == jsjson.Object && t == "text":
				n += len(stringOrEmpty(e.Get("text")))
			case e.Kind == jsjson.Object && t == "file":
				n += len(stringOrEmpty(e.Get("content")))
			case isStructured(e):
				n += nestedBytes(e)
			}
		}
		return n
	}
	return 0
}

func nestedBytes(v *jsjson.Value) int {
	switch {
	case v == nil:
		return 0
	case v.Kind == jsjson.Str:
		return len(v.Str.String())
	case v.Kind == jsjson.Array:
		n := 0
		for _, it := range v.Items {
			n += nestedBytes(it)
		}
		return n
	case v.Kind == jsjson.Object:
		if isImageWithData(v) {
			return 0
		}
		n := 0
		for _, it := range v.Vals {
			n += nestedBytes(it)
		}
		return n
	}
	return 0
}

// cacheable reports whether a tool's results take MessageBuilder's
// recovery path from 4.1.23 (resultPolicy "cache-oversized"): an MCP
// tool, named <server>__<tool> or, sanitised and cut, <base>_<hash8>,
// or one of Composio's meta tools.
func cacheable(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "__") || mcpHashed.MatchString(name) || composioTools[name]
}

var mcpHashed = regexp.MustCompile(`^.{1,55}_[0-9a-f]{8}$`)

var composioTools = map[string]bool{
	"composio_search_tools": true, "composio_manage_connections": true,
	"composio_wait_for_connections": true, "composio_multi_execute_tool": true,
}

// imageAttachedText is what the preview puts in place of an image it
// sends apart.
const imageAttachedText = "[image attached]"

// splitMedia is splitToolResultMedia: the images a result holds taken
// out, each replaced by a marker, and the images.
func splitMedia(v *jsjson.Value, images *[]*jsjson.Value) *jsjson.Value {
	switch {
	case v == nil:
		return v
	case v.Kind == jsjson.Object:
		t, _ := v.Get("type").Text()
		_, data := v.Get("data").Text()
		if t == "image" && data {
			if _, ok := v.Get("mediaType").Text(); ok {
				*images = append(*images, v)
				return jsjson.NewString(imageAttachedText)
			}
			if mt, ok := v.Get("mimeType").Text(); ok {
				d, _ := v.Get("data").Text()
				im := &jsjson.Value{Kind: jsjson.Object}
				im.Set("type", jsjson.NewString("image"))
				im.Set("data", jsjson.NewString(d))
				im.Set("mediaType", jsjson.NewString(mt))
				*images = append(*images, im)
				return jsjson.NewString(imageAttachedText)
			}
		}
		out := &jsjson.Value{Kind: jsjson.Object, Keys: append([]jsjson.String(nil), v.Keys...), Vals: make([]*jsjson.Value, len(v.Vals))}
		for i, it := range v.Vals {
			out.Vals[i] = splitMedia(it, images)
		}
		return out
	case v.Kind == jsjson.Array:
		out := &jsjson.Value{Kind: jsjson.Array, Items: make([]*jsjson.Value, len(v.Items))}
		for i, it := range v.Items {
			out.Items[i] = splitMedia(it, images)
		}
		return out
	}
	return v
}

// cachePreview is MessageBuilder's recovery projection of an oversized
// result: the result as text (JSON.stringify(result, null, 2) unless it
// is a string), its images marked, cut to 8,000 characters in the
// middle, then the images. ok is false for a result within the limit,
// which takes the usual path.
func cachePreview(content *jsjson.Value) (*jsjson.Value, bool) {
	var images []*jsjson.Value
	textual := splitMedia(content, &images)
	text, isText := textual.Text()
	if !isText {
		text = string(textual.StringifyIndent())
	}
	if utf16Len(text) <= maxToolResultChars {
		return nil, false
	}
	part := &jsjson.Value{Kind: jsjson.Object}
	part.Set("type", jsjson.NewString("text"))
	part.Set("text", jsjson.NewString(truncateMiddle(text, maxToolResultChars, truncatedMarker)))
	return &jsjson.Value{Kind: jsjson.Array, Items: append([]*jsjson.Value{part}, images...)}, true
}
