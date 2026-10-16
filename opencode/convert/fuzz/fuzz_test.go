// Package fuzz holds the OpenCode converter's fuzz target, apart from
// the converter's tests, which build their fixtures with a database
// driver: a test binary that links one spends its time on the driver's
// coverage, not the converter's.
package fuzz

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentimport/opencode"
	"github.com/ChristopherDavenport/agentimport/opencode/convert"
)

// fuzzStore builds a store in memory: a session of the V1 tables with a
// user message, an assistant message of any info and two parts of any
// bytes, a child session of it, and the session's V2 copy, its rows of
// any bytes after its migrated first message.
func fuzzStore(version string, info, p1, p2, row1, row2 []byte) (*opencode.Memory, int) {
	mem := &opencode.Memory{StoreName: "fuzz.db", MessagesOf: map[string][]*opencode.MessageRecord{}, RowsOf: map[string][]*opencode.Row2{}}
	units := 0
	sess := func(gen opencode.Generation, id, parent string) {
		cols := opencode.Columns{{Name: "id", Value: id}, {Name: "version", Value: version}, {Name: "directory", Value: "/w"}, {Name: "time_created", Value: int64(1760000000000)}}
		if parent != "" {
			cols = append(cols, opencode.Column{Name: "parent_id", Value: parent})
		}
		table := "session"
		if gen == opencode.G3 {
			table = "session_v2"
		}
		mem.SessionList = append(mem.SessionList, opencode.DecodeSessionRow(gen, "fuzz.db:"+table+"/"+id, cols))
		units++
	}
	sess(opencode.G2, "ses_f", "")
	sess(opencode.G2, "ses_kid", "ses_f")
	sess(opencode.G3, "ses_f", "")
	user := opencode.DecodeMessage("fuzz.db:message/msg_1", "msg_1", "ses_f", 1, []byte(`{"role":"user","time":{"created":1},"agent":"build","model":{"providerID":"p","modelID":"m"}}`))
	user.Parts = []*opencode.PartRecord{opencode.DecodePart("fuzz.db:part/prt_1", "prt_1", "msg_1", []byte(`{"type":"text","text":"hi"}`))}
	asst := opencode.DecodeMessage("fuzz.db:message/msg_2", "msg_2", "ses_f", 2, info)
	asst.Parts = []*opencode.PartRecord{
		opencode.DecodePart("fuzz.db:part/prt_2", "prt_2", "msg_2", p1),
		opencode.DecodePart("fuzz.db:part/prt_3", "prt_3", "msg_2", p2),
	}
	mem.MessagesOf["ses_f"] = []*opencode.MessageRecord{user, asst}
	units += 5
	kid := opencode.DecodeMessage("fuzz.db:message/msg_k", "msg_k", "ses_kid", 3, []byte(`{"role":"user","time":{"created":3},"agent":"build","model":{"providerID":"p","modelID":"m"}}`))
	mem.MessagesOf["ses_kid"] = []*opencode.MessageRecord{kid}
	units++
	for k, data := range [][]byte{[]byte(`{"text":"hi","time":{"created":1}}`), row1, row2} {
		typ := "user"
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &probe) == nil && probe.Type != "" {
			typ = probe.Type
		}
		id := []string{"msg_1", "msg_r1", "msg_r2"}[k]
		mem.RowsOf["ses_f"] = append(mem.RowsOf["ses_f"], opencode.DecodeRow2("fuzz.db:session_message/"+id, opencode.Columns{
			{Name: "id", Value: id}, {Name: "session_id", Value: "ses_f"}, {Name: "type", Value: typ}, {Name: "seq", Value: int64(k)}, {Name: "data", Value: string(data)},
		}))
		units++
	}
	return mem, units
}

// FuzzConvert converts any session: an assistant message of any info
// and parts, and V2 rows of any data. It must never panic or fail, and
// it accounts for every record.
func FuzzConvert(f *testing.F) {
	f.Add("1.4.0",
		[]byte(`{"role":"assistant","parentID":"msg_1","providerID":"anthropic","modelID":"m","time":{"created":2,"completed":3},"finish":"stop","summary":true}`),
		[]byte(`{"type":"tool","callID":"c","tool":"task","state":{"status":"completed","input":{},"output":"o","metadata":{"sessionId":"ses_kid"},"time":{"start":2,"end":3,"compacted":4}}}`),
		[]byte(`{"type":"step-finish","reason":"stop","tokens":{"input":1,"output":2,"reasoning":1,"cache":{"read":1,"write":1}},"cost":0.1}`),
		[]byte(`{"type":"assistant","agent":"a","model":{"id":"m","providerID":"p"},"content":[{"type":"tool","id":"c2","name":"subagent","state":{"status":"completed","input":{},"content":[{"type":"text","text":"x"}],"metadata":{"sessionID":"ses_kid"}}}],"time":{"created":5}}`),
		[]byte(`{"type":"compaction","status":"completed","summary":"s","recent":"r","time":{"created":6}}`))
	f.Add("1.18.0",
		[]byte(`{"role":"assistant","parentID":"msg_1","providerID":"openai","modelID":"m2","error":{"name":"APIError","data":{"message":"x"}}}`),
		[]byte(`{"type":"reasoning","text":"t","metadata":{"openai":{"itemId":"rs","reasoningEncryptedContent":"e"},"anthropic":{"redactedData":"d"}}}`),
		[]byte(`{"type":"tool","callID":"c","tool":"bash","state":{"status":"error","input":"x","error":"The user rejected permission to use this specific tool call."}}`),
		[]byte(`{"type":"model-switched","model":{"id":"m3"},"time":{"created":1}}`),
		[]byte(`{"type":"user","files":[{"data":"AA==","mime":"text/plain","source":{"type":"uri","uri":"file:///x"},"description":"d"}],"skills":[{"text":"s"}]}`))
	f.Add("x", []byte(`null`), []byte(`{"type":"compaction","tail_start_id":"msg_1"}`), []byte(`{"type":"step-start"}`), []byte(`{oops`), []byte(`{"type":"assistant","content":[null,{"type":"reasoning"}],"error":{"type":"aborted"}}`))
	f.Fuzz(func(t *testing.T, version string, info, p1, p2, row1, row2 []byte) {
		for _, opts := range []convert.Options{{}, {Slim: true}, {Alone: true}} {
			mem, units := fuzzStore(version, info, p1, p2, row1, row2)
			res, err := convert.Convert(opts, mem)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if got := res.Report.Lines(); got != units {
				var b strings.Builder
				res.Report.WriteText(&b)
				t.Fatalf("the report accounts for %d records; the store holds %d\n%s", got, units, b.String())
			}
		}
	})
}
