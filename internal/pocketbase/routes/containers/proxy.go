package containers

// HTTP reverse-proxy that fronts the jlesage/firefox sidecar container's web UI.
//
// Why a proxy instead of direct port access:
//   - The sidecar's HTTP port (browser_web) and VNC port (browser_vnc) are bound
//     to 127.0.0.1 only (see internal/podman/podman.go:createBrowser). Direct
//     access from a browser doesn't work over the internet.
//   - All traffic flows through PocketBase's :8090, which is the single port
//     that needs to be public when deploying behind a TLS reverse-proxy.
//   - Auth is enforced via the same PocketBase JWT used everywhere else.
//
// The HTML base-href injection rewrites jlesage's noVNC entry-point so that
// its relative asset paths (`app/`, `core/`, `vendor/`) resolve under the
// proxied prefix. The /websockify upgrade is handled transparently by
// httputil.ReverseProxy — its built-in switching protocols path supports
// WebSockets as long as the upstream URL is preserved.

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
)

func init() {
	register(registerScreenProxy)
}

// Screen dial-retry tuning, read once at register time (OnServe, after any .env
// is in the process environment). screenDialBudget bounds the total time the
// reverse proxy spends retrying the TCP dial to a *running-but-booting* browser
// container (nginx/s6-overlay take a few seconds to accept connections after
// `podman start`). screenPerDialTimeout bounds each individual attempt so a
// filtered/silently-dropped port can't stall a single dial past the budget.
// Defaults preserve the historical ~10s boot-race window.
var (
	screenDialBudget     = 10 * time.Second
	screenPerDialTimeout = 3 * time.Second
)

func registerScreenProxy() {
	// Mounted directly on se.Router (NOT on Group) so we can authenticate
	// via ?token= rather than the Authorization header — iframes cannot
	// set headers on their own requests.
	if Router == nil {
		return
	}

	// The pre-rename CONTAINERS_KIOSK_* names are still honoured as fallbacks so
	// a deployed .env keeps working; drop the aliases when the sidecar goes.
	if d := envDurationMS("CONTAINERS_SCREEN_DIAL_TIMEOUT_MS", "CONTAINERS_KIOSK_DIAL_TIMEOUT_MS"); d > 0 {
		screenDialBudget = d
	}
	if d := envDurationMS("CONTAINERS_SCREEN_PER_DIAL_TIMEOUT_MS", "CONTAINERS_KIOSK_PER_DIAL_TIMEOUT_MS"); d > 0 {
		screenPerDialTimeout = d
	}

	Router.GET("/api/admin/containers/{name}/screen/{path...}", handleScreenProxy)
	Router.HEAD("/api/admin/containers/{name}/screen/{path...}", handleScreenProxy)
	Router.POST("/api/admin/containers/{name}/screen/{path...}", handleScreenProxy)

	// Bare `/screen` (no trailing slash) — redirect to `/screen/` so the
	// browser sees a stable origin path and the base-href works.
	Router.GET("/api/admin/containers/{name}/screen", func(e *core.RequestEvent) error {
		name := e.Request.PathValue("name")
		http.Redirect(e.Response, e.Request,
			"/api/admin/containers/"+url.PathEscape(name)+"/screen/",
			http.StatusFound)
		return nil
	})
}

func handleScreenProxy(e *core.RequestEvent) error {
	name := e.Request.PathValue("name")
	// box.view on this container: a scoped principal, the box owner, a
	// player rostered in it, or a device key bound to it (rule table).
	if !authorizeBoxAccess(e, name, authz.ActionBoxView) {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
	}

	info, ok := Manager.Get(name)
	if !ok {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "container not found"})
	}

	// Gate on live podman status before entering the dial-retry loop. A
	// container that is *recorded* but *not running* passes the Get() existence
	// check (existence ≠ liveness); without this gate it would get dialed on a
	// dead port and hang the full ~10s dial-retry budget before surfacing as a
	// 502. ScreenLive fast-fails that case with a clean 503 the browser can
	// render immediately; a running-but-still-booting container reads as live
	// and the dial retry below covers its nginx warm-up.
	if !Manager.ScreenLive(name) {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{"error": "container not running"})
	}

	// When the request brought a fresh ?token=, persist it as a path-scoped
	// HttpOnly cookie so the iframe's sub-resource fetches (CSS/JS/images/
	// /websockify) authenticate without anyone rewriting URLs.
	if t := e.Request.URL.Query().Get("token"); t != "" {
		setScreenTokenCookie(e, "/api/admin/containers/"+url.PathEscape(name)+"/screen/", t)
	}

	// PB's default security headers middleware sets X-Frame-Options: SAMEORIGIN
	// on every response. That blocks the iframe whenever the embedding page is
	// on a different origin (dev: Vite :5173 → PB :8090). Strip it here — auth
	// is enforced via the ?token= JWT, not by frame-ancestors. Same trick PB
	// itself uses for file routes (see apis/file.go).
	e.Response.Header().Del("X-Frame-Options")

	target := &url.URL{
		Scheme: "http",
		Host:   "127.0.0.1:" + strconv.Itoa(info.Ports.BrowserWeb),
	}
	prefix := "/api/admin/containers/" + url.PathEscape(name) + "/screen"

	newScreenProxy(target, prefix).ServeHTTP(e.Response, e.Request)
	return nil
}

