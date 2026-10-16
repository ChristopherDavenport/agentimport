package convert

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/openresponses"
)

// sentText is the text Hermes sends for a user or assistant row whose
// content is text: the api_content sidecar when the row holds one,
// substituted at send time (agent/turn_context.py
// substitute_api_content), else the content as the loader gives it,
// sanitize_context(...).strip() (_rows_to_conversation); the request's
// closing pass strips every text either way.
func sentText(m *hermes.Message) (text string, fromSidecar bool) {
	if m.HasAPIContent && (m.Role == hermes.RoleUser || m.Role == hermes.RoleAssistant) {
		return strip(m.APIContent), true
	}
	if m.Role == hermes.RoleUser || m.Role == hermes.RoleAssistant {
		return strip(sanitizeContext(m.Content.Text)), false
	}
	return strip(m.Content.Text), false
}

// chatPart is a chat-completions content part.
type chatPart struct {
	Type     string          `json:"type"`
	Text     *string         `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

// parts maps chat-completions content parts to Open Responses content:
// text to input or output text, an image_url to an input image, a part
// Open Responses defines under its own name as itself, and any other
// part to an extension part of its definer's prefix, carrying it. kept
// counts the parts so carried.
func parts(raws []json.RawMessage, assistant bool) (out openresponses.Contents, kept int) {
	for _, raw := range raws {
		var p chatPart
		if json.Unmarshal(raw, &p) != nil {
			out = append(out, extensionPart("hermes.part", raw))
			kept++
			continue
		}
		switch {
		case p.Type == "text" && p.Text != nil:
			if assistant {
				out = append(out, &openresponses.OutputText{Text: *p.Text})
			} else {
				out = append(out, &openresponses.InputText{Text: *p.Text})
			}
		case p.Type == "image_url" && len(p.ImageURL) > 0:
			if img, ok := imageURL(p.ImageURL); ok {
				out = append(out, img)
				continue
			}
			out = append(out, extensionPart("openai.image_url", raw))
			kept++
		case p.Type == "input_text" || p.Type == "output_text" || p.Type == "input_image" || p.Type == "input_file":
			var cs openresponses.Contents
			if json.Unmarshal([]byte("["+string(raw)+"]"), &cs) == nil && len(cs) == 1 {
				if _, unknown := cs[0].(*openresponses.UnknownContent); !unknown {
					out = append(out, cs[0])
					continue
				}
			}
			out = append(out, extensionPart("hermes.part", raw))
			kept++
		case p.Type != "" && !strings.Contains(p.Type, "."):
			out = append(out, extensionPart("openai."+p.Type, raw))
			kept++
		default:
			out = append(out, extensionPart("hermes.part", raw))
			kept++
		}
	}
	return out, kept
}

// imageURL reads an image_url member, an object with url and detail or
// a bare URL.
func imageURL(raw json.RawMessage) (*openresponses.InputImage, bool) {
	var obj struct {
		URL    string `json:"url"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.URL != "" {
		img := &openresponses.InputImage{ImageURL: obj.URL}
		switch obj.Detail {
		case "low", "high", "auto":
			img.Detail = openresponses.ImageDetail(obj.Detail)
		}
		return img, true
	}
	var url string
	if json.Unmarshal(raw, &url) == nil && url != "" {
		return &openresponses.InputImage{ImageURL: url}, true
	}
	return nil, false
}

// extensionPart is a content part Open Responses does not define,
// carried under typ: the part's members with its type replaced, or,
// when it is no object, the part whole in "part".
func extensionPart(typ string, raw json.RawMessage) openresponses.Content {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || m == nil {
		m = map[string]json.RawMessage{"part": raw}
	}
	if old, ok := m["type"]; ok && typ == "hermes.part" {
		m["native_type"] = old
	}
	m["type"] = rawJSON(typ)
	out, _ := json.Marshal(m)
	out, _ = cleanRaw(out)
	return &openresponses.UnknownContent{Type: typ, Raw: out}
}

