// Portions of this file are ported from anomalyco/opencode
// (https://github.com/anomalyco/opencode) at v1.19.1 and earlier tags,
// Copyright (c) 2025 opencode, licensed under the MIT License; they
// have been modified. See THIRD_PARTY_NOTICES.md.

package convert

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/openresponses"
)

// provided is what the AI SDK's provider metadata on a part says that
// a provider needs back: Anthropic's thinking signature or redacted
// thinking, OpenAI's item ID, encrypted reasoning and phase, Gemini's
// thought signature.
type provided struct {
	// ns is the namespace the metadata was under: the AI SDK provider
	// name, such as anthropic, openai or google.
	ns        string
	signature string
	redacted  string
	itemID    string
	encrypted string
	thought   string
	phase     string
	// nested says the record was under its namespace where OpenCode 2
	// keeps it bare: a row its migration copied from the V1 tables.
	nested bool
}

// metadataKeys are the members of a provider's record a converter reads.
var metadataKeys = []string{"signature", "redactedData", "itemId", "reasoningEncryptedContent", "thoughtSignature", "phase"}

// readRecord reads one provider's record.
func readRecord(raw json.RawMessage) (provided, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return provided{}, false
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(m[k], &s)
		return s
	}
	p := provided{
		signature: str("signature"), redacted: str("redactedData"), itemID: str("itemId"),
		encrypted: str("reasoningEncryptedContent"), thought: str("thoughtSignature"), phase: str("phase"),
	}
	ok := false
	for _, k := range metadataKeys {
		if _, has := m[k]; has {
			ok = true
		}
	}
	return p, ok
}

// readMetadata reads V1 provider metadata, a record per provider
// namespace, as the AI SDK writes it: the first namespace, in name
// order, holding a member a provider needs back.
func readMetadata(raw json.RawMessage) provided {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return provided{}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if p, ok := readRecord(m[k]); ok {
			p.ns = k
			return p
		}
	}
	return provided{}
}

// readState reads V2 provider state: the bare record of the session's
// provider, or, in a row OpenCode 2's migration copied, the V1
// namespaced metadata.
func readState(raw json.RawMessage, provider string) provided {
	if p, ok := readRecord(raw); ok {
		p.ns = provider
		return p
	}
	p := readMetadata(raw)
	if p.ns != "" {
		p.nested = true
	}
	return p
}

// reasoningItems maps a reasoning part: Anthropic's redacted thinking
// to an anthropic.redacted_thinking item carrying the block, anything
// else to a reasoning item, its text the summary and the signature or
// encrypted content in encrypted_content, as providers/anthropic does.
func reasoningItems(text string, p provided) openresponses.Item {
	if p.redacted != "" {
		raw, _ := json.Marshal(map[string]any{
			"type":  "anthropic.redacted_thinking",
			"block": map[string]string{"type": "redacted_thinking", "data": p.redacted},
		})
		return &openresponses.UnknownItem{Type: "anthropic.redacted_thinking", Raw: raw}
	}
	r := &openresponses.ReasoningItem{ID: p.itemID}
	if text != "" {
		r.Summary = openresponses.Contents{&openresponses.SummaryText{Text: text}}
	}
	switch {
	case p.signature != "":
		r.EncryptedContent = p.signature
	case p.encrypted != "":
		r.EncryptedContent = p.encrypted
	case p.thought != "":
		r.EncryptedContent = p.thought
	}
	return r
}

// thoughtItem is the reasoning item holding only a Gemini thought
// signature that goes before the text or call it was on, as
// providers/gemini reads one.
func thoughtItem(p provided) openresponses.Item {
	if p.thought == "" {
		return nil
	}
	return &openresponses.ReasoningItem{EncryptedContent: p.thought}
}

// textItem is an assistant text: its item ID and phase from OpenAI's
// metadata.
func textItem(text string, p provided) *openresponses.Message {
	m := openresponses.AssistantText(text)
	m.ID = p.itemID
	switch p.phase {
	case string(openresponses.PhaseCommentary), string(openresponses.PhaseFinalAnswer):
		m.Phase = openresponses.Phase(p.phase)
	}
	return m
}

