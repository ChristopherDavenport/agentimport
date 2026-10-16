package convert

import (
	"errors"
	"strings"
	"testing"

	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/openresponses"
)

// TestSyntheticID mints the IDs OpenCode 2's migration gives the rows
// it adds, as v1-migration.bun.ts's syntheticID does.
func TestSyntheticID(t *testing.T) {
	used := map[string]bool{"msg_sy3": true}
	if got := syntheticID("msg_sy3", used); got != "msg_sy3vxnnxz19ht6Tu9" {
		t.Errorf("syntheticID = %s", got)
	}
	if got := syntheticID("msg_01JZ0000000000AAAAAAAAAAAAAA", used); got != "msg_01JZ00000000oBmWHfstUcmsaD" {
		t.Errorf("syntheticID = %s", got)
	}
	used["msg_sy3vxnnxz19ht6Tu9"] = true
	if got := syntheticID("msg_sy3", used); got == "msg_sy3vxnnxz19ht6Tu9" || !strings.HasPrefix(got, "msg_sy3") {
		t.Errorf("a used ID is minted again: %s", got)
	}
}

// TestCompactionSummaries holds every compaction written to a summary
// that is a message, or a provider's checkpoint: a cut OpenCode makes
// at the head of its context is an omit, whatever its first item.
func TestCompactionSummaries(t *testing.T) {
	for _, name := range fixtures(t) {
		res := convertFixture(t, name, Options{})
		for _, s := range res.Sessions {
			for _, e := range s.Entries() {
				c, ok := e.(*agentsession.CompactionEntry)
				if !ok {
					continue
				}
				switch it := c.Summary.(type) {
				case *openresponses.Message:
				case *openresponses.UnknownItem:
					if it.Type != "opencode.native_checkpoint" {
						t.Errorf("%s: compaction summary %s", name, it.Type)
					}
				default:
					t.Errorf("%s: compaction summary %s", name, it.ItemType())
				}
			}
		}
	}
}

// TestFailedParent converts the sub-agents of a session that fails on
// their own, every record still accounted for.
func TestFailedParent(t *testing.T) {
	testHookRun = func(sc *sconv) error {
		if sc.rec.id == "ses_par" {
			return errors.New("made to fail")
		}
		return nil
	}
	defer func() { testHookRun = nil }()
	res := convertFixture(t, "g2-subagents", Options{})
	got := map[string]bool{}
	for _, s := range res.Sessions {
		got[s.ID()] = true
	}
	for _, id := range []string{"ses_kid1", "ses_kid2", "ses_kid3", "ses_kid4", "ses_kid5", "ses_fork"} {
		if !got[id] {
			t.Errorf("session %s is lost with its parent; converted %v", id, got)
		}
	}
	if got["ses_par"] {
		t.Error("the failed parent is converted")
	}
	alone := 0
	for _, f := range res.Report.Findings {
		if f.Code == "opencode/subagent-alone" {
			alone++
		}
	}
	if alone < 5 {
		t.Errorf("%d sub-agents reported alone", alone)
	}
}
