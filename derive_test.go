package agentimport

import (
	"reflect"
	"testing"

	"github.com/ChristopherDavenport/agentsession"
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
		{"dry run, short and last", "git push origin feat -n", push, nil},
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

func TestCommitRevision(t *testing.T) {
	for command, want := range map[string]string{
		"git commit -m wip":           "abc1234",
		"git commit --dry-run -m wip": "",
		"git status":                  "",
	} {
		if got := CommitRevision(command, "[main abc1234] wip\n 1 file changed"); got != want {
			t.Errorf("%q: %q, want %q", command, got, want)
		}
	}
	if got := CommitRevision("git commit -q -m wip", ""); got != "" {
		t.Errorf("a quiet commit: %q", got)
	}
	if got := CommitRevision("git commit -m first", "[main (root-commit) 0a1b2c3d] first"); got != "0a1b2c3d" {
		t.Errorf("a root commit: %q", got)
	}
}

// TestRevisionEnv: the env entry after a commit repeats the one in
// force, an env entry being a whole snapshot: its working directory,
// workspace and vcs members, with the commit's revision and no dirty.
func TestRevisionEnv(t *testing.T) {
	base := agentsession.NewEnvEntry("/work/demo")
	base.Workspace = &agentsession.Workspace{Kind: "local"}
	base.VCS = &agentsession.VCS{System: "git", Revision: "0000000", Dirty: true}
	if err := base.VCS.SetMember("codex:branch", "main"); err != nil {
		t.Fatal(err)
	}
	ds, err := DerivedEntries("call_1", "git commit -m wip", "[main abc1234] wip", base, "/elsewhere")
	if err != nil || len(ds) != 1 {
		t.Fatalf("%v, %v", ds, err)
	}
	env := ds[0].Entry.(*agentsession.EnvEntry)
	if env.CWD != "/work/demo" || env.Workspace == nil || env.Workspace.Kind != "local" || env.VCS == nil ||
		env.VCS.Revision != "abc1234" || env.VCS.Dirty || string(env.VCS.Unknown["codex:branch"]) != `"main"` {
		t.Errorf("env %+v, vcs %+v", env, env.VCS)
	}
	if base.VCS.Revision != "0000000" || !base.VCS.Dirty {
		t.Error("the env in force was changed")
	}
}
