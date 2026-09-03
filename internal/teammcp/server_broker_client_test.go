package teammcp

import "testing"

// TestResolveSlugRejectsSpoofedCEO pins the R6 hardening: a specialist
// process (env slug != cos) cannot claim my_slug=cos to reach the
// CEO-only scope-shaping actions (create/define/reassign/approve/...).
// The real CEO process (WUPHF_AGENT_SLUG=cos) is unaffected.
func TestResolveSlugRejectsSpoofedCEO(t *testing.T) {
	t.Setenv("WUPHF_AGENT_SLUG", "eng")
	if _, err := resolveSlug("cos"); err == nil {
		t.Fatal("specialist claiming my_slug=cos must be rejected")
	}
	t.Setenv("WUPHF_AGENT_SLUG", "cos")
	slug, err := resolveSlug("cos")
	if err != nil || slug != "cos" {
		t.Fatalf("real cos must resolve; got %q, %v", slug, err)
	}
}
