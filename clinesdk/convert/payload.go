package convert

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/clinesdk"
	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// The payload follows what Cline's AI SDK formatting sends
// (@cline/shared llms/ai-sdk-format.ts) and openresponses' Anthropic
// provider: thinking a reasoning item with its signature in
// encrypted_content, redacted thinking the anthropic.redacted_thinking
// item, a tool_use a function_call, a tool_result a
// function_call_output, a file block the text Cline frames it in.

// imageOmitted and imageAttached are the texts Cline puts in place of
// an image it does not send, or sends apart from a structured result.
const (
	imageOmitted  = "[media omitted: invalid or exceeds size limit]"
	imageAttached = "[image attached]"
)

// fileContent is formatFileContentBlock.
func fileContent(path, content string) string {
	return `<file_content path="` + path + `">` + "\n" + content + "\n</file_content>"
}

// dataURL is an image's data URL.
func dataURL(mediaType, data string) string { return "data:" + mediaType + ";base64," + data }

// messageEntry appends an item of message k, carrying the message's own
// members on the first entry the message writes.
func (sc *sconv) messageEntry(k int, m *clinesdk.Message, e *agentsession.ItemEntry) (string, error) {
	if !sc.rested[k] {
		sc.rested[k] = true
		rest, err := messageRest(m, "modelInfo", "metrics")
		if m.Role != clinesdk.RoleAssistant {
			rest, err = messageRest(m)
		}
		if err != nil {
			return "", err
		}
		if err := sc.keepMembers(e, k, "message", rest, "the message's"); err != nil {
			return "", err
		}
	}
	id, err := sc.appendEntry(e)
	if err != nil {
		return "", err
	}
	sc.written(k, id)
	return id, nil
}

