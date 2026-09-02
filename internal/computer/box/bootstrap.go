package box

import (
	"encoding/base64"
	"strings"
)

// Everything the box needs installed is done by shell over the command API.
// The daemon stays loopback-only inside the VM; the provider never opens
// another inbound port for us.
const (
	RemoteCuaVersion    = "0.20.0"
	RemoteCuaExecutable = "/opt/gawkbot/cua-driver"
	RemoteCuaSocket     = "/opt/gawkbot/run/cua.sock"
	RemoteCuaSession    = "gawkbot"
	MaxCommandLength    = 4000
	PanelPath           = "/tmp/gawkbot-panel.jpg"
	ShotPath            = "/tmp/gawkbot-shot.jpg"
	ShotWidth           = 1280
	jpegQuality         = 70
)

type wheel struct{ URL, SHA256 string }

var remoteWheels = map[string]wheel{
	"x86_64": {
		URL:    "https://files.pythonhosted.org/packages/fa/d7/a43008a328a40c85e7bc706fc20235b9abedc75e28b413817655153157ff/cua_driver-0.20.0-py3-none-manylinux_2_31_x86_64.whl",
		SHA256: "f60c35696a37f37ac954935e478ae4754f220856d022036625c9400d72185961",
	},
	"aarch64": {
		URL:    "https://files.pythonhosted.org/packages/94/9d/1c1838b69067e83266c3d2aae02d74eef353a43dc8644884ccf03fe7f933/cua_driver-0.20.0-py3-none-manylinux_2_31_aarch64.whl",
		SHA256: "48833bc5e4c60e701fc9eefb57dbac36ec77ef3990f816fbbe85b4e954af2c77",
	},
}

// ShellQuote single-quotes a value for POSIX sh.
func ShellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// IsolatedCommand runs a user- or model-supplied command without inheriting
// provider or account credentials from an older box image. Shared by the
// bot tool and the owner's console so the two boundaries cannot drift.
func IsolatedCommand(command string) string {
	return strings.Join([]string{
		"exec env -i",
		`HOME="$HOME"`,
		`USER="${USER:-$(id -un)}"`,
		`LOGNAME="${LOGNAME:-${USER:-$(id -un)}}"`,
		`PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"`,
		`DISPLAY="${DISPLAY:-:0}"`,
		`XAUTHORITY="${XAUTHORITY:-$HOME/.Xauthority}"`,
		`XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"`,
		`DBUS_SESSION_BUS_ADDRESS="${DBUS_SESSION_BUS_ADDRESS:-}"`,
		"/bin/bash -c",
		ShellQuote(command),
	}, " ")
}

// EnsureCuaCommand starts the already-installed daemon after a resume. It
// is cheap when healthy and installs nothing on the hot path.
func EnsureCuaCommand() string {
	dir := RemoteCuaSocket[:strings.LastIndex(RemoteCuaSocket, "/")]
	return strings.Join([]string{
		"if [ -x " + RemoteCuaExecutable + " ]; then",
		"  mkdir -p " + dir,
		"  if ! " + RemoteCuaExecutable + " status --socket " + RemoteCuaSocket + " >/dev/null 2>&1; then",
		"    rm -f " + RemoteCuaSocket,
		`    display=${DISPLAY:-$(find /tmp/.X11-unix -maxdepth 1 -name "X*" -printf ":%f\n" 2>/dev/null | sed "s/:X/:/" | head -1)}`,
		"    display=${display:-:0}",
		`    nohup env HOME="$HOME" DISPLAY="$display" CUA_DRIVER_INSTALL_CHANNEL=python_package CUA_DRIVER_RS_TELEMETRY_ENABLED=0 ` + RemoteCuaExecutable + " serve --socket " + RemoteCuaSocket + " --permission-mode standard > /tmp/gawkbot-cua-driver.log 2>&1 &",
		"    for i in 1 2 3 4 5 6 7 8 9 10; do " + RemoteCuaExecutable + " status --socket " + RemoteCuaSocket + " >/dev/null 2>&1 && break; sleep 0.2; done",
		"  fi",
		"fi",
	}, "\n")
}

