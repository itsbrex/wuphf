package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The bug: a read-time fallback to the legacy config directory doubled as the
// WRITE path, because Save() resolves through ConfigPath(). Anyone who
// installed before the directory rename kept writing into the old location
// forever and was never migrated. These tests pin the copy-forward that
// replaced it, and the properties that make it safe to run on someone's real
// home directory.

func TestMigrateLegacyConfigDirCopiesForward(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".nex")
	newDir := filepath.Join(home, ".wuphf")

	if err := os.MkdirAll(filepath.Join(legacy, "workflows"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{"company_name":"Dunder"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A sibling directory, because callers derive these with
	// filepath.Dir(ConfigPath()) and they were being stranded too.
	if err := os.WriteFile(filepath.Join(legacy, "workflows", "a.json"), []byte(`{"id":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if !migrateLegacyConfigDir(newDir, legacy) {
		t.Fatal("migration should have reported success")
	}

	got, err := os.ReadFile(filepath.Join(newDir, "config.json"))
	if err != nil || string(got) != `{"company_name":"Dunder"}` {
		t.Fatalf("config.json did not come forward: %q err=%v", got, err)
	}
	if _, err := os.ReadFile(filepath.Join(newDir, "workflows", "a.json")); err != nil {
		t.Fatalf("nested sibling directory did not come forward: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newDir, legacyMigrationMarker)); err != nil {
		t.Errorf("expected a marker recording where this came from: %v", err)
	}

	// COPY, not move. Deleting a user's data as a side effect of an upgrade is
	// not ours to do, and a downgrade must still find its files.
	if _, err := os.Stat(filepath.Join(legacy, "config.json")); err != nil {
		t.Errorf("the legacy directory must be left untouched, got %v", err)
	}
}

// It must only ever fill a vacuum. If the new directory already exists the
// migration is skipped outright: it never merges two trees or overwrites live
// config with something older.
func TestMigrateLegacyConfigDirNeverOverwrites(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".nex")
	newDir := filepath.Join(home, ".wuphf")

	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{"company_name":"OLD"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "config.json"), []byte(`{"company_name":"CURRENT"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if !migrateLegacyConfigDir(newDir, legacy) {
		t.Fatal("an existing new directory is a success case")
	}
	got, _ := os.ReadFile(filepath.Join(newDir, "config.json"))
	if string(got) != `{"company_name":"CURRENT"}` {
		t.Fatalf("live config was clobbered by the legacy copy: %q", got)
	}
	if _, err := os.Stat(filepath.Join(newDir, legacyMigrationMarker)); err == nil {
		t.Error("a directory that was never migrated must not be marked as migrated")
	}
}

// Nothing to migrate from is not an error, it is the normal case for a fresh
// install.
func TestMigrateLegacyConfigDirNoLegacy(t *testing.T) {
	home := t.TempDir()
	if migrateLegacyConfigDir(filepath.Join(home, ".wuphf"), filepath.Join(home, ".nex")) {
		t.Error("with no legacy directory and no new directory there is nothing to report success about")
	}
}

// Symlinks are skipped rather than followed, so a link inside the old config
// directory cannot make the migration write outside the destination.
func TestMigrateLegacyConfigDirSkipsSymlinks(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, ".nex")
	newDir := filepath.Join(home, ".wuphf")
	outside := filepath.Join(home, "outside.txt")

	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("not config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(legacy, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}

	if !migrateLegacyConfigDir(newDir, legacy) {
		t.Fatal("migration should have succeeded")
	}
	if _, err := os.Lstat(filepath.Join(newDir, "link.txt")); err == nil {
		t.Error("symlinks must not be carried across")
	}
	if _, err := os.Stat(filepath.Join(newDir, "config.json")); err != nil {
		t.Errorf("real files must still come across: %v", err)
	}
}
