package sqlite

import (
	"io"

	isqlite "github.com/ChristopherDavenport/agentimport/internal/sqlite"
)

// Fixture builds a database at path from SQL text, for tests: the
// fixtures of this module are SQL text, never database files. The
// database is put in WAL mode with automatic checkpoints off and
// returned open: rows the script writes after a checkpoint stay in the
// write-ahead log, where a reader finds them only through the log, for
// as long as the returned closer is not closed. Closing it checkpoints
// the log into the database.
func Fixture(path, script string) (io.Closer, error) { return isqlite.Fixture(path, script) }
