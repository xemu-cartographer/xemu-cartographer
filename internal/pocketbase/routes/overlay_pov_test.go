package routes

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	sc "github.com/xemu-cartographer/xc-scraper/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb/pbtest"
	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
	scraperroutes "github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/routes/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/xcclient"
)

// fakeInspect implements scraperiface.Inspect for the resolver test.
type fakeInspect struct {
	infos  []scraperiface.Info
	states map[string]scraperiface.InspectState
}

func (f fakeInspect) List() []scraperiface.Info { return f.infos }
func (f fakeInspect) Inspect(name string) (scraperiface.InspectState, bool) {
	s, ok := f.states[name]
	return s, ok
}

func b(v bool) *bool { return &v }

// A host "stream" with a System Link lobby of stream/BlueBox/RedBox, and a
// second idle instance whose own console is "stewball32" (mirrors live beta).
func fixture() fakeInspect {
	streamGD := &sc.GameData{
		Gametype:   "slayer",
		IsTeamGame: true,
		Machines: []sc.GameMachine{
			{Index: 0, Name: "stream", IsLocal: b(true)},
			{Index: 1, Name: "BlueBox", IsLocal: b(false)},
			{Index: 2, Name: "RedBox", IsLocal: b(false)},
		},
	}
	return fakeInspect{
		infos: []scraperiface.Info{
			{Name: "beta-stream", XboxName: "stream"},
			{Name: "beta-play", XboxName: "stewball32"},
		},
		states: map[string]scraperiface.InspectState{
			"beta-stream": {GameData: streamGD},
			"beta-play":   {GameData: nil}, // idle, no lobby
		},
	}
}

// sharedLobbyFixture is the two-box System Link case: BOTH hosts scrape the
// same lobby, so each one's own console also appears as a peer in the other's
// machine list. beta-stream is listed first, so a global scan for
// "stewball32" lands on beta-stream (peer, machine 1) rather than beta-play
// (its own console, machine 0).
func sharedLobbyFixture() fakeInspect {
	streamGD := &sc.GameData{
		Gametype:   "ctf",
		IsTeamGame: true,
		Machines: []sc.GameMachine{
			{Index: 0, Name: "stream", IsLocal: b(true)},
			{Index: 1, Name: "stewball32", IsLocal: b(false)},
		},
	}
	playGD := &sc.GameData{
		Gametype:   "ctf",
		IsTeamGame: true,
		Machines: []sc.GameMachine{
			{Index: 0, Name: "stewball32", IsLocal: b(true)},
			{Index: 1, Name: "stream", IsLocal: b(false)},
		},
	}
	return fakeInspect{
		infos: []scraperiface.Info{
			{Name: "beta-stream", XboxName: "stream"},
			{Name: "beta-play", XboxName: "stewball32"},
		},
		states: map[string]scraperiface.InspectState{
			"beta-stream": {GameData: streamGD},
			"beta-play":   {GameData: playGD},
		},
	}
}