// arguments is a call's input as the arguments string of a function
// call: an object re-encoded compact, a string as it is.
func arguments(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return "{}"
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s
		}
	}
	var b bytes.Buffer
	if json.Compact(&b, t) == nil {
		return b.String()
	}
	return string(t)
}

// usage maps OpenCode's token counts to Responses usage. OpenCode's
// input counts neither cache reads nor writes, so both are folded in,
// as providers/anthropic does; its output counts reasoning up to 1.3.15
// and leaves it out after. What Responses usage has no place for, the
// cache writes, the cost and OpenCode's total, is returned apart.
func usage(t *opencode.Tokens, cost json.RawMessage, outputHasReasoning bool) (*openresponses.Usage, map[string]any) {
	if t == nil {
		return nil, nil
	}
	read, write := 0, 0
	if t.Cache != nil {
		read, write = opencode.Count(t.Cache.Read), opencode.Count(t.Cache.Write)
	}
	in := opencode.Count(t.Input) + read + write
	reasoning := opencode.Count(t.Reasoning)
	out := opencode.Count(t.Output)
	if !outputHasReasoning {
		out += reasoning
	}
	u := &openresponses.Usage{
		InputTokens:         in,
		OutputTokens:        out,
		TotalTokens:         in + out,
		InputTokensDetails:  openresponses.InputTokensDetails{CachedTokens: read},
		OutputTokensDetails: openresponses.OutputTokensDetails{ReasoningTokens: reasoning},
	}
	extra := map[string]any{}
	if write > 0 {
		extra["cache_write_tokens"] = write
	}
	if f, ok := opencode.Float(cost); ok && f != 0 {
		extra["cost"] = json.RawMessage(bytes.TrimSpace(cost))
	}
	if n := opencode.Count(t.Total); n > 0 {
		extra["total"] = n
	}
	if len(extra) == 0 {
		extra = nil
	}
	return u, extra
}

// isImage reports a mime type a provider takes as an image.
func isImage(mime string) bool { return strings.HasPrefix(mime, "image/") }

// fileContent maps a file the AI SDK sends by its URL: a data URL
// inline, any other URL by reference, which the provider fetches.
func fileContent(mime, filename, url string) openresponses.Content {
	data := strings.HasPrefix(url, "data:")
	if isImage(mime) {
		return &openresponses.InputImage{ImageURL: url}
	}
	if data {
		return &openresponses.InputFile{Filename: filename, FileData: url}
	}
	return &openresponses.InputFile{Filename: filename, FileURL: url}
}

// toolOutput is a completed V1 tool's output as OpenCode's
// toModelOutput gives it: the text alone, or, with attachments whose
// URLs are data URLs, the text when it is not empty and each
// attachment as media.
func toolOutput(text string, attachments []json.RawMessage) (openresponses.FunctionCallOutputData, int) {
	var media []openresponses.Content
	for _, raw := range attachments {
		a, ok := opencode.Attachment(raw)
		if !ok || !strings.HasPrefix(a.URL, "data:") || !strings.Contains(a.URL, ",") {
			continue
		}
		media = append(media, fileContent(a.Mime, a.Filename, a.URL))
	}
	if len(media) == 0 {
		return openresponses.FunctionCallOutputData{Text: text}, 0
	}
	var parts openresponses.Contents
	if text != "" {
		parts = append(parts, &openresponses.InputText{Text: text})
	}
	return openresponses.FunctionCallOutputData{Parts: append(parts, media...)}, len(media)
}

// mediaInToolResults lists the AI SDK providers OpenCode sends media in
// a tool result to; for the others it moves the media to a user message
// after the assistant's.
var mediaInToolResults = map[string]bool{
	"anthropic": true, "openai": true, "amazon-bedrock": true, "google-vertex-anthropic": true,
}
