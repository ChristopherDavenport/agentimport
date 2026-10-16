package opencode

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IsDataDir reports whether dir is an OpenCode data directory, such as
// ~/.local/share/opencode: it holds opencode.db, or a storage/ tree
// with sessions.
func IsDataDir(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, DatabaseName)); err == nil && info.Mode().IsRegular() {
		return true
	}
	info, err := os.Stat(filepath.Join(dir, DirStorage, DirSession))
	return err == nil && info.IsDir()
}

// IsDatabaseName reports whether a file name is one OpenCode gives a
// database: opencode.db, or opencode-<channel>.db for a build of
// another channel. OpenCode 2's preview database, opencode-next.db,
// holds another schema and is not one.
func IsDatabaseName(name string) bool {
	if name == DatabaseName {
		return true
	}
	return strings.HasPrefix(name, "opencode-") && strings.HasSuffix(name, ".db") && name != "opencode-next.db"
}

// Databases lists the databases of a data directory other than
// opencode.db: those of other channels.
func Databases(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() && IsDatabaseName(e.Name()) && e.Name() != DatabaseName {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// StoreOf returns the store a path names: the data directory it is, or
// is in, or else the database file itself. A file of the storage/ tree
// names its data directory, as does opencode.db.
func StoreOf(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		if IsDataDir(path) {
			return path, true
		}
		return "", false
	}
	name := filepath.Base(path)
	if name == DatabaseName {
		return filepath.Dir(path), true
	}
	if IsDatabaseName(name) {
		return path, true
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == DirStorage && IsDataDir(filepath.Dir(dir)) {
			return filepath.Dir(dir), true
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	return "", false
}

// FindSessions lists the OpenCode stores under root: each data
// directory, each database of another channel in one, and each
// database file outside one. A file root is returned as it is. A store
// is converted whole: OpenCode keeps every session of a data directory
// in one database or one storage/ tree. Directories that cannot be
// read are passed over, and symbolic links are not followed.
func FindSessions(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if IsDataDir(path) {
				out = append(out, path)
				out = append(out, Databases(path)...)
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && IsDatabaseName(d.Name()) {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