func TestResolveConsoleFor(t *testing.T) {
	f := sharedLobbyFixture()
	anon := func(inst string) authz.Principal { return authz.Anonymous(inst, []string{"overlay.read_state:*"}) }
	cases := []struct {
		name         string
		p            authz.Principal
		console      string
		wantInstance string
		wantMachine  int
		wantOK       bool
	}{
		// Unbound: the global scan, first-listed host wins.
		{"unbound peer-first", authz.Nobody(), "stewball32", "beta-stream", 1, true},
		{"unbound own", authz.Nobody(), "stream", "beta-stream", 0, true},
		// Bound to the box whose console it is: that box, its local machine.
		{"bound own console", anon("beta-play"), "stewball32", "beta-play", 0, true},
		{"bound own console (stream)", anon("beta-stream"), "stream", "beta-stream", 0, true},
		// Bound box sees the name as a peer: stays on the bound box.
		{"bound sees peer", anon("beta-stream"), "stewball32", "beta-stream", 1, true},
		// Bound box doesn't see the name: global scan (Can then decides).
		{"bound miss falls back", authz.Anonymous("beta-nope", nil), "stream", "beta-stream", 0, true},
		{"unknown console", anon("beta-play"), "GreenBox", "", 0, false},
	}
	for _, c := range cases {
		inst, mi, _, _, ok := resolveConsoleFor(f, c.p, c.console)
		if ok != c.wantOK {
			t.Errorf("%s: ok=%v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && (inst != c.wantInstance || mi != c.wantMachine) {
			t.Errorf("%s: resolved to %s/%d, want %s/%d", c.name, inst, mi, c.wantInstance, c.wantMachine)
		}
	}
}

func TestResolveConsole(t *testing.T) {
	f := fixture()
	cases := []struct {
		console      string
		wantInstance string
		wantMachine  int
	}{
		{"RedBox", "beta-stream", 2},    // remote lobby peer → machine 2
		{"BlueBox", "beta-stream", 1},   // remote lobby peer → machine 1
		{"stream", "beta-stream", 0},    // host's own console is machine 0 in its lobby
		{"redbox", "beta-stream", 2},    // case-insensitive
		{"stewball32", "beta-play", -1}, // own xbox_name, no live lobby → whole instance
	}
	for _, c := range cases {
		inst, mi, _, _, ok := resolveConsole(f, c.console)
		if !ok {
			t.Errorf("%q: not resolved, want %s/%d", c.console, c.wantInstance, c.wantMachine)
			continue
		}
		if inst != c.wantInstance || mi != c.wantMachine {
			t.Errorf("%q resolved to %s/%d, want %s/%d", c.console, inst, mi, c.wantInstance, c.wantMachine)
		}
	}

	if _, _, _, _, ok := resolveConsole(f, "GreenBox"); ok {
		t.Error("unknown console should not resolve")
	}
}

func TestListConsoles(t *testing.T) {
	got := listConsoles(fixture())
	// name -> expected {instance, machineIndex, isLocal}
	want := map[string]struct {
		instance string
		machine  int
		local    bool
	}{
		"stream":     {"beta-stream", 0, true}, // xbox_name deduped to lobby machine 0
		"BlueBox":    {"beta-stream", 1, false},
		"RedBox":     {"beta-stream", 2, false},
		"stewball32": {"beta-play", -1, true}, // idle host, no lobby
	}
	if len(got) != len(want) {
		t.Fatalf("got %d consoles, want %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		w, ok := want[c.Console]
		if !ok {
			t.Errorf("unexpected console %q", c.Console)
			continue
		}
		if c.Instance != w.instance || c.MachineIndex != w.machine || c.IsLocal != w.local {
			t.Errorf("%q = {%s,%d,%v}, want {%s,%d,%v}", c.Console,
				c.Instance, c.MachineIndex, c.IsLocal, w.instance, w.machine, w.local)
		}
	}
}

// overlayFake serves both halves of the R-14 route tests: the embedded
// pbtest.FakeScraper feeds pb.Default().InstanceByConsole (List → XboxName),
// while Inspect is overridden with the fixture's lobby states so
// resolveConsole sees the System Link peers.
type overlayFake struct {
	*pbtest.FakeScraper
	states map[string]scraperiface.InspectState
}

func (f overlayFake) Inspect(name string) (scraperiface.InspectState, bool) {
	s, ok := f.states[name]
	return s, ok
}

// newOverlayApp builds a pbtest app whose adapter and scraperroutes.Manager
// both see fixture(): beta-stream (console "stream", lobby stream/BlueBox/
// RedBox) and beta-play (console "stewball32", idle).
func newOverlayApp(t *testing.T) (core.App, *pb.PBDeps) {
	t.Helper()
	return newOverlayAppWith(t, fixture())
}

// newOverlayAppWith is newOverlayApp over an arbitrary fixture.
func newOverlayAppWith(t *testing.T, fx fakeInspect) (core.App, *pb.PBDeps) {
	t.Helper()
	fake := overlayFake{FakeScraper: &pbtest.FakeScraper{}, states: fx.states}
	for _, info := range fx.infos {
		fake.AddInstance(info.Name, info.XboxName)
	}
	app, d := pbtest.NewAppWith(t, fake, func() string { return pbtest.DefaultPrefix })
	prev := scraperroutes.Manager
	scraperroutes.SetManager(fake)
	t.Cleanup(func() { scraperroutes.SetManager(prev) })
	return app, d
}

// overlayGet drives handler (optionally behind mw) with a hand-built
// RequestEvent and returns the HTTP status plus the decoded JSON body.
// *router.ApiError returns are folded into their status like the router
// would.
func overlayGet(t *testing.T, app core.App, auth *core.Record, path, pathName, bearer string, mw, handler func(*core.RequestEvent) error) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if pathName != "" {
		req.SetPathValue("name", pathName)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{
		App:   app,
		Auth:  auth,
		Event: router.Event{Request: req, Response: rec},
	}
	run := handler
	if mw != nil {
		run = func(e *core.RequestEvent) error {
			// A hand-built event has no next hook: Next() is a no-op nil, so
			// a passing middleware falls through to the handler here.
			if err := mw(e); err != nil {
				return err
			}
			return handler(e)
		}
	}
	if err := run(e); err != nil {
		var apiErr *router.ApiError
		if errors.As(err, &apiErr) {
			return apiErr.Status, nil
		}
		t.Fatalf("%s %s: non-API error: %v", http.MethodGet, path, err)
	}
	body := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: body %q: %v", http.MethodGet, path, rec.Body.String(), err)
		}
	}
	return rec.Code, body
}

