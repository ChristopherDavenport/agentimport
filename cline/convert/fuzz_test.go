package convert

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ChristopherDavenport/agentimport/cline"
)

// FuzzConvert converts any task: an API history, UI messages and
// context edits of any bytes. It must never panic or fail, and it
// accounts for every unit.
func FuzzConvert(f *testing.F) {
	dirs, _ := filepath.Glob("testdata/*/*/tasks/*")
	for _, d := range dirs {
		api, _ := os.ReadFile(filepath.Join(d, cline.FileAPI))
		ui, _ := os.ReadFile(filepath.Join(d, cline.FileUI))
		ctx, _ := os.ReadFile(filepath.Join(d, cline.FileContextHistory))
		f.Add(api, ui, ctx, false)
	}
	f.Add([]byte(`[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"new_task","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"Subtask ../x completed."},{"type":"tool_result","tool_use_id":"a","content":"again"}]}]`),
		[]byte(`[{"ts":5,"type":"say","say":"condense_context","contextCondense":{"condenseId":7}}]`), []byte(nil), true)
	f.Add([]byte(`[{"role":"user","content":"a","ts":1},{"role":"assistant","content":"s","isSummary":true,"ts":2},{"role":"user","content":"m","isTruncationMarker":true,"truncationId":"t","ts":3},{"role":"user","content":"b","truncationParent":"t","condenseParent":"c","ts":4},{"role":"assistant","content":"x","isSummary":true,"condenseId":"c","ts":0}]`),
		[]byte(`[{"ts":3,"type":"say","say":"sliding_window_truncation","contextTruncation":{"truncationId":"t"}}]`), []byte(nil), true)
	f.Add([]byte(`[{"role":"user","content":"a"},{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"f","input":{}}]},{"role":"user","content":[{"type":"text","text":"t"}]},{"role":"assistant","content":"y"}]`),
		[]byte(`[{"ts":1,"type":"say","say":"x","conversationHistoryIndex":0,"conversationHistoryDeletedRange":[2,1]},{"ts":2,"type":"say","say":"x","conversationHistoryIndex":3,"conversationHistoryDeletedRange":[2,9]},{"ts":3,"type":"say","say":"x"}]`),
		[]byte(`[[0,[0,[[0,[[1,"text",["n"],[]]]]]]],[99,[0,[[5,[[1,"text",["n"],[]]]]]]]]`), false)
	f.Fuzz(func(t *testing.T, api, ui, ctx []byte, roo bool) {
		files := map[string][]byte{}
		if len(api) > 0 {
			files[cline.FileAPI] = api
		}
		if len(ui) > 0 {
			files[cline.FileUI] = ui
		}
		if len(ctx) > 0 {
			files[cline.FileContextHistory] = ctx
		}
		task := cline.ParseTask("1760000000000", files)
		if roo {
			task.Variant = cline.Roo
		} else {
			task.Variant = cline.Classic
		}
		res, err := Convert(task, Options{CreatedAt: fixed})
		if err != nil {
			t.Fatalf("convert: %v", err)
		}
		if got, want := res.Report.Lines(), task.Units(); got != want {
			t.Fatalf("the report accounts for %d units; the task holds %d", got, want)
		}
	})
}
