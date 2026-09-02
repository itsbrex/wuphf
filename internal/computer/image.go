package computer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Every version below is pinned on purpose. The base image is addressed by
// digest, the driver wheel by exact version and SHA-256, and the derived
// image carries labels for all three so an inspect can refuse a container
// built from anything else. Bump ImageLayerVersion whenever the Dockerfile
// changes without a driver or base change.
const (
	CuaDriverVersion    = "0.20.0"
	BaseImageRepository = "docker.io/trycua/xfce-cua"
	// Official multi-architecture Cua XFCE 0.1.0 manifest (amd64 + arm64).
	BaseImageDigest = "sha256:274eb636f5cf3fc58f705916ee72b7a701270b3877369d08533a385c5325be9b"
	BaseImage       = BaseImageRepository + "@" + BaseImageDigest

	// The explicit localhost registry keeps Podman from resolving the tag
	// on Docker Hub. Labels, not the tag, are the compatibility check.
	ImageRepository   = "localhost/gawkbot/cua-computer"
	ImageLayerVersion = "1"
	Image             = ImageRepository + ":driver-" + CuaDriverVersion + "-v" + ImageLayerVersion

	ManagedLabel   = "bot.gawk.computer"
	DriverLabel    = "bot.gawk.cua-driver"
	BaseImageLabel = "bot.gawk.cua-base"
	LayerLabel     = "bot.gawk.image-layer"
	WorkspaceLabel = "bot.gawk.workspace"
	TargetLabel    = "bot.gawk.target"

	WorkspaceGuest = "/home/cua/workspace"
	Display        = ":1"
	CuaSocket      = "/run/user/1000/gawkbot-cua.sock"
	CuaExecutable  = "/usr/local/libexec/gawkbot/cua-driver"

	internalViewerPort = 6901
)

type wheel struct {
	URL    string
	SHA256 string
}

var linuxWheels = map[string]wheel{
	"x86_64": {
		URL:    "https://files.pythonhosted.org/packages/fa/d7/a43008a328a40c85e7bc706fc20235b9abedc75e28b413817655153157ff/cua_driver-0.20.0-py3-none-manylinux_2_31_x86_64.whl",
		SHA256: "f60c35696a37f37ac954935e478ae4754f220856d022036625c9400d72185961",
	},
	"aarch64": {
		URL:    "https://files.pythonhosted.org/packages/94/9d/1c1838b69067e83266c3d2aae02d74eef353a43dc8644884ccf03fe7f933/cua_driver-0.20.0-py3-none-manylinux_2_31_aarch64.whl",
		SHA256: "48833bc5e4c60e701fc9eefb57dbac36ec77ef3990f816fbbe85b4e954af2c77",
	},
}

