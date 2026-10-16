// Package fuzz holds the Hermes converter's fuzz target, apart from the
// converter's tests, which build their fixtures with a database driver:
// a test binary that links one spends its time on the driver's
// coverage, not the converter's.
package fuzz

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/hermes"
	"github.com/ChristopherDavenport/agentimport/hermes/convert"
)

// fuzzStore builds a store in memory: a session whose assistant row
// holds any content, calls and reasoning, whose tool row answers any
// call, with any flags, a sub-agent and a continuation of it.
func fuzzStore(content, calls, details, codex string, flags uint8) (*hermes.Memory, int) {
	mem := &hermes.Memory{StoreName: "fuzz.db", Schema: 31, MessagesOf: map[string][]*hermes.Message{}}
	sess := func(id, parent, source, end, cfg string, started float64) {
		cols := hermes.Columns{{Name: "id", Value: id}, {Name: "source", Value: source}, {Name: "started_at", Value: started}, {Name: "end_reason", Value: end}}
		if parent != "" {
			cols = append(cols, hermes.Column{Name: "parent_session_id", Value: parent})
		}
		if cfg != "" {
			cols = append(cols, hermes.Column{Name: "model_config", Value: cfg})
		}
		mem.SessionRows = append(mem.SessionRows, hermes.DecodeSessionRow("fuzz.db:sessions/"+id, cols))
	}
	sess("s", "", "cli", "compression", "", 1e9)
	sess("k", "s", "subagent", "", `{"_delegate_from":"s"}`, 1e9+2.5)
	sess("c", "s", "cli", "", "", 1e9+9)
	units := 3
	id := int64(0)
	msg := func(session, role string, cols ...hermes.Column) {
		id++
		all := hermes.Columns{{Name: "id", Value: id}, {Name: "session_id", Value: session}, {Name: "role", Value: role}, {Name: "timestamp", Value: 1e9 + float64(id)}}
		all = append(all, cols...)
		mem.MessagesOf[session] = append(mem.MessagesOf[session], hermes.DecodeMessageRow(fmt.Sprintf("fuzz.db:messages/%d", id), all))
		units++
	}
	flag := func(bit uint8) int64 {
		if flags&bit != 0 {
			return 1
		}
		return 0
	}
	msg("s", "user", hermes.Column{Name: "content", Value: "go"}, hermes.Column{Name: "active", Value: flag(1)}, hermes.Column{Name: "compacted", Value: flag(2)})
	msg("s", "assistant", hermes.Column{Name: "content", Value: content}, hermes.Column{Name: "tool_calls", Value: calls},
		hermes.Column{Name: "reasoning_details", Value: details}, hermes.Column{Name: "codex_reasoning_items", Value: codex},
		hermes.Column{Name: "active", Value: flag(4)}, hermes.Column{Name: "_compressed_summary", Value: flag(8)})
	msg("s", "tool", hermes.Column{Name: "content", Value: content}, hermes.Column{Name: "tool_call_id", Value: "c1"}, hermes.Column{Name: "active", Value: flag(16)})
	msg("s", "user", hermes.Column{Name: "content", Value: content}, hermes.Column{Name: "_compressed_summary", Value: flag(32)}, hermes.Column{Name: "api_content", Value: codex})
	msg("s", "assistant", hermes.Column{Name: "content", Value: "ok"}, hermes.Column{Name: "compacted", Value: flag(64)}, hermes.Column{Name: "active", Value: flag(128)})
	msg("k", "user", hermes.Column{Name: "content", Value: content})
	msg("c", "user", hermes.Column{Name: "content", Value: content}, hermes.Column{Name: "_compressed_summary", Value: int64(1)})
	return mem, units
}

// FuzzConvert converts any rows: an assistant row of any content, calls
// and reasoning, a tool output of any content, and any flags. It must
// never panic or fail, and it accounts for every row.
func FuzzConvert(f *testing.F) {
	f.Add("hello", `[{"id":"c1","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"go\"}"}}]`, `[{"type":"thinking","thinking":"t","signature":"s"}]`, ``, uint8(0xff))
	f.Add("\x00json:[{\"type\":\"image_url\",\"image_url\":\"data:,\"},7]", `[7,null,{"id":"c1"}]`, `[{"type":"reasoning.encrypted","data":"x"},{"type":"hermes.native_assistant"}]`, `[{"type":"reasoning","encrypted_content":"e","_issuer_kind":"k"},{"type":"compaction","encrypted_content":"z"},5]`, uint8(0x5a))
	f.Add("[memory]", `{oops`, `{oops`, `null`, uint8(0))
	f.Add(`{"status":"dispatched","mode":"background","delegation_id":"d"}`, `[{"call_id":"c1|x","function":{"name":"delegate_task","arguments":{"goal":"go"}}}]`, ``, `[{"type":"message"}]`, uint8(0x03))
	f.Fuzz(func(t *testing.T, content, calls, details, codex string, flags uint8) {
		for _, opts := range []convert.Options{{}, {Slim: true}, {Alone: true}} {
			mem, units := fuzzStore(content, calls, details, codex, flags)
			res, err := convert.Convert(opts, mem)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got := res.Report.Lines(); got != units {
				var b strings.Builder
				res.Report.WriteText(&b)
				t.Fatalf("the report accounts for %d rows; the store holds %d\n%s", got, units, b.String())
			}
			for _, fd := range res.Report.Findings {
				if fd.Code == "hermes/session-failed" || fd.Code == "hermes/unreached" {
					t.Fatalf("%+v", fd)
				}
			}
		}
	})
}
