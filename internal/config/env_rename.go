package config

import (
	"log"
	"os"
	"strings"
	"sync"
)

// env_rename.go lets one environment variable answer to two names while the
// product rename is in flight.
//
// WHY A DUAL READ AND NOT A FIND-AND-REPLACE.
// These names are not ours. They are in people's shells, .env files, CI
// secrets, systemd units, and Dockerfiles we cannot reach or update. Renaming
// WUPHF_X to GAWKBOT_X in the source does not rename it in a customer's CI: it
// makes their existing setting stop being read.
//
// And the failure is SILENT. A missing variable is not an error here, it is a
// default. Someone's pipeline keeps going green while quietly running against
// the wrong endpoint, the wrong model, or the wrong home directory. Nothing
// says why. That is the worst shape a break can take, so the fallback warns
// loudly and by name rather than trusting anyone to read a changelog.
//
// Precedent: BaseURL() already reads WUPHF_DEV_URL and falls back to
// NEX_DEV_URL from the previous rename. This generalises that.

// EnvPrefix is the current environment-variable prefix.
const EnvPrefix = "WUPHF_"

// LegacyEnvPrefixes are prefixes this runtime used to use, NEWEST FIRST.
//
// The rename adds the new prefix as EnvPrefix and moves "WUPHF_" to the front
// of this list; nothing else has to change, because callers pass the bare
// suffix and every generation is tried in order.
var LegacyEnvPrefixes = []string{"NEX_"}

// EnvFallbackRemovedIn names the release that stops honouring the legacy
// prefixes. A deprecation with no end date is not a deprecation, it is a second
// permanent API, so the warning always states when the old name dies.
const EnvFallbackRemovedIn = "v1.0.0"

// warnedEnvFallback keeps the deprecation warning to once per variable per
// process. A resolver on a hot path must not turn the logs into a stream.
var warnedEnvFallback sync.Map

// LookupEnv resolves one setting across the rename.
//
// `name` may be given either bare ("RUNTIME_HOME") or fully prefixed with any
// generation ("WUPHF_RUNTIME_HOME", "NEX_RUNTIME_HOME") — the prefix is
// stripped and every generation is tried in order, current first. Passing the
// fully prefixed current name is what existing call sites already have, so the
// migration at each call site is `os.LookupEnv` -> `config.LookupEnv` with the
// string untouched.
//
// Returns the value and whether it was set anywhere.
func LookupEnv(name string) (string, bool) {
	return lookupEnvWith(os.LookupEnv, name)
}

// LookupEnvFunc is LookupEnv against a caller-supplied getenv, for packages
// that inject their own lookup for testing (internal/runtimebin does). An
// empty string is treated as unset, matching os.Getenv's shape.
func LookupEnvFunc(getenv func(string) string, name string) (string, bool) {
	if getenv == nil {
		return LookupEnv(name)
	}
	return lookupEnvWith(func(k string) (string, bool) {
		v := getenv(k)
		return v, v != ""
	}, name)
}

func lookupEnvWith(lookup func(string) (string, bool), name string) (string, bool) {
	suffix := stripEnvPrefix(name)
	if suffix == "" {
		return "", false
	}

	current := EnvPrefix + suffix
	if v, ok := lookup(current); ok {
		return v, true
	}
	for _, legacy := range LegacyEnvPrefixes {
		old := legacy + suffix
		v, ok := lookup(old)
		if !ok {
			continue
		}
		warnEnvFallbackOnce(old, current)
		return v, true
	}
	return "", false
}

// Getenv is LookupEnv for callers that do not care whether the value was set,
// mirroring os.Getenv.
func Getenv(name string) string {
	v, _ := LookupEnv(name)
	return v
}

func stripEnvPrefix(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, EnvPrefix) {
		return strings.TrimPrefix(name, EnvPrefix)
	}
	for _, legacy := range LegacyEnvPrefixes {
		if strings.HasPrefix(name, legacy) {
			return strings.TrimPrefix(name, legacy)
		}
	}
	return name
}

// warnEnvFallbackOnce names BOTH variables and the removal release. Naming only
// the old one leaves the reader to guess the new spelling, which is how a
// deprecation notice becomes a support ticket.
func warnEnvFallbackOnce(old, current string) {
	if _, seen := warnedEnvFallback.LoadOrStore(old, true); seen {
		return
	}
	log.Printf("DEPRECATED: %s is set but %s is not. %s is being read for now and will STOP being read in %s. Rename it to %s.",
		old, current, old, EnvFallbackRemovedIn, current)
}