// user converts a user message: its text, image and file blocks a user
// message, each tool result an output, in the order they stand.
func (sc *sconv) user(k int, m *clinesdk.Message, ts time.Time) error {
	blocks := m.Blocks()
	toolOnly := len(blocks) > 0
	for _, b := range blocks {
		if b.Err != nil || b.Type != clinesdk.BlockToolResult {
			toolOnly = false
		}
	}
	if !toolOnly {
		if err := sc.flushMissing(k, ts); err != nil {
			return err
		}
	}
	if len(blocks) == 0 || m.Content.IsText && clinesdk.Trim(m.Content.Text) == "" {
		item := userMessage(openresponses.Contents{&openresponses.InputText{Text: emptyContent}})
		if _, err := sc.messageEntry(k, m, itemEntry(item, ts)); err != nil {
			return err
		}
		sc.at(agentimport.Lossy, "clinesdk/empty-message", k, "a message with no content; Cline sends %q in its place", emptyContent)
		return nil
	}
	var parts openresponses.Contents
	var originals []json.RawMessage
	flush := func() error {
		if len(parts) == 0 {
			return nil
		}
		e := itemEntry(userMessage(parts), ts)
		if len(originals) > 0 && !sc.fam.opts.Slim {
			if err := sc.b.Member(e, "original", originals); err != nil {
				return err
			}
		}
		parts, originals = nil, nil
		_, err := sc.messageEntry(k, m, e)
		return err
	}
	for _, b := range blocks {
		if b.Err != nil {
			sc.at(agentimport.Dropped, "clinesdk/null-block", k, "a content block that is not an object is left out")
			continue
		}
		if len(b.Mistyped()) > 0 {
			sc.at(agentimport.Lossy, "clinesdk/mistyped-member", k, "a %s block's members %s are not of their type; they are kept and read as empty", b.Type, strings.Join(b.Mistyped(), ", "))
		}
		switch b.Type {
		case clinesdk.BlockText:
			text := clinesdk.NormalizeUserInput(b.Text)
			parts = append(parts, &openresponses.InputText{Text: text})
			if text != b.Text || len(blockRest(b, "text")) > 0 {
				orig, err := sc.original(k, b)
				if err != nil {
					return err
				}
				originals = append(originals, orig)
				if text != b.Text {
					sc.at(agentimport.Lossy, "clinesdk/user-input", k, "Cline sends a user's text trimmed and without its <user_input> or <user_command> wrapper, which names the mode; the item holds the text sent, the block as written is %s:original", Source)
				}
			}
		case clinesdk.BlockImage:
			parts = append(parts, &openresponses.InputImage{ImageURL: dataURL(b.MediaType, b.Data), Detail: openresponses.ImageDetailAuto})
			sc.images(k)
			if rest := blockRest(b, "data", "mediaType"); len(rest) > 0 {
				orig, err := sc.original(k, b)
				if err != nil {
					return err
				}
				originals = append(originals, orig)
			}
		case clinesdk.BlockFile:
			content, _ := b.FileText()
			cut := truncateMiddle(content, maxFileContentChars, truncatedMarker)
			parts = append(parts, &openresponses.InputText{Text: fileContent(b.Path, cut)})
			orig, err := sc.original(k, b)
			if err != nil {
				return err
			}
			originals = append(originals, orig)
			sc.at(agentimport.Lossy, "clinesdk/file-block", k, "a file block is the text Cline sends for it, the content in <file_content path=...>; the block as written is %s:original", Source)
			if cut != content {
				sc.at(agentimport.Lossy, "clinesdk/request-projection", k, "a file block of %d characters, which Cline cuts to %d in the middle for every request; the item holds the cut text", utf16Len(content), maxFileContentChars)
			}
		case clinesdk.BlockToolResult:
			if err := flush(); err != nil {
				return err
			}
			if err := sc.result(k, m, b, ts); err != nil {
				return err
			}
		default:
			if err := flush(); err != nil {
				return err
			}
			if err := sc.extension(k, m, b, ts, ""); err != nil {
				return err
			}
			sc.at(agentimport.Kept, "clinesdk/unexpected-block", k, "a %s block in a user message is a %s item", b.Type, extType(b.Type))
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if toolOnly {
		return sc.flushMissing(k, ts)
	}
	return nil
}

// original is a block as written, for a member beside what Cline sent.
func (sc *sconv) original(k int, b *clinesdk.Block) (json.RawMessage, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return sc.clean(k+1, raw)
}

// images reports, once, that the session holds images.
func (sc *sconv) images(k int) {
	if sc.imaged {
		return
	}
	sc.imaged = true
	sc.at(agentimport.Lossy, "clinesdk/request-projection", k, "Cline checks each request's images against its media budget, and a model without image input gets a placeholder; the images are written as stored")
}

func userMessage(parts openresponses.Contents) *openresponses.Message {
	return &openresponses.Message{Role: openresponses.RoleUser, Status: openresponses.StatusCompleted, Content: parts}
}

// extType is the extension type of a block Cline defines and Open
// Responses does not.
func extType(t string) string {
	if t == clinesdk.BlockRedactedThinking {
		return "anthropic.redacted_thinking"
	}
	if t == "" {
		t = "block"
	}
	return "cline." + t
}

// extension writes a block as an extension item carrying it.
func (sc *sconv) extension(k int, m *clinesdk.Message, b *clinesdk.Block, ts time.Time, respID string) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if raw, err = sc.clean(k+1, raw); err != nil {
		return err
	}
	typ := extType(b.Type)
	item := &openresponses.UnknownItem{Type: typ, Status: openresponses.StatusCompleted}
	body, err := json.Marshal(struct {
		Type   string               `json:"type"`
		Status openresponses.Status `json:"status"`
		Block  json.RawMessage      `json:"block"`
	}{typ, item.Status, raw})
	if err != nil {
		return err
	}
	item.Raw = body
	e := itemEntry(item, ts)
	e.ResponseID = respID
	_, err = sc.messageEntry(k, m, e)
	return err
}

// flushMissing answers the calls of the last assistant message that no
// result in the history answers, as each request does: the output
// Cline synthesises, an error.
func (sc *sconv) flushMissing(k int, ts time.Time) error {
	pending := sc.pending
	sc.pending = nil
	for _, p := range pending {
		if p.output || sc.answered[p.native] {
			continue
		}
		text := missingToolResultText
		if p.name != "" {
			text += " Tool: " + p.name + "."
		}
		out := openresponses.NewFunctionCallOutput(p.id, text)
		e := itemEntry(out, ts)
		if err := sc.b.Member(e, "is_error", true); err != nil {
			return err
		}
		if err := sc.b.Member(e, "synthesized", true); err != nil {
			return err
		}
		id, err := sc.appendEntry(e)
		if err != nil {
			return err
		}
		sc.written(k, id)
		p.output = true
		sc.at(agentimport.Repaired, "clinesdk/result-missing", k, "no tool result answers call %s (%s); every request after it sends %q in its place, which is written, marked %s:synthesized", p.id, p.name, text, Source)
	}
	return nil
}

// callFor is the unanswered call a result answers: the latest call of
// its native ID.
func (sc *sconv) callFor(native string) *call {
	p := sc.calls[native]
	if p == nil || p.output {
		return nil
	}
	return p
}

// result converts a tool result: a reject decision first when it
// refuses the call, a sub-agent's link when the call spawned one, the
// output as Cline sends it, and a team task's link after it.
func (sc *sconv) result(k int, m *clinesdk.Message, b *clinesdk.Block, ts time.Time) error {
	p := sc.callFor(b.ToolUseID)
	if p == nil {
		return sc.orphan(k, m, b, ts)
	}
	content, err := jsjson.Parse(b.Content)
	if err != nil || len(b.Content) == 0 {
		content = jsjson.NewString("")
		sc.at(agentimport.Lossy, "clinesdk/result-malformed", k, "a tool result with no content, or content that is not JSON, is an empty output")
	}
	if by, reason, ok := refusal(content); ok {
		d := agentsession.NewDecision(p.id, p.entry, agentsession.VerdictReject, by).WithReason(reason)
		d.Timestamp = ts
		id, err := sc.appendEntry(d)
		if err != nil {
			return err
		}
		sc.written(k, id)
		sc.at(agentimport.Lossy, "clinesdk/rejection", k, "the tool result refuses call %s (%s); a reject decision by %s precedes the output", p.id, p.name, by)
	}
	parents, err := sc.linkBefore(k, p, ts)
	if err != nil {
		return err
	}
	projected := truncateResult(content)
	out := &openresponses.FunctionCallOutput{CallID: p.id, Output: render(projected, b.IsError)}
	e := itemEntry(out, ts)
	e.Parents = parents
	if b.IsError {
		if err := sc.b.Member(e, "is_error", true); err != nil {
			return err
		}
		sc.at(agentimport.Gap, "agentimport/tool-error", k, "is_error is the member %s:is_error; function_call_output has no error flag", Source)
	}
	if projected != content {
		sc.at(agentimport.Lossy, "clinesdk/request-projection", k, "a tool result Cline cuts to %d characters in the middle for every request; the output holds the cut text, the content as written is %s:original", maxToolResultChars, Source)
		if !sc.fam.opts.Slim {
			raw, err := sc.clean(k+1, b.Content)
			if err != nil {
				return err
			}
			if err := sc.b.Member(e, "original", raw); err != nil {
				return err
			}
		}
	}
	if structured(projected) {
		sc.at(agentimport.Lossy, "clinesdk/structured-output", k, "a structured tool result is the JSON text Cline sends for it")
	}
	if hasImage(projected) {
		sc.images(k)
	}
	mapped := []string{"tool_use_id", "content", "is_error"}
	if b.Name == p.name || b.Name == "" {
		mapped = append(mapped, "name")
	}
	if err := sc.keepMembers(e, k, "block", blockRest(b, mapped...), "the tool result's"); err != nil {
		return err
	}
	if _, err := sc.messageEntry(k, m, e); err != nil {
		return err
	}
	p.output = true
	return sc.linkAfter(k, p, ts)
}

// orphan writes a result no call of the history before it made as a
// custom entry: the session's outputs answer calls on their path.
func (sc *sconv) orphan(k int, m *clinesdk.Message, b *clinesdk.Block, ts time.Time) error {
	raw, err := sc.original(k, b)
	if err != nil {
		return err
	}
	data, err := json.Marshal(map[string]json.RawMessage{"kind": rawJSON("orphan_result"), "block": raw})
	if err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: data}
	e.Timestamp = ts
	id, err := sc.appendEntry(e)
	if err != nil {
		return err
	}
	sc.written(k, id)
	sc.at(agentimport.Lossy, "clinesdk/orphan-result", k, "a tool result for %s, which no call before it made or another result answered, is a custom entry; Cline sends it as written", b.ToolUseID)
	return nil
}

