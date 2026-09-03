package provider

import (
	"os/exec"
	"syscall"
)

// configureClaudeProcess detaches the CLI from the broker's terminal and job
// control so a Ctrl+C or hangup on the broker never reaches the agent.
//
// Setsid alone does all of that: a new session is by construction a new
// process group with no controlling terminal. Do NOT add Setpgid or Noctty:
// setpgid(2) on a fresh session leader fails with EPERM, and Noctty issues
// TIOCNOTTY on stdin, which fails with ENOTTY whenever the broker runs
// without a terminal (systemd, nohup, CI). Either flag turns every Claude
// launch on Linux into "fork/exec: operation not permitted" (verified in a
// golang:1.24 container, 2026-09-03).
func configureClaudeProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
