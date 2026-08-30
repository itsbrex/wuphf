package action

import (
	"path/filepath"
	"testing"
)

// TestComposioConfiguredWithOnlyComposioCredentials pins the regression that
// motivated removing the Nex coupling: Composio authenticates directly against
// Composio's own REST API with a project `ak_` key (or the `uak_` + org-id
// pair). Nothing in that path proxies through any other service, so Composio
// must report itself configured given nothing but Composio credentials.
//
// The bug this replaces: Configured() gated on a "Nex disabled" flag and the
// credential resolvers returned "" under it, so a user with valid Composio
// credentials was told "composio is not configured; set COMPOSIO_API_KEY".
func TestComposioConfiguredWithOnlyComposioCredentials(t *testing.T) {
	isolateComposioConfig(t)
	t.Setenv("WUPHF_COMPOSIO_API_KEY", "ak_project_key")
	t.Setenv("WUPHF_COMPOSIO_USER_ID", "operator@example.com")

	client := NewComposioFromEnv()
	if !client.Configured() {
		t.Fatalf("Configured() = false with a project key and a user id; want true (APIKey=%q UserID=%q)", client.APIKey, client.UserID)
	}
}

// TestComposioConfiguredWithUserKeyOnly covers the one-click sign-in auth mode
// (uak_ session key + org id), which is the only mode the current Composio CLI
// can produce. It must be equally free of any unrelated gate.
func TestComposioConfiguredWithUserKeyOnly(t *testing.T) {
	isolateComposioConfig(t)
	t.Setenv("WUPHF_COMPOSIO_USER_API_KEY", "uak_session_key")
	t.Setenv("WUPHF_COMPOSIO_ORG_ID", "org_1")
	t.Setenv("WUPHF_COMPOSIO_USER_ID", "operator@example.com")

	client := NewComposioFromEnv()
	if !client.Configured() {
		t.Fatalf("Configured() = false with a user key + org id; want true")
	}
}

// TestComposioConfiguredIgnoresLegacyNoNexEnv is the failing-before/passing-after
// half of the regression. WUPHF_NO_NEX (the `--no-nex` flag) used to blank every
// Composio credential resolver and short-circuit Configured(). The flag is now
// an accepted no-op, so a launcher that still passes it must not disable an
// integration surface that never had anything to do with it.
func TestComposioConfiguredIgnoresLegacyNoNexEnv(t *testing.T) {
	isolateComposioConfig(t)
	t.Setenv("WUPHF_NO_NEX", "1")
	t.Setenv("WUPHF_COMPOSIO_API_KEY", "ak_project_key")
	t.Setenv("WUPHF_COMPOSIO_USER_ID", "operator@example.com")

	client := NewComposioFromEnv()
	if !client.Configured() {
		t.Fatalf("Configured() = false with WUPHF_NO_NEX set; the legacy flag must not gate Composio")
	}
}

// isolateComposioConfig points config resolution at an empty temp config file
// so the test never reads (or is rescued by) the developer's real
// ~/.wuphf/config.json, and clears every Composio env var the resolvers read.
func isolateComposioConfig(t *testing.T) {
	t.Helper()
	t.Setenv("WUPHF_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	for _, key := range []string{
		"WUPHF_COMPOSIO_API_KEY", "COMPOSIO_API_KEY",
		"WUPHF_COMPOSIO_USER_API_KEY",
		"WUPHF_COMPOSIO_ORG_ID",
		"WUPHF_COMPOSIO_PROJECT_ID",
		"WUPHF_COMPOSIO_USER_ID", "COMPOSIO_USER_ID",
		"WUPHF_NO_NEX", "NEX_NO_NEX",
	} {
		t.Setenv(key, "")
	}
}