// ManagedImageDockerfile is the reproducible derivative of Cua's desktop:
// the exact driver wheel for the build architecture, verified by SHA-256,
// installed under supervisord so it starts, restarts, and stops with the
// desktop. The first RUN also rejects a defective base layer (some upstream
// ARM64 layers shipped zero-byte OpenSSL libraries) at the step that can
// name the problem.
func ManagedImageDockerfile() string {
	x := linuxWheels["x86_64"]
	a := linuxWheels["aarch64"]
	var sb strings.Builder
	fmt.Fprintf(&sb, "FROM %s\n", BaseImage)
	sb.WriteString("USER root\n")
	sb.WriteString("RUN set -eux; \\\n")
	sb.WriteString("    arch=\"$(uname -m)\"; \\\n")
	sb.WriteString("    case \"$arch\" in \\\n")
	fmt.Fprintf(&sb, "      x86_64) wheel_url='%s'; wheel_sha='%s'; wheel_path='/tmp/cua_driver-%s-py3-none-manylinux_2_31_x86_64.whl'; lib_triplet='x86_64-linux-gnu' ;; \\\n", x.URL, x.SHA256, CuaDriverVersion)
	fmt.Fprintf(&sb, "      aarch64|arm64) wheel_url='%s'; wheel_sha='%s'; wheel_path='/tmp/cua_driver-%s-py3-none-manylinux_2_31_aarch64.whl'; lib_triplet='aarch64-linux-gnu' ;; \\\n", a.URL, a.SHA256, CuaDriverVersion)
	sb.WriteString("      *) echo \"unsupported architecture: $arch\" >&2; exit 1 ;; \\\n")
	sb.WriteString("    esac; \\\n")
	sb.WriteString("    for ssl_lib in \"/lib/$lib_triplet/libssl.so.3\" \"/lib/$lib_triplet/libcrypto.so.3\"; do \\\n")
	sb.WriteString("      if [ -e \"$ssl_lib\" ] && [ ! -s \"$ssl_lib\" ]; then \\\n")
	sb.WriteString("        echo \"pinned base image is defective on $arch: $ssl_lib is zero bytes\" >&2; exit 1; \\\n")
	sb.WriteString("      fi; \\\n")
	sb.WriteString("    done; \\\n")
	sb.WriteString("    curl -fsSL \"$wheel_url\" -o \"$wheel_path\"; \\\n")
	sb.WriteString("    echo \"$wheel_sha  $wheel_path\" | sha256sum -c -; \\\n")
	sb.WriteString("    /opt/venv/bin/python -m pip install --no-cache-dir --force-reinstall --no-deps \"$wheel_path\"; \\\n")
	sb.WriteString("    rm -f \"$wheel_path\"; \\\n")
	sb.WriteString("    driver_bin=\"$(find /opt/venv/lib -path '*/cua_driver/bin/cua-driver' -type f -print -quit)\"; \\\n")
	sb.WriteString("    test -n \"$driver_bin\"; \\\n")
	fmt.Fprintf(&sb, "    install -D -m 0755 \"$driver_bin\" %s; \\\n", CuaExecutable)
	fmt.Fprintf(&sb, "    install -d -o cua -g cua -m 0700 %s; \\\n", WorkspaceGuest)
	fmt.Fprintf(&sb, "    test \"$(%s --version)\" = \"cua-driver %s\"\n", CuaExecutable, CuaDriverVersion)
	// Workspace preparation: browser profiles live in the durable workspace
	// so logins survive a container recreate.
	sb.WriteString("RUN printf '%s\\n' \\\n")
	for _, line := range []string{
		"#!/bin/sh",
		"set -eu",
		"workspace=" + WorkspaceGuest,
		"profiles=\"$workspace/.browser-profiles\"",
		"mkdir -p \"$profiles/google-chrome\" \"$profiles/chromium\" \"$HOME/.config\"",
		"chmod 0700 \"$workspace\" \"$profiles\" \"$profiles/google-chrome\" \"$profiles/chromium\" 2>/dev/null || true",
		"migrate_profile() {",
		"  name=\"$1\"",
		"  source=\"$HOME/.config/$name\"",
		"  target=\"$profiles/$name\"",
		"  if [ -d \"$source\" ] && [ ! -L \"$source\" ] && [ -z \"$(find \"$target\" -mindepth 1 -print -quit)\" ]; then",
		"    cp -a \"$source\"/. \"$target\"/",
		"  fi",
		"  rm -rf \"$source\"",
		"  ln -s \"$target\" \"$source\"",
		"}",
		"migrate_profile google-chrome",
		"migrate_profile chromium",
		"find \"$profiles\" \\( -name SingletonLock -o -name SingletonSocket -o -name SingletonCookie -o -name .parentlock \\) -delete",
	} {
		fmt.Fprintf(&sb, "      '%s' \\\n", line)
	}
	sb.WriteString("      > /usr/local/bin/prepare-gawkbot-workspace.sh \\\n")
	sb.WriteString("    && chmod 0755 /usr/local/bin/prepare-gawkbot-workspace.sh\n")
	// Driver start script: wait for X, then serve on a private socket.
	sb.WriteString("RUN printf '%s\\n' \\\n")
	for _, line := range []string{
		"#!/bin/sh",
		"/usr/local/bin/prepare-gawkbot-workspace.sh",
		"attempt=0",
		"until DISPLAY=" + Display + " xset q >/dev/null 2>&1; do",
		"  attempt=$((attempt + 1))",
		"  if [ \"$attempt\" -ge 45 ]; then echo \"X display did not become ready within 45 seconds\" >&2; exit 1; fi",
		"  sleep 1",
		"done",
		"exec env CUA_DRIVER_INSTALL_CHANNEL=python_package CUA_DRIVER_RS_TELEMETRY_ENABLED=0 " + CuaExecutable + " serve --socket " + CuaSocket + " --permission-mode standard",
	} {
		fmt.Fprintf(&sb, "      '%s' \\\n", line)
	}
	sb.WriteString("      > /usr/local/bin/start-gawkbot-cua-driver.sh \\\n")
	sb.WriteString("    && chmod 0755 /usr/local/bin/start-gawkbot-cua-driver.sh\n")
	sb.WriteString("RUN printf '%s\\n' \\\n")
	for _, line := range []string{
		"",
		"[program:gawkbot-cua-driver]",
		"command=/usr/local/bin/start-gawkbot-cua-driver.sh",
		"user=cua",
		"environment=HOME=\"/home/cua\",USER=\"cua\",DISPLAY=\"" + Display + "\"",
		"autorestart=true",
		"startsecs=2",
		"stdout_logfile=/var/log/supervisor/cua-driver.log",
		"stderr_logfile=/var/log/supervisor/cua-driver.error.log",
		"priority=30",
	} {
		fmt.Fprintf(&sb, "      '%s' \\\n", line)
	}
	sb.WriteString("      >> /etc/supervisor/supervisord.conf\n")
	fmt.Fprintf(&sb, "LABEL %s=\"1\" \\\n      %s=\"%s\" \\\n      %s=\"%s\" \\\n      %s=\"%s\"\n",
		ManagedLabel, DriverLabel, CuaDriverVersion, BaseImageLabel, BaseImageDigest, LayerLabel, ImageLayerVersion)
	return sb.String()
}

