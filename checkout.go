package agentimport

import (
	"io/fs"
	"maps"
	"net/url"
	"path"
	"strings"

	"github.com/ChristopherDavenport/agentsession"
)

// CheckoutPath is the path RFC 0001 (env, since 0.22) gives the
// checkout whose top directory is top, in an env entry whose cwd is
// cwd: "." when they are one directory, the relative name when top is
// below cwd, and top itself when it is an absolute path elsewhere. A
// POSIX path is cleaned first, so a trailing slash does not make a
// second spelling. ok is false when none applies: top is empty or
// relative, or is not in the form the format takes.
func CheckoutPath(cwd, top string) (p string, ok bool) {
	if top == "" {
		return "", false
	}
	if strings.HasPrefix(top, "/") {
		top = path.Clean(top)
	}
	if strings.HasPrefix(cwd, "/") {
		cwd = path.Clean(cwd)
	}
	if top == cwd {
		return ".", true
	}
	if cwd != "" {
		if rest, found := strings.CutPrefix(top, strings.TrimRight(cwd, "/")+"/"); found && agentsession.ValidCheckoutPath(rest) && fs.ValidPath(rest) {
			return rest, true
		}
	}
	if absolute(top) && agentsession.ValidCheckoutPath(top) {
		return top, true
	}
	return "", false
}

// absolute reports a POSIX absolute path, a Windows drive path (C:\ or
// C:/) or a UNC path, as agentsession reads one, whatever system this
// runs on, since a log written on one is converted on another.
func absolute(p string) bool {
	if path.IsAbs(p) || strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		('a' <= p[0] && p[0] <= 'z' || 'A' <= p[0] && p[0] <= 'Z')
}

// CheckoutBranch is a source's branch as a checkout's branch: git
// prints HEAD for the branch of a detached HEAD (`git rev-parse
// --abbrev-ref HEAD`), and refuses HEAD as a branch name, so HEAD is
// on no branch, which RFC 0001 writes as no branch member.
func CheckoutBranch(b string) string {
	if b == "HEAD" {
		return ""
	}
	return b
}

// CloneCheckouts copies a list of checkouts, each one's vcs, upstream
// and members included; a nil list stays nil, which an env entry reads
// as no checkouts member, and an empty one stays empty.
func CloneCheckouts(list []agentsession.Checkout) []agentsession.Checkout {
	if list == nil {
		return nil
	}
	out := make([]agentsession.Checkout, len(list))
	for i, c := range list {
		if c.VCS != nil {
			c.VCS = cloneVCS(c.VCS)
		}
		if c.Upstream != nil {
			u := *c.Upstream
			u.Unknown = maps.Clone(c.Upstream.Unknown)
			c.Upstream = &u
		}
		c.Unknown = maps.Clone(c.Unknown)
		out[i] = c
	}
	return out
}

func cloneVCS(v *agentsession.VCS) *agentsession.VCS {
	out := *v
	out.Unknown = maps.Clone(v.Unknown)
	return &out
}

// WithoutUserinfo returns a repository URL with any userinfo removed,
// and whether there was any: RFC 0001 (env, since 0.22) has a writer
// that keeps a remote's URL in an entry remove it first, since it can
// carry a credential. A URL that does not parse with a scheme, such as
// git's scp-like user@host:path, is returned as written.
func WithoutUserinfo(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.User == nil {
		return raw, false
	}
	u.User = nil
	return u.String(), true
}

// holdsDir reports whether the absolute checkout path p is dir or a
// directory above it, with either separator.
func holdsDir(p, dir string) bool {
	p = strings.TrimRight(p, `/\`)
	if p == "" || p == "." {
		return false
	}
	if dir == p {
		return true
	}
	rest, ok := strings.CutPrefix(dir, p)
	return ok && (strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, `\`))
}