// TestConsoleRouteAnonymousBindsXboxNameOnly pins R-14 on
// GET /api/overlay/console/{name}: the anonymous door binds by the
// instance's own console nickname only. A lobby-peer name still resolves an
// instance (so it is not a 404) but binds nothing ⇒ 403 with the spec'd
// body; a spectator key scoped to the instance gets through on the same
// peer name, and one scoped to a different instance does not.
func TestConsoleRouteAnonymousBindsXboxNameOnly(t *testing.T) {
	app, d := newOverlayApp(t)
	get := func(name, bearer string, auth *core.Record) (int, map[string]any) {
		return overlayGet(t, app, auth, "/api/overlay/console/"+name, name, bearer, nil, handleOverlayConsole)
	}

	t.Run("anonymous xbox_name is bound", func(t *testing.T) {
		for _, name := range []string{"stream", "stewball32", "STREAM"} {
			status, body := get(name, "", nil)
			if status != http.StatusOK {
				t.Fatalf("%q: status %d body %v, want 200", name, status, body)
			}
		}
		if _, body := get("stream", "", nil); body["instance"] != "beta-stream" || body["machine_index"] != float64(0) {
			t.Fatalf("stream resolved to %v, want beta-stream/0", body)
		}
	})

	t.Run("anonymous lobby peer is refused", func(t *testing.T) {
		for _, name := range []string{"RedBox", "BlueBox"} {
			status, body := get(name, "", nil)
			if status != http.StatusForbidden {
				t.Fatalf("%q: status %d body %v, want 403", name, status, body)
			}
			if body["error"] != "forbidden" || body["console"] != name {
				t.Fatalf("%q: body %v, want {error: forbidden, console: %s}", name, body, name)
			}
		}
	})

	t.Run("unknown console is 404 before any check", func(t *testing.T) {
		if status, _ := get("GreenBox", "", nil); status != http.StatusNotFound {
			t.Fatalf("status %d, want 404", status)
		}
	})

	t.Run("anonymous role without the scope closes the door", func(t *testing.T) {
		pbtest.SetRoleScopes(t, app, "anonymous", []string{"room.join:host:*:tick"})
		t.Cleanup(func() {
			seed, _ := authz.SeedRole("anonymous")
			pbtest.SetRoleScopes(t, app, "anonymous", seed.Scopes)
		})
		if status, _ := get("stream", "", nil); status != http.StatusForbidden {
			t.Fatalf("status %d, want 403", status)
		}
	})

	t.Run("spectator key bound to the instance", func(t *testing.T) {
		_, token := pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:beta-stream"})
		if status, body := get("RedBox", token, nil); status != http.StatusOK {
			t.Fatalf("RedBox with beta-stream key: status %d body %v, want 200", status, body)
		}
		// Same key on the other box: the scope names beta-stream, P:bound
		// refuses beta-play.
		if status, _ := get("stewball32", token, nil); status != http.StatusForbidden {
			t.Fatalf("stewball32 with beta-stream key: status %d, want 403", status)
		}
	})

	t.Run("revoked spectator key does not get a peer", func(t *testing.T) {
		kid, token := pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:beta-stream"})
		if err := pb.RevokeToken(app, d, authz.Internal("test"), kid, "test"); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if status, _ := get("RedBox", token, nil); status != http.StatusForbidden {
			t.Fatalf("status %d, want 403", status)
		}
	})

	t.Run("users JWT is checked by scope", func(t *testing.T) {
		mgr := pbtest.NewUser(t, app, "ovm@test.dev")
		pbtest.GrantRole(t, app, mgr.Id, "overlay_manager")
		if status, body := get("RedBox", "", mgr); status != http.StatusOK {
			t.Fatalf("overlay_manager: status %d body %v, want 200", status, body)
		}
		member := pbtest.NewUser(t, app, "member@test.dev")
		pbtest.GrantRole(t, app, member.Id, "member")
		if status, _ := get("RedBox", "", member); status != http.StatusForbidden {
			t.Fatalf("member: status %d, want 403", status)
		}
	})
}