// ImageLabelsMatch is the one compatibility rule shared by the local and
// self-hosted paths.
func ImageLabelsMatch(labels map[string]string) bool {
	return labels[ManagedLabel] == "1" &&
		labels[DriverLabel] == CuaDriverVersion &&
		labels[BaseImageLabel] == BaseImageDigest &&
		labels[LayerLabel] == ImageLayerVersion
}

// PrepareImage pulls the pinned base and builds the managed derivative,
// streaming progress lines. It is idempotent: a second call with the image
// already present is a fast no-op at the daemon.
func PrepareImage(ctx context.Context, rt Runtime, stream StreamRunner, onLine func(string)) error {
	report := func(s string) {
		if onLine != nil {
			onLine(s)
		}
	}
	report("Pulling the pinned Cua desktop image (about 1.3 GB, first time only)…")
	if err := stream(ctx, string(rt), []string{"pull", BaseImage}, onLine); err != nil {
		return fmt.Errorf("pull base image: %w", err)
	}
	dir, err := os.MkdirTemp("", "gawkbot-cua-image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(ManagedImageDockerfile()), 0o600); err != nil {
		return err
	}
	report("Building the gawkbot desktop layer with Cua Driver " + CuaDriverVersion + "…")
	args := []string{"build", "-t", Image}
	if rt != RuntimeContainer {
		args = append(args, "--progress=plain")
	}
	args = append(args, dir)
	if err := stream(ctx, string(rt), args, onLine); err != nil {
		return fmt.Errorf("build desktop image: %w", err)
	}
	report("Desktop image ready.")
	return nil
}
