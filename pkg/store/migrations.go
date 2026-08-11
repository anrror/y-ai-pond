package store

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration holds a single schema change.
type Migration struct {
	Name string
	SQL  string
}

// LoadMigrations reads all SQL files from the embedded migrations directory,
// sorted by filename.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations dir: %w", err)
	}

	var migs []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read migration %s: %w", entry.Name(), err)
		}
		migs = append(migs, Migration{
			Name: strings.TrimSuffix(entry.Name(), ".sql"),
			SQL:  string(data),
		})
	}

	sort.Slice(migs, func(i, j int) bool {
		return migs[i].Name < migs[j].Name
	})

	return migs, nil
}

// SanitizeMigrationName removes characters that could break single-quoted SQL literals.
func SanitizeMigrationName(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '\'' {
			return -1
		}
		return r
	}, name)
}

// MigrationNames returns the names of all embedded migrations.
func MigrationNames() []string {
	migs, err := LoadMigrations()
	if err != nil {
		return nil
	}
	names := make([]string, len(migs))
	for i, m := range migs {
		names[i] = m.Name
	}
	return names
}