// TestConsoleRouteSharedLobbyBindsOwnBox pins the two-box System Link case
// (§10.13 "overlay pages via ?console= keep working"): when both hosts see
// each other as lobby peers, the anonymous door on a box's own console name
// lands on THAT box (its local machine), not on whichever host is listed
// first; a spectator key bound to the other box still reads the same name as
// a peer of its own box.
func TestConsoleRouteSharedLobbyBindsOwnBox(t *testing.T) {
	app, d := newOverlayAppWith(t, sharedLobbyFixture())
	get := func(name, bearer string) (int, map[string]any) {
		return overlayGet(t, app, nil, "/api/overlay/console/"+name, name, bearer, nil, handleOverlayConsole)
	}

	for _, c := range []struct {
		console      string
		wantInstance string
		wantMachine  float64
	}{
		{"stewball32", "beta-play", 0},
		{"STEWBALL32", "beta-play", 0},
		{"stream", "beta-stream", 0},
	} {
		status, body := get(c.console, "")
		if status != http.StatusOK {
			t.Fatalf("anonymous %q: status %d body %v, want 200", c.console, status, body)
		}
		if body["instance"] != c.wantInstance || body["machine_index"] != c.wantMachine {
			t.Fatalf("anonymous %q resolved to %v/%v, want %s/%v", c.console,
				body["instance"], body["machine_index"], c.wantInstance, c.wantMachine)
		}
	}

	// A key bound to beta-stream sees stewball32 as beta-stream's peer.
	_, streamKey := pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:beta-stream"})
	status, body := get("stewball32", streamKey)
	if status != http.StatusOK || body["instance"] != "beta-stream" || body["machine_index"] != float64(1) {
		t.Fatalf("beta-stream key on stewball32: status %d body %v, want 200 beta-stream/1", status, body)
	}
	// And the mirror: a beta-play key sees stream as beta-play's peer.
	_, playKey := pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:beta-play"})
	status, body = get("stream", playKey)
	if status != http.StatusOK || body["instance"] != "beta-play" || body["machine_index"] != float64(1) {
		t.Fatalf("beta-play key on stream: status %d body %v, want 200 beta-play/1", status, body)
	}

	if status, _ := get("GreenBox", ""); status != http.StatusNotFound {
		t.Fatalf("GreenBox: status %d, want 404", status)
	}
}