// refusal reads a refused call's result: the runtime's {"error":
// "<reason> -- NOT a tool or system failure. ..."}, or a tool its
// policy disables. by is who refused.
func refusal(content *jsjson.Value) (by, reason string, ok bool) {
	text, _ := content.Text()
	if content.Kind == jsjson.Array {
		for _, e := range content.Items {
			if s, ok := e.Get("text").Text(); ok {
				text = s
				break
			}
		}
	}
	if v, err := jsjson.Parse([]byte(text)); err == nil {
		if s, ok := v.Get("error").Text(); ok {
			text = s
		}
	}
	text = clinesdk.Trim(text)
	if strings.HasSuffix(text, rejectionSuffix) {
		reason = clinesdk.Trim(strings.TrimSuffix(strings.TrimSuffix(text, rejectionSuffix), " -- "))
		by = agentsession.ByHuman
		if strings.Contains(reason, "requires approval but no approval callback is configured") || strings.Contains(reason, "approval request failed") {
			by = agentsession.ByPolicy
		}
		return by, reason, true
	}
	if strings.HasPrefix(text, `Tool "`) && strings.HasSuffix(text, `" is disabled by policy`) {
		return agentsession.ByPolicy, text, true
	}
	return "", "", false
}

// structured reports whether a result is sent as JSON text.
func structured(v *jsjson.Value) bool {
	switch v.Kind {
	case jsjson.Str:
		return false
	case jsjson.Array:
		return !blockArray(v)
	}
	return true
}

