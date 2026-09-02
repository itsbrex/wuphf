package computer

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Resource limits every managed container runs with, and that the inspect
// below requires. A 16 GB Mac running three bots is the sizing case.
var (
	MemoryBytes int64 = 2 * 1024 * 1024 * 1024
	CPUs              = 2
	PidsLimit   int64 = 512
	ShmBytes    int64 = 512 * 1024 * 1024
)

// ContainerState is the coarse lifecycle position of a target's container.
type ContainerState string

const (
	ContainerMissing ContainerState = "missing"
	ContainerStopped ContainerState = "stopped"
	ContainerRunning ContainerState = "running"
)

// Status is the full inspection of one target. Every field the hardening
// check reads is reported so the panel can say exactly which rule failed.
type Status struct {
	Runtime   Runtime        `json:"runtime"`
	DaemonUp  bool           `json:"daemon_up"`
	Image     bool           `json:"image"`
	ImageID   string         `json:"image_id,omitempty"`
	ImageRef  string         `json:"image_ref"`
	Container ContainerState `json:"container"`
	// ImageMatches: the container was created from the current image.
	ImageMatches bool `json:"image_matches"`
	// Managed: the container carries every label we write, for this target.
	Managed bool `json:"managed"`
	// Network: "loopback" when the only published port is the viewer on
	// 127.0.0.1; "unsafe" otherwise.
	Network string `json:"network"`
	// Security: "hardened" when every resource limit and namespace rule
	// holds; "unsafe" otherwise.
	Security string `json:"security"`
	// Persistence: "durable" when the only mount is the target workspace.
	Persistence string `json:"persistence"`
	ViewerPort  int    `json:"viewer_port,omitempty"`
	// ViewerPassword is the VNC password baked into the container env. It is
	// never serialized; the broker embeds it in a loopback-only URL.
	ViewerPassword string `json:"-"`
	DesktopReady   bool   `json:"desktop_ready"`
	DesktopError   string `json:"desktop_error,omitempty"`
	// StartedAt is when the container last started, so a driver that is
	// still booting is reported as starting rather than broken.
	StartedAt time.Time `json:"started_at,omitempty"`
	// Problem is the first failing check in fix order, or empty when Ready.
	Problem string `json:"problem,omitempty"`
	Ready   bool   `json:"ready"`
}

// BootGrace is how long after a container start a silent driver still
// counts as booting rather than broken.
var BootGrace = 120 * time.Second

// Booting reports whether the container started recently enough that a
// missing driver is expected.
func (s Status) Booting() bool {
	return s.Container == ContainerRunning && !s.StartedAt.IsZero() && time.Since(s.StartedAt) < BootGrace
}

func (s Status) verified() bool {
	return s.Container == ContainerRunning && s.ImageMatches && s.Managed &&
		s.Network == "loopback" && s.Security == "hardened" && s.Persistence == "durable"
}

func statusProblem(s Status, rt RuntimeStatus) string {
	switch {
	case rt.Runtime == "":
		return rt.Problem
	case !rt.DaemonUp:
		return rt.Problem
	case !s.Image:
		return "Prepare the desktop image first"
	case s.Container == ContainerMissing:
		return "Create this bot's computer"
	case !s.ImageMatches:
		return "This computer was built from an older desktop image; replace it"
	case !s.Managed:
		return "A container with this name was not created by gawkbot; remove it"
	case s.Network == "unsafe":
		return "This computer publishes a port beyond loopback; replace it"
	case s.Security == "unsafe":
		return "This computer is missing its safety limits; replace it"
	case s.Persistence == "unsafe":
		return "This computer is missing its durable workspace; replace it"
	case s.Container == ContainerStopped:
		return "This computer is asleep"
	case s.DesktopError != "" && s.Booting():
		return "The computer started, but the desktop is not ready yet"
	case s.DesktopError != "":
		return "The desktop failed to start: " + s.DesktopError
	case !s.DesktopReady:
		return "The computer started, but the desktop is not ready yet"
	}
	return ""
}

// Inspector resolves target status against a runtime with a short cache for
// the expensive readiness probe, which the screen poller would otherwise run
// several times a second.
type Inspector struct {
	Run      Runner
	Platform string

	mu    sync.Mutex
	ready map[string]readyCache
}

