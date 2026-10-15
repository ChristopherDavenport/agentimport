package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentsession"
	"github.com/ChristopherDavenport/agentsession/jsonl"
)

const convertUsage = `usage: agentimport convert -o <store> [-from auto|claudecode|pi|codex] [-strict] [-slim] [-force] [-report text|json] <file|dir>...

Converts each session and writes every session the conversion makes,
the main session and its subsessions, into the agentsession jsonl store
rooted at -o: <store>/<project-key>/<created-at>_<session-id>.jsonl.
A session already in the store is kept as it is, and listed, unless
-force replaces it; the other sessions of its conversion are written
all the same, so converting a tree again adds what is new.
Prints the report: in full for one input, and a line per input with
the reports added up per source for several. Exits 1 when a conversion
or a write failed, or with -strict when a report holds a dropped or gap
finding.
`

// written is one session written to the store.
type written struct {
	ID   string `json:"id"`
	File string `json:"file"`
}

// convertJSON is one input in convert -report json.
type convertJSON struct {
	Path    string    `json:"path"`
	Source  string    `json:"source"`
	Written []written `json:"written,omitempty"`
	// Kept lists the sessions already in the store, left as they are.
	Kept    []string            `json:"kept,omitempty"`
	Report  *agentimport.Report `json:"report,omitempty"`
	Summary *summary            `json:"summary,omitempty"`
	Error   string              `json:"error,omitempty"`
}

func convertCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, convertUsage); fs.PrintDefaults() }
	var c common
	c.register(fs)
	out := fs.String("o", "", "the store's root directory (required)")
	force := fs.Bool("force", false, "replace a session the store already holds")
	format := fs.String("report", "text", "the report's form: text|json")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	err := c.check()
	switch {
	case err != nil:
	case *out == "":
		err = errors.New("-o is required")
	case *format != "text" && *format != "json":
		err = fmt.Errorf("-report %q: want text|json", *format)
	case fs.NArg() == 0:
		err = errors.New("no input")
	}
	if err != nil {
		fmt.Fprintln(stderr, "agentimport convert:", err)
		fs.Usage()
		return 2
	}
	ins, skip, err := collect(fs.Args(), c.from)
	if err != nil {
		fmt.Fprintln(stderr, "agentimport convert:", err)
		return 1
	}
	// Nothing is synced per line: Release syncs each session once it is
	// written, which is all an import needs.
	st, err := jsonl.Open(*out, jsonl.WithSync(jsonl.SyncNever))
	if err != nil {
		fmt.Fprintln(stderr, "agentimport convert:", err)
		return 1
	}
	defer st.Close()

	ctx := context.Background()
	t := newTotals()
	var results []convertJSON
	newRunner(c.slim, &skip).each(ins, func(o outcome) {
		var ws []written
		var kept []string
		if o.err == nil {
			ws, kept, o.err = writeAll(ctx, st, o.res, *force)
		}
		t.add(o)
		r := convertJSON{Path: o.in.path, Source: o.in.source.name, Written: ws, Kept: kept, Error: errString(o.err)}
		if o.res != nil {
			if len(ins) == 1 {
				r.Report = o.res.Report
			} else {
				sum := summarise(o.res.Report)
				r.Summary = &sum
			}
		}
		results = append(results, r)
	})

	if *format == "json" {
		err = writeJSON(stdout, struct {
			Sessions  []convertJSON      `json:"sessions"`
			Aggregate map[string]summary `json:"aggregate"`
			Skipped   []skipped          `json:"skipped,omitempty"`
		}{results, t.aggregate(), sortedSkips(skip)})
	} else {
		err = writeConvertText(stdout, results, ins, t, skip)
	}
	if err != nil {
		fmt.Fprintln(stderr, "agentimport convert:", err)
		return 1
	}
	return t.exit(c.strict)
}

func writeConvertText(w io.Writer, results []convertJSON, ins []input, t *totals, skip []skipped) error {
	for _, r := range results {
		for _, s := range r.Written {
			if _, err := fmt.Fprintf(w, "wrote %s\n", displayPath(s.File)); err != nil {
				return err
			}
		}
		for _, id := range r.Kept {
			if _, err := fmt.Fprintf(w, "kept %s: already in the store; -force replaces it\n", id); err != nil {
				return err
			}
		}
	}
	if len(results) == 1 && results[0].Report != nil {
		if err := results[0].Report.WriteText(w); err != nil {
			return err
		}
		if r := results[0]; r.Error != "" {
			_, err := fmt.Fprintf(w, "failed: %s: %s\n", displayPath(r.Path), r.Error)
			return err
		}
		return nil
	}
	for _, r := range results {
		var err error
		if r.Summary != nil {
			s := r.Summary
			_, err = fmt.Fprintf(w, "%s: %s, %d sessions, %d lines: mapped %d kept %d dropped %d malformed %d\n",
				displayPath(r.Path), r.Source, len(r.Written), s.Lines,
				s.Tally[agentimport.LineMapped], s.Tally[agentimport.LineKept],
				s.Tally[agentimport.LineDropped], s.Tally[agentimport.LineMalformed])
		}
		if err != nil {
			return err
		}
	}
	return t.writeText(w, skip)
}

// writeAll writes every session of a result into the store, the main
// session first, each on its own: a session already in the store is
// kept as it is unless force, and listed, and a session that fails to
// write does not stop the others, so importing a tree again writes the
// sub-agents spawned since. A session's entries are appended in the
// order the converter wrote them, so the store computes the same IDs
// and moves the leaf the same way.
func writeAll(ctx context.Context, st *jsonl.Store, res *agentimport.Result, force bool) (ws []written, kept []string, err error) {
	var errs []error
	for _, s := range res.Sessions {
		w, err := write(ctx, st, s, force)
		switch {
		case errors.Is(err, errStored):
			kept = append(kept, s.ID())
		case err != nil:
			errs = append(errs, err)
		default:
			ws = append(ws, w)
		}
	}
	return ws, kept, errors.Join(errs...)
}

// errStored is a session the store already holds, written without
// force.
var errStored = errors.New("already in the store")

func write(ctx context.Context, st *jsonl.Store, s *agentsession.Session, force bool) (written, error) {
	h := s.Header()
	if _, err := st.Path(h.ID); err == nil {
		if !force {
			return written{}, errStored
		}
		if err := st.Delete(ctx, h.ID); err != nil {
			return written{}, err
		}
	}
	sess, err := st.Create(ctx, h)
	if err != nil {
		return written{}, err
	}
	for _, e := range s.Entries() {
		if e.Base().Parent == "" {
			// A root: the store's session appends under its leaf
			// otherwise, and a converted session may hold several.
			sess.ResetLeaf()
		}
		if _, err := st.Append(ctx, h.ID, e); err != nil {
			// A session cut short would block the next run; it goes.
			st.Delete(ctx, h.ID)
			return written{}, fmt.Errorf("session %s: %w", h.ID, err)
		}
	}
	path, err := st.Path(h.ID)
	if err != nil {
		return written{}, err
	}
	return written{ID: h.ID, File: path}, st.Release(h.ID)
}
