package containers

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// makeResp builds a minimal *http.Response for rewriteScreenHTML to chew on.
func makeResp(body []byte, gzipped bool) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "text/html; charset=utf-8")
	if gzipped {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(body)
		_ = gw.Close()
		body = buf.Bytes()
		h.Set("Content-Encoding", "gzip")
	}
	return &http.Response{
		Header: h,
		Body:   io.NopCloser(bytes.NewReader(body)),
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("gzip reader: %v", err)
		}
		defer func() { _ = gr.Close() }()
		body, err = io.ReadAll(gr)
		if err != nil {
			t.Fatalf("gunzip: %v", err)
		}
	}
	return string(body)
}

func TestInjectBaseHrefPlain(t *testing.T) {
	resp := makeResp([]byte("<html><head><title>k</title></head><body>x</body></html>"), false)
	if err := rewriteScreenHTML(resp, "/api/admin/containers/alpha/screen/"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	got := readBody(t, resp)
	if !strings.Contains(got, `<base href="/api/admin/containers/alpha/screen/">`) {
		t.Errorf("missing <base> tag: %q", got)
	}
	if !strings.Contains(got, "<title>k</title>") {
		t.Errorf("body damaged: %q", got)
	}
}

func TestInjectBaseHrefGzipped(t *testing.T) {
	resp := makeResp([]byte("<html><head></head><body></body></html>"), true)
	if err := rewriteScreenHTML(resp, "/p/"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	got := readBody(t, resp)
	if !strings.Contains(got, `<base href="/p/">`) {
		t.Errorf("missing <base> after gzip round-trip: %q", got)
	}
}

func TestInjectBaseHrefNoHead(t *testing.T) {
	// HTML without an explicit <head> — the helper should splice one in.
	resp := makeResp([]byte("<html><body>hi</body></html>"), false)
	if err := rewriteScreenHTML(resp, "/p/"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	got := readBody(t, resp)
	if !strings.Contains(got, "<head>") || !strings.Contains(got, `<base href="/p/">`) {
		t.Errorf("missing synthesised <head>+<base>: %q", got)
	}
}

// TestScreenCookieSecureAndMaxAge pins the PD-11 cookie attributes: the
// screen_token cookie is HttpOnly + SameSite=Lax, expires after 12h, and is
// Secure whenever the request came in over TLS — directly or through a
// reverse proxy declaring X-Forwarded-Proto: https — but not over plain HTTP
// (dev: Vite → PB on localhost).
func TestScreenCookieSecureAndMaxAge(t *testing.T) {
	const path = "/api/admin/containers/pod1/screen/"

	plain := httptest.NewRequest(http.MethodGet, "http://pb.local"+path+"?token=abc", nil)
	forwarded := httptest.NewRequest(http.MethodGet, "http://pb.local"+path+"?token=abc", nil)
	forwarded.Header.Set("X-Forwarded-Proto", "https")
	forwardedHTTP := httptest.NewRequest(http.MethodGet, "http://pb.local"+path+"?token=abc", nil)
	forwardedHTTP.Header.Set("X-Forwarded-Proto", "http")
	tls := httptest.NewRequest(http.MethodGet, "https://pb.local"+path+"?token=abc", nil)
	if tls.TLS == nil {
		t.Fatal("httptest should populate TLS for an https URL")
	}

	cases := []struct {
		name       string
		req        *http.Request
		wantSecure bool
	}{
		{"plain http", plain, false},
		{"x-forwarded-proto https", forwarded, true},
		{"x-forwarded-proto http", forwardedHTTP, false},
		{"direct tls", tls, true},
		{"nil request", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ck := screenCookie(c.req, path, "abc")
			if ck.Name != screenTokenCookie || ck.Value != "abc" || ck.Path != path {
				t.Fatalf("cookie identity = %s=%s path=%s", ck.Name, ck.Value, ck.Path)
			}
			if ck.Secure != c.wantSecure {
				t.Errorf("Secure = %v, want %v", ck.Secure, c.wantSecure)
			}
			if ck.MaxAge != 43200 {
				t.Errorf("MaxAge = %d, want 43200", ck.MaxAge)
			}
			if !ck.HttpOnly {
				t.Error("HttpOnly must be set")
			}
			if ck.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", ck.SameSite)
			}
		})
	}

	// The serialized Set-Cookie header carries the attributes (what the
	// browser actually sees).
	rec := httptest.NewRecorder()
	http.SetCookie(rec, screenCookie(forwarded, path, "abc"))
	got := rec.Header().Get("Set-Cookie")
	for _, want := range []string{"Max-Age=43200", "Secure", "HttpOnly", "SameSite=Lax", "Path=" + path} {
		if !strings.Contains(got, want) {
			t.Errorf("Set-Cookie %q lacks %q", got, want)
		}
	}
}

// upstreamSeen is what a fake sidecar container recorded of the last request
// the proxy forwarded to it.
type upstreamSeen struct {
	path, query, cookie, authorization, apiKey, host string
}

// upstreamRecorder is the handler of a fake sidecar container: it counts the
// requests the proxy forwarded and keeps the last one (mutex-guarded — the
// httptest server answers on its own goroutine) and answers 200 "screen".
type upstreamRecorder struct {
	mu   sync.Mutex
	hits int
	last upstreamSeen
}

func (u *upstreamRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.hits++
	u.last = upstreamSeen{
		path:          r.URL.Path,
		query:         r.URL.RawQuery,
		cookie:        r.Header.Get("Cookie"),
		authorization: r.Header.Get("Authorization"),
		apiKey:        r.Header.Get("X-Api-Key"),
		host:          r.Host,
	}
	u.mu.Unlock()
	_, _ = w.Write([]byte("screen"))
}

// seen returns the forwarded-request count and the last request recorded.
func (u *upstreamRecorder) seen() (int, upstreamSeen) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits, u.last
}