type readyCache struct {
	status    Status
	expiresAt time.Time
}

// ReadyCacheTTL bounds how long a verified, desktop-ready status is trusted
// before the driver is probed again.
var ReadyCacheTTL = 10 * time.Second

// Inspect returns the target's status. rt must be a daemon-up runtime from
// DetectRuntime; callers pass the RuntimeStatus so the problem text can name
// a missing or stopped daemon.
func (in *Inspector) Inspect(ctx context.Context, rt RuntimeStatus, target Target) Status {
	s := Status{Runtime: rt.Runtime, DaemonUp: rt.DaemonUp, ImageRef: Image, Container: ContainerMissing,
		Network: "unknown", Security: "unknown", Persistence: "unknown"}
	if rt.Runtime == "" || !rt.DaemonUp {
		s.Problem = statusProblem(s, rt)
		return s
	}
	if cached, ok := in.cachedReady(target); ok {
		return cached
	}
	if stdout, _, err := in.Run(ctx, string(rt.Runtime), []string{"image", "inspect", Image}, DefaultTimeout); err == nil {
		labels, id := inspectedImage(stdout)
		s.Image = ImageLabelsMatch(labels)
		s.ImageID = id
	}
	if stdout, _, err := in.Run(ctx, string(rt.Runtime), []string{"inspect", target.ContainerName}, DefaultTimeout); err == nil {
		if rt.Runtime == RuntimeContainer {
			applyAppleInspect(&s, stdout, target, in.Platform)
		} else {
			applyDockerInspect(&s, stdout, target, in.Platform, rt.Runtime)
		}
	}
	if s.verified() {
		in.probeDesktop(ctx, rt.Runtime, target, &s)
	}
	s.Problem = statusProblem(s, rt)
	s.Ready = s.Problem == ""
	if s.Ready {
		in.remember(target, s)
	}
	return s
}

func (in *Inspector) cachedReady(target Target) (Status, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	c, ok := in.ready[target.Key]
	if !ok || time.Now().After(c.expiresAt) {
		return Status{}, false
	}
	return c.status, true
}

func (in *Inspector) remember(target Target, s Status) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.ready == nil {
		in.ready = map[string]readyCache{}
	}
	in.ready[target.Key] = readyCache{status: s, expiresAt: time.Now().Add(ReadyCacheTTL)}
}

// Forget drops the cached readiness for a target after any lifecycle change.
func (in *Inspector) Forget(target Target) {
	in.mu.Lock()
	defer in.mu.Unlock()
	delete(in.ready, target.Key)
}

