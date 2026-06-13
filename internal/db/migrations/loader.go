package migrations

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

var migrationFilePattern = regexp.MustCompile(`^([0-9]+)_(.+)\.(up|down)\.sql$`)

type Migration struct {
	Version  int64  `json:"version"`
	Name     string `json:"name"`
	UpPath   string `json:"up_path,omitempty"`
	DownPath string `json:"down_path,omitempty"`
	UpSQL    string `json:"-"`
	DownSQL  string `json:"-"`
}

func LoadDir(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	byVersion := map[int64]*Migration{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		match := migrationFilePattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}
		version, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse migration version %q: %w", match[1], err)
		}
		migration := byVersion[version]
		if migration == nil {
			migration = &Migration{Version: version, Name: match[2]}
			byVersion[version] = migration
		}
		if migration.Name != match[2] {
			return nil, fmt.Errorf("migration version %d has inconsistent names %q and %q", version, migration.Name, match[2])
		}

		path := filepath.Join(dir, entry.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", path, err)
		}
		switch match[3] {
		case "up":
			migration.UpPath = path
			migration.UpSQL = string(body)
		case "down":
			migration.DownPath = path
			migration.DownSQL = string(body)
		}
	}

	migrations := make([]Migration, 0, len(byVersion))
	for _, migration := range byVersion {
		if migration.UpSQL == "" {
			return nil, fmt.Errorf("migration %d_%s is missing up SQL", migration.Version, migration.Name)
		}
		if migration.DownSQL == "" {
			return nil, fmt.Errorf("migration %d_%s is missing down SQL", migration.Version, migration.Name)
		}
		migrations = append(migrations, *migration)
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}

func FindByVersion(migrations []Migration, version int64) (Migration, bool) {
	for _, migration := range migrations {
		if migration.Version == version {
			return migration, true
		}
	}
	return Migration{}, false
}