// newRecordingUpstream starts an httptest server standing in for a sidecar
// container's web UI and returns it with its recorder and parsed URL.
func newRecordingUpstream(t *testing.T) (*upstreamRecorder, *url.URL) {
	t.Helper()
	rec := &upstreamRecorder{}
	srv := httptest.NewServer(rec)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse upstream url: %v", err)
	}
	return rec, target
}

// TestScreenProxyStripsCredential pins that the reverse proxy never forwards
// the caller's credential to the container: the ?token= query parameter, the
// screen_token cookie and the REST carriers are removed from the upstream
// request while the rest of the query and the other cookies pass through,
// and the path is rebased under the prefix.
func TestScreenProxyStripsCredential(t *testing.T) {
	up, target := newRecordingUpstream(t)
	const prefix = "/api/admin/containers/pod1/screen"

	req := httptest.NewRequest(http.MethodGet, "http://pb.local"+prefix+"/app/ui.js?token=secret-jwt&x=1", nil)
	req.Header.Set("Authorization", "Bearer secret-jwt")
	req.Header.Set("X-Api-Key", "mk_secret")
	req.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	req.AddCookie(&http.Cookie{Name: screenTokenCookie, Value: "secret-jwt"})
	req.AddCookie(&http.Cookie{Name: "pb_auth", Value: "keep"})
	rec := httptest.NewRecorder()

	newScreenProxy(target, prefix).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "screen" {
		t.Fatalf("proxy response = %d %q, want 200 \"screen\"", rec.Code, rec.Body.String())
	}
	_, seen := up.seen()
	if seen.path != "/app/ui.js" {
		t.Errorf("upstream path = %q, want /app/ui.js", seen.path)
	}
	if seen.query != "x=1" {
		t.Errorf("upstream query = %q, want x=1 (token stripped)", seen.query)
	}
	if seen.cookie != "theme=dark; pb_auth=keep" {
		t.Errorf("upstream cookie = %q, want the non-screen cookies only", seen.cookie)
	}
	if seen.authorization != "" || seen.apiKey != "" {
		t.Errorf("upstream got Authorization=%q X-Api-Key=%q, want neither", seen.authorization, seen.apiKey)
	}
	if seen.host != target.Host {
		t.Errorf("upstream host = %q, want %q", seen.host, target.Host)
	}

	// A request with no credential at all stays untouched (no empty Cookie
	// header invented, query preserved verbatim).
	req = httptest.NewRequest(http.MethodGet, "http://pb.local"+prefix+"/?b=2&a=1", nil)
	rec = httptest.NewRecorder()
	newScreenProxy(target, prefix).ServeHTTP(rec, req)
	_, seen = up.seen()
	if rec.Code != http.StatusOK || seen.path != "/" || seen.query != "b=2&a=1" || seen.cookie != "" {
		t.Errorf("bare request forwarded as %+v (status %d)", seen, rec.Code)
	}
}
