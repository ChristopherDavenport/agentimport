package opencode

import (
	"encoding/json"
	"os"
	"testing"
)

// FuzzRead reads any bytes as each kind of record: a session file, a
// message, a part, a V2 row's data and its content, an attachment. It
// must never panic, and what decodes must write back as JSON that
// reads again.
func FuzzRead(f *testing.F) {
	for _, path := range storageFilesF(f) {
		data, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{"role":"assistant","error":{"name":7},"tokens":{"input":"x","cache":[1]},"summary":true,"time":null}`))
	f.Add([]byte(`{"type":"tool","callID":5,"state":{"status":"error","metadata":{"interrupted":true,"output":3},"time":{"compacted":"x"},"attachments":[null,7,{"url":"data:,"}]}}`))
	f.Add([]byte(`{"content":[null,{"type":"tool","state":"x"},{"type":"tool","state":{"status":"completed","content":[1]}}],"files":[{"data":7,"source":5,"mention":null}],"skills":[null,{"text":null}]}`))
	f.Add([]byte(`[1,2,3]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var vs []any
		s := DecodeSessionFile("u", data)
		if s.Err == nil {
			s.StagedRevert()
			s.ForkPoint()
			vs = append(vs, s.Record)
		}
		if m := DecodeMessage("u", "msg", "ses", 0, data); m.Info != nil {
			m.Info.IsSummary()
			m.Info.Created()
			m.Info.Completed()
			if m.Info.Error != nil {
				m.Info.Error.Message()
			}
			vs = append(vs, m.Info)
		}
		if p := DecodePart("u", "prt", "msg", data); p.Part != nil {
			if st := p.Part.State; st != nil {
				st.Task()
				st.Interrupted()
				st.Compacted()
				for _, a := range st.Attachments {
					Attachment(a)
				}
			}
			vs = append(vs, p.Part)
		}
		r := DecodeRow2("u", Columns{{"id", "msg"}, {"seq", int64(1)}, {"data", string(data)}})
		if r.Data != nil {
			vs = append(vs, r.Data)
			for _, raw := range r.Data.Content {
				c := &Content2{}
				if c.UnmarshalJSON(raw) != nil {
					continue
				}
				c.ReasoningDone()
				if st, err := c.Tool(); err == nil {
					st.Task()
					for _, x := range st.Content {
						DecodeToolContent(x)
					}
					vs = append(vs, st)
				}
				vs = append(vs, c)
			}
			for _, raw := range r.Data.Files {
				DecodeFile2(raw)
			}
			for _, raw := range r.Data.Skills {
				SkillText(raw)
			}
			LocationDirectory(r.Data.Location)
			ShellOutput(r.Data.Output)
		}
		DecodeSessionRow(G3, "u", Columns{{"id", "ses"}, {"revert", string(data)}, {"model", string(data)}, {"fork_boundary", string(data)}})
		for _, v := range vs {
			out, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !json.Valid(out) {
				t.Fatalf("invalid JSON written back: %s", out)
			}
		}
	})
}

// storageFilesF lists the fixtures' storage files for a fuzz target's
// seeds.
func storageFilesF(f *testing.F) []string {
	f.Helper()
	var out []string
	for _, p := range []string{"convert/testdata/g1-basic/storage", "convert/testdata/upgraded/storage"} {
		entries, _ := os.ReadDir(p)
		for _, e := range entries {
			sub := p + "/" + e.Name()
			dirs, _ := os.ReadDir(sub)
			for _, d := range dirs {
				files, _ := os.ReadDir(sub + "/" + d.Name())
				for _, fl := range files {
					out = append(out, sub+"/"+d.Name()+"/"+fl.Name())
				}
			}
		}
	}
	return out
}