// reasoning is what an assistant row's reasoning carriers map to.
type reasoning struct {
	items []openresponses.Item
	// from names the carrier the items came from; providers name the
	// provider of each item's encrypted content, when it carries one.
	from      string
	providers []string
	// kept holds the entries of the carrier keptFrom no item holds.
	kept     []json.RawMessage
	keptFrom string
	// issuer holds each item's stamps Hermes put on it.
	issuer []json.RawMessage
	// bad counts the carrier entries that are not objects.
	bad int
}

// reasoningOf maps a row's reasoning, from the carrier the route that
// replays it reads first: the Responses items Hermes keeps for its
// Codex route (codex_reasoning_items, replayed verbatim but for id and
// Hermes' own stamps), else the provider's native reasoning_details,
// else the text, reasoning_content before reasoning
// (_replayable_reasoning_text).
func reasoningOf(m *hermes.Message) reasoning {
	var r reasoning
	if len(m.CodexReasoningItems) > 0 {
		r.from = "codex_reasoning_items"
		for _, raw := range splitArray(m.CodexReasoningItems) {
			var obj map[string]json.RawMessage
			if json.Unmarshal(raw, &obj) != nil || obj == nil {
				r.bad++
				r.kept = append(r.kept, raw)
				continue
			}
			stamp := map[string]json.RawMessage{}
			for _, k := range []string{"id", "_issuer_kind", "_issuer_model"} {
				if v, ok := obj[k]; ok {
					stamp[k] = v
					delete(obj, k)
				}
			}
			clean, _ := json.Marshal(obj)
			it, err := openresponses.UnmarshalItem(clean)
			switch it.(type) {
			case *openresponses.ReasoningItem, *openresponses.Compaction:
			default:
				err = errUnmapped
			}
			if err != nil {
				r.kept = append(r.kept, raw)
				continue
			}
			r.items = append(r.items, it)
			r.issuer = append(r.issuer, rawJSON(stamp))
			provider := "openai"
			var kind string
			if json.Unmarshal(stamp["_issuer_kind"], &kind) == nil && kind != "" {
				provider = kind
			}
			r.providers = append(r.providers, provider)
		}
		if len(r.kept) > 0 {
			r.keptFrom = "codex_reasoning_items"
		}
		if len(r.items) > 0 {
			return r
		}
	}
	if len(m.ReasoningDetails) > 0 && len(r.kept) == 0 {
		r = reasoning{from: "reasoning_details"}
		for _, raw := range splitArray(m.ReasoningDetails) {
			it, provider, ok := detailItem(raw)
			if !ok {
				if !isObject(raw) {
					r.bad++
				}
				r.kept = append(r.kept, raw)
				continue
			}
			r.items = append(r.items, it)
			r.providers = append(r.providers, provider)
		}
		if len(r.kept) > 0 {
			r.keptFrom = "reasoning_details"
		}
		if len(r.items) > 0 {
			return r
		}
	}
	text := ""
	if m.ReasoningContent != nil && strings.TrimSpace(*m.ReasoningContent) != "" {
		text, r.from = *m.ReasoningContent, "reasoning_content"
	} else if strings.TrimSpace(m.Reasoning) != "" {
		text, r.from = m.Reasoning, "reasoning"
	}
	if text != "" {
		r.items = []openresponses.Item{&openresponses.ReasoningItem{Content: openresponses.Contents{&openresponses.ReasoningText{Text: text}}}}
		r.providers = []string{""}
	} else {
		r.from = ""
	}
	return r
}

var errUnmapped = jsonErr("not a reasoning or compaction item")

type jsonErr string

func (e jsonErr) Error() string { return string(e) }

