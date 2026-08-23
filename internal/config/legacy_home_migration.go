package config

import (
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// legacy_home_migration.go moves a pre-rename config directory forward, once.
//
// THE BUG THIS FIXES. ConfigPath() used to resolve to the legacy directory
// whenever the new one was absent, and Save() writes through ConfigPath(). A
// read-time fallback therefore doubled as a WRITE path: anyone who installed
// before the directory was renamed has been writing into the old directory ever
// since, and would have kept doing so forever. The fallback never migrated
// anybody; it just made the old location permanent. Several callers also derive
// sibling directories with filepath.Dir(ConfigPath()) (workflow storage, for
// one), so the whole config tree stayed behind, not only config.json.
//
// THE FIX. On first resolution, if the new directory does not exist and the old
// one does, copy the old tree forward and use the new location from then on.
//
// Deliberate properties, each of which is a rule for the next rename too:
//
//   - COPY, never move. The old directory is left exactly as it was. Deleting a
//     user's data as a side effect of an upgrade is not ours to do, and a copy
//     means a downgrade still finds its files.
//   - ONE SHOT. sync.Once, so a path lookup on a hot path cannot turn into
//     repeated directory walks.
//   - ONLY into a vacuum. If the new directory already exists the migration is
//     skipped entirely. It never merges, overwrites, or reconciles two trees.
//   - DEGRADE, do not fail. If the copy fails the caller keeps working against
//     the legacy path. A broken migration must not lock somebody out of their
//     own config.
//   - It leaves a marker so the next person can tell a migrated directory from
//     a native one without guessing.
var legacyMigrationOnce sync.Once

const legacyMigrationMarker = ".migrated-from-legacy-home"

// migrateLegacyConfigDirOnce copies legacyDir to newDir when newDir is absent.
// Returns true when the new directory is usable afterwards (either it already
// existed, or the copy succeeded).
func migrateLegacyConfigDirOnce(newDir, legacyDir string) bool {
	migrated := false
	legacyMigrationOnce.Do(func() {
		migrated = migrateLegacyConfigDir(newDir, legacyDir)
	})
	if migrated {
		return true
	}
	// After the first call, answer from the filesystem rather than re-running.
	if st, err := os.Stat(newDir); err == nil && st.IsDir() {
		return true
	}
	return false
}

func migrateLegacyConfigDir(newDir, legacyDir string) bool {
	if st, err := os.Stat(newDir); err == nil && st.IsDir() {
		return true // already on the new location; nothing to do
	}
	st, err := os.Stat(legacyDir)
	if err != nil || !st.IsDir() {
		return false // nothing to migrate from
	}
	if err := copyTree(legacyDir, newDir); err != nil {
		log.Printf("config: could not migrate %s to %s (%v); continuing to use the old location", legacyDir, newDir, err)
		// Do not leave a half-copied tree pretending to be a real config dir.
		_ = os.RemoveAll(newDir)
		return false
	}
	marker := filepath.Join(newDir, legacyMigrationMarker)
	if err := os.WriteFile(marker, []byte(legacyDir+"\n"), 0o600); err != nil {
		log.Printf("config: migrated %s to %s but could not write the marker: %v", legacyDir, newDir, err)
	}
	log.Printf("config: copied %s forward to %s. The old directory was left untouched and can be removed once you are happy.", legacyDir, newDir)
	return true
}

// copyTree copies src to dst, creating dst. Regular files and directories only:
// symlinks are skipped rather than followed, so a link pointing outside the
// config directory cannot make the migration write somewhere unexpected.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			return nil // skip links; see doc comment
		case !d.Type().IsRegular():
			return nil // sockets, devices, fifos are not config
		default:
			return copyFile(path, target, d)
		}
	})
}

func copyFile(src, dst string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
