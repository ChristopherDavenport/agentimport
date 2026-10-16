package clinesdk

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ChristopherDavenport/agentimport/internal/jsjson"
)

// The compaction sidecar's anchors (core/src/session/models/
// session-compaction.ts). A sidecar replaces the first
// source_message_count messages of the canonical history that are not
// display-only when the SHA-256 of those messages still matches
// source_prefix_hash, or, for a sidecar written before the hash, when
// the last of them still has source_last_message_key. Cline hashes each
// message's role, content, agent, sessionId, metadata, modelInfo and
// metrics as JSON.stringify writes them, a user's text as
// formatDisplayUserInput shows it; its id and ts are left out.

// hashVersion opens the hash input, version 2 of it.
const hashVersion = "cline-session-compaction-source-v2\n"

// ErrUnhashable is returned for a message Cline itself cannot hash: an
// element that is not an object, a role that is not a string, a part
// that is not an object. Cline throws on such a history, so no sidecar
// it wrote projects over it.
var ErrUnhashable = errors.New("a message Cline cannot hash")

// ErrLoneSurrogate is returned for user text holding a lone surrogate,
// which Cline hashes and the user-input rules, written for Go strings
// here, cannot carry.
var ErrLoneSurrogate = errors.New("user text holding a lone surrogate, which the hash here cannot reproduce")

// CanonicalSource is the history a sidecar counts and hashes: every
// message but the display-only ones.
func CanonicalSource(ms []*Message) []*Message {
	out := make([]*Message, 0, len(ms))
	for _, m := range ms {
		if m.Err == nil && m.DisplayOnly() {
			continue
		}
		out = append(out, m)
	}
	return out
}

