package convert

import (
	"reflect"
	"testing"
	"time"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentsession"
)

// TestSeedConfigParts checks a continuation's first config carries the
// predecessor's instruction parts whole, their source and trust with
// them, when they are whole, and the instructions string when one of
// them could not be rebuilt, rather than failing the conversion.
func TestSeedConfigParts(t *testing.T) {
	trust := &agentsession.Trust{Reads: []string{"untrusted"}}
	whole := []agentsession.InstructionPart{
		{ID: "product", Text: "Be careful.", Source: "harness", Keep: 2},
		{ID: "memory", Text: "Remember the tests.", Source: "agentmemory", Trust: trust, Hash: agentsession.HashText("Remember the tests.")},
	}
	wantWhole := []agentsession.InstructionPart{
		{ID: "product", Text: "Be careful.", Source: "harness"},
		{ID: "memory", Text: "Remember the tests.", Source: "agentmemory", Trust: trust},
	}
	tests := []struct {
		name  string
		parts []agentsession.InstructionPart
		// want is the parts the config carries, nil for the string.
		want []agentsession.InstructionPart
	}{
		{"whole, a keep and its own hash cleared", whole, wantWhole},
		{"a part named by a hash alone", []agentsession.InstructionPart{
			{ID: "product", Text: "Be careful.", Source: "harness"},
			{ID: "memory", Hash: agentsession.HashText("Remember the tests."), Source: "agentmemory"},
		}, nil},
		{"a hash of another text", []agentsession.InstructionPart{
			{ID: "product", Text: "Be careful.", Source: "harness"},
			{ID: "memory", Text: "Remember the tests.", Hash: agentsession.HashText("something else")},
		}, nil},
		{"a keep naming no part", []agentsession.InstructionPart{
			{ID: "product", Text: "Be careful.", Source: "harness"},
			{Keep: 1},
		}, nil},
		{"a part with no ID", []agentsession.InstructionPart{
			{ID: "product", Text: "Be careful."},
			{Text: "Remember the tests."},
		}, nil},
		{"a repeated ID", []agentsession.InstructionPart{
			{ID: "product", Text: "Be careful."},
			{ID: "product", Text: "Remember the tests."},
		}, nil},
		{"parts that do not join to the instructions", []agentsession.InstructionPart{
			{ID: "product", Text: "Be careful."},
		}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instructions := "Be careful.\n\nRemember the tests."
			if j := agentsession.JoinInstructions(wantWhole); j != instructions {
				t.Fatalf("fixture joins to %q", j)
			}
			seed := agentsession.Settings{Model: "m", Instructions: instructions, InstructionsParts: tc.parts}
			c := seedConv(&seed)
			if err := c.seedConfig(); err != nil {
				t.Fatalf("seedConfig: %v", err)
			}
			cfg, ok := c.b.S.Entries()[0].(*agentsession.ConfigEntry)
			if !ok {
				t.Fatalf("first entry %T", c.b.S.Entries()[0])
			}
			if tc.want == nil {
				if cfg.Instructions == nil || *cfg.Instructions != instructions || cfg.InstructionsParts != nil {
					t.Errorf("config instructions %v, parts %+v; want the string alone", cfg.Instructions, cfg.InstructionsParts)
				}
			} else if cfg.Instructions != nil || !reflect.DeepEqual(cfg.InstructionsParts, tc.want) {
				t.Errorf("config instructions %v, parts %+v; want parts %+v", cfg.Instructions, cfg.InstructionsParts, tc.want)
			}
			reported := false
			for _, f := range c.report.Findings {
				reported = reported || f.Code == "atif/continuation-parts"
			}
			if reported != (tc.want == nil) {
				t.Errorf("atif/continuation-parts reported %v, want %v", reported, tc.want == nil)
			}
			ctx, err := c.b.S.ContextAt(c.tail)
			if err != nil {
				t.Fatal(err)
			}
			if ctx.Settings.Instructions != instructions || ctx.Settings.Model != "m" {
				t.Errorf("settings in force: model %q instructions %q", ctx.Settings.Model, ctx.Settings.Instructions)
			}
		})
	}
	// The seed is left as it was: clearing a keep or a hash is the
	// config's, not the predecessor's settings'.
	if whole[0].Keep != 2 || whole[1].Hash == "" {
		t.Errorf("seed parts changed: %+v", whole)
	}
}

// seedConv is a document conversion with nothing appended, continuing
// from seed.
func seedConv(seed *agentsession.Settings) *docConv {
	fam := newFamily(Options{})
	c := &docConv{fam: fam, in: docInput{name: "next.json"}, tgt: target{id: "seed-next", seed: seed},
		report: fam.report, counts: map[string]int{}, firstAt: map[string]int{}, spawnIDs: map[string]bool{}}
	h := agentsession.Header{ID: "seed-next", CreatedAt: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)}
	c.b = agentimport.NewBuilder(h, Source, fam.report)
	c.created = h.CreatedAt
	return c
}
