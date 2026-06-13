package migrations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDirPairsAndSortsMigrations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "000002_second.down.sql", "DROP TABLE second;")
	writeFile(t, dir, "000001_first.up.sql", "CREATE TABLE first(id int);")
	writeFile(t, dir, "000002_second.up.sql", "CREATE TABLE second(id int);")
	writeFile(t, dir, "000001_first.down.sql", "DROP TABLE first;")
	writeFile(t, dir, "README.md", "ignored")

	migrations, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	if len(migrations) != 2 {
		t.Fatalf("migration count = %d", len(migrations))
	}
	if migrations[0].Version != 1 || migrations[0].Name != "first" {
		t.Fatalf("first migration = %#v", migrations[0])
	}
	if migrations[1].Version != 2 || migrations[1].Name != "second" {
		t.Fatalf("second migration = %#v", migrations[1])
	}
}

func TestLoadDirRequiresDownMigration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "000001_first.up.sql", "CREATE TABLE first(id int);")

	_, err := LoadDir(dir)
	if err == nil {
		t.Fatal("expected missing down migration error")
	}
}

func TestFindByVersion(t *testing.T) {
	migration, ok := FindByVersion([]Migration{{Version: 7, Name: "lucky"}}, 7)
	if !ok {
		t.Fatal("expected migration")
	}
	if migration.Name != "lucky" {
		t.Fatalf("migration name = %q", migration.Name)
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