// TestConsolesRouteRequiresListScope pins the other half of R-14:
// GET /api/overlay/consoles is gated by overlay.list_consoles — 401 with no
// credential, 403 without the scope, 200 for overlay_manager / admin and a
// machine key carrying the scope.
func TestConsolesRouteRequiresListScope(t *testing.T) {
	app, d := newOverlayApp(t)
	list := func(auth *core.Record, bearer string) int {
		status, _ := overlayGet(t, app, auth, "/api/overlay/consoles", "", bearer,
			requireOverlay(authz.ActionOverlayListConsoles), handleOverlayConsoleList)
		return status
	}

	if status := list(nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous: status %d, want 401", status)
	}

	member := pbtest.NewUser(t, app, "member@test.dev")
	pbtest.GrantRole(t, app, member.Id, "member")
	if status := list(member, ""); status != http.StatusForbidden {
		t.Fatalf("member: status %d, want 403", status)
	}

	mgr := pbtest.NewUser(t, app, "ovm@test.dev")
	pbtest.GrantRole(t, app, mgr.Id, "overlay_manager")
	if status := list(mgr, ""); status != http.StatusOK {
		t.Fatalf("overlay_manager: status %d, want 200", status)
	}

	admin := pbtest.NewUser(t, app, "admin@test.dev")
	pbtest.GrantRole(t, app, admin.Id, "admin")
	if status := list(admin, ""); status != http.StatusOK {
		t.Fatalf("admin: status %d, want 200", status)
	}

	_, machine := pbtest.MintToken(t, app, d, "machine", []string{"overlay.list_consoles"})
	if status := list(nil, machine); status != http.StatusOK {
		t.Fatalf("machine key: status %d, want 200", status)
	}

	_, spectator := pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:beta-stream"})
	if status := list(nil, spectator); status != http.StatusForbidden {
		t.Fatalf("spectator key: status %d, want 403", status)
	}
}

// wireFake is overlayFake plus the Ctl() the scraper routes gate on
// (scraperroutes.Wire / UpstreamDown). With connected=false every proxied
// Inspect fails fast with xcclient.ErrUpstreamDown in production, which the
// bool-returning Inspect port can only report as "not present" — so the fake
// carries no states either, mirroring what the resolver would actually see.
type wireFake struct {
	overlayFake
	connected bool
}

func (f wireFake) Ctl() *xcclient.Ctl {
	return &xcclient.Ctl{Connected: func() bool { return f.connected }}
}

// TestConsoleRouteUpstreamDownIsNotAbsent pins xc-studio F0.4 on
// GET /api/overlay/console/{name}: a manager that cannot be inspected answers
// 503 {"error":"upstream"} (no manager at all: 503 as before), while "no live
// lobby sees that console" stays a 404 — the caster is told "the scraper is
// down" and "start a game" respectively, never one for the other.
func TestConsoleRouteUpstreamDownIsNotAbsent(t *testing.T) {
	fx := fixture()
	app, _ := newOverlayAppWith(t, fx)
	mk := func(connected bool, states map[string]scraperiface.InspectState) wireFake {
		fs := &pbtest.FakeScraper{}
		for _, info := range fx.infos {
			fs.AddInstance(info.Name, info.XboxName)
		}
		return wireFake{overlayFake{FakeScraper: fs, states: states}, connected}
	}
	cases := []struct {
		name       string
		manager    scraperiface.Service
		console    string
		wantStatus int
		wantError  string
	}{
		// No manager wired at all: the pre-existing 503.
		{"nil manager", nil, "stream", http.StatusServiceUnavailable, "scraper not running"},
		// Wire mode, stream away: Inspect fails fast (no states), which used
		// to fall through to the resolver's "console not found".
		{"upstream down", mk(false, nil), "stream", http.StatusServiceUnavailable, "upstream"},
		// Wire mode, stream up, name unknown to every live lobby.
		{"no match", mk(true, fx.states), "GreenBox", http.StatusNotFound, "console not found in any live lobby"},
		// Wire mode, stream up, the own console of a live box.
		{"match", mk(true, fx.states), "stream", http.StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scraperroutes.SetManager(c.manager)
			status, body := overlayGet(t, app, nil, "/api/overlay/console/"+c.console, c.console, "", nil, handleOverlayConsole)
			if status != c.wantStatus {
				t.Fatalf("status %d body %v, want %d", status, body, c.wantStatus)
			}
			if c.wantError != "" && body["error"] != c.wantError {
				t.Fatalf("body %v, want error %q", body, c.wantError)
			}
			if c.wantStatus == http.StatusOK && body["instance"] != "beta-stream" {
				t.Fatalf("body %v, want instance beta-stream", body)
			}
		})
	}
}
