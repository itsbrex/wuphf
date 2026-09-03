package gbrain

// version.go — know which gbrain we are talking to, so the workarounds for its
// bugs can be applied only where they are needed.
//
// Why this exists
// ===============
// This package carries defensive code for gbrain defects found by probing
// 0.42.58.0. Three of the four were fixed upstream by 0.48.1.0, verified by
// re-probing:
//
//	put_page left deleted_at set        FIXED in 0.48 (a re-write resurrects)
//	list_pages ignored `offset`         FIXED in 0.48 (offset is honoured)
//	add_link rejected a missing endpoint FIXED in 0.48 (tolerated)
//	query ignores the `type` filter      STILL BROKEN — the client-side filter
//	                                     in Search stays, unconditionally
//
// Carrying a fix for a bug the installed gbrain no longer has is not free: the
// put_page carve-out costs one extra MCP round-trip on EVERY page write, which
// on a bulk reconcile of a few hundred facts is a few hundred wasted calls.
// Carrying none of them silently corrupts a brain on an older gbrain, where a
// retired-then-re-extracted fact vanishes with no error. So the version decides.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// MinRecommendedVersion is the first gbrain that fixes the three defects above.
//
// Below it WUPHF still works — the compensating code activates — but the
// operator is told, because a stale gbrain is a silent-data-loss risk they can
// fix in one command.
const MinRecommendedVersion = "0.48.0.0"

// versionOnce caches the probe. The binary cannot change under a running
// process in any way that matters, and this is consulted on hot write paths.
var (
	versionOnce sync.Once
	versionStr  string
	versionErr  error
)

// Version returns the installed gbrain's version string (e.g. "0.48.1.0").
func Version(ctx context.Context) (string, error) {
	versionOnce.Do(func() {
		out, err := runGBrain(ctx, "--version")
		if err != nil {
			versionErr = err
			return
		}
		versionStr = parseVersionLine(out)
		if versionStr == "" {
			versionErr = fmt.Errorf("gbrain --version: unrecognised output %.60q", out)
		}
	})
	return versionStr, versionErr
}

// parseVersionLine extracts the dotted version from `gbrain --version` output.
//
// The output carries a prefix ("gbrain 0.48.1.0") and the command may also emit
// an upgrade banner, so this scans for the first token that looks like a
// version rather than assuming a position.
func parseVersionLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		for _, tok := range strings.Fields(line) {
			tok = strings.TrimSpace(tok)
			if looksLikeVersion(tok) {
				return tok
			}
		}
	}
	return ""
}

// looksLikeVersion reports whether tok is a dotted numeric version.
func looksLikeVersion(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// compareVersions returns -1, 0 or 1 for a<b, a==b, a>b.
//
// Components are compared numerically, and a missing component counts as 0 so
// "0.48" and "0.48.0.0" compare equal. gbrain uses a four-part scheme that does
// not fit semver libraries, which is why this is hand-rolled and small.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// AtLeast reports whether the installed gbrain is at least `min`.
//
// An UNKNOWN version returns false: the compensating code is cheap and correct
// on every version, so when in doubt it is applied. Guessing "new enough" would
// trade a few round-trips for silent data loss.
func AtLeast(ctx context.Context, min string) bool {
	v, err := Version(ctx)
	if err != nil || v == "" {
		return false
	}
	return compareVersions(v, min) >= 0
}

// NeedsPutPageRestore reports whether put_page must be followed by
// restore_page to clear a soft-delete tombstone.
//
// Before 0.48 a write to a soft-deleted slug updated the row but left it
// invisible to get_page AND search, so a retired-then-re-extracted fact was
// lost permanently with no error anywhere.
func NeedsPutPageRestore(ctx context.Context) bool {
	return !AtLeast(ctx, MinRecommendedVersion)
}

// VersionAdvisory returns a one-line warning when the installed gbrain predates
// the fixes, or "" when it is current. Intended for a single startup log line.
func VersionAdvisory(ctx context.Context) string {
	v, err := Version(ctx)
	if err != nil {
		return "gbrain version unknown (" + err.Error() + "); compatibility workarounds stay enabled"
	}
	if compareVersions(v, MinRecommendedVersion) >= 0 {
		return ""
	}
	return fmt.Sprintf(
		"gbrain %s predates %s: writes to a previously deleted page can be silently lost, "+
			"and page listing cannot paginate. WUPHF compensates, at the cost of an extra call per write. "+
			"Upgrade with `gbrain self-upgrade`.", v, MinRecommendedVersion)
}

// resetVersionCache clears the memoised probe. Test-only: the version cannot
// change under a running process in production, which is why it is cached at
// all.
func resetVersionCache() {
	versionOnce = sync.Once{}
	versionStr = ""
	versionErr = nil
}
