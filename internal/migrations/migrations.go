// Package migrations embeds the SQL migrations applied by the database
// package at startup. Files are named with a zero-padded numeric prefix and
// are applied in ascending order. Migrations are up-only; rollback means
// restoring the backup taken before the migration run.
package migrations

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed *.sql
var FS embed.FS

// Migration is one versioned SQL migration.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// All returns the embedded migrations ordered by version.
func All() ([]Migration, error) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil, err
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := versionOf(entry.Name())
		if err != nil {
			return nil, err
		}
		body, err := FS.ReadFile(entry.Name())
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, Migration{
			Version: version,
			Name:    entry.Name(),
			SQL:     string(body),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	for i, m := range migrations {
		if i > 0 && migrations[i-1].Version == m.Version {
			return nil, fmt.Errorf("duplicate migration version %d", m.Version)
		}
	}
	return migrations, nil
}

func versionOf(name string) (int, error) {
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return 0, fmt.Errorf("migration %q must start with a numeric version followed by an underscore", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration %q has an invalid version: %w", name, err)
	}
	if version <= 0 {
		return 0, fmt.Errorf("migration %q has a non-positive version", name)
	}
	return version, nil
}
