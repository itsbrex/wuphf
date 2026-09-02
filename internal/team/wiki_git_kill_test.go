package team

import (
	"errors"
	"os/exec"
	"syscall"
	"testing"
)

// TestIsTransientProcessKill separates "the OS reaped this process" from "the
// program failed", which is the whole basis for retrying one and not the other.
//
// Retrying a non-zero exit would be wrong: git is reporting that the command
// itself was invalid, and a retry just repeats the mistake. Retrying a SIGKILL
// is safe because the operation never ran.
func TestIsTransientProcessKill(t *testing.T) {
	t.Run("SIGKILL is transient", func(t *testing.T) {
		// `sh -c 'kill -9 $$'` reproduces the real signature: an ExitError
		// whose WaitStatus reports Signaled() with SIGKILL.
		err := exec.Command("sh", "-c", "kill -9 $$").Run()
		if err == nil {
			t.Skip("could not produce a SIGKILLed process on this platform")
		}
		if !isTransientProcessKill(err) {
			t.Errorf("SIGKILLed process not classified as transient: %v", err)
		}
	})

	t.Run("a normal non-zero exit is NOT transient", func(t *testing.T) {
		err := exec.Command("sh", "-c", "exit 1").Run()
		if err == nil {
			t.Fatal("expected a non-zero exit")
		}
		if isTransientProcessKill(err) {
			t.Error("a plain exit-1 was classified as transient; retrying it would just repeat a real failure")
		}
	})

	t.Run("SIGTERM is NOT retried", func(t *testing.T) {
		// SIGTERM is usually a deliberate shutdown — ours or an operator's.
		// Retrying through it would fight the caller's intent.
		err := exec.Command("sh", "-c", "kill -15 $$").Run()
		if err == nil {
			t.Skip("could not produce a SIGTERMed process on this platform")
		}
		if isTransientProcessKill(err) {
			t.Error("SIGTERM classified as transient; only SIGKILL should retry")
		}
	})

	t.Run("a non-exec error is not transient", func(t *testing.T) {
		if isTransientProcessKill(errors.New("boom")) {
			t.Error("a plain error was classified as a process kill")
		}
		if isTransientProcessKill(nil) {
			t.Error("nil was classified as a process kill")
		}
	})

	t.Run("classification reads the wait status, not the message", func(t *testing.T) {
		// Guards against a future refactor to string matching on "signal:
		// killed", which would misfire on a git command whose OUTPUT happens to
		// contain that text.
		var exitErr *exec.ExitError
		err := exec.Command("sh", "-c", "echo 'signal: killed'; exit 3").Run()
		if !errors.As(err, &exitErr) {
			t.Fatalf("expected an ExitError, got %T", err)
		}
		if isTransientProcessKill(err) {
			t.Error("classified on message text rather than wait status")
		}
		_ = syscall.SIGKILL // keep the syscall import meaningful on all platforms
	})
}