// newScreenProxy builds the reverse proxy for one container's web UI: requests
// under prefix are rewritten onto target with the prefix stripped, the
// caller's credential removed (stripScreenCredential), and HTML responses
// rebased under the prefix.
func newScreenProxy(target *url.URL, prefix string) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			// Strip the prefix so the upstream sees the original noVNC paths.
			req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
			req.Host = target.Host
			stripScreenCredential(req)
		},
		// The browser container's HTTP listener (s6-overlay → nginx) takes
		// several seconds to come up after `podman start`. Without a retry,
		// the user's first iframe load races that boot and gets a 502.
		Transport: &http.Transport{
			DialContext: newDialWithRetry(screenDialBudget, screenPerDialTimeout),
		},
		ModifyResponse: func(resp *http.Response) error {
			// Drop framing restrictions from upstream too — same reason as
			// the PB-default header strip above.
			resp.Header.Del("X-Frame-Options")
			resp.Header.Del("Content-Security-Policy")

			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				return nil
			}
			return rewriteScreenHTML(resp, prefix+"/")
		},
	}
}

// stripScreenCredential removes the caller's credential from the outbound
// upstream request before the proxy forwards it: the ?token= query parameter,
// the screen_token cookie (every other cookie is kept as sent) and the REST
// carriers (Authorization / X-Api-Key). The sidecar container never needs the
// credential — authorizeBoxAccess already admitted the request — and
// forwarding it would hand a bearer credential to whatever the container
// process serves.
func stripScreenCredential(req *http.Request) {
	if q := req.URL.Query(); q.Has("token") {
		q.Del("token")
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Del("Authorization")
	req.Header.Del("X-Api-Key")
	cookies := req.Cookies()
	if len(cookies) == 0 {
		return
	}
	req.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != screenTokenCookie {
			req.AddCookie(c)
		}
	}
}

// newDialWithRetry builds a DialContext that retries a TCP dial for up to
// `budget` when the upstream refuses the connection — covering the gap between
// `podman start` returning and the browser container's HTTP listener actually
// accepting connections. Each individual attempt is bounded by `perDial` so a
// filtered (silently-dropped) port can't stall a single dial past the budget.
// Once the listener is up, the first attempt succeeds with no measurable
// overhead.
//
// The liveness gate in handleScreenProxy means we only reach here for a
// container podman reports as running, so this retry now only ever smooths a
// genuine boot race — never a recorded-but-dead container.
func newDialWithRetry(budget, perDial time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		deadline := time.Now().Add(budget)
		d := net.Dialer{Timeout: perDial}
		for {
			conn, err := d.DialContext(ctx, network, addr)
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil || time.Now().After(deadline) {
				return nil, err
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

// envDurationMS reads the first set integer-milliseconds env var among keys
// into a time.Duration, returning 0 when none is set (or the first set one is
// empty/invalid) so callers keep their default.
func envDurationMS(keys ...string) time.Duration {
	for _, key := range keys {
		v, ok := os.LookupEnv(key)
		if !ok || v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0
		}
		return time.Duration(n) * time.Millisecond
	}
	return 0
}

// permissionsPolyfillScript wraps navigator.permissions.query so Firefox stops
// throwing TypeError on unknown permission names like 'clipboard-write' (a
// Chromium-only enum). noVNC's RFB constructor probes that name and the
// rejection floods the sidecar console on every load. Returning a denied
// PermissionStatus-shaped object matches Chromium's behavior. Chromium itself
// is a no-op since it doesn't throw on the same query.
const permissionsPolyfillScript = `<script>(function(){var p=navigator.permissions;if(!p||!p.query)return;var orig=p.query.bind(p);p.query=function(d){try{return orig(d).catch(function(e){if(e&&e.name==='TypeError')return{state:'denied',onchange:null};throw e;});}catch(e){if(e&&e.name==='TypeError')return Promise.resolve({state:'denied',onchange:null});return Promise.reject(e);}};})();</script>`

// rewriteScreenHTML splices a <base href> tag and the permissions polyfill into
// the sidecar's HTML <head>. The base href makes the upstream's relative asset
// paths resolve under our proxy prefix; the polyfill silences a Firefox-only
// noVNC console error. Handles gzip transparently and rewrites Content-Length.
func rewriteScreenHTML(resp *http.Response, base string) error {
	var (
		body []byte
		err  error
	)

	gzipped := strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip")
	if gzipped {
		gr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("screen proxy: gzip reader: %w", err)
		}
		body, err = io.ReadAll(gr)
		_ = gr.Close()
		if err != nil {
			return fmt.Errorf("screen proxy: gzip read: %w", err)
		}
	} else {
		body, err = io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("screen proxy: read body: %w", err)
		}
	}
	_ = resp.Body.Close()

	injection := []byte(`<base href="` + base + `">` + permissionsPolyfillScript)
	if i := bytes.Index(bytes.ToLower(body), []byte("<head>")); i != -1 {
		insert := i + len("<head>")
		body = append(body[:insert], append(injection, body[insert:]...)...)
	} else if i := bytes.Index(bytes.ToLower(body), []byte("<html")); i != -1 {
		// No <head> — splice after the <html ...> opening tag's `>`.
		if end := bytes.IndexByte(body[i:], '>'); end != -1 {
			insert := i + end + 1
			body = append(body[:insert], append([]byte("<head>"+string(injection)+"</head>"), body[insert:]...)...)
		}
	}

	if gzipped {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		if _, err := gw.Write(body); err != nil {
			return fmt.Errorf("screen proxy: gzip write: %w", err)
		}
		if err := gw.Close(); err != nil {
			return fmt.Errorf("screen proxy: gzip close: %w", err)
		}
		body = buf.Bytes()
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return nil
}