// detailItem maps one entry of reasoning_details: an Anthropic thinking
// block (agent/transports/anthropic.py keeps the blocks there) to a
// reasoning item, its text the summary and its signature in
// encrypted_content, as providers/anthropic does, and redacted thinking
// to an anthropic.redacted_thinking item; OpenRouter's reasoning.text
// the same way; reasoning.summary to a summary and reasoning.encrypted
// to encrypted_content. Thinking text that carries no signature is the
// item's content. Anything else, a provider's native carrier among them,
// is no item.
func detailItem(raw json.RawMessage) (openresponses.Item, string, bool) {
	var d struct {
		Type      string `json:"type"`
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
		Data      string `json:"data"`
		Text      string `json:"text"`
		Summary   string `json:"summary"`
		Format    string `json:"format"`
		ID        string `json:"id"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return nil, "", false
	}
	summary := func(text string) openresponses.Contents {
		if text == "" {
			return nil
		}
		return openresponses.Contents{&openresponses.SummaryText{Text: text}}
	}
	// Text a signature covers is a summary beside its encrypted_content,
	// as providers/anthropic writes a thinking block; text no signature
	// covers is the item's content, as openresponses/chatcompletions
	// reads plain reasoning text and replays it.
	text := func(t, sig string) *openresponses.ReasoningItem {
		if sig != "" {
			return &openresponses.ReasoningItem{Summary: summary(t), EncryptedContent: sig}
		}
		r := &openresponses.ReasoningItem{}
		if t != "" {
			r.Content = openresponses.Contents{&openresponses.ReasoningText{Text: t}}
		}
		return r
	}
	switch d.Type {
	case "thinking":
		return text(d.Thinking, d.Signature), "anthropic", true
	case "redacted_thinking":
		block, _ := json.Marshal(map[string]string{"type": "redacted_thinking", "data": d.Data})
		item, _ := json.Marshal(map[string]json.RawMessage{"type": rawJSON("anthropic.redacted_thinking"), "block": block})
		return &openresponses.UnknownItem{Type: "anthropic.redacted_thinking", Raw: item}, "anthropic", true
	case "reasoning.text":
		return text(d.Text, d.Signature), providerOf(d.Format), true
	case "reasoning.summary":
		return &openresponses.ReasoningItem{Summary: summary(d.Summary)}, providerOf(d.Format), true
	case "reasoning.encrypted":
		return &openresponses.ReasoningItem{EncryptedContent: d.Data}, providerOf(d.Format), true
	}
	return nil, "", false
}

// providerOf names the provider of an OpenRouter reasoning detail by its
// format, such as anthropic-claude-v1 or openai-responses-v1.
func providerOf(format string) string {
	if format == "" {
		return "openrouter"
	}
	head, _, _ := strings.Cut(format, "-")
	return head
}

// splitArray splits a JSON array into its elements.
func splitArray(raw json.RawMessage) []json.RawMessage {
	var out []json.RawMessage
	_ = json.Unmarshal(raw, &out)
	return out
}

// isObject reports whether raw is a JSON object.
func isObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

// codexMessages are the assistant messages of codex_message_items,
// each replayed as its own message with its output text and phase
// (agent/codex_responses_adapter.py _replay_message_items); its ID is
// dropped when the turn's Codex reasoning items carry encrypted
// content, which the provider refuses the ID without. Nil when the
// column holds none.
func codexMessages(m *hermes.Message) []*openresponses.Message {
	linked := false
	for _, raw := range splitArray(m.CodexReasoningItems) {
		var ri struct {
			EncryptedContent string `json:"encrypted_content"`
		}
		if json.Unmarshal(raw, &ri) == nil && ri.EncryptedContent != "" {
			linked = true
		}
	}
	var out []*openresponses.Message
	for _, raw := range splitArray(m.CodexMessageItems) {
		var it struct {
			Type    string            `json:"type"`
			Role    string            `json:"role"`
			ID      string            `json:"id"`
			Phase   string            `json:"phase"`
			Content []json.RawMessage `json:"content"`
		}
		if json.Unmarshal(raw, &it) != nil || it.Type != "message" || it.Role != "assistant" {
			continue
		}
		msg := &openresponses.Message{Role: openresponses.RoleAssistant}
		for _, c := range it.Content {
			var part struct {
				Type string  `json:"type"`
				Text *string `json:"text"`
			}
			if json.Unmarshal(c, &part) == nil && part.Text != nil && (part.Type == "output_text" || part.Type == "text") {
				msg.Content = append(msg.Content, &openresponses.OutputText{Text: *part.Text})
			}
		}
		if len(msg.Content) == 0 {
			continue
		}
		switch it.Phase {
		case string(openresponses.PhaseCommentary), string(openresponses.PhaseFinalAnswer):
			msg.Phase = openresponses.Phase(it.Phase)
		}
		if !linked {
			msg.ID = it.ID
		}
		out = append(out, msg)
	}
	return out
}

// thoughtSignature reads the Gemini thought signature an
// OpenAI-compatible Gemini route puts on a tool call,
// extra_content.google.thought_signature.
func thoughtSignature(c *hermes.ToolCall) string {
	var ec struct {
		Google struct {
			ThoughtSignature string `json:"thought_signature"`
		} `json:"google"`
	}
	if raw, ok := c.Extra["extra_content"]; ok && json.Unmarshal(raw, &ec) == nil {
		return ec.Google.ThoughtSignature
	}
	return ""
}

// handle is what a delegate_task call's output says of the run: for a
// background run, Hermes' handle of it, {"status": "dispatched", "mode":
// "background", "delegation_id": …, "goals": […], "units": [{
// "delegation_id", "task_indexes"}], "inline_results": […]}
// (tools/delegate_tool_dispatch.py _dispatched_payload and
// _dispatch_background): one delegation per unit of tasks, and the
// tasks of a unit the pool refused run inline before the call returns.
type handle struct {
	background   bool
	delegationID string
	goals        []string
	units        []unit
	inline       map[int]bool
}

// unit is a background unit: its delegation and the tasks it ran.
type unit struct {
	id    string
	tasks []int
}

// readHandle reads a delegate_task call's output.
func readHandle(output string) handle {
	var p struct {
		Status       string   `json:"status"`
		Mode         string   `json:"mode"`
		DelegationID string   `json:"delegation_id"`
		Goals        []string `json:"goals"`
		Units        []struct {
			DelegationID string `json:"delegation_id"`
			TaskIndexes  []int  `json:"task_indexes"`
		} `json:"units"`
		Inline []struct {
			TaskIndex *int `json:"task_index"`
		} `json:"inline_results"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &p) != nil {
		return handle{}
	}
	h := handle{background: p.Status == "dispatched" || p.Mode == "background", delegationID: p.DelegationID, goals: p.Goals}
	for _, u := range p.Units {
		h.units = append(h.units, unit{id: u.DelegationID, tasks: u.TaskIndexes})
	}
	for _, r := range p.Inline {
		if r.TaskIndex != nil {
			if h.inline == nil {
				h.inline = map[int]bool{}
			}
			h.inline[*r.TaskIndex] = true
		}
	}
	return h
}

// ids lists the delegations a handle names: its units', else its own.
func (h handle) ids() []string {
	var out []string
	for _, u := range h.units {
		if u.id != "" {
			out = append(out, u.id)
		}
	}
	if len(out) == 0 && h.delegationID != "" {
		out = append(out, h.delegationID)
	}
	return out
}

// unitOf is the delegation that ran task i: its unit's, else the
// handle's.
func (h handle) unitOf(i int) string {
	for _, u := range h.units {
		for _, t := range u.tasks {
			if t == i {
				return u.id
			}
		}
	}
	return h.delegationID
}

// goals reads the goals of a delegate_task call: each task's, or the
// legacy single goal.
func goals(args string) []string {
	var a struct {
		Goal  string `json:"goal"`
		Tasks []struct {
			Goal string `json:"goal"`
		} `json:"tasks"`
	}
	if json.Unmarshal([]byte(args), &a) != nil {
		return nil
	}
	var out []string
	if strings.TrimSpace(a.Goal) != "" {
		out = append(out, strings.TrimSpace(a.Goal))
	}
	for _, t := range a.Tasks {
		if strings.TrimSpace(t.Goal) != "" {
			out = append(out, strings.TrimSpace(t.Goal))
		}
	}
	return out
}
