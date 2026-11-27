package agentimport

import (
	"encoding/json"
	"testing"

	"github.com/ChristopherDavenport/agentsession"
)

// TestCheckoutPath: a checkout's top as seen from an env entry's cwd is
// ".", a relative name below it, or an absolute path elsewhere, in the
// clean form RFC 0001 takes; anything else is no path.
func TestCheckoutPath(t *testing.T) {
	for _, tt := range []struct {
		cwd, top, want string
		ok             bool
	}{
		{"/work/demo", "/work/demo", ".", true},
		{"/work/demo/", "/work/demo", ".", true},
		{"/work/demo", "/work/demo/", ".", true},
		{"/work", "/work/demo", "demo", true},
		{"/work", "/work/demo/sub", "demo/sub", true},
		{"/work/demo/pkg", "/work/demo", "/work/demo", true},
		{"/work/demo", "/work/demo-other", "/work/demo-other", true},
		{"", "/work/demo", "/work/demo", true},
		{"/work/demo", "/work/x/../demo", ".", true},
		{`C:\work\demo`, `C:\work\demo`, ".", true},
		{`C:\work\demo\pkg`, `C:\work\demo`, `C:\work\demo`, true},
		{"/work/demo", "", "", false},
		{"/work/demo", "demo", "", false},
		{"/work/demo", `C:demo`, "", false},
	} {
		got, ok := CheckoutPath(tt.cwd, tt.top)
		if got != tt.want || ok != tt.ok {
			t.Errorf("CheckoutPath(%q, %q) = %q, %v; want %q, %v", tt.cwd, tt.top, got, ok, tt.want, tt.ok)
		}
		if ok && !agentsession.ValidCheckoutPath(got) {
			t.Errorf("CheckoutPath(%q, %q) = %q, which the format does not take", tt.cwd, tt.top, got)
		}
	}
}

// TestCheckoutBranch: git's HEAD, printed for a detached HEAD and
// refused as a branch name, is no branch.
func TestCheckoutBranch(t *testing.T) {
	for in, want := range map[string]string{"main": "main", "HEAD": "", "": "", "feat/HEAD": "feat/HEAD"} {
		if got := CheckoutBranch(in); got != want {
			t.Errorf("CheckoutBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestWithoutUserinfo: a URL's userinfo, which can be a credential, is
// left out; a URL with none, or one that is not a URL with a scheme, is
// as written.
func TestWithoutUserinfo(t *testing.T) {
	for _, tt := range []struct {
		in, want string
		stripped bool
	}{
		{"https://example.com/demo.git", "https://example.com/demo.git", false},
		{"https://bob:ghp_token@example.com/demo.git", "https://example.com/demo.git", true},
		{"https://x-access-token@example.com/demo.git", "https://example.com/demo.git", true},
		{"ssh://git@example.com/demo.git", "ssh://example.com/demo.git", true},
		{"git@example.com:acme/demo.git", "git@example.com:acme/demo.git", false},
		{"", "", false},
	} {
		got, stripped := WithoutUserinfo(tt.in)
		if got != tt.want || stripped != tt.stripped {
			t.Errorf("WithoutUserinfo(%q) = %q, %v; want %q, %v", tt.in, got, stripped, tt.want, tt.stripped)
		}
	}
}

// TestRevisionEnvCheckouts: the env entry after a commit repeats the
// checkouts in force; the one at "." takes the new vcs, an absolute one
// holding the working directory the revision, and the rest are as they
// were. With none in force it lists none, and the base is not changed.
func TestRevisionEnvCheckouts(t *testing.T) {
	base := agentsession.NewEnvEntry("/work/demo/pkg")
	base.VCS = &agentsession.VCS{System: "git", Revision: "0000000", Dirty: true}
	base.Checkouts = []agentsession.Checkout{
		{Path: ".", VCS: &agentsession.VCS{System: "git", Revision: "0000000", Dirty: true}, Branch: "main"},
		{Path: "/work/demo", VCS: &agentsession.VCS{System: "git", Revision: "0000000", Dirty: true}, Branch: "main",
			Unknown: map[string]json.RawMessage{"acme:tree": json.RawMessage(`"t1"`)}},
		{Path: "/work/lib", VCS: &agentsession.VCS{System: "git", Revision: "1111111"}, Branch: "dev"},
		{Path: "sub", Branch: "x"},
	}
	env := RevisionEnv(base, "/work/demo/pkg", "abc1234")
	if _, err := agentsession.New(agentsession.Header{ID: agentsession.NewSessionID(), CWD: "/work/demo/pkg"}).Append(env); err != nil {
		t.Fatalf("the env after the commit is not one a writer may append: %v", err)
	}
	if len(env.Checkouts) != 4 {
		t.Fatalf("checkouts %+v", env.Checkouts)
	}
	if c := env.Checkout("."); c.VCS == nil || c.VCS.Revision != "abc1234" || c.VCS.Dirty || c.Branch != "main" || c.VCS == env.VCS {
		t.Errorf(". = %+v (vcs %+v), entry vcs %+v", c, c.VCS, env.VCS)
	}
	if c := env.Checkout("/work/demo"); c.VCS.Revision != "abc1234" || c.VCS.Dirty || string(c.Unknown["acme:tree"]) != `"t1"` {
		t.Errorf("/work/demo = %+v (vcs %+v)", c, c.VCS)
	}
	if c := env.Checkout("/work/lib"); c.VCS.Revision != "1111111" || c.Branch != "dev" {
		t.Errorf("/work/lib = %+v (vcs %+v)", c, c.VCS)
	}
	if c := env.Checkout("sub"); c.VCS != nil || c.Branch != "x" {
		t.Errorf("sub = %+v", c)
	}
	if base.Checkouts[0].VCS.Revision != "0000000" || base.Checkouts[1].VCS.Revision != "0000000" || !base.Checkouts[1].VCS.Dirty {
		t.Error("the env in force was changed")
	}

	none := agentsession.NewEnvEntry("/work/demo")
	none.VCS = &agentsession.VCS{System: "git"}
	if env := RevisionEnv(none, "/work/demo", "abc1234"); env.Checkouts != nil {
		t.Errorf("with no checkouts in force: %+v", env.Checkouts)
	}
	empty := agentsession.NewEnvEntry("/work/demo")
	empty.Checkouts = []agentsession.Checkout{}
	if env := RevisionEnv(empty, "/work/demo", "abc1234"); env.Checkouts == nil || len(env.Checkouts) != 0 {
		t.Errorf("an empty list in force: %+v", env.Checkouts)
	}
}
