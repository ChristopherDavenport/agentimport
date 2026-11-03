package agentimport

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/ChristopherDavenport/agentsession"
)

// DerivedMember marks an entry no native line maps to that a converter
// inferred from the content of the converted path: its value names the
// rule and its inputs. Only an importer infers entries from content, so
// the mark is this module's own, not a format member, and every
// rewriter keeps it (docs/plans/agentimport.md, "Entries no line maps
// to"). A derived entry is written only when the conversion asks for
// derivation, and states only what its input states.
const DerivedMember = "agentimport:derived"

// Derivation is the value of [DerivedMember].
type Derivation struct {
	// Rule names the rule, the code its findings are reported under
	// without the agentimport/ prefix.
	Rule string `json:"rule"`
	// CallID is the call whose arguments and output the rule read.
	CallID string `json:"call_id,omitempty"`
}

// DeriveRules are the derivation rules a conversion that asks for
// derivation runs, as agentimport:source's derived names them.
var DeriveRules = []string{"published-refs"}

// PublishedNS is the namespace of the custom entry that records what a
// call published: a branch or tag pushed, a pull request opened. What
// a tool did is not the environment, so it is a record of the call,
// carrying its call_id, which goes with the call when a redaction leaves
// it out (RFC 0001, custom, 0.15).
const PublishedNS = "agentimport:published"

// PublishedCode is the code a published-refs record is reported under.
const PublishedCode = "agentimport/published-refs"

// PublishedRef is one ref a command published, as its output says.
type PublishedRef struct {
	// Kind is branch, tag or pull_request.
	Kind string `json:"kind"`
	// Ref is the branch or tag pushed, as the remote names it.
	Ref string `json:"ref,omitempty"`
	// Remote is where git said it pushed ("To <remote>").
	Remote string `json:"remote,omitempty"`
	// URL is the pull request's URL.
	URL string `json:"url,omitempty"`
}

var (
	pushTo   = regexp.MustCompile(`(?m)^To (\S+)\s*$`)
	pushLine = regexp.MustCompile(`(?m)^ [ +*=!-] +(?:\[new (branch|tag)\]|[0-9a-f]{4,}\.\.\.?[0-9a-f]{4,}) +\S+ -> (\S+)`)
	prURL    = regexp.MustCompile(`https://[^\s/]+/[^\s/]+/[^\s/]+/pull/[0-9]+`)
)

// PublishedRefs reads what a shell command published from its output:
// the refs a git push reports it updated, and the URL gh pr create
// prints. A command that is not one of those, a dry run, or output
// that does not say, yields nothing.
func PublishedRefs(command, output string) []PublishedRef {
	var out []PublishedRef
	if strings.Contains(command, "git push") && !dryRun(command) {
		remote := ""
		if m := pushTo.FindStringSubmatch(output); m != nil {
			remote = m[1]
		}
		for _, m := range pushLine.FindAllStringSubmatch(output, -1) {
			kind := m[1]
			if kind == "" {
				kind = "branch"
				if strings.HasPrefix(m[2], "refs/tags/") {
					kind = "tag"
				}
			}
			if strings.Contains(m[0], "[rejected]") {
				continue
			}
			out = append(out, PublishedRef{Kind: kind, Ref: strings.TrimPrefix(strings.TrimPrefix(m[2], "refs/heads/"), "refs/tags/"), Remote: remote})
		}
	}
	if strings.Contains(command, "gh pr create") {
		if url := prURL.FindString(output); url != "" {
			out = append(out, PublishedRef{Kind: "pull_request", URL: url})
		}
	}
	return out
}

// PublishedEntry is the derived record of what a call published, or nil
// when it published nothing the output says.
func PublishedEntry(callID, command, output string) (*agentsession.CustomEntry, error) {
	refs := PublishedRefs(command, output)
	if len(refs) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(struct {
		Refs []PublishedRef `json:"refs"`
	}{refs})
	if err != nil {
		return nil, err
	}
	mark, err := json.Marshal(Derivation{Rule: "published-refs", CallID: callID})
	if err != nil {
		return nil, err
	}
	e := &agentsession.CustomEntry{NS: PublishedNS, Data: data, CallID: callID}
	e.Unknown = map[string]json.RawMessage{DerivedMember: mark}
	return e, nil
}

// ShellCommand reads the command a shell tool call ran from its JSON
// arguments: a "command" or "cmd" string, or a "command" argv, joined.
func ShellCommand(arguments string) string {
	var a struct {
		Command json.RawMessage `json:"command"`
		Cmd     string          `json:"cmd"`
	}
	if json.Unmarshal([]byte(arguments), &a) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(a.Command, &s) == nil && s != "" {
		return s
	}
	var argv []string
	if json.Unmarshal(a.Command, &argv) == nil && len(argv) > 0 {
		return strings.Join(argv, " ")
	}
	return a.Cmd
}

// dryRun reports whether a git push command asks for a dry run, which
// publishes nothing: --dry-run, or -n as an argument of its own.
func dryRun(command string) bool {
	for _, f := range strings.Fields(command) {
		if f == "--dry-run" || f == "-n" {
			return true
		}
	}
	return false
}
