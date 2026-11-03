package agentimport

import (
	"reflect"
	"testing"
)

func TestPublishedRefs(t *testing.T) {
	push := "Enumerating objects: 5, done.\nTo github.com:acme/demo.git\n * [new branch]      feat -> feat\n   1a2b3c4..5d6e7f8  main -> main\n * [new tag]         v1.0.0 -> v1.0.0\n ! [rejected]        old -> old (fetch first)\n"
	for _, tc := range []struct {
		name, command, output string
		want                  []PublishedRef
	}{
		{"push", "git push origin feat main --tags", push, []PublishedRef{
			{Kind: "branch", Ref: "feat", Remote: "github.com:acme/demo.git"},
			{Kind: "branch", Ref: "main", Remote: "github.com:acme/demo.git"},
			{Kind: "tag", Ref: "v1.0.0", Remote: "github.com:acme/demo.git"},
		}},
		{"dry run", "git push --dry-run origin feat", push, nil},
		{"quiet push says nothing", "git push -q origin feat", "", nil},
		{"pr", "gh pr create --fill", "Creating pull request for feat into main\n\nhttps://github.com/acme/demo/pull/42\n", []PublishedRef{
			{Kind: "pull_request", URL: "https://github.com/acme/demo/pull/42"},
		}},
		{"failed pr", "gh pr create --fill", "pull request create failed: GraphQL: No commits", nil},
		{"not a publish", "cat notes.txt", push, nil},
	} {
		if got := PublishedRefs(tc.command, tc.output); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
	e, err := PublishedEntry("call_1", "gh pr create", "https://github.com/acme/demo/pull/42")
	if err != nil || e == nil || e.CallID != "call_1" || e.NS != PublishedNS || string(e.Unknown[DerivedMember]) != `{"rule":"published-refs","call_id":"call_1"}` {
		t.Errorf("PublishedEntry = %+v, %v", e, err)
	}
}
