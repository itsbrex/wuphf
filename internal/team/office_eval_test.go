package team

import (
	"os"
	"testing"
)

// TestOfficeEvals runs the U0.1 outcome eval harness in CI. Checks marked
// as known gaps are allowed to be red (they are the executable form of the
// uplift plan and flip green as phases land); anything else failing is a
// harness regression and fails the build.
func TestOfficeEvals(t *testing.T) {
	// Deliberately NOT t.TempDir().
	//
	// RunOfficeEvals starts brokers and wiki workers and never stops them — it
	// contains no Stop() call — so background goroutines are still writing into
	// the scratch directory when it returns. t.TempDir()'s automatic cleanup
	// races those writers and fails the test with
	// "TempDir RemoveAll cleanup: directory not empty", AFTER every eval check
	// has already passed. That reads like an eval regression and is not one; it
	// cost real bisection time, and was repeatedly mistaken for load.
	//
	// This removes the false failure only. It does NOT fix the underlying
	// issue: the eval harness has no shutdown path, and workers outliving it is
	// worth addressing on its own terms rather than through a test's temp-dir
	// lifecycle. Best-effort removal keeps the scratch dir from accumulating
	// without letting a late write fail an otherwise-passing run.
	dir, err := os.MkdirTemp("", "wuphf-office-evals-")
	if err != nil {
		t.Fatalf("scratch dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	report, err := RunOfficeEvals(dir)
	if err != nil {
		t.Fatalf("run office evals: %v", err)
	}
	for _, c := range report.Checks {
		status := "PASS"
		if !c.Pass {
			status = "FAIL"
			if c.KnownGap != "" {
				status = "KNOWN-GAP (red until " + c.KnownGap + ")"
			}
		}
		t.Logf("[%s] %s / %s — %s", status, c.Job, c.Check, c.Detail)
	}
	for _, c := range report.UnexpectedFailures() {
		t.Errorf("eval regression: %s / %s — %s", c.Job, c.Check, c.Detail)
	}
	// A known gap going green means a phase landed: the KnownGap marker
	// must be removed in the same PR so the check becomes a regression
	// guard. Surface that loudly instead of letting it ride.
	for _, c := range report.KnownGapStatus() {
		if c.Pass {
			t.Errorf("known gap %q now PASSES (%s / %s) — remove its KnownGap marker to lock it in as a regression guard", c.KnownGap, c.Job, c.Check)
		}
	}
}
