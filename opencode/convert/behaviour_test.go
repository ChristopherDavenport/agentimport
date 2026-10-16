package convert

import (
	"encoding/json"
	"testing"

	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/openresponses"
)

func TestParseVersion(t *testing.T) {
	for s, want := range map[string]version{"1.14.40": {1, 14, 40}, "v1.3.16": {1, 3, 16}, "0.0.0-202601010000": {0, 0, 0}, "2.0.30+build": {2, 0, 30}} {
		if got, ok := parseVersion(s); !ok || got != want {
			t.Errorf("parseVersion(%q) = %v, %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "1.2", "x.y.z", "1.-2.3"} {
		if _, ok := parseVersion(s); ok {
			t.Errorf("parseVersion(%q) reads", s)
		}
	}
	v, _ := parseVersion("1.14.39")
	if v.atLeast(v11440) || !v.atLeast(v11419) {
		t.Error("1.14.39 is between 1.14.19 and 1.14.40")
	}
}

func TestPermissionText(t *testing.T) {
	for text, want := range map[string]string{
		"The user rejected permission to use this specific tool call.":                                       "human",
		"Error: The user rejected permission to use this specific tool call with the following feedback: no": "human",
		"The user has specified a rule which prevents you from using this specific tool call. Here are":      "policy",
		"Error: file not found": "",
	} {
		if _, by := permissionText(text); by != want {
			t.Errorf("permissionText(%q) = %q, want %q", text, by, want)
		}
	}
}

// TestBlocks splits an assistant message's parts as the AI SDK's
// convertToModelMessages does: at each step-start, the parts before the
// first a block of their own.
func TestBlocks(t *testing.T) {
	part := func(typ string) *opencode.PartRecord {
		return &opencode.PartRecord{Part: &opencode.Part{Type: typ}}
	}
	parts := []*opencode.PartRecord{part("text"), part("step-start"), part("reasoning"), part("tool"), part("step-finish"), part("patch"), part("step-start"), part("text")}
	bs := blocks(parts)
	if len(bs) != 3 {
		t.Fatalf("%d blocks", len(bs))
	}
	if bs[0].start != nil || len(bs[0].parts) != 1 || bs[1].finish == nil || len(bs[1].parts) != 3 || bs[2].finish != nil {
		t.Errorf("blocks %+v %+v %+v", bs[0], bs[1], bs[2])
	}
}

// TestErrorResult renders a failed call's result as OpenCode 2 sends
// it: {error, content}, members in the order stored.
func TestErrorResult(t *testing.T) {
	for state, want := range map[string]string{
		`{"status":"error","error":{"type":"aborted","message":"m"}}`:                                  `{"error":{"type":"aborted","message":"m"},"content":[]}`,
		`{"status":"error","error":{"type":"x","message":"m"},"content":[{"type":"text","text":"t"}]}`: `{"error":{"type":"x","message":"m"},"content":[{"type":"text","text":"t"}]}`,
		`{"status":"error"}`: `{"content":[]}`,
	} {
		if got := errorResult(json.RawMessage(state)); got != want {
			t.Errorf("errorResult(%s) = %s, want %s", state, got, want)
		}
	}
}

// TestUsage folds cache reads and writes into input, and counts
// reasoning in output by the release's rule.
func TestUsage(t *testing.T) {
	tok := &opencode.Tokens{Input: json.RawMessage(`10`), Output: json.RawMessage(`20`), Reasoning: json.RawMessage(`5`),
		Cache: &opencode.Cache{Read: json.RawMessage(`100`), Write: json.RawMessage(`7`)}}
	u, extra := usage(tok, json.RawMessage(`0.5`), false)
	if u.InputTokens != 117 || u.InputTokensDetails.CachedTokens != 100 || u.OutputTokens != 25 || u.TotalTokens != 142 || u.OutputTokensDetails.ReasoningTokens != 5 {
		t.Errorf("usage %+v", u)
	}
	if extra["cache_write_tokens"] != 7 || string(extra["cost"].(json.RawMessage)) != "0.5" {
		t.Errorf("extra %v", extra)
	}
	if u, _ := usage(tok, nil, true); u.OutputTokens != 20 {
		t.Errorf("output with reasoning counted in it: %d", u.OutputTokens)
	}
}

// TestToolOutput sends a completed call's data-URL attachments as media
// beside its text, and the text alone when it has none.
func TestToolOutput(t *testing.T) {
	d, n := toolOutput("t", []json.RawMessage{json.RawMessage(`{"mime":"image/png","url":"data:image/png;base64,AA=="}`), json.RawMessage(`{"mime":"image/png","url":"https://x/y.png"}`), json.RawMessage(`null`)})
	if n != 1 || len(d.Parts) != 2 {
		t.Fatalf("%d media, parts %v", n, d.Parts)
	}
	if _, ok := d.Parts[1].(*openresponses.InputImage); !ok {
		t.Errorf("part %T", d.Parts[1])
	}
	if d, n := toolOutput("t", nil); n != 0 || d.Parts != nil || d.Text != "t" {
		t.Errorf("output %+v", d)
	}
}

// TestReasoningMetadata reads each provider's record.
func TestReasoningMetadata(t *testing.T) {
	p := readMetadata(json.RawMessage(`{"zz":{"x":1},"anthropic":{"signature":"s"}}`))
	if p.ns != "anthropic" || p.signature != "s" {
		t.Errorf("%+v", p)
	}
	p = readState(json.RawMessage(`{"itemId":"rs","reasoningEncryptedContent":"e"}`), "openai")
	if p.ns != "openai" || p.itemID != "rs" || p.nested {
		t.Errorf("%+v", p)
	}
	p = readState(json.RawMessage(`{"anthropic":{"signature":"s"}}`), "anthropic")
	if !p.nested || p.signature != "s" {
		t.Errorf("%+v", p)
	}
	r := reasoningItems("t", provided{redacted: "d"})
	if r.ItemType() != "anthropic.redacted_thinking" {
		t.Errorf("%s", r.ItemType())
	}
}
