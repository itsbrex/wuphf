package config

import "path/filepath"

// runtime_dir.go is the single place the runtime home DIRECTORY NAME is
// defined, and the ordered history of what it used to be called.
//
// WHY THIS EXISTS, AND WHY IT STILL SAYS ".wuphf".
//
// The product is being renamed, and the runtime home moves with it. That move
// is NOT safe to make one file at a time. 83 non-test call sites across 51
// files build their own path with filepath.Join(home, ".wuphf", ...) rather
// than deriving it from ConfigPath() or RuntimeHomeDir() — the calendar store,
// the cache, the wiki root, the workspace root, and the OpenClaw identity file
// among them. Flipping the name here while those still say ".wuphf" would not
// migrate a user, it would SPLIT them: config.json under the new directory and
// their calendar, wiki, and workspace under the old one. That is strictly worse
// than not renaming at all, because the two halves then drift apart.
//
// So the rename of this constant is deliberately NOT part of the migration
// shim. The shim is the machinery: the chain below, and the copy-forward in
// legacy_home_migration.go, both landed and tested ahead of time.
//
// TO ACTUALLY PERFORM THE RENAME, as one atomic change:
//  1. set RuntimeDirName to the new name
//  2. prepend the OLD name to LegacyRuntimeDirNames (newest first)
//  3. repoint the 51 files that hardcode the literal at RuntimeDir()
//
// Steps 1 and 2 are two lines and the migration then runs itself. Step 3 is the
// mechanical sweep. Doing 1 and 2 without 3 is the split described above.

// RuntimeDirName is the current runtime home directory name, and the single
// point the rename above turns on.
const RuntimeDirName = ".wuphf"

// LegacyRuntimeDirNames are directory names this runtime used to use, NEWEST
// FIRST. A user two renames behind is migrated from their most recent
// directory, not their oldest, so the freshest data wins.
var LegacyRuntimeDirNames = []string{".nex"}

// RuntimeDir returns the runtime home directory inside the given home.
// Callers should prefer this over building the path with a literal, so the
// rename above stays a one-line change.
func RuntimeDir(home string) string {
	return filepath.Join(home, RuntimeDirName)
}

// legacyRuntimeDirs returns the historical directories inside home, newest
// first, ready to hand to the copy-forward migration.
func legacyRuntimeDirs(home string) []string {
	dirs := make([]string, 0, len(LegacyRuntimeDirNames))
	for _, name := range LegacyRuntimeDirNames {
		dirs = append(dirs, filepath.Join(home, name))
	}
	return dirs
}
