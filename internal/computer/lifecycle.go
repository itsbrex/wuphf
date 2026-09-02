package computer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// Action is one container lifecycle mutation.
type Action string

const (
	ActionRun    Action = "run"
	ActionStart  Action = "start"
	ActionStop   Action = "stop"
	ActionRemove Action = "remove"
)

// LifecycleError carries an HTTP status so the broker can answer 409 for a
// refused action without string-matching.
type LifecycleError struct {
	Status  int
	Message string
}

func (e *LifecycleError) Error() string { return e.Message }

func conflict(msg string) error { return &LifecycleError{Status: http.StatusConflict, Message: msg} }

// LifecycleTimeout bounds one run/start/stop/remove call.
var LifecycleTimeout = 2 * time.Minute

// Manager applies lifecycle actions to targets with one lock per target so a
// stop cannot interleave with a create.
type Manager struct {
	Run       Runner
	Inspector *Inspector
	Platform  string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (m *Manager) lockFor(target Target) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.locks == nil {
		m.locks = map[string]*sync.Mutex{}
	}
	l, ok := m.locks[target.Key]
	if !ok {
		l = &sync.Mutex{}
		m.locks[target.Key] = l
	}
	return l
}

// Apply runs one action and returns the status afterwards. Refusals are
// LifecycleError values with a 409 status.
func (m *Manager) Apply(ctx context.Context, rt RuntimeStatus, action Action, target Target) (Status, error) {
	lock := m.lockFor(target)
	lock.Lock()
	defer lock.Unlock()
	m.Inspector.Forget(target)
	before := m.Inspector.Inspect(ctx, rt, target)
	if rt.Runtime == "" {
		return before, conflict(firstNonEmpty(before.Problem, "No container runtime is installed"))
	}
	if !rt.DaemonUp {
		return before, conflict(firstNonEmpty(before.Problem, string(rt.Runtime)+" is not running"))
	}
	switch action {
	case ActionRun:
		if before.Container != ContainerMissing {
			return before, conflict("This bot already has a computer; remove it before creating a replacement")
		}
		if !before.Image {
			return before, conflict("Prepare the desktop image before creating a computer")
		}
		if err := os.MkdirAll(target.WorkspaceDir, 0o700); err != nil {
			return before, fmt.Errorf("create workspace: %w", err)
		}
		if m.Platform != "windows" {
			_ = os.Chmod(target.WorkspaceDir, 0o700)
		}
		password, err := randomPassword()
		if err != nil {
			return before, err
		}
		hostPort := 0
		if rt.Runtime == RuntimeContainer {
			// Apple container cannot publish an ephemeral loopback port, so
			// pick a free one here. The inspect afterwards reads it back.
			hostPort, err = freeLoopbackPort()
			if err != nil {
				return before, err
			}
		}
		args, err := ContainerRunArgs(rt.Runtime, password, target, hostPort)
		if err != nil {
			return before, err
		}
		if _, _, err := m.Run(ctx, string(rt.Runtime), args, LifecycleTimeout); err != nil {
			return before, err
		}
	case ActionStart:
		if before.Container != ContainerStopped {
			return before, conflict("This computer is not asleep")
		}
		if !before.Managed || !before.ImageMatches || before.Security == "unsafe" || before.Network == "unsafe" || before.Persistence == "unsafe" {
			return before, conflict(firstNonEmpty(before.Problem, "This computer failed verification; replace it"))
		}
		if _, _, err := m.Run(ctx, string(rt.Runtime), []string{"start", target.ContainerName}, LifecycleTimeout); err != nil {
			return before, err
		}
	case ActionStop:
		if before.Container != ContainerRunning {
			return before, conflict("This computer is not running")
		}
		if _, _, err := m.Run(ctx, string(rt.Runtime), []string{"stop", "-t", "15", target.ContainerName}, LifecycleTimeout); err != nil {
			return before, err
		}
	case ActionRemove:
		if before.Container == ContainerMissing {
			return before, nil
		}
		force := "-f"
		if rt.Runtime == RuntimeContainer {
			force = "--force"
		}
		if _, _, err := m.Run(ctx, string(rt.Runtime), []string{"rm", force, target.ContainerName}, LifecycleTimeout); err != nil {
			return before, err
		}
	default:
		return before, &LifecycleError{Status: http.StatusBadRequest, Message: "unknown action " + string(action)}
	}
	m.Inspector.Forget(target)
	return m.Inspector.Inspect(ctx, rt, target), nil
}

// Exists is a cheap probe by exact container name.
func (m *Manager) Exists(ctx context.Context, rt Runtime, target Target) bool {
	_, _, err := m.Run(ctx, string(rt), []string{"inspect", target.ContainerName}, DefaultTimeout)
	return err == nil
}

func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate viewer port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

func randomPassword() (string, error) {
	buf := make([]byte, 9)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ContainerRunArgs is the exact `run` argv. Apple container cannot publish
// an ephemeral loopback port, so callers on that runtime pass a fixed
// hostPort they allocated; Docker and Podman use `127.0.0.1::6901`.
func ContainerRunArgs(rt Runtime, password string, target Target, hostPort int) ([]string, error) {
	args := []string{"run", "-d", "--name", target.ContainerName,
		"--label", ManagedLabel + "=1",
		"--label", DriverLabel + "=" + CuaDriverVersion,
		"--label", BaseImageLabel + "=" + BaseImageDigest,
		"--label", LayerLabel + "=" + ImageLayerVersion,
		"--label", WorkspaceLabel + "=1",
		"--label", TargetLabel + "=" + target.Label,
	}
	memory := strconv.FormatInt(MemoryBytes/(1024*1024), 10) + "m"
	cpus := strconv.Itoa(CPUs)
	switch rt {
	case RuntimeContainer:
		if hostPort <= 0 {
			return nil, fmt.Errorf("apple container requires a fixed host port for the viewer")
		}
		args = append(args,
			"--memory", memory,
			"--cpus", cpus,
			"--cap-drop", "ALL",
			"--cap-add", "SETUID",
			"--cap-add", "SETGID",
			"--shm-size", "512m",
			"--mount", "type=bind,source="+target.WorkspaceDir+",target="+WorkspaceGuest,
			"-e", "VNC_PW="+password,
			"-p", "127.0.0.1:"+strconv.Itoa(hostPort)+":"+strconv.Itoa(internalViewerPort),
			Image,
		)
	default:
		mount := "type=bind,source=" + target.WorkspaceDir + ",target=" + WorkspaceGuest
		if rt == RuntimePodman {
			mount += ",relabel=private,U=true"
		}
		args = append(args,
			"--hostname", target.ContainerName,
			"--memory", memory,
			"--memory-swap", memory,
			"--cpus", cpus,
			"--pids-limit", strconv.FormatInt(PidsLimit, 10),
			"--ipc", "private",
			"--cgroupns", "private",
			"--cap-drop", "ALL",
			"--cap-add", "SETUID",
			"--cap-add", "SETGID",
			"--shm-size", "512m",
			"--mount", mount,
			"-e", "VNC_PW="+password,
			"-p", "127.0.0.1::"+strconv.Itoa(internalViewerPort),
			Image,
		)
	}
	return args, nil
}
