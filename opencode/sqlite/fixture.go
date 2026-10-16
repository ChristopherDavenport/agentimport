package sqlite

import (
	"context"
	"database/sql"
	"io"
	"net/url"
	"path/filepath"
)

// Fixture builds a database at path from SQL text, for tests: the
// fixtures of this module are SQL text, never database files. The
// database is put in WAL mode with automatic checkpoints off and
// returned open: rows the script writes after a checkpoint stay in the
// write-ahead log, where a reader finds them only through the log, for
// as long as the returned closer is not closed. Closing it checkpoints
// the log into the database.
func Fixture(path, script string) (io.Closer, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: abs}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", script} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}