// BootstrapCommand is the idempotent first-run setup: X11 tooling for the
// degraded path, the exact driver wheel verified by SHA-256 and installed
// asynchronously so provisioning does not block on a multi-minute install,
// and a tmux session with a banner. The display name is untrusted and is
// base64-encoded before it touches the nested shell.
func BootstrapCommand(botName string) string {
	x, a := remoteWheels["x86_64"], remoteWheels["aarch64"]
	installer := strings.Join([]string{
		"set -eu",
		"trap 'rm -f /tmp/gawkbot-cua-installing' EXIT",
		"sudo mkdir -p /opt/gawkbot/run",
		`sudo chown -R "$(id -u):$(id -g)" /opt/gawkbot`,
		`arch="$(uname -m)"`,
		`case "$arch" in x86_64) url=` + ShellQuote(x.URL) + `; sha=` + x.SHA256 + ` ;; aarch64|arm64) url=` + ShellQuote(a.URL) + `; sha=` + a.SHA256 + ` ;; *) echo "unsupported architecture: $arch" >&2; exit 1 ;; esac`,
		`wheel="/tmp/cua-driver-${sha}.whl"`,
		`curl -fsSL "$url" -o "$wheel"`,
		`echo "$sha  $wheel" | sha256sum -c -`,
		`python3 - "$wheel" <<'PY'
import os, sys, zipfile
wheel = sys.argv[1]
with zipfile.ZipFile(wheel) as archive:
    names = [name for name in archive.namelist() if name == "cua_driver/bin/cua-driver" or name.endswith("/cua_driver/bin/cua-driver")]
    if len(names) != 1:
        raise SystemExit("cua-driver executable missing from wheel")
    with archive.open(names[0]) as source, open("` + RemoteCuaExecutable + `", "wb") as target:
        target.write(source.read())
os.chmod("` + RemoteCuaExecutable + `", 0o755)
PY`,
		`test "$(` + RemoteCuaExecutable + ` --version)" = "cua-driver ` + RemoteCuaVersion + `"`,
		"touch /opt/gawkbot/cua-" + RemoteCuaVersion + "-ready",
		`rm -f "$wheel"`,
	}, "\n")
	banner := base64.StdEncoding.EncodeToString([]byte("  ▦ " + botName + "'s computer — gawkbot"))
	tmuxCommand := strings.Join([]string{"echo", "printf %s " + ShellQuote(banner) + " | base64 -d", "echo", "exec bash -i"}, "; ")
	return strings.Join([]string{
		"if ! command -v xdotool >/dev/null || ! command -v convert >/dev/null || ! command -v curl >/dev/null || ! command -v python3 >/dev/null; then sudo apt-get update -qq || true; sudo apt-get install -y -qq ca-certificates curl python3 xclip wmctrl xdotool imagemagick scrot >/dev/null 2>&1 || true; fi",
		"sudo mkdir -p /opt/gawkbot/run",
		"[ -f /opt/gawkbot/cua-" + RemoteCuaVersion + "-ready ] || [ -f /tmp/gawkbot-cua-installing ] || { touch /tmp/gawkbot-cua-installing; nohup bash -c " + ShellQuote(installer) + " > /tmp/gawkbot-cua-install.log 2>&1 & }",
		EnsureCuaCommand(),
		"tmux has-session -t work 2>/dev/null || tmux new-session -d -s work " + ShellQuote(tmuxCommand),
		"echo bootstrapped",
	}, "\n")
}

// PanelScreenshotCommand captures the whole screen to a JPEG for the panel,
// downscaling only when the display is wider than the panel.
func PanelScreenshotCommand() string {
	return strings.Join([]string{
		"export DISPLAY=${DISPLAY:-:0}",
		"f=" + PanelPath,
		`w=$(xdotool getdisplaygeometry 2>/dev/null | cut -d" " -f1)`,
		`case "$w" in ""|*[!0-9]*) w=0;; esac`,
		`scrot -o -q 70 "$f" 2>/dev/null || import -window root -quality 70 "$f" 2>/dev/null || ffmpeg -y -f x11grab -i "$DISPLAY" -frames:v 1 -q:v 7 "$f" >/dev/null 2>&1`,
		`if [ "$w" -gt 1024 ] 2>/dev/null && command -v convert >/dev/null 2>&1; then convert "$f" -thumbnail 1024x -quality 70 "$f" 2>/dev/null || true; fi`,
		`test -s "$f" && echo captured`,
	}, "; ")
}

// QuiesceBrowserCommand asks Chrome to exit cleanly before an archive so the
// profile is not restored crash-marked next wake.
func QuiesceBrowserCommand() string {
	return strings.Join([]string{
		`for name in chrome google-chrome chromium chromium-browser; do pid=$(pgrep -o -x "$name" 2>/dev/null || true); [ -z "$pid" ] || kill -TERM "$pid" 2>/dev/null || true; done`,
		`for i in 1 2 3 4 5 6 7 8; do if ! pgrep -x chrome >/dev/null 2>&1 && ! pgrep -x google-chrome >/dev/null 2>&1 && ! pgrep -x chromium >/dev/null 2>&1 && ! pgrep -x chromium-browser >/dev/null 2>&1; then break; fi; sleep 0.25; done`,
	}, "; ")
}
