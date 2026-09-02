package computer

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Runtime is a supported container CLI. Detection order matters: `docker`
// covers OrbStack and Docker Desktop, Apple `container` ships with macOS 26,
// Podman is the Linux fallback.
type Runtime string

const (
	RuntimeDocker    Runtime = "docker"
	RuntimeContainer Runtime = "container"
	RuntimePodman    Runtime = "podman"
)

// Runtimes lists every runtime in detection order.
var Runtimes = []Runtime{RuntimeDocker, RuntimeContainer, RuntimePodman}

// IsRuntime reports whether s names a supported runtime.
func IsRuntime(s string) bool {
	for _, r := range Runtimes {
		if string(r) == s {
			return true
		}
	}
	return false
}

// RuntimeStatus is what the machine can run containers with right now.
type RuntimeStatus struct {
	Available bool    `json:"available"`
	Runtime   Runtime `json:"runtime"`
	DaemonUp  bool    `json:"daemon_up"`
	Version   string  `json:"version,omitempty"`
	// Problem is empty when a runtime is installed and its daemon answers.
	Problem string `json:"problem,omitempty"`
	// InstallHint tells a person with no runtime what to install.
	InstallHint string `json:"install_hint,omitempty"`
	// StartHint tells a person with a stopped daemon how to start it.
	StartHint string `json:"start_hint,omitempty"`
}

// DetectRuntime probes the runtimes in order and returns the first one whose
// CLI is installed, preferring one whose daemon is up. platform is
// runtime.GOOS.
func DetectRuntime(ctx context.Context, run Runner, platform string) RuntimeStatus {
	var installedButDown *RuntimeStatus
	for _, rt := range Runtimes {
		status, installed := probeRuntime(ctx, run, rt)
		if !installed {
			continue
		}
		if status.DaemonUp {
			return status
		}
		if installedButDown == nil {
			copy := status
			installedButDown = &copy
		}
	}
	if installedButDown != nil {
		s := *installedButDown
		s.StartHint = startHint(s.Runtime, platform)
		s.Problem = "Start " + string(s.Runtime) + " first"
		return s
	}
	return RuntimeStatus{
		Problem:     "Install a container runtime first",
		InstallHint: installHint(platform),
	}
}

func probeRuntime(ctx context.Context, run Runner, rt Runtime) (RuntimeStatus, bool) {
	var args []string
	switch rt {
	case RuntimeDocker:
		args = []string{"info", "--format", "{{.ServerVersion}}"}
	case RuntimePodman:
		args = []string{"info", "--format", "{{.Version.Version}}"}
	case RuntimeContainer:
		args = []string{"system", "status"}
	}
	stdout, _, err := run(ctx, string(rt), args, DefaultTimeout)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return RuntimeStatus{}, false
		}
		// Installed, but the daemon refused or is not running.
		return RuntimeStatus{Available: true, Runtime: rt}, true
	}
	version := strings.TrimSpace(stdout)
	if rt == RuntimeContainer {
		// `container system status` prints prose; only "running" means up.
		if !strings.Contains(strings.ToLower(version), "running") {
			return RuntimeStatus{Available: true, Runtime: rt}, true
		}
		version = ""
	}
	return RuntimeStatus{Available: true, Runtime: rt, DaemonUp: true, Version: version}, true
}

func installHint(platform string) string {
	switch platform {
	case "darwin":
		return "Install OrbStack (https://orbstack.dev) or Docker Desktop, or Apple's container CLI on macOS 26 (https://github.com/apple/container)."
	case "linux":
		return "Install Docker Engine (https://docs.docker.com/engine/install/) or Podman."
	case "windows":
		return "Install Docker Desktop or Podman Desktop."
	}
	return "Install Docker or Podman."
}

func startHint(rt Runtime, platform string) string {
	switch rt {
	case RuntimeContainer:
		return "container system start"
	case RuntimePodman:
		if platform == "linux" {
			return "sudo systemctl start podman"
		}
		return "podman machine start"
	case RuntimeDocker:
		if platform == "darwin" {
			return "Open OrbStack or Docker Desktop"
		}
		if platform == "linux" {
			return "sudo systemctl start docker"
		}
		return "Open Docker Desktop"
	}
	return ""
}

// probeTimeout is how long a daemon has to answer an inspection call.
var probeTimeout = 8 * time.Second
