package team

// broker_box_signin.go: "Sign in to ascii.dev" without copying a key.
//
// The Box CLI is machine-friendly: `box login --json` prints a login_url
// event and waits for the browser to finish, `box status --json` says whether
// this machine is signed in, and `box api-key create <name> --json` mints a
// key whose secret is printed exactly once. So the broker installs the CLI
// (a single static binary from ascii.dev, no shell script executed), starts
// the login, hands the URL to the web UI, waits for the session, mints a key
// named gawkbot, verifies it with the provider, and stores it through the
// same config path a pasted key uses. Mirrors broker_composio_signin.go.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nex-crm/wuphf/internal/computer/box"
	"github.com/nex-crm/wuphf/internal/config"
)

const (
	boxSigninStatusIdle          = "idle"
	boxSigninStatusInstalling    = "installing"
	boxSigninStatusCLIMissing    = "cli_missing"
	boxSigninStatusAwaitingLogin = "awaiting_login"
	boxSigninStatusProvisioning  = "provisioning"
	boxSigninStatusDone          = "done"
	boxSigninStatusError         = "error"

	boxCLIDownloadBase = "https://ascii.dev/api/box/cli/download"
	boxCLIChannel      = "ascii-prod"
	boxInstallCommand  = "curl -fsSL https://ascii.dev/api/box/install | sh"
	boxKeyName         = "gawkbot"
)

var (
	boxInstallTimeout = 3 * time.Minute
	boxLoginWindow    = 15 * time.Minute
	boxProbeTimeout   = 20 * time.Second
	boxMintTimeout    = 60 * time.Second
	// boxInstaller downloads the CLI; a package var so tests substitute a fake.
	boxInstaller = defaultBoxInstaller
)

