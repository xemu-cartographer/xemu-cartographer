package containers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb/pbtest"
	"github.com/xemu-cartographer/xemu-cartographer/internal/podman"
)

// memStore is an in-memory podman.Store seeded with the containers a test
// wants the Manager to know about.
type memStore struct {
	containers map[string]*podman.ContainerInfo
}

func (s *memStore) LoadAll() (map[string]*podman.ContainerInfo, error) { return s.containers, nil }
func (s *memStore) Upsert(*podman.ContainerInfo) error                 { return nil }
func (s *memStore) Delete(string) error                                { return nil }

// installFakeManager swaps the package Manager for one that knows a single
// container whose browser web port is the given upstream, backed by a fake
// `podman` that reports every container as running (so ScreenLive passes and
// the proxy dials the upstream). Restored on cleanup.
func installFakeManager(t *testing.T, name string, upstream *url.URL) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "podman.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho running\n"), 0o755); err != nil {
		t.Fatalf("write fake podman: %v", err)
	}
	port, err := strconv.Atoi(upstream.Port())
	if err != nil {
		t.Fatalf("upstream port %q: %v", upstream.Port(), err)
	}
	store := &memStore{containers: map[string]*podman.ContainerInfo{
		name: {Name: name, Ports: podman.Ports{BrowserWeb: port}},
	}}
	m, err := podman.NewManager(podman.Config{PodmanCmd: "/bin/sh " + script}, store)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	prev := Manager
	Manager = m
	t.Cleanup(func() { Manager = prev })
}

// screenRequest builds the RequestEvent handleScreenProxy sees for GET
// /api/admin/containers/{name}/screen/{path} with the credential carried the
// way an iframe does it — ?token= on the entry-point, the screen_token cookie
// on sub-resources — or with no credential at all.
func screenRequest(app core.App, name, path, query, cookie string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	target := "http://pb.local/api/admin/containers/" + name + "/screen/" + path
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("name", name)
	req.SetPathValue("path", path)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: screenTokenCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	return &core.RequestEvent{App: app, Event: router.Event{Request: req, Response: rec}}, rec
}

// TestAuthorizeBoxAccess pins the screen gate's decisions on the rule table
// (design §10 item 11): a user whose usable gamertag is on the container's
// live roster is admitted to both box.view and box.drive; a user not on
// the roster is not; a device key minted with box.view:<box> only may view
// that box but not drive it (and neither on another box); a request with no
// credential is refused. The credential is read from ?token= or the
// screen_token cookie alike.
func TestAuthorizeBoxAccess(t *testing.T) {
	fake := &pbtest.FakeScraper{}
	app, d := pbtest.NewAppWith(t, fake, func() string { return "" })
	const box = "gate-box1"
	fake.AddInstance(box, "BOX1")
	fake.SetMembership(box, "Spartan One", "Random Guest")

	rostered := pbtest.NewUser(t, app, "rostered@test.dev")
	pbtest.GrantRole(t, app, rostered.Id, "member")
	pbtest.NewTag(t, app, rostered.Id, "Spartan One", "approved")
	rosteredJWT, err := rostered.NewAuthToken()
	if err != nil {
		t.Fatalf("NewAuthToken: %v", err)
	}
	outsider := pbtest.NewUser(t, app, "outsider@test.dev")
	pbtest.GrantRole(t, app, outsider.Id, "member")
	pbtest.NewTag(t, app, outsider.Id, "Gate Outsider", "approved")
	outsiderJWT, err := outsider.NewAuthToken()
	if err != nil {
		t.Fatalf("NewAuthToken: %v", err)
	}
	_, viewKey := pbtest.MintToken(t, app, d, "device", []string{"box.view:" + box})

	cases := []struct {
		name   string
		query  string
		cookie string
		box    string
		action authz.Action
		want   bool
	}{
		{"rostered member, box.view via ?token=", "token=" + rosteredJWT, "", box, authz.ActionBoxView, true},
		{"rostered member, box.drive via cookie", "", rosteredJWT, box, authz.ActionBoxDrive, true},
		{"member not on the roster, box.view", "token=" + outsiderJWT, "", box, authz.ActionBoxView, false},
		{"member not on the roster, box.drive", "", outsiderJWT, box, authz.ActionBoxDrive, false},
		{"rostered member, another box", "token=" + rosteredJWT, "", "gate-box2", authz.ActionBoxView, false},
		{"device key box.view only, box.view", "token=" + viewKey, "", box, authz.ActionBoxView, true},
		{"device key box.view only, box.drive", "token=" + viewKey, "", box, authz.ActionBoxDrive, false},
		{"device key box.view only, another box", "", viewKey, "gate-box2", authz.ActionBoxView, false},
		{"anonymous", "", "", box, authz.ActionBoxView, false},
		{"garbage token", "token=not.a.jwt", "", box, authz.ActionBoxView, false},
	}
	for _, c := range cases {
		e, _ := screenRequest(app, c.box, "", c.query, c.cookie)
		if got := authorizeBoxAccess(e, c.box, c.action); got != c.want {
			t.Errorf("%s: authorizeBoxAccess = %v, want %v", c.name, got, c.want)
		}
	}
	if authorizeBoxAccess(nil, box, authz.ActionBoxView) {
		t.Error("nil event must be refused")
	}
}

