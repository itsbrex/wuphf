package computer

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The live desktop is noVNC served by the container on a loopback port.
// The browser cannot reach that port through the web UI's origin, and an
// <iframe> cannot carry a bearer token, so the web-UI port hosts a reverse
// proxy under a signed, expiring capability path:
//
//	/computer-view/{slug}/{policy}/{expires}.{signature}/vnc.html?…
//
// The signature is an HMAC over slug, policy, and expiry with a per-boot
// secret. Policy is "view" or "control"; the noVNC page is asked for
// view_only accordingly, and the policy is also what a future server-side
// input filter would key on. The VNC password rides only in the URL
// fragment, which never leaves the browser.

// ViewerPathPrefix is the web-UI mount for the proxy.
const ViewerPathPrefix = "/computer-view/"

// ViewerTTL is how long a capability path stays valid.
var ViewerTTL = time.Hour

// ViewerSigner mints and checks capability tokens.
type ViewerSigner struct {
	secret []byte
}

// NewViewerSigner returns a signer with a fresh per-boot secret.
func NewViewerSigner() *ViewerSigner {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		secret = []byte(time.Now().String())
	}
	return &ViewerSigner{secret: secret}
}

// NewViewerSignerWithSecret is for tests.
func NewViewerSignerWithSecret(secret []byte) *ViewerSigner {
	return &ViewerSigner{secret: append([]byte(nil), secret...)}
}

func (v *ViewerSigner) sign(slug, policy string, expires int64) string {
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(slug + "\x00" + policy + "\x00" + strconv.FormatInt(expires, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ViewerWindow is how long a minted token stays byte-identical. The panel
// re-reads status every few seconds and swaps the iframe src whenever the
// URL changes, and every swap is a noVNC reconnect; pinning the expiry to
// a window keeps the URL stable between renewals while every token still
// carries at least half of ViewerTTL.
var ViewerWindow = ViewerTTL / 2

// Token mints "<expires>.<signature>" for a slug and policy.
func (v *ViewerSigner) Token(slug, policy string, now time.Time) string {
	expires := now.Truncate(ViewerWindow).Add(ViewerTTL).Unix()
	return strconv.FormatInt(expires, 10) + "." + v.sign(slug, policy, expires)
}

// Verify checks a token for a slug and policy against now.
func (v *ViewerSigner) Verify(slug, policy, token string, now time.Time) bool {
	dot := strings.IndexByte(token, '.')
	if dot <= 0 {
		return false
	}
	expires, err := strconv.ParseInt(token[:dot], 10, 64)
	if err != nil || expires < now.Unix() {
		return false
	}
	expected := v.sign(slug, policy, expires)
	return hmac.Equal([]byte(expected), []byte(token[dot+1:]))
}

// ViewerPolicy names who may drive through a capability.
const (
	PolicyView    = "view"
	PolicyControl = "control"
)

// ViewerBase returns the capability path prefix for a slug and policy.
func (v *ViewerSigner) ViewerBase(slug, policy string, now time.Time) string {
	return ViewerPathPrefix + url.PathEscape(slug) + "/" + policy + "/" + v.Token(slug, policy, now)
}

// ViewerURL builds the iframe src for noVNC behind the proxy. The websocket
// path is expressed relative to the origin, exactly as noVNC's `path`
// parameter expects.
func (v *ViewerSigner) ViewerURL(slug, policy, password string, now time.Time) string {
	base := v.ViewerBase(slug, policy, now)
	q := url.Values{}
	q.Set("autoconnect", "true")
	q.Set("resize", "scale")
	q.Set("reconnect", "true")
	q.Set("show_dot", "true")
	q.Set("path", strings.TrimPrefix(base, "/")+"/websockify")
	if policy == PolicyView {
		q.Set("view_only", "true")
	} else {
		q.Set("view_only", "false")
	}
	u := base + "/vnc.html?" + q.Encode()
	if password != "" {
		u += "#password=" + url.QueryEscape(password)
	}
	return u
}

// ViewerProxy serves the capability paths. Resolve maps a slug to the
// loopback port of its running desktop; a slug without a running desktop
// answers 404 even with a valid token.
type ViewerProxy struct {
	Signer  *ViewerSigner
	Resolve func(slug string) (port int, ok bool)
	Now     func() time.Time
}

func (p *ViewerProxy) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// ServeHTTP parses /computer-view/{slug}/{policy}/{token}/{rest} and
// proxies rest to the desktop, websocket upgrades included.
func (p *ViewerProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, ViewerPathPrefix) {
		http.NotFound(w, r)
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, ViewerPathPrefix), "/", 4)
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	slug, policy, token, rest := parts[0], parts[1], parts[2], parts[3]
	if s, err := url.PathUnescape(slug); err == nil {
		slug = s
	}
	if policy != PolicyView && policy != PolicyControl {
		http.NotFound(w, r)
		return
	}
	if !p.Signer.Verify(slug, policy, token, p.now()) {
		http.Error(w, "viewer link expired", http.StatusForbidden)
		return
	}
	port, ok := p.Resolve(slug)
	if !ok || port <= 0 {
		http.Error(w, "this computer is not running", http.StatusNotFound)
		return
	}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = "/" + rest
			pr.Out.URL.RawPath = ""
			pr.Out.Host = target.Host
			// The desktop must never learn where the gawker is browsing from.
			pr.Out.Header.Del("Referer")
			pr.Out.Header.Del("Cookie")
		},
		FlushInterval: -1,
	}
	proxy.ServeHTTP(w, r)
}
