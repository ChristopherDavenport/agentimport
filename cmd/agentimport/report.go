package main

import (
	"flag"
	"fmt"
	"io"
)

const reportUsage = `usage: agentimport report [-from auto|claudecode|pi|codex|atif|cline|openhands|opencode] [-strict] [-slim] [-json] <file|dir>...

Converts each session without writing anything and prints what the
conversions changed, added up per source: where every native line went
and the findings grouped by code. Exits 1 when a conversion failed, or
with -strict when a report holds a dropped or gap finding.
`

// fileJSON is one input in report -json.
type fileJSON struct {
	Path     string   `json:"path"`
	Source   string   `json:"source"`
	Sessions int      `json:"sessions,omitempty"`
	Report   *summary `json:"report,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func reportCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, reportUsage); fs.PrintDefaults() }
	var c common
	c.register(fs)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if err := c.check(); err != nil || fs.NArg() == 0 {
		if err != nil {
			fmt.Fprintln(stderr, "agentimport report:", err)
		}
		fs.Usage()
		return 2
	}
	ins, skip, err := collect(fs.Args(), c.from)
	if err != nil {
		fmt.Fprintln(stderr, "agentimport report:", err)
		return 1
	}
	t := newTotals()
	var files []fileJSON
	newRunner(c.slim, &skip).each(ins, func(o outcome) {
		t.add(o)
		f := fileJSON{Path: o.in.path, Source: o.in.source.name, Error: errString(o.err)}
		if o.res != nil {
			sum := summarise(o.res.Report)
			f.Sessions, f.Report = len(o.res.Sessions), &sum
		}
		files = append(files, f)
	})
	if *asJSON {
		err = writeJSON(stdout, struct {
			Files     []fileJSON         `json:"files"`
			Aggregate map[string]summary `json:"aggregate"`
			Skipped   []skipped          `json:"skipped,omitempty"`
		}{files, t.aggregate(), sortedSkips(skip)})
	} else {
		err = t.writeText(stdout, skip)
	}
	if err != nil {
		fmt.Fprintln(stderr, "agentimport report:", err)
		return 1
	}
	return t.exit(c.strict)
}