// TestScreenProxyHandlerGate drives handleScreenProxy end to end against a
// fake podman Manager and a recording upstream: the rostered member's
// entry-point request is proxied (200, the screen_token cookie is set, and the
// upstream receives neither the ?token= nor the cookie), the sub-resource
// request authenticated by the cookie is proxied without the cookie reaching
// the upstream, and a member who is not on the roster, or no credential at
// all, gets the 403 {"error":"forbidden"} without the upstream ever being
// dialed.
func TestScreenProxyHandlerGate(t *testing.T) {
	fake := &pbtest.FakeScraper{}
	app, _ := pbtest.NewAppWith(t, fake, func() string { return "" })
	const box = "proxy-box1"
	fake.AddInstance(box, "BOX1")
	fake.SetMembership(box, "Spartan Two")

	rostered := pbtest.NewUser(t, app, "rostered@test.dev")
	pbtest.GrantRole(t, app, rostered.Id, "member")
	pbtest.NewTag(t, app, rostered.Id, "Spartan Two", "approved")
	rosteredJWT, err := rostered.NewAuthToken()
	if err != nil {
		t.Fatalf("NewAuthToken: %v", err)
	}
	outsider := pbtest.NewUser(t, app, "outsider@test.dev")
	pbtest.GrantRole(t, app, outsider.Id, "member")
	pbtest.NewTag(t, app, outsider.Id, "Proxy Outsider", "approved")
	outsiderJWT, err := outsider.NewAuthToken()
	if err != nil {
		t.Fatalf("NewAuthToken: %v", err)
	}

	up, target := newRecordingUpstream(t)
	installFakeManager(t, box, target)

	// Entry point: ?token= admits, gets persisted as the cookie, never
	// reaches the container.
	e, rec := screenRequest(app, box, "", "token="+rosteredJWT, "")
	if err := handleScreenProxy(e); err != nil {
		t.Fatalf("entry: %v", err)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "screen" {
		t.Fatalf("entry: response = %d %q, want 200 \"screen\"", rec.Code, rec.Body.String())
	}
	if hits, seen := up.seen(); hits != 1 || seen.path != "/" || seen.query != "" || seen.cookie != "" {
		t.Errorf("entry: upstream saw %+v (hits %d), want / with no credential", seen, hits)
	}
	cookieSet := false
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == screenTokenCookie && ck.Value == rosteredJWT && strings.HasPrefix(ck.Path, "/api/admin/containers/"+box+"/screen/") {
			cookieSet = true
		}
	}
	if !cookieSet {
		t.Errorf("entry: screen_token cookie not set: %v", rec.Result().Cookies())
	}

	// Sub-resource: the cookie admits; the upstream gets the path and the
	// other cookies, not screen_token.
	e, rec = screenRequest(app, box, "app/ui.js", "v=2", rosteredJWT)
	e.Request.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	if err := handleScreenProxy(e); err != nil {
		t.Fatalf("sub-resource: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("sub-resource: status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if hits, seen := up.seen(); hits != 2 || seen.path != "/app/ui.js" || seen.query != "v=2" || seen.cookie != "theme=dark" {
		t.Errorf("sub-resource: upstream saw %+v (hits %d), want /app/ui.js?v=2 with theme cookie only", seen, hits)
	}

	// Denied callers never reach the container.
	denied := []struct {
		name   string
		query  string
		cookie string
	}{
		{"member not on the roster", "token=" + outsiderJWT, ""},
		{"member not on the roster via cookie", "", outsiderJWT},
		{"anonymous", "", ""},
	}
	for _, c := range denied {
		e, rec := screenRequest(app, box, "", c.query, c.cookie)
		if err := handleScreenProxy(e); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rec.Code != http.StatusForbidden || strings.TrimSpace(rec.Body.String()) != `{"error":"forbidden"}` {
			t.Errorf("%s: response = %d %q, want 403 {\"error\":\"forbidden\"}", c.name, rec.Code, rec.Body.String())
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: a denied request must not set a cookie: %v", c.name, rec.Result().Cookies())
		}
	}
	if hits, _ := up.seen(); hits != 2 {
		t.Errorf("denied requests reached the upstream: hits = %d, want 2", hits)
	}
}
