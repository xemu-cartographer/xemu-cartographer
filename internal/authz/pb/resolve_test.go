package pb_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb/pbtest"
)

// newEvent builds a RequestEvent the way the router would, with an optional
// verified auth record.
func newEvent(app core.App, auth *core.Record, method, target string, hdr map[string]string) *core.RequestEvent {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return &core.RequestEvent{
		App:   app,
		Auth:  auth,
		Event: router.Event{Request: req, Response: httptest.NewRecorder()},
	}
}

func TestClassifyToken(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"mk_abc.secret":                "opaque",
		"sp_abc.secret":                "opaque",
		"dv_abc.secret":                "opaque",
		"legacy-env.secret":            "opaque",
		"eyJhbGciOi.eyJpZCI6.c2lnbmF0": "jwt",
		"rawlegacysecret":              "legacy",
		"xx_abc.secret":                "",
		"a.b.c.d":                      "",
	}
	for in, want := range cases {
		if got := pb.ClassifyToken(in); got != want {
			t.Errorf("classifyToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveRequestJWTAndOpaque(t *testing.T) {
	app, d := pbtest.NewApp(t)
	user := pbtest.NewUser(t, app, "u1@test.dev")
	pbtest.GrantRole(t, app, user.Id, "organizer")

	// Verified PB JWT ⇒ pb_user with its roles / scopes / level.
	e := newEvent(app, user, http.MethodGet, "/api/x", nil)
	p, err := pb.ResolveRequest(app, d, e)
	if err != nil {
		t.Fatalf("ResolveRequest(user): %v", err)
	}
	if p.Kind != authz.KindPBUser || p.UserID != user.Id || !p.HasRole("organizer") || p.Level != 50 {
		t.Fatalf("user principal = %+v", p)
	}
	if !p.HasScope("library.manage:x") {
		t.Fatalf("organizer scopes missing library.manage: %v", p.Scopes)
	}
	if got := pb.Get(e); got.UserID != user.Id {
		t.Fatalf("Get(e) did not return the stored principal: %+v", got)
	}

	// Superuser JWT ⇒ superuser principal.
	su := pbtest.NewSuperuser(t, app, "root@test.dev")
	p, err = pb.ResolveRequest(app, d, newEvent(app, su, http.MethodGet, "/api/x", nil))
	if err != nil || p.Kind != authz.KindSuperuser || !p.IsAdmin() {
		t.Fatalf("superuser principal = %+v, err=%v", p, err)
	}

	// Banned user ⇒ Nobody + ErrBanned.
	pbtest.SetField(t, app, "users", user.Id, "is_banned", true)
	p, err = pb.ResolveRequest(app, d, newEvent(app, user, http.MethodGet, "/api/x", nil))
	if !errors.Is(err, pb.ErrBanned) || p.Kind != authz.KindAnonymous {
		t.Fatalf("banned: p=%+v err=%v", p, err)
	}
	// Ban that already expired ⇒ usable again.
	pbtest.SetField(t, app, "users", user.Id, "banned_until", time.Now().Add(-time.Hour))
	if _, err := pb.ResolveRequest(app, d, newEvent(app, user, http.MethodGet, "/api/x", nil)); err != nil {
		t.Fatalf("expired ban should resolve: %v", err)
	}
	// Soft-deleted ⇒ ErrBanned.
	pbtest.SetField(t, app, "users", user.Id, "deleted_at", time.Now())
	if _, err := pb.ResolveRequest(app, d, newEvent(app, user, http.MethodGet, "/api/x", nil)); !errors.Is(err, pb.ErrBanned) {
		t.Fatalf("deleted user should be ErrBanned, got %v", err)
	}

	// Opaque machine key via Authorization: Bearer and X-Api-Key.
	kid, token := pbtest.MintToken(t, app, d, "machine", []string{"lan.saves.*"}, func(r *pb.MintRequest) {
		r.StationID = "station-7"
	})
	for _, hdr := range []map[string]string{
		{"Authorization": "Bearer " + token},
		{"Authorization": token},
		{"X-Api-Key": token},
	} {
		p, err := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/api/x", hdr))
		if err != nil {
			t.Fatalf("opaque %v: %v", hdr, err)
		}
		if p.Kind != authz.KindMachine || p.ID != kid || p.Extra["station_id"] != "station-7" {
			t.Fatalf("machine principal for %v = %+v", hdr, p)
		}
	}
	if rec := pbtest.TokenRecord(t, app, kid); rec.GetDateTime("last_used_at").IsZero() {
		t.Fatalf("last_used_at not stamped")
	}

	// Bad secret on a known kid is an error, not a fall-through.
	_, err = pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/api/x", map[string]string{"Authorization": "Bearer " + kid + ".wrong"}))
	if !errors.Is(err, authz.ErrBadSecret) {
		t.Fatalf("bad secret: %v", err)
	}
	// Unknown kid.
	_, err = pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/api/x", map[string]string{"Authorization": "Bearer mk_nope.secret"}))
	if !errors.Is(err, authz.ErrUnknownKid) {
		t.Fatalf("unknown kid: %v", err)
	}
	// Nothing presented ⇒ Nobody, no error.
	p, err = pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/api/x", nil))
	if err != nil || p.Kind != authz.KindAnonymous || p.BoundInstance() != "" || len(p.Scopes) != 0 {
		t.Fatalf("nobody = %+v err=%v", p, err)
	}
	// REST never accepts a raw legacy secret (LAN carriers only).
	pb.ImportLegacyEnv(d, func(string) string { return "rawsecret" })
	p, err = pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/api/x", map[string]string{"Authorization": "Bearer rawsecret"}))
	if err != nil || p.Kind != authz.KindAnonymous {
		t.Fatalf("raw legacy secret over REST must not resolve: %+v err=%v", p, err)
	}
}

func TestResolveWSConsoleBindsXboxNameOnly(t *testing.T) {
	fake := &pbtest.FakeScraper{}
	app, d := pbtest.NewAppWith(t, fake, func() string { return "xc-" })
	_ = app
	// Console nickname SPARTAN-1 on instance xc-1; SPARTAN-2 is a machine in
	// xc-1's lobby (Membership), not a console.
	fake.AddInstance("xc-1", "SPARTAN-1")
	fake.SetMembership("xc-1", "SPARTAN-1", "SPARTAN-2", "player one")

	p, err := pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?console=spartan-1", nil))
	if err != nil {
		t.Fatalf("console=spartan-1: %v", err)
	}
	if p.Kind != authz.KindAnonymous || p.BoundInstance() != "xc-1" || p.Extra["console"] != "spartan-1" {
		t.Fatalf("console principal = %+v", p)
	}
	if len(p.Scopes) == 0 {
		t.Fatalf("anonymous seed scopes not applied: %+v", p)
	}

	p, err = pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?console=spartan-2", nil))
	if err != nil {
		t.Fatalf("console=spartan-2: %v", err)
	}
	if p.Kind != authz.KindAnonymous || p.BoundInstance() != "" {
		t.Fatalf("a lobby machine name must bind nothing: %+v", p)
	}
	if p.Extra["console"] != "spartan-2" {
		t.Fatalf("console name not carried for re-bind: %+v", p)
	}

	// Case / whitespace insensitive on the console side too.
	p, _ = pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?console=%20Spartan-1%20", nil))
	if p.BoundInstance() != "xc-1" {
		t.Fatalf("console match must be sanitized: %+v", p)
	}

	// No query at all ⇒ Nobody.
	p, err = pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws", nil))
	if err != nil || p.Kind != authz.KindAnonymous || p.BoundInstance() != "" || len(p.Scopes) != 0 {
		t.Fatalf("bare connect = %+v err=%v", p, err)
	}
}

func TestResolveWSTokenAndSpectator(t *testing.T) {
	app, d := pbtest.NewApp(t)
	user := pbtest.NewUser(t, app, "ws@test.dev")
	pbtest.GrantRole(t, app, user.Id, "member")

	jwt, err := user.NewAuthToken()
	if err != nil {
		t.Fatalf("NewAuthToken: %v", err)
	}
	p, err := pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?token="+jwt, nil))
	if err != nil || p.Kind != authz.KindPBUser || p.UserID != user.Id {
		t.Fatalf("jwt: p=%+v err=%v", p, err)
	}

	_, sp := pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:xc-1"})
	p, err = pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?spectator="+sp, nil))
	if err != nil || p.Kind != authz.KindSpectator || p.BoundInstance() != "xc-1" {
		t.Fatalf("spectator: p=%+v err=%v", p, err)
	}
	// ?token= also carries opaque keys.
	p, err = pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?token="+sp, nil))
	if err != nil || p.Kind != authz.KindSpectator {
		t.Fatalf("spectator via token=: p=%+v err=%v", p, err)
	}
	// Garbage JWT ⇒ error.
	if _, err := pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?token=a.b.c", nil)); err == nil {
		t.Fatalf("garbage jwt must error")
	}
}

func TestResolveScreen(t *testing.T) {
	app, d := pbtest.NewApp(t)
	user := pbtest.NewUser(t, app, "screen@test.dev")
	jwt, _ := user.NewAuthToken()

	e := newEvent(app, nil, http.MethodGet, "/api/admin/containers/xc-1/screen/", nil)
	e.Request.AddCookie(&http.Cookie{Name: "screen_token", Value: jwt})
	p, raw := pb.ResolveScreen(app, d, e)
	if p.Kind != authz.KindPBUser || raw != jwt {
		t.Fatalf("cookie jwt: p=%+v raw=%q", p, raw)
	}

	_, dv := pbtest.MintToken(t, app, d, "device", []string{"box.view:xc-1", "box.drive:xc-1"})
	e = newEvent(app, nil, http.MethodGet, "/api/admin/containers/xc-1/screen/?token="+dv, nil)
	p, raw = pb.ResolveScreen(app, d, e)
	if p.Kind != authz.KindDevice || p.BoundInstance() != "xc-1" || raw != dv {
		t.Fatalf("device query: p=%+v raw=%q", p, raw)
	}

	e = newEvent(app, nil, http.MethodGet, "/api/admin/containers/xc-1/screen/?token=dv_bad.x", nil)
	p, raw = pb.ResolveScreen(app, d, e)
	if p.Kind != authz.KindAnonymous || raw != "" {
		t.Fatalf("bad device key must yield Nobody: p=%+v raw=%q", p, raw)
	}
}

func TestPrincipalFromAuth(t *testing.T) {
	app, d := pbtest.NewApp(t)
	if p := pb.PrincipalFromAuth(app, d, nil); p.Kind != authz.KindInternal {
		t.Fatalf("nil auth = %+v", p)
	}
	user := pbtest.NewUser(t, app, "pfa@test.dev")
	pbtest.GrantRole(t, app, user.Id, "member")
	pbtest.GrantRole(t, app, user.Id, "overlay_manager")
	p := pb.PrincipalFromAuth(app, d, user)
	if p.Kind != authz.KindPBUser || p.Collection != "users" || p.Level != 40 {
		t.Fatalf("principal = %+v", p)
	}
	if len(p.Roles) != 2 || p.Roles[0] != "member" || p.Roles[1] != "overlay_manager" {
		t.Fatalf("roles not sorted: %v", p.Roles)
	}
	if !p.HasScope("overlay.mint") || p.HasScope("token.mint") {
		t.Fatalf("scopes = %v", p.Scopes)
	}
	// nil deps ⇒ still resolves from the app (loadRoles).
	if q := pb.PrincipalFromAuth(app, nil, user); q.Level != 40 || !q.HasScope("overlay.mint") {
		t.Fatalf("nil deps principal = %+v", q)
	}
}

func TestReResolveEvicts(t *testing.T) {
	fake := &pbtest.FakeScraper{}
	app, d := pbtest.NewAppWith(t, fake, nil)

	// pb_user: banned ⇒ evicted.
	user := pbtest.NewUser(t, app, "rr@test.dev")
	pbtest.GrantRole(t, app, user.Id, "member")
	p := pb.PrincipalFromAuth(app, d, user)
	if np, ok := pb.ReResolve(app, d, p); !ok || np.UserID != user.Id {
		t.Fatalf("live user: %+v %v", np, ok)
	}
	pbtest.SetField(t, app, "users", user.Id, "is_banned", true)
	if _, ok := pb.ReResolve(app, d, p); ok {
		t.Fatalf("banned user must be evicted")
	}

	// machine: revoked ⇒ evicted.
	kid, token := pbtest.MintToken(t, app, d, "machine", []string{"lan.*"})
	mp, err := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/", map[string]string{"X-Api-Key": token}))
	if err != nil {
		t.Fatalf("resolve machine: %v", err)
	}
	if _, ok := pb.ReResolve(app, d, mp); !ok {
		t.Fatalf("live machine key must survive")
	}
	if err := pb.RevokeToken(app, d, authz.Internal("test"), kid, ""); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, ok := pb.ReResolve(app, d, mp); ok {
		t.Fatalf("revoked key must be evicted")
	}

	// spectator: expired ⇒ evicted.
	skid, stoken := pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:xc-1"})
	sp, err := pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?spectator="+stoken, nil))
	if err != nil {
		t.Fatalf("resolve spectator: %v", err)
	}
	if _, ok := pb.ReResolve(app, d, sp); !ok {
		t.Fatalf("live spectator must survive")
	}
	rec := pbtest.TokenRecord(t, app, skid)
	pbtest.SetField(t, app, "api_tokens", rec.Id, "expires_at", time.Now().Add(-time.Minute))
	if _, ok := pb.ReResolve(app, d, sp); ok {
		t.Fatalf("expired key must be evicted")
	}

	// anonymous console: re-bound from the live console list.
	fake.AddInstance("xc-9", "LAN-BOX")
	ap, _ := pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?console=lan-box", nil))
	if ap.BoundInstance() != "xc-9" {
		t.Fatalf("initial bind = %+v", ap)
	}
	fake.Infos = nil // the console went away
	np, ok := pb.ReResolve(app, d, ap)
	if !ok || np.Kind != authz.KindAnonymous || np.BoundInstance() != "" {
		t.Fatalf("console gone: %+v %v", np, ok)
	}
	fake.AddInstance("xc-10", "LAN-BOX") // came back under a new instance
	np, ok = pb.ReResolve(app, d, ap)
	if !ok || np.BoundInstance() != "xc-10" {
		t.Fatalf("console re-bind: %+v %v", np, ok)
	}
	// Closing the door (anonymous scopes emptied) strips the scopes on tick.
	pbtest.SetRoleScopes(t, app, "anonymous", nil)
	np, _ = pb.ReResolve(app, d, ap)
	if len(np.Scopes) != 0 {
		t.Fatalf("closed door must yield no scopes: %v", np.Scopes)
	}

	// Nobody stays Nobody; internal stays.
	if np, ok := pb.ReResolve(app, d, authz.Nobody()); !ok || np.Kind != authz.KindAnonymous {
		t.Fatalf("nobody: %+v %v", np, ok)
	}
	if np, ok := pb.ReResolve(app, d, authz.Internal("x")); !ok || np.ID != "x" {
		t.Fatalf("internal: %+v %v", np, ok)
	}
	// superuser: gone ⇒ evicted.
	if _, ok := pb.ReResolve(app, d, authz.Superuser("nope")); ok {
		t.Fatalf("missing superuser must be evicted")
	}
}

func TestUserUnusable(t *testing.T) {
	app, _ := pbtest.NewApp(t)
	u := pbtest.NewUser(t, app, "uu@test.dev")
	now := time.Now()
	if pb.UserUnusable(u, now) {
		t.Fatalf("fresh user unusable")
	}
	u.Set("is_banned", true)
	if !pb.UserUnusable(u, now) {
		t.Fatalf("open-ended ban must be unusable")
	}
	u.Set("banned_until", now.Add(time.Hour))
	if !pb.UserUnusable(u, now) {
		t.Fatalf("future ban must be unusable")
	}
	u.Set("banned_until", now.Add(-time.Hour))
	if pb.UserUnusable(u, now) {
		t.Fatalf("lapsed ban must be usable")
	}
	u.Set("is_deleted", true)
	if !pb.UserUnusable(u, now) {
		t.Fatalf("is_deleted must be unusable")
	}
	if !pb.UserUnusable(nil, now) {
		t.Fatalf("nil must be unusable")
	}
}