// blockArray reports whether a result is an array of text and image
// blocks alone, which Cline sends as content parts.
func blockArray(v *jsjson.Value) bool {
	if v.Kind != jsjson.Array || len(v.Items) == 0 {
		return false
	}
	for _, e := range v.Items {
		t, _ := e.Get("type").Text()
		switch {
		case e.Kind != jsjson.Object:
			return false
		case t == "text":
			if _, ok := e.Get("text").Text(); !ok {
				return false
			}
		case t == "image":
			if !isImageWithData(e) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func hasImage(v *jsjson.Value) bool {
	switch {
	case v == nil:
		return false
	case v.Kind == jsjson.Object:
		if t, _ := v.Get("type").Text(); t == "image" {
			return true
		}
		for _, it := range v.Vals {
			if hasImage(it) {
				return true
			}
		}
	case v.Kind == jsjson.Array:
		for _, it := range v.Items {
			if hasImage(it) {
				return true
			}
		}
	}
	return false
}

// render is toAiSdkToolResultOutput: a string as text; an array of
// text and image blocks as parts; a structured result as its JSON text,
// the images it holds sent after it as parts.
func render(v *jsjson.Value, isError bool) openresponses.FunctionCallOutputData {
	if s, ok := v.Text(); ok {
		return openresponses.FunctionCallOutputData{Text: s}
	}
	if !isError && blockArray(v) {
		parts := openresponses.Contents{}
		for _, e := range v.Items {
			if t, _ := e.Get("type").Text(); t == "image" {
				data, _ := e.Get("data").Text()
				mt, _ := e.Get("mediaType").Text()
				parts = append(parts, &openresponses.InputImage{ImageURL: dataURL(mt, data), Detail: openresponses.ImageDetailAuto})
				continue
			}
			text, _ := e.Get("text").Text()
			parts = append(parts, &openresponses.InputText{Text: text})
		}
		return openresponses.FunctionCallOutputData{Parts: parts}
	}
	if v.Kind == jsjson.Array || v.Kind == jsjson.Object {
		var images []*jsjson.Value
		stripped, changed := stripImages(v, &images, !isError)
		if !isError && len(images) > 0 {
			header, ok := stripped.Text()
			if !ok {
				header = string(stripped.Stringify())
			}
			parts := openresponses.Contents{&openresponses.InputText{Text: header}}
			for _, im := range images {
				data, _ := im.Get("data").Text()
				mt, _ := im.Get("mediaType").Text()
				parts = append(parts, &openresponses.InputImage{ImageURL: dataURL(mt, data), Detail: openresponses.ImageDetailAuto})
			}
			return openresponses.FunctionCallOutputData{Parts: parts}
		}
		if changed {
			return openresponses.FunctionCallOutputData{Text: string(stripped.Stringify())}
		}
	}
	return openresponses.FunctionCallOutputData{Text: string(v.Stringify())}
}

// stripImages is stripImagesFromOutput: images taken out of a
// structured result, into images when hoist is set, else replaced by a
// placeholder, and a text block in an array replaced by its text.
// media says an image was taken out.
func stripImages(v *jsjson.Value, images *[]*jsjson.Value, hoist bool) (out *jsjson.Value, media bool) {
	switch {
	case v == nil || v.Kind != jsjson.Array && v.Kind != jsjson.Object:
		return v, false
	case v.Kind == jsjson.Array:
		res := &jsjson.Value{Kind: jsjson.Array, Items: []*jsjson.Value{}}
		changed := false
		for _, it := range v.Items {
			if it.Kind == jsjson.Object {
				t, _ := it.Get("type").Text()
				switch {
				case t == "image" && isImageWithData(it):
					changed, media = true, true
					if !hoist {
						res.Items = append(res.Items, jsjson.NewString(imageOmitted))
						continue
					}
					*images = append(*images, it)
					continue
				case t == "image":
					changed, media = true, true
					res.Items = append(res.Items, jsjson.NewString(imageOmitted))
					continue
				case t == "text":
					if s, ok := it.Get("text").Text(); ok {
						changed = true
						res.Items = append(res.Items, jsjson.NewString(s))
						continue
					}
				}
			}
			s, m := stripImages(it, images, hoist)
			res.Items = append(res.Items, s)
			changed = changed || s != it
			media = media || m
		}
		if !changed {
			return v, media
		}
		return res, media
	}
	if t, _ := v.Get("type").Text(); t == "image" {
		if isImageWithData(v) && hoist {
			*images = append(*images, v)
			return jsjson.NewString(imageAttached), true
		}
		return jsjson.NewString(imageOmitted), true
	}
	res := &jsjson.Value{Kind: jsjson.Object, Keys: append([]jsjson.String(nil), v.Keys...), Vals: make([]*jsjson.Value, len(v.Vals))}
	changed := false
	for i, it := range v.Vals {
		s, m := stripImages(it, images, hoist)
		res.Vals[i] = s
		changed = changed || s != it
		media = media || m
	}
	if !changed {
		return v, media
	}
	return res, media
}

// assistant converts an assistant message: its blocks the items of one
// response, then the response entry with its model and usage.
func (sc *sconv) assistant(k int, m *clinesdk.Message, ts time.Time) error {
	if err := sc.flushMissing(k, ts); err != nil {
		return err
	}
	if err := sc.request(k, ts); err != nil {
		return err
	}
	if mi := m.ModelInfo; mi != nil && mi.ID != "" && mi.ID != sc.model {
		cfg := &agentsession.ConfigEntry{Model: mi.ID}
		cfg.Timestamp = ts
		if mi.Provider != "" {
			if err := sc.b.Member(cfg, "provider", mi.Provider); err != nil {
				return err
			}
		}
		id, err := sc.appendEntry(cfg)
		if err != nil {
			return err
		}
		sc.written(k, id)
		sc.model = mi.ID
	}
	respID := sc.responseID(k)
	provider := ""
	if m.ModelInfo != nil {
		provider = m.ModelInfo.Provider
	}
	add := func(item openresponses.Item, b *clinesdk.Block, mapped []string, sigProvider string) error {
		e := itemEntry(item, ts)
		e.ResponseID = respID
		if sigProvider != "" {
			if err := sc.b.Member(e, "provider", sigProvider); err != nil {
				return err
			}
			sc.at(agentimport.Gap, "agentimport/reasoning-provider", k, "a reasoning signature from %s in encrypted_content, tagged %s:provider", sigProvider, Source)
		}
		if b != nil && !m.Content.IsText {
			if err := sc.keepMembers(e, k, "block", blockRest(b, mapped...), "the "+b.Type+" block's"); err != nil {
				return err
			}
		}
		_, err := sc.messageEntry(k, m, e)
		return err
	}
	signature := func(sig string) error {
		r := &openresponses.ReasoningItem{Status: openresponses.StatusCompleted, Summary: openresponses.Contents{}, EncryptedContent: sig}
		return add(r, nil, nil, orUnknown(provider))
	}
	var texts []string
	var calls []*call
	for _, b := range m.Blocks() {
		if b.Err != nil {
			sc.at(agentimport.Dropped, "clinesdk/null-block", k, "a content block that is not an object is left out")
			continue
		}
		if len(b.Mistyped()) > 0 {
			sc.at(agentimport.Lossy, "clinesdk/mistyped-member", k, "a %s block's members %s are not of their type; they are kept and read as empty", b.Type, strings.Join(b.Mistyped(), ", "))
		}
		switch b.Type {
		case clinesdk.BlockThinking:
			r := &openresponses.ReasoningItem{Status: openresponses.StatusCompleted, Summary: openresponses.Contents{}, EncryptedContent: b.Signature}
			if b.Thinking != "" {
				r.Summary = openresponses.Contents{&openresponses.SummaryText{Text: b.Thinking}}
			}
			sig := ""
			if b.Signature != "" {
				sig = orUnknown(provider)
			}
			if err := add(r, b, []string{"thinking", "signature"}, sig); err != nil {
				return err
			}
		case clinesdk.BlockText:
			if b.Signature != "" {
				if err := signature(b.Signature); err != nil {
					return err
				}
			}
			item := &openresponses.Message{Role: openresponses.RoleAssistant, Status: openresponses.StatusCompleted,
				Content: openresponses.Contents{&openresponses.OutputText{Text: b.Text, Annotations: openresponses.Annotations{}}}}
			if err := add(item, b, []string{"text", "signature"}, ""); err != nil {
				return err
			}
			texts = append(texts, b.Text)
		case clinesdk.BlockToolUse:
			if b.Signature != "" {
				if err := signature(b.Signature); err != nil {
					return err
				}
			}
			p, err := sc.toolUse(k, m, b, respID, ts)
			if err != nil {
				return err
			}
			calls = append(calls, p)
		case clinesdk.BlockRedactedThinking:
			if err := sc.extension(k, m, b, ts, respID); err != nil {
				return err
			}
			sc.at(agentimport.Kept, "clinesdk/extension-item", k, "a redacted_thinking block is the anthropic.redacted_thinking item")
		default:
			if err := sc.extension(k, m, b, ts, respID); err != nil {
				return err
			}
			sc.at(agentimport.Kept, "clinesdk/extension-item", k, "a %s block is a %s item", b.Type, extType(b.Type))
		}
	}
	re, err := sc.response(k, m, respID, ts)
	if err != nil {
		return err
	}
	id, err := sc.appendEntry(re)
	if err != nil {
		return err
	}
	sc.written(k, id)
	if len(m.Blocks()) == 0 {
		sc.at(agentimport.Lossy, "clinesdk/empty-message", k, "an assistant message with no content is a response with no items; Cline sends %q in its place", emptyContent)
	}
	sc.pending = calls
	for _, t := range texts {
		if cut := truncateAssistantText(t); cut != t {
			if err := sc.replaced(k, ts, map[string]any{"kind": "assistant_text", "response": respID, "text": cut}); err != nil {
				return err
			}
			sc.at(agentimport.Lossy, "clinesdk/request-projection", k, "an assistant text of %d characters, which every later request sends cut in the middle; the cut text is a custom entry after the response", utf16Len(t))
		}
	}
	return nil
}

func orUnknown(provider string) string {
	if provider == "" {
		return "unknown"
	}
	return provider
}

// toolUse writes a tool_use block as a function call.
func (sc *sconv) toolUse(k int, m *clinesdk.Message, b *clinesdk.Block, respID string, ts time.Time) (*call, error) {
	args := "{}"
	if in, err := jsjson.Parse(b.Input); err == nil {
		args = string(in.Stringify())
		if in.Kind != jsjson.Object {
			sc.at(agentimport.Lossy, "clinesdk/invalid-input", k, "a tool_use input that is not a JSON object is recorded as its JSON text")
		}
	} else if !clinesdk.IsNull(b.Input) {
		sc.at(agentimport.Lossy, "clinesdk/invalid-input", k, "a tool_use input that is not JSON is recorded as {}")
	}
	id, renamed := sc.b.Calls.Assign(b.ID)
	item := &openresponses.FunctionCall{Status: openresponses.StatusCompleted, CallID: id, Name: b.Name, Arguments: args}
	e := itemEntry(item, ts)
	e.ResponseID = respID
	if renamed {
		if err := sc.b.Member(e, "call_id", b.ID); err != nil {
			return nil, err
		}
		sc.at(agentimport.Repaired, "clinesdk/call-id-renamed", k, "call %q is recorded as %q, unique in the session", b.ID, id)
	}
	if err := sc.keepMembers(e, k, "block", blockRest(b, "id", "name", "input", "signature"), "the tool_use block's"); err != nil {
		return nil, err
	}
	eid, err := sc.messageEntry(k, m, e)
	if err != nil {
		return nil, err
	}
	p := &call{native: b.ID, id: id, name: b.Name, entry: eid, msg: k}
	sc.calls[b.ID] = p
	return p, nil
}

// responseID gives each response an ID: Cline records none.
func (sc *sconv) responseID(k int) string {
	sc.respN++
	id := fmt.Sprintf("resp_%d", k+1)
	for n := 2; sc.respIDs[id]; n++ {
		id = fmt.Sprintf("resp_%d_%d", k+1, n)
	}
	sc.respIDs[id] = true
	sc.at(agentimport.Lossy, "clinesdk/no-response-id", k, "Cline records no response ID; the response is %s", id)
	return id
}

// response is the envelope of an assistant message: its model and its
// usage, kept off it for a copied message.
func (sc *sconv) response(k int, m *clinesdk.Message, respID string, ts time.Time) (*agentsession.ResponseEntry, error) {
	e := &agentsession.ResponseEntry{ResponseID: respID, Status: openresponses.ResponseStatusCompleted}
	e.Timestamp = ts
	if m.ModelInfo != nil {
		e.Model = m.ModelInfo.ID
	}
	if x := m.Metrics; x != nil {
		u := usage(x)
		if k < sc.copyEnd {
			if err := sc.b.Member(e, "copied_usage", m.Metrics); err != nil {
				return nil, err
			}
			sc.at(agentimport.Kept, "clinesdk/copied-usage", k, "the response is a copy of %s, whose usage is %s:copied_usage, so it is not counted twice", copyWhose(sc.copyWhy), Source)
		} else {
			e.Usage = u
			if err := sc.b.Member(e, "usage", m.Metrics); err != nil {
				return nil, err
			}
			sc.at(agentimport.Gap, "agentimport/usage-detail", k, "inputTokens counts cache reads and writes already; cacheReadTokens is cached_tokens, and the metrics with the cache writes and the cost are %s:usage", Source)
		}
	}
	rest, err := messageRest(m, "modelInfo", "metrics")
	if err != nil {
		return nil, err
	}
	if m.ModelInfo != nil {
		mi := map[string]string{"provider": m.ModelInfo.Provider, "family": m.ModelInfo.Family}
		for key, v := range mi {
			if v == "" {
				delete(mi, key)
			}
		}
		if len(mi) > 0 || len(m.ModelInfo.Extra) > 0 {
			raw, err := json.Marshal(m.ModelInfo)
			if err != nil {
				return nil, err
			}
			if err := sc.b.Member(e, "model_info", json.RawMessage(raw)); err != nil {
				return nil, err
			}
		}
	}
	if !sc.rested[k] {
		sc.rested[k] = true
		return e, sc.keepMembers(e, k, "message", rest, "the message's")
	}
	return e, nil
}

func copyWhose(why string) string {
	switch why {
	case "legacy":
		return "the classic task's history"
	case "fork":
		return "the forked session's history"
	}
	return "an earlier team task's conversation"
}

// usage is the Responses usage of Cline's metrics: inputTokens is the
// whole input already, cache reads included.
func usage(x *clinesdk.Metrics) *openresponses.Usage {
	n := func(raw json.RawMessage) int {
		v, exact, ok := clinesdk.Int(raw)
		if !ok || !exact || v < 0 || v > 1<<40 {
			return 0
		}
		return int(v)
	}
	u := &openresponses.Usage{InputTokens: n(x.InputTokens), OutputTokens: n(x.OutputTokens)}
	u.InputTokensDetails.CachedTokens = n(x.CacheReadTokens)
	u.TotalTokens = u.InputTokens + u.OutputTokens
	return u
}

// replaced writes a context edit the session format cannot write in
// place: what Cline sends from this request on in place of an earlier
// item, as a custom entry.
func (sc *sconv) replaced(k int, ts time.Time, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if raw, err = sc.clean(k+1, raw); err != nil {
		return err
	}
	e := &agentsession.CustomEntry{NS: Source, Data: raw}
	e.Timestamp = ts
	id, err := sc.appendEntry(e)
	if err != nil {
		return err
	}
	sc.written(k, id)
	sc.at(agentimport.Gap, "agentimport/context-replace", k, "Cline sends an earlier item rewritten from this request on; the original stays in context and the rewrite is a custom entry")
	return nil
}