func (in *Inspector) probeDesktop(ctx context.Context, rt Runtime, target Target, s *Status) {
	expected := "cua-driver " + CuaDriverVersion
	version, _, err := in.Run(ctx, string(rt), CuaExecArgs([]string{"--version"}, target.ContainerName, false), DefaultTimeout)
	if err == nil && strings.TrimSpace(version) != expected {
		err = &CommandError{Name: string(rt), Err: errString("expected " + expected + ", got " + strings.TrimSpace(version))}
	}
	if err == nil {
		_, _, err = in.Run(ctx, string(rt), CuaExecArgs([]string{"status", "--socket", CuaSocket}, target.ContainerName, false), DefaultTimeout)
	}
	if err == nil {
		var health string
		health, _, err = in.Run(ctx, string(rt), CuaExecArgs([]string{"call", "health_report", "{}", "--socket", CuaSocket}, target.ContainerName, false), 15*time.Second)
		if err == nil {
			var report struct {
				SchemaVersion string          `json:"schema_version"`
				Overall       string          `json:"overall"`
				Checks        json.RawMessage `json:"checks"`
			}
			if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(health)), &report); jsonErr != nil ||
				report.SchemaVersion != "1" || (report.Overall != "ok" && report.Overall != "degraded") {
				err = errString("Cua health report is " + firstNonEmpty(report.Overall, "invalid"))
			}
		}
	}
	if err == nil {
		s.DesktopReady = true
		return
	}
	// An empty log means XFCE and the driver are still starting; a real
	// startup failure should be actionable in the panel.
	s.DesktopError = truncate(err.Error(), 320)
	if tail, _, logErr := in.Run(ctx, string(rt), []string{"exec", target.ContainerName, "tail", "-n", "4", "/var/log/supervisor/cua-driver.error.log"}, 4*time.Second); logErr == nil {
		if t := strings.TrimSpace(strings.Join(strings.Fields(tail), " ")); t != "" {
			s.DesktopError = truncate(t, 320)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// CuaExecArgs is the one authoritative `exec … cua-driver` argv, shared by
// the inspector, the screenshot path, and the MCP bridge so identity, env,
// and telemetry knobs never drift.
func CuaExecArgs(args []string, container string, interactive bool) []string {
	out := []string{"exec"}
	if interactive {
		out = append(out, "-i")
	}
	out = append(out,
		"-u", "cua",
		"-e", "HOME=/home/cua",
		"-e", "DISPLAY="+Display,
		"-e", "CUA_DRIVER_INSTALL_CHANNEL=python_package",
		"-e", "CUA_DRIVER_RS_TELEMETRY_ENABLED=0",
		container, CuaExecutable,
	)
	return append(out, args...)
}

// ── inspect parsing ────────────────────────────────────────────────────

func inspectedImage(stdout string) (map[string]string, string) {
	var parsed []struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
		LowerID       string `json:"id"`
		Configuration struct {
			Labels     map[string]string `json:"labels"`
			Descriptor struct {
				Digest string `json:"digest"`
			} `json:"descriptor"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil || len(parsed) == 0 {
		return nil, ""
	}
	p := parsed[0]
	labels := p.Config.Labels
	if labels == nil {
		labels = p.Configuration.Labels
	}
	return labels, normalizeImageID(firstNonEmpty(p.ID, p.LowerID, p.Configuration.Descriptor.Digest))
}

func normalizeImageID(id string) string {
	return strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type dockerInspect struct {
	Config struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	HostConfig struct {
		hardeningConfig
		PortBindings map[string][]portBinding `json:"PortBindings"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Ports map[string][]portBinding `json:"Ports"`
	} `json:"NetworkSettings"`
	Mounts []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          *bool  `json:"RW"`
	} `json:"Mounts"`
	EffectiveCaps []string `json:"EffectiveCaps"`
	BoundingCaps  []string `json:"BoundingCaps"`
	State         struct {
		Running   bool   `json:"Running"`
		StartedAt string `json:"StartedAt"`
	} `json:"State"`
	Image string `json:"Image"`
}

// hardeningConfig is the Docker/Podman HostConfig surface the hardening
// check reads.
type hardeningConfig struct {
	Memory         int64    `json:"Memory"`
	MemorySwap     int64    `json:"MemorySwap"`
	NanoCpus       int64    `json:"NanoCpus"`
	PidsLimit      *int64   `json:"PidsLimit"`
	CapDrop        []string `json:"CapDrop"`
	CapAdd         []string `json:"CapAdd"`
	Privileged     bool     `json:"Privileged"`
	PidMode        string   `json:"PidMode"`
	IpcMode        string   `json:"IpcMode"`
	UTSMode        string   `json:"UTSMode"`
	ShmSize        int64    `json:"ShmSize"`
	Devices        []any    `json:"Devices"`
	DeviceRequests []any    `json:"DeviceRequests"`
	SecurityOpt    []string `json:"SecurityOpt"`
	UsernsMode     string   `json:"UsernsMode"`
	CgroupnsMode   string   `json:"CgroupnsMode"`
	OomKillDisable *bool    `json:"OomKillDisable"`
	AutoRemove     bool     `json:"AutoRemove"`
	RestartPolicy  struct {
		Name string `json:"Name"`
	} `json:"RestartPolicy"`
}

func applyDockerInspect(s *Status, stdout string, target Target, platform string, rt Runtime) {
	var parsed []dockerInspect
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil || len(parsed) == 0 {
		return
	}
	d := parsed[0]
	if d.State.Running {
		s.Container = ContainerRunning
		if started, err := time.Parse(time.RFC3339Nano, d.State.StartedAt); err == nil {
			s.StartedAt = started
		}
	} else {
		s.Container = ContainerStopped
	}
	if dockerPortsAreLocal(d.HostConfig.PortBindings) {
		s.Network = "loopback"
	} else {
		s.Network = "unsafe"
	}
	s.ViewerPort = dockerViewerPort(d.NetworkSettings.Ports)
	s.ImageMatches = d.Config.Image == Image && ImageLabelsMatch(d.Config.Labels) &&
		s.ImageID != "" && normalizeImageID(d.Image) == s.ImageID
	s.Managed = containerLabelsMatch(d.Config.Labels, target)
	durable := len(d.Mounts) == 1 && d.Mounts[0].Type == "bind" &&
		sameWorkspaceSource(d.Mounts[0].Source, platform, target.WorkspaceDir) &&
		d.Mounts[0].Destination == WorkspaceGuest && (d.Mounts[0].RW == nil || *d.Mounts[0].RW)
	if durable {
		s.Persistence = "durable"
	} else {
		s.Persistence = "unsafe"
	}
	hardened := false
	if rt == RuntimePodman {
		hardened = podmanSecurityIsHardened(d.HostConfig.hardeningConfig, d.EffectiveCaps, d.BoundingCaps)
	} else {
		hardened = DockerSecurityIsHardened(d.HostConfig.hardeningConfig, "")
	}
	if hardened {
		s.Security = "hardened"
	} else {
		s.Security = "unsafe"
	}
	s.ViewerPassword = viewerPassword(d.Config.Env)
}

func applyAppleInspect(s *Status, stdout string, target Target, platform string) {
	var parsed []struct {
		Configuration struct {
			Image     any `json:"image"`
			Resources struct {
				CPUs          int   `json:"cpus"`
				MemoryInBytes int64 `json:"memoryInBytes"`
			} `json:"resources"`
			PublishedPorts []struct {
				HostAddress   string `json:"hostAddress"`
				HostPort      int    `json:"hostPort"`
				ContainerPort int    `json:"containerPort"`
			} `json:"publishedPorts"`
			Environment []string          `json:"environment"`
			Labels      map[string]string `json:"labels"`
			Mounts      []struct {
				Source      string   `json:"source"`
				Destination string   `json:"destination"`
				Options     []string `json:"options"`
			} `json:"mounts"`
		} `json:"configuration"`
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil || len(parsed) == 0 {
		return
	}
	d := parsed[0]
	if d.Status.State == "running" {
		s.Container = ContainerRunning
	} else {
		s.Container = ContainerStopped
	}
	ports := d.Configuration.PublishedPorts
	if len(ports) == 1 && ports[0].ContainerPort == internalViewerPort && loopback(ports[0].HostAddress) {
		s.Network = "loopback"
		s.ViewerPort = ports[0].HostPort
	} else {
		s.Network = "unsafe"
	}
	imageRef := ""
	switch v := d.Configuration.Image.(type) {
	case string:
		imageRef = v
	case map[string]any:
		if ref, ok := v["reference"].(string); ok {
			imageRef = ref
		}
	}
	s.ImageMatches = imageRef == Image && s.ImageID != ""
	s.Managed = containerLabelsMatch(d.Configuration.Labels, target)
	m := d.Configuration.Mounts
	ro := false
	if len(m) == 1 {
		for _, o := range m[0].Options {
			if o == "ro" || o == "readonly" {
				ro = true
			}
		}
	}
	if len(m) == 1 && sameWorkspaceSource(m[0].Source, platform, target.WorkspaceDir) && m[0].Destination == WorkspaceGuest && !ro {
		s.Persistence = "durable"
	} else {
		s.Persistence = "unsafe"
	}
	if d.Configuration.Resources.MemoryInBytes >= MemoryBytes && d.Configuration.Resources.CPUs == CPUs {
		s.Security = "hardened"
	} else {
		s.Security = "unsafe"
	}
	s.ViewerPassword = viewerPassword(d.Configuration.Environment)
}

func containerLabelsMatch(labels map[string]string, target Target) bool {
	return ImageLabelsMatch(labels) && labels[WorkspaceLabel] == "1" && labels[TargetLabel] == target.Label
}

func loopback(addr string) bool {
	return addr == "127.0.0.1" || addr == "::1" || addr == "[::1]"
}

func viewerKey() string { return strconv.Itoa(internalViewerPort) + "/tcp" }

func dockerPortsAreLocal(bindings map[string][]portBinding) bool {
	viewer := bindings[viewerKey()]
	published := 0
	for _, entries := range bindings {
		for _, e := range entries {
			published++
			if !loopback(e.HostIP) {
				return false
			}
		}
	}
	return len(viewer) > 0 && published == len(viewer)
}

func dockerViewerPort(ports map[string][]portBinding) int {
	for _, e := range ports[viewerKey()] {
		if loopback(e.HostIP) {
			if p, err := strconv.Atoi(e.HostPort); err == nil && p > 0 && p <= 65535 {
				return p
			}
		}
	}
	return 0
}

func viewerPassword(env []string) string {
	for _, e := range env {
		if strings.HasPrefix(e, "VNC_PW=") {
			return strings.TrimPrefix(e, "VNC_PW=")
		}
	}
	return ""
}

func sameWorkspaceSource(source, platform, expected string) bool {
	if source == "" {
		return false
	}
	a, b := filepath.Clean(source), filepath.Clean(expected)
	if platform == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// DockerSecurityIsHardened is the hardening contract: exact resource limits,
// no privilege, no host namespaces or devices, no disabled security
// profiles. restartPolicy is "" for local computers (a desktop must not
// auto-resume unattended) and "unless-stopped" for a self-hosted server.
func DockerSecurityIsHardened(c hardeningConfig, restartPolicy string) bool {
	capDrop := lowerAll(c.CapDrop)
	capAdd := normalizeCaps(c.CapAdd)
	for _, opt := range c.SecurityOpt {
		l := strings.ToLower(opt)
		if strings.HasSuffix(l, "unconfined") || strings.HasSuffix(l, "disable") {
			return false
		}
	}
	restartOK := false
	switch restartPolicy {
	case "unless-stopped":
		restartOK = c.RestartPolicy.Name == "unless-stopped"
	default:
		restartOK = c.RestartPolicy.Name == "" || c.RestartPolicy.Name == "no"
	}
	return c.Memory == MemoryBytes &&
		c.MemorySwap == MemoryBytes &&
		c.NanoCpus == int64(CPUs)*1_000_000_000 &&
		c.PidsLimit != nil && *c.PidsLimit == PidsLimit &&
		contains(capDrop, "all") &&
		strings.Join(capAdd, ",") == "setgid,setuid" &&
		!c.Privileged &&
		c.PidMode == "" &&
		c.IpcMode == "private" &&
		c.UTSMode == "" &&
		c.ShmSize == ShmBytes &&
		len(c.Devices) == 0 &&
		len(c.DeviceRequests) == 0 &&
		c.UsernsMode == "" &&
		c.CgroupnsMode == "private" &&
		(c.OomKillDisable == nil || !*c.OomKillDisable) &&
		!c.AutoRemove &&
		restartOK
}

// podmanSecurityIsHardened validates Podman's authoritative effective and
// bounding capability sets, then normalizes the representation differences
// in its HostConfig serialization through the Docker contract.
func podmanSecurityIsHardened(c hardeningConfig, effective, bounding []string) bool {
	if strings.Join(normalizeCaps(effective), ",") != "setgid,setuid" {
		return false
	}
	if strings.Join(normalizeCaps(bounding), ",") != "setgid,setuid" {
		return false
	}
	n := c
	n.CapDrop = []string{"all"}
	n.CapAdd = effective
	if n.PidMode == "private" {
		n.PidMode = ""
	}
	if n.UTSMode == "private" {
		n.UTSMode = ""
	}
	if n.CgroupnsMode == "" {
		n.CgroupnsMode = "private"
	}
	return DockerSecurityIsHardened(n, "")
}

func lowerAll(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, strings.ToLower(s))
	}
	return out
}

func normalizeCaps(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, strings.TrimPrefix(strings.ToLower(s), "cap_"))
	}
	sortStrings(out)
	return out
}

func contains(items []string, needle string) bool {
	for _, s := range items {
		if s == needle {
			return true
		}
	}
	return false
}

func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
