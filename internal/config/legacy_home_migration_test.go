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

// ─── rename CHAIN ────────────────────────────────────────────────────────────
// Renames chain: .nex became .wuphf, and .wuphf becomes whatever is next. The
// guard used to be one package-level sync.Once, which is correct for exactly
// one rename and silently wrong for the second: the first migration consumed
// it and every later one was skipped without a word, stranding that user's data
// at the old path forever. These pin the per-destination guard that replaced it.

func TestMigrateChainRunsMoreThanOneMigrationPerProcess(t *testing.T) {
	home := t.TempDir()
	first := filepath.Join(home, ".gen1")
	second := filepath.Join(home, ".gen2")
	third := filepath.Join(home, ".gen3")

	if err := os.MkdirAll(first, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "config.json"), []byte(`{"n":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if !migrateLegacyConfigDirOnce(second, first) {
		t.Fatal("first migration should succeed")
	}
	// The one that used to be silently skipped.
	if !migrateLegacyConfigDirOnce(third, second) {
		t.Fatal("SECOND migration in the same process must also run")
	}
	got, err := os.ReadFile(filepath.Join(third, "config.json"))
	if err != nil || string(got) != `{"n":1}` {
		t.Fatalf("data did not survive the chain: %q err=%v", got, err)
	}
}

// A user two renames behind lands on their most recent directory, not their
// oldest, so the freshest data wins rather than a stale generation.
func TestMigrateChainPrefersTheNewestLegacyGeneration(t *testing.T) {
	home := t.TempDir()
	oldest := filepath.Join(home, ".oldest")
	newer := filepath.Join(home, ".newer")
	target := filepath.Join(home, ".target")

	for dir, body := range map[string]string{oldest: `{"gen":"oldest"}`, newer: `{"gen":"newer"}`} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Candidates are passed newest first.
	if !migrateLegacyConfigDir(target, newer, oldest) {
		t.Fatal("migration should have succeeded")
	}
	got, _ := os.ReadFile(filepath.Join(target, "config.json"))
	if string(got) != `{"gen":"newer"}` {
		t.Fatalf("the newest generation must win, got %q", got)
	}
}

// Skipping a generation is normal: someone may have installed at .nex, never
// run the middle version, and jumped straight to the newest.
func TestMigrateChainSkipsGenerationsTheUserNeverUsed(t *testing.T) {
	home := t.TempDir()
	oldest := filepath.Join(home, ".oldest")
	missing := filepath.Join(home, ".never-used")
	target := filepath.Join(home, ".target")

	if err := os.MkdirAll(oldest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldest, "config.json"), []byte(`{"gen":"oldest"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if !migrateLegacyConfigDir(target, missing, oldest) {
		t.Fatal("should fall through the unused generation to the one that exists")
	}
	got, _ := os.ReadFile(filepath.Join(target, "config.json"))
	if string(got) != `{"gen":"oldest"}` {
		t.Fatalf("expected the oldest generation to come forward, got %q", got)
	}
}

// The end-to-end shape the whole task exists for: seed a POPULATED old-name
// home, resolve config under the new name, and assert the data is READABLE.
func TestPopulatedLegacyHomeIsReadableUnderTheNewName(t *testing.T) {
	home := t.TempDir()
	legacy := filepath.Join(home, LegacyRuntimeDirNames[0])
	if err := os.MkdirAll(filepath.Join(legacy, "wiki"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "config.json"), []byte(`{"company_name":"Dunder"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "wiki", "page.md"), []byte("# hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WUPHF_RUNTIME_HOME", home)
	path := ConfigPath()

	if filepath.Dir(path) != RuntimeDir(home) {
		t.Fatalf("config should resolve under the current runtime dir, got %s", path)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != `{"company_name":"Dunder"}` {
		t.Fatalf("the populated legacy home was not readable under the new name: %q err=%v", body, err)
	}
	// Siblings matter as much as config.json: callers derive them with
	// filepath.Dir(ConfigPath()) and they were the ones getting stranded.
	if _, err := os.ReadFile(filepath.Join(RuntimeDir(home), "wiki", "page.md")); err != nil {
		t.Fatalf("sibling directories must come forward too: %v", err)
	}
}

// RuntimeDirName is the single point the rename turns on. If someone flips it
// without also updating the 51 files that hardcode the literal, a user's config
// and their calendar/wiki/workspace end up in DIFFERENT directories. This pins
// the invariant that the current name and the legacy list never overlap, which
// is the one mistake that would make the migration copy a directory onto itself.
func TestRuntimeDirNameIsNotAlsoListedAsLegacy(t *testing.T) {
	for _, name := range LegacyRuntimeDirNames {
		if name == RuntimeDirName {
			t.Fatalf("%q is both the current runtime dir and a legacy one; the migration would target its own source", name)
		}
	}
}