type boxSigninState struct {
	Status         string `json:"status"`
	AuthURL        string `json:"auth_url,omitempty"`
	InstallCommand string `json:"install_command,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

type boxSigninFlow struct {
	mu       sync.Mutex
	state    boxSigninState
	deadline time.Time
}

func boxInstallDir() string {
	if dir := strings.TrimSpace(os.Getenv("WUPHF_BOX_CLI_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ascii", "bin")
}

// boxCLIBinary resolves the `box` CLI: an explicit override, PATH, then the
// installer's ~/.ascii/bin, which the running broker's PATH never learns
// about because the installer only edits shell profiles.
func boxCLIBinary() (string, bool) {
	if p := strings.TrimSpace(os.Getenv("WUPHF_BOX_CLI")); p != "" {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, true
		}
		return "", false
	}
	if p, err := exec.LookPath("box"); err == nil {
		return p, true
	}
	dir := boxInstallDir()
	if dir == "" {
		return "", false
	}
	candidate := filepath.Join(dir, "box")
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
		return candidate, true
	}
	return "", false
}

func boxCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	bin, ok := boxCLIBinary()
	if !ok {
		return nil, errors.New("the Box CLI is not installed")
	}
	full := append([]string{}, args...)
	full = append(full, "--json", "--no-update")
	cmd := exec.CommandContext(ctx, bin, full...)
	cmd.Env = composioCommandEnv(filepath.Dir(bin))
	if api := strings.TrimSpace(os.Getenv("WUPHF_BOX_API_URL")); api != "" {
		cmd.Env = append(cmd.Env, "BOX_API_URL="+api)
	}
	return cmd, nil
}

// defaultBoxInstaller downloads the static CLI binary for this platform into
// ~/.ascii/bin. It deliberately does not pipe the vendor's install script
// into a shell: the script's only other job is an interactive onboard, which
// the broker runs itself, and running remote shell is not a thing gawkbot
// does on a person's machine.
func defaultBoxInstaller(ctx context.Context) error {
	var platform string
	switch runtime.GOOS {
	case "darwin", "linux":
		platform = runtime.GOOS
	default:
		return fmt.Errorf("the Box CLI has no build for %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64":
		platform += "-x64"
	case "arm64":
		platform += "-arm64"
	default:
		return fmt.Errorf("the Box CLI has no build for %s", runtime.GOARCH)
	}
	dir := boxInstallDir()
	if dir == "" {
		return errors.New("no home directory to install into")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, boxCLIDownloadBase+"?platform="+platform+"&channel="+boxCLIChannel, nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("ascii.dev answered %d for the CLI download", res.StatusCode)
	}
	tmp, err := os.CreateTemp(dir, "box.*.tmp")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, io.LimitReader(res.Body, 512<<20)); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "box"))
}

// boxCLILoggedIn asks the CLI whether this machine holds a session.
func boxCLILoggedIn(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, boxProbeTimeout)
	defer cancel()
	cmd, err := boxCommand(ctx, "status")
	if err != nil {
		return false
	}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	var status struct {
		Account struct {
			LoginState string `json:"loginState"`
			Status     string `json:"status"`
		} `json:"account"`
	}
	if json.Unmarshal(out, &status) != nil {
		return false
	}
	state := strings.ToLower(strings.TrimSpace(firstNonEmpty(status.Account.LoginState, status.Account.Status)))
	return state != "" && state != "signed out"
}

// ── routes ──────────────────────────────────────────────────────────────

func (b *Broker) handleBoxSigninStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	flow := &b.boxSignin
	flow.mu.Lock()
	switch flow.state.Status {
	case boxSigninStatusInstalling, boxSigninStatusAwaitingLogin, boxSigninStatusProvisioning:
		state := flow.state
		flow.mu.Unlock()
		writeJSON(w, http.StatusOK, state)
		return
	}
	if config.ResolveBoxAPIKey() != "" {
		flow.state = boxSigninState{Status: boxSigninStatusDone}
		state := flow.state
		flow.mu.Unlock()
		writeJSON(w, http.StatusOK, state)
		return
	}
	if _, ok := boxCLIBinary(); !ok {
		flow.state = boxSigninState{Status: boxSigninStatusInstalling, InstallCommand: boxInstallCommand}
		flow.deadline = time.Now().Add(boxInstallTimeout + 30*time.Second)
		state := flow.state
		flow.mu.Unlock()
		go b.boxSigninAutoInstall()
		writeJSON(w, http.StatusOK, state)
		return
	}
	flow.state = boxSigninState{Status: boxSigninStatusInstalling}
	state := flow.state
	flow.mu.Unlock()
	go b.boxSigninBeginLogin()
	writeJSON(w, http.StatusOK, state)
}

func (b *Broker) handleBoxSigninStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	flow := &b.boxSignin
	flow.mu.Lock()
	state := flow.state
	deadline := flow.deadline
	flow.mu.Unlock()
	if state.Status == "" {
		state.Status = boxSigninStatusIdle
		if config.ResolveBoxAPIKey() != "" {
			state.Status = boxSigninStatusDone
		}
	}
	if state.Status == boxSigninStatusInstalling && !deadline.IsZero() && time.Now().After(deadline) {
		flow.mu.Lock()
		if flow.state.Status == boxSigninStatusInstalling {
			flow.state = boxSigninState{Status: boxSigninStatusCLIMissing, InstallCommand: boxInstallCommand,
				Reason: "the Box CLI install is taking too long — run the install command shown, then try again"}
		}
		state = flow.state
		flow.mu.Unlock()
	}
	if state.Status == boxSigninStatusAwaitingLogin {
		if b.boxSigninAdvanceIfLoggedIn(r.Context()) {
			flow.mu.Lock()
			state = flow.state
			flow.mu.Unlock()
		} else if !deadline.IsZero() && time.Now().After(deadline) {
			flow.mu.Lock()
			if flow.state.Status == boxSigninStatusAwaitingLogin {
				flow.state = boxSigninState{Status: boxSigninStatusError, Reason: "sign-in timed out — try again, or paste a key from ascii.dev"}
			}
			state = flow.state
			flow.mu.Unlock()
		}
	}
	writeJSON(w, http.StatusOK, state)
}

// ── flow ────────────────────────────────────────────────────────────────

func (b *Broker) boxSigninAutoInstall() {
	ctx, cancel := context.WithTimeout(context.Background(), boxInstallTimeout)
	defer cancel()
	runErr := boxInstaller(ctx)
	if _, ok := boxCLIBinary(); !ok {
		flow := &b.boxSignin
		flow.mu.Lock()
		if flow.state.Status == boxSigninStatusInstalling {
			reason := "could not install the Box CLI automatically — run the install command shown, then try again"
			if runErr != nil {
				reason = "could not install the Box CLI: " + runErr.Error()
			}
			flow.state = boxSigninState{Status: boxSigninStatusCLIMissing, InstallCommand: boxInstallCommand, Reason: reason}
		}
		flow.mu.Unlock()
		return
	}
	b.boxSigninBeginLogin()
}

// boxSigninBeginLogin runs `box login --json`, surfaces the login URL, then
// waits for the CLI to report the finished session and provisions a key.
func (b *Broker) boxSigninBeginLogin() {
	flow := &b.boxSignin
	flow.mu.Lock()
	if flow.state.Status != boxSigninStatusInstalling {
		flow.mu.Unlock()
		return
	}
	if boxCLILoggedIn(context.Background()) {
		flow.state = boxSigninState{Status: boxSigninStatusProvisioning}
		flow.mu.Unlock()
		go b.boxSigninProvision()
		return
	}
	flow.state = boxSigninState{Status: boxSigninStatusAwaitingLogin}
	flow.deadline = time.Now().Add(boxLoginWindow)
	flow.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), boxLoginWindow)
	defer cancel()
	cmd, err := boxCommand(ctx, "login")
	if err != nil {
		b.boxSigninFail("the Box CLI could not start: " + err.Error())
		return
	}
	cmd.Stdin = nil
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		b.boxSigninFail("the Box CLI could not start: " + err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		b.boxSigninFail("the Box CLI could not start: " + err.Error())
		return
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		var evt struct {
			Event string `json:"event"`
			URL   string `json:"url"`
		}
		if json.Unmarshal(scanner.Bytes(), &evt) != nil {
			continue
		}
		if evt.Event == "login_url" && evt.URL != "" {
			flow.mu.Lock()
			if flow.state.Status == boxSigninStatusAwaitingLogin {
				flow.state.AuthURL = evt.URL
			}
			flow.mu.Unlock()
		}
	}
	// Output deliberately not logged: it may carry a session token.
	_ = cmd.Wait()
	b.boxSigninAdvanceIfLoggedIn(context.Background())
}

func (b *Broker) boxSigninAdvanceIfLoggedIn(ctx context.Context) bool {
	if !boxCLILoggedIn(ctx) {
		return false
	}
	flow := &b.boxSignin
	flow.mu.Lock()
	if flow.state.Status != boxSigninStatusAwaitingLogin {
		flow.mu.Unlock()
		return false
	}
	flow.state = boxSigninState{Status: boxSigninStatusProvisioning}
	flow.mu.Unlock()
	go b.boxSigninProvision()
	return true
}

func (b *Broker) boxSigninFail(reason string) {
	flow := &b.boxSignin
	flow.mu.Lock()
	flow.state = boxSigninState{Status: boxSigninStatusError, Reason: reason}
	flow.mu.Unlock()
}

// boxSigninProvision mints a key named gawkbot, verifies it, and stores it.
func (b *Broker) boxSigninProvision() {
	ctx, cancel := context.WithTimeout(context.Background(), boxMintTimeout)
	defer cancel()
	secret, err := boxMintAPIKey(ctx)
	if err == nil {
		err = box.VerifyToken(ctx, boxAPIBase(), secret)
	}
	if err == nil {
		err = b.storeBoxAPIKey(secret)
		if err != nil {
			log.Printf("box signin: %v", err)
			err = errors.New("could not save the Box key to config — check the broker logs")
		}
	}
	flow := &b.boxSignin
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if err != nil {
		flow.state = boxSigninState{Status: boxSigninStatusError, Reason: err.Error()}
		return
	}
	flow.state = boxSigninState{Status: boxSigninStatusDone}
}

func boxMintAPIKey(ctx context.Context) (string, error) {
	cmd, err := boxCommand(ctx, "api-key", "create", boxKeyName)
	if err != nil {
		return "", err
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("box api-key create failed: %w", err)
	}
	// The CLI may emit JSONL; the secret is on whichever object carries it.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var obj map[string]any
		if json.Unmarshal([]byte(line), &obj) != nil {
			continue
		}
		for _, field := range []string{"secret", "key", "token", "apiKey", "api_key"} {
			if v, ok := obj[field].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v), nil
			}
		}
		if nested, ok := obj["apiKey"].(map[string]any); ok {
			if v, ok := nested["secret"].(string); ok && v != "" {
				return v, nil
			}
		}
	}
	return "", errors.New("the Box CLI did not print a key secret")
}

func (b *Broker) storeBoxAPIKey(key string) error {
	b.configMu.Lock()
	defer b.configMu.Unlock()
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}
	cfg.BoxAPIKey = strings.TrimSpace(key)
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("config save failed: %w", err)
	}
	return nil
}