// SourcePrefixHash is the hash Cline records of the first count
// messages of a canonical source, "sha256:<hex>".
func SourcePrefixHash(source []*Message, count int) (string, error) {
	if count < 0 || count > len(source) {
		return "", fmt.Errorf("the history holds %d messages, not %d", len(source), count)
	}
	h := sha256.New()
	h.Write([]byte(hashVersion))
	fmt.Fprintf(h, "%d\n", count)
	for _, m := range source[:count] {
		in, err := hashInput(m)
		if err != nil {
			return "", err
		}
		h.Write(in)
		h.Write([]byte("\n"))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// normalized is a message as Cline hashes it: parsed as JavaScript
// holds it, a user's text shown as formatDisplayUserInput shows it.
func normalized(m *Message) (*jsjson.Value, error) {
	v, err := jsjson.Parse(m.Elem)
	if err != nil || v.Kind != jsjson.Object {
		return nil, ErrUnhashable
	}
	if r, ok := v.Get("role").Text(); !ok || r != RoleUser {
		return v, nil
	}
	content := v.Get("content")
	switch {
	case content == nil:
		return nil, ErrUnhashable
	case content.Kind == jsjson.Str:
		text, err := display(content)
		if err != nil {
			return nil, err
		}
		v.Set("content", text)
	case content.Kind == jsjson.Array:
		for i, part := range content.Items {
			// part.type of a string, a number or an array is undefined;
			// of null, Cline throws.
			if part.Kind == jsjson.Null {
				return nil, ErrUnhashable
			}
			if part.Kind != jsjson.Object {
				continue
			}
			if t, ok := part.Get("type").Text(); !ok || t != BlockText {
				continue
			}
			text, err := display(part.Get("text"))
			if err != nil {
				return nil, err
			}
			p := part.Clone()
			p.Set("text", text)
			content.Items[i] = p
		}
	default:
		// content.map is no function: Cline throws.
		return nil, ErrUnhashable
	}
	return v, nil
}

// display is formatDisplayUserInput of a value: a string, or absent,
// which it reads as the empty string.
func display(v *jsjson.Value) (*jsjson.Value, error) {
	if v == nil || v.Kind == jsjson.Null {
		return jsjson.NewString(""), nil
	}
	if v.Kind != jsjson.Str {
		return nil, ErrUnhashable
	}
	if v.Str.Lone() {
		return nil, ErrLoneSurrogate
	}
	return jsjson.NewString(FormatDisplayUserInput(v.Str.String())), nil
}

// hashInput is one message's line of the hash input.
func hashInput(m *Message) ([]byte, error) {
	v, err := normalized(m)
	if err != nil {
		return nil, err
	}
	if r, ok := v.Get("role").Text(); !ok || strings.Contains(r, ":") {
		return nil, ErrUnhashable
	}
	pair := func(name string) *jsjson.Value {
		val := v.Get(name)
		if val == nil {
			val = &jsjson.Value{Kind: jsjson.Null}
		}
		return &jsjson.Value{Kind: jsjson.Array, Items: []*jsjson.Value{jsjson.NewString(name), val}}
	}
	content := v.Get("content")
	contentPair := &jsjson.Value{Kind: jsjson.Array, Items: []*jsjson.Value{jsjson.NewString("content"), content}}
	arr := &jsjson.Value{Kind: jsjson.Array, Items: []*jsjson.Value{
		pair("role"), contentPair, pair("agent"), pair("sessionId"), pair("metadata"), pair("modelInfo"), pair("metrics"),
	}}
	return arr.Stringify(), nil
}

// BoundaryKey is the key Cline records of a sidecar's last source
// message: "id:<id>", else "ts:<role>:<ts>", else
// "content:<role>:<content as JSON>".
func BoundaryKey(m *Message) (string, error) {
	v, err := normalized(m)
	if err != nil {
		return "", err
	}
	if id := v.Get("id"); id != nil && id.Kind == jsjson.Str {
		if s := Trim(id.Str.String()); s != "" {
			return "id:" + s, nil
		}
	}
	role, ok := v.Get("role").Text()
	if !ok || strings.Contains(role, ":") {
		return "", ErrUnhashable
	}
	if ts := v.Get("ts"); ts != nil && ts.Kind == jsjson.Number {
		return "ts:" + role + ":" + jsjson.Number2String(ts.Num), nil
	}
	content := v.Get("content")
	if content == nil {
		return "content:" + role + ":undefined", nil
	}
	return "content:" + role + ":" + string(content.Stringify()), nil
}

// isoDateTime is zod's datetime() with its defaults: UTC, a Z, any
// fraction of a second.
var isoDateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:\d{2}(\.\d+)?)?Z$`)

// Valid says why Cline would refuse the sidecar as it parses it
// (SessionCompactionStateSchema), nil when it would accept it. Cline
// ignores a sidecar it refuses.
func (c *Compaction) Valid() error {
	switch {
	case c.Err != nil:
		return c.Err
	case string(bytes.TrimSpace(c.Version)) != "1":
		return errors.New("its version is not 1")
	case !c.Has("updated_at") || c.IsNull("updated_at") || !isoDateTime.MatchString(c.UpdatedAt):
		return errors.New("its updated_at is not a UTC date and time")
	case c.Has("conversation_id") && (c.IsNull("conversation_id") || c.ConversationID == ""):
		return errors.New("its conversation_id is not a string")
	case !c.countOK():
		return errors.New("its source_message_count is not a non-negative integer")
	case c.Has("source_prefix_hash") && (c.IsNull("source_prefix_hash") || c.SourcePrefixHash == ""):
		return errors.New("its source_prefix_hash is not a string")
	case c.Has("source_last_message_key") && (c.IsNull("source_last_message_key") || c.SourceLastMessageKey == ""):
		return errors.New("its source_last_message_key is not a string")
	case c.Has("system_prompt") && c.SystemPrompt == nil:
		return errors.New("its system_prompt is not a string")
	case !c.HasMessages:
		return errors.New("it has no messages array")
	}
	for _, mistyped := range c.Mistyped() {
		return fmt.Errorf("its %s is not of its type", mistyped)
	}
	for i, m := range c.Messages {
		if m.Err != nil || m.Role != RoleUser && m.Role != RoleAssistant || !m.Content.IsText && (m.Content.Err != nil || m.Content.Blocks == nil) {
			return fmt.Errorf("its message %d is not a user or assistant message with content", i+1)
		}
	}
	return nil
}

// countOK reports whether source_message_count is a non-negative
// integer written as a number.
func (c *Compaction) countOK() bool {
	_, ok := c.Count()
	return ok
}

// Projection says whether a sidecar applies to a canonical history.
type Projection struct {
	// Applies says Cline would project the sidecar: the context is its
	// messages, then the canonical history after its source.
	Applies bool
	// By says what matched: "hash", or "boundary" for a sidecar older
	// than the hash; Why says why it did not apply.
	By, Why string
	// Count is source_message_count; Unhashed is set when the history
	// holds a message the hash here cannot reproduce, so the hash was
	// not compared.
	Count    int
	Unhashed error
}

// Project decides whether Cline's projectSessionCompactionState applies
// the sidecar to a session's canonical history, its display-only
// messages included, which the source leaves out.
func (c *Compaction) Project(history []*Message) Projection {
	if err := c.Valid(); err != nil {
		return Projection{Why: "Cline refuses the sidecar: " + err.Error()}
	}
	source := CanonicalSource(history)
	n, _ := c.Count()
	p := Projection{Count: n}
	if n > len(source) {
		p.Why = fmt.Sprintf("it replaces %d messages, and the history holds %d", n, len(source))
		return p
	}
	if c.Has("source_prefix_hash") {
		h, err := SourcePrefixHash(source, n)
		if err != nil {
			p.Why, p.Unhashed = "the history before its source cannot be hashed: "+err.Error(), err
			return p
		}
		if h != c.SourcePrefixHash {
			p.Why = "the history before its source no longer hashes to source_prefix_hash"
			return p
		}
		p.Applies, p.By = true, "hash"
		return p
	}
	if n == 0 || !c.Has("source_last_message_key") {
		p.Why = "it records neither a source_prefix_hash nor a source_last_message_key"
		return p
	}
	key, err := BoundaryKey(source[n-1])
	if err != nil {
		p.Why, p.Unhashed = "its last source message has no key: "+err.Error(), err
		return p
	}
	if key != c.SourceLastMessageKey {
		p.Why = "its last source message no longer has source_last_message_key"
		return p
	}
	p.Applies, p.By = true, "boundary"
	return p
}
