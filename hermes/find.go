package hermes

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// DirProfiles is the directory of a home's further profiles, each a
// directory holding its own state.db.
const DirProfiles = "profiles"

// IsHome reports whether dir is a Hermes home or profile directory: it
// holds state.db.
func IsHome(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, DatabaseName))
	return err == nil && info.Mode().IsRegular()
}

// Databases lists the databases a home holds: its own state.db and each
// profile's, ~/.hermes/profiles/<name>/state.db, in name order.
func Databases(dir string) []string {
	var out []string
	if IsHome(dir) {
		out = append(out, filepath.Join(dir, DatabaseName))
	}
	entries, err := os.ReadDir(filepath.Join(dir, DirProfiles))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(dir, DirProfiles, e.Name())
		if IsHome(p) {
			out = append(out, filepath.Join(p, DatabaseName))
		}
	}
	return out
}

// StoreOf returns the database a path names: the path itself when it is
// a file, the state.db of a home or profile directory, else false.
func StoreOf(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if !info.IsDir() {
		return path, info.Mode().IsRegular()
	}
	if IsHome(path) {
		return filepath.Join(path, DatabaseName), true
	}
	return "", false
}

// FindSessions lists the Hermes databases under root: each state.db,
// a home's and each of its profiles'. A file root is returned as it
// is. A database is converted whole: Hermes keeps every session of a
// profile in one. Directories that cannot be read are passed over, and
// symbolic links are not followed.
func FindSessions(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{root}, nil
	}
	var out []string
	seen := map[string]bool{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && path != root {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if IsHome(path) {
				for _, db := range Databases(path) {
					if !seen[db] {
						seen[db] = true
						out = append(out, db)
					}
				}
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && d.Name() == DatabaseName && !seen[path] {
			seen[path] = true
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
