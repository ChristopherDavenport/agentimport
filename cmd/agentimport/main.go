// Command agentimport converts other coding agents' session logs into
// the Agent Session Format, and reports what each conversion changed.
//
//	agentimport convert -o <store> [-from auto|claudecode|pi|codex|atif|cline|clinesdk|openhands|opencode|hermes] [-strict] [-slim] [-force] [-report text|json] <file|dir>...
//	agentimport report [-from auto|claudecode|pi|codex|atif|cline|clinesdk|openhands|opencode|hermes] [-strict] [-slim] [-json] <file|dir>...
//	agentimport version
//
// convert writes every session a conversion makes, the main session and
// its subsessions, into the jsonl session store rooted at -o, the
// layout agentsession and dax read, and prints the report. report
// converts without writing and prints the findings of every input
// added together, which is how a corpus is surveyed.
//
// A directory argument is searched for sessions; a file argument is one
// session. With -from auto, each file's source is told from its name
// and its first lines, an ATIF document by its schema_version, a Cline
// or Roo Code task by its files' names in a task directory, an OpenHands
// conversation by its directory, its base_state.json or an event file,
// an OpenCode store by its data directory, a file in it, or its
// database, which is converted whole, a Cline SDK session by its
// directory or a file in it, converted with its children, a Hermes
// database by its tables, or the home or profile directory holding it,
// converted whole; a file that is not a session of any source is
// skipped when it was found in a directory and an error when named.
//
// Both commands exit 1 when a conversion failed, and with -strict when
// a report holds a dropped or gap finding; 2 on bad usage.
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	sdksqlite "github.com/ChristopherDavenport/agentimport/clinesdk/sqlite"
	hsqlite "github.com/ChristopherDavenport/agentimport/hermes/sqlite"
	"github.com/ChristopherDavenport/agentimport/opencode/sqlite"
)

func main() {
	// An OpenCode, Cline or Hermes database is read from a private copy, which
	// an interrupted run removes before it exits.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		sqlite.RemoveCopies()
		sdksqlite.RemoveCopies()
		hsqlite.RemoveCopies()
		os.Exit(130)
	}()
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const usage = `usage: agentimport <command> [arguments]

commands:
  convert -o <store> [flags] <file|dir>...  convert sessions into an agentsession store
  report [flags] <file|dir>...             report what converting would change, without writing
  version                                  print the version

Run agentimport <command> -h for a command's flags.
`

// run executes the command line and returns the exit status.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "convert":
		return convertCmd(args[1:], stdout, stderr)
	case "report":
		return reportCmd(args[1:], stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "agentimport", version())
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "agentimport: unknown command %q\n%s", args[0], usage)
	return 2
}

// version is the main module's version, as go install stamps it.
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
