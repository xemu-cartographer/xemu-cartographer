package authz_test

import (
	"strings"
	"testing"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/authztest"
)

// Fixtures shared by the matrix.
const (
	uID    = "u1"
	boxA   = "box1"
	boxB   = "box2"
	teamID = "t1"
)

func user(scopes ...string) authz.Principal {
	return authz.Principal{Kind: authz.KindPBUser, ID: uID, UserID: uID, Collection: "users", Scopes: authz.CanonScopes(scopes), Level: 10}
}

func adminUser() authz.Principal {
	p := user(authz.SeedRoles[0].Scopes...)
	p.Roles = []string{"admin"}
	p.Level = 100
	return p
}

func machine(scopes ...string) authz.Principal {
	return authz.Principal{Kind: authz.KindMachine, ID: "mk_1", Scopes: authz.CanonScopes(scopes)}
}

func spectator(bound string, scopes ...string) authz.Principal {
	p := authz.Principal{Kind: authz.KindSpectator, ID: "sp_1", Scopes: authz.CanonScopes(scopes)}
	if bound != "" {
		p.Bound = map[string]string{"instance": bound}
	}
	return p
}

func device(bound string, scopes ...string) authz.Principal {
	p := authz.Principal{Kind: authz.KindDevice, ID: "dv_1", Scopes: authz.CanonScopes(scopes)}
	if bound != "" {
		p.Bound = map[string]string{"instance": bound}
	}
	return p
}

func discord(perms, userID string) authz.Principal {
	return authz.Principal{Kind: authz.KindDiscord, ID: "123", UserID: userID, Extra: map[string]string{"guild_id": "g1", "member_permissions": perms}}
}

func hostBare(inst string) authz.Resource {
	return authz.RoomRes(authz.Room{Type: "host", Instance: inst})
}
func hostClass(inst, class string) authz.Resource {
	return authz.RoomRes(authz.Room{Type: "host", Instance: inst, Class: class})
}

var (
	anonScopes = authz.SeedRoles[4].Scopes
	roomAdmin  = authz.RoomRes(authz.Room{Type: "admin"})
	roomPublic = authz.RoomRes(authz.Room{Type: "public"})
)

// baseDeps answers "no" to everything relational; individual cases override.
func baseDeps() *authztest.FakeDeps {
	return &authztest.FakeDeps{
		Levels: map[string]int{"admin": 100, "organizer": 50, "member": 10},
		Admins: 2,
	}
}

func rosteredDeps() *authztest.FakeDeps {
	d := baseDeps()
	d.Rostered = map[string]bool{authztest.Key(uID, boxA): true}
	return d
}

func ownerDeps() *authztest.FakeDeps {
	d := baseDeps()
	d.Owned = map[string]string{uID: boxA}
	return d
}

func authorityDeps() *authztest.FakeDeps {
	d := baseDeps()
	d.Authority = map[string]bool{authztest.Key(uID, teamID): true}
	return d
}

type matrixCase struct {
	name   string
	deps   authz.Deps
	p      authz.Principal
	a      authz.Action
	r      authz.Resource
	allow  bool
	reason string // exact, or prefix when it ends with ":"
}

func (c matrixCase) check(t *testing.T) {
	t.Helper()
	got := authz.CanWith(c.deps, c.p, c.a, c.r)
	if got.Allow != c.allow {
		t.Fatalf("CanWith(%s, %s, %+v) = %+v, want allow=%v", c.p.Kind, c.a, c.r, got, c.allow)
	}
	if c.reason != "" && got.Reason != c.reason {
		t.Fatalf("CanWith(%s, %s) reason = %q, want %q", c.p.Kind, c.a, got.Reason, c.reason)
	}
	if strings.HasPrefix(got.Reason, "scope:") {
		if got.Matched == "" || got.Reason != "scope:"+got.Matched {
			t.Fatalf("scope allow must set Matched: %+v", got)
		}
	} else if got.Matched != "" {
		t.Fatalf("Matched set on a non-scope decision: %+v", got)
	}
	if authz.Can(c.deps, c.p, c.a, c.r) != got.Allow {
		t.Fatal("Can disagrees with CanWith")
	}
}

// TestCanMatrix has one subtest per non-empty cell of the §4 table, named
// <action>/<kind>/<scope|pred|deny>, with every predicate exercised true and
// false.
func TestCanMatrix(t *testing.T) {
	d := baseDeps()
	cases := []matrixCase{
		// ---- room.join host bare: pb_user S ∨ rostered ∨ box_owner ----
		{"room.join/pb_user/scope:bare", d, adminUser(), authz.ActionRoomJoin, hostBare(boxA), true, "scope:room.join:*"},
		{"room.join/pb_user/scope:bare-literal", d, user("room.join:host:box1"), authz.ActionRoomJoin, hostBare(boxA), true, "scope:room.join:host:box1"},
		{"room.join/pb_user/scope:bare-class-scope-misses", rosteredDeps(), user("room.join:host:*:tick"), authz.ActionRoomJoin, hostBare(boxB), false, "pred:rostered|box_owner"},
		{"room.join/pb_user/pred:rostered-true", rosteredDeps(), user(), authz.ActionRoomJoin, hostBare(boxA), true, "pred:rostered"},
		{"room.join/pb_user/pred:rostered-false", rosteredDeps(), user(), authz.ActionRoomJoin, hostBare(boxB), false, "pred:rostered|box_owner"},
		{"room.join/pb_user/pred:box_owner-true", ownerDeps(), user(), authz.ActionRoomJoin, hostBare(boxA), true, "pred:box_owner"},
		{"room.join/pb_user/pred:box_owner-false", ownerDeps(), user(), authz.ActionRoomJoin, hostBare(boxB), false, "pred:rostered|box_owner"},
		{"room.join/machine/kind:bare", d, machine("*"), authz.ActionRoomJoin, hostBare(boxA), false, "kind"},
		{"room.join/spectator/kind:bare", d, spectator(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostBare(boxA), false, "kind"},
		{"room.join/device/kind:bare", d, device(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostBare(boxA), false, "kind"},
		{"room.join/anonymous/kind:bare", d, authz.Anonymous(boxA, anonScopes), authz.ActionRoomJoin, hostBare(boxA), false, "kind"},
		{"room.join/discord/kind:bare", d, discord("32", ""), authz.ActionRoomJoin, hostBare(boxA), false, "kind"},

		// ---- room.join host class ----
		{"room.join/pb_user/scope:class", d, adminUser(), authz.ActionRoomJoin, hostClass(boxA, "tick"), true, "scope:room.join:*"},
		{"room.join/pb_user/pred:class-rostered-true", rosteredDeps(), user(), authz.ActionRoomJoin, hostClass(boxA, "tick"), true, "pred:rostered"},
		{"room.join/pb_user/pred:class-rostered-false", rosteredDeps(), user(), authz.ActionRoomJoin, hostClass(boxB, "tick"), false, "pred:rostered|box_owner"},
		{"room.join/pb_user/pred:class-box_owner-true", ownerDeps(), user(), authz.ActionRoomJoin, hostClass(boxA, "game_filtered"), true, "pred:box_owner"},
		{"room.join/machine/scope:class", d, machine("room.join:host:*:tick"), authz.ActionRoomJoin, hostClass(boxA, "tick"), true, "scope:room.join:host:*:tick"},
		{"room.join/machine/scope:class-star", d, machine("*"), authz.ActionRoomJoin, hostClass(boxA, "tick"), true, "scope:*"},
		{"room.join/machine/scope:class-none", d, machine("lan.*"), authz.ActionRoomJoin, hostClass(boxA, "tick"), false, "no_scope"},
		{"room.join/spectator/scope+pred:bound-true", d, spectator(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass(boxA, "tick"), true, "pred:bound"},
		{"room.join/spectator/scope+pred:bound-false", d, spectator(boxB, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass(boxA, "tick"), false, "pred:bound"},
		{"room.join/spectator/scope+pred:no-scope", d, spectator(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass(boxA, "game"), false, "no_scope"},
		{"room.join/spectator/scope+pred:unbound", d, spectator("", "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass(boxA, "tick"), false, "pred:bound"},
		// The binding is compared under the same fold the scope match uses
		// (scopes are Canon-lowercased; room / URL instance names are not), so
		// a differently-cased live name is the same instance — and a different
		// name still is not.
		{"room.join/spectator/scope+pred:bound-case-fold", d, spectator(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass("Box1", "tick"), true, "pred:bound"},
		{"room.join/spectator/scope+pred:bound-case-fold-miss", d, spectator(boxA, "room.join:host:*:tick"), authz.ActionRoomJoin, hostClass("Box2", "tick"), false, "pred:bound"},
		{"room.join/device/scope+pred:bound-case-fold", d, device("BOX1", "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass(boxA, "tick"), true, "pred:bound"},
		{"room.join/pb_user/pred:box_owner-case-fold", ownerDeps(), user(), authz.ActionRoomJoin, hostBare("Box1"), true, "pred:box_owner"},
		{"room.join/device/scope+pred:bound-true", d, device(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass(boxA, "tick"), true, "pred:bound"},
		{"room.join/device/scope+pred:bound-false", d, device(boxB, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostClass(boxA, "tick"), false, "pred:bound"},
		{"room.join/anonymous/scope+pred:bound-true", d, authz.Anonymous(boxA, anonScopes), authz.ActionRoomJoin, hostClass(boxA, "game_filtered"), true, "pred:bound"},
		{"room.join/anonymous/scope+pred:bound-false", d, authz.Anonymous(boxB, anonScopes), authz.ActionRoomJoin, hostClass(boxA, "game_filtered"), false, "pred:bound"},
		{"room.join/anonymous/scope+pred:no-scope-raw-game", d, authz.Anonymous(boxA, anonScopes), authz.ActionRoomJoin, hostClass(boxA, "game"), false, "no_scope"},
		{"room.join/anonymous/scope+pred:nobody", d, authz.Nobody(), authz.ActionRoomJoin, hostClass(boxA, "tick"), false, "no_scope"},
		{"room.join/discord/kind:class", d, discord("32", ""), authz.ActionRoomJoin, hostClass(boxA, "tick"), false, "kind"},

		// ---- room.join host:all / host:summary ----
		{"room.join/pb_user/scope:summary", d, adminUser(), authz.ActionRoomJoin, hostBare("summary"), true, "scope:room.join:*"},
		{"room.join/pb_user/scope:all", d, adminUser(), authz.ActionRoomJoin, hostBare("all"), true, "scope:room.join:*"},
		{"room.join/pb_user/scope:summary-none", rosteredDeps(), user(), authz.ActionRoomJoin, hostBare("summary"), false, "no_scope"},
		{"room.join/machine/scope:summary", d, machine("room.join:host:summary"), authz.ActionRoomJoin, hostBare("summary"), true, "scope:room.join:host:summary"},
		{"room.join/machine/scope:summary-none", d, machine("room.join:host:*:tick"), authz.ActionRoomJoin, hostBare("summary"), false, "no_scope"},
		{"room.join/spectator/kind:summary", d, spectator(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostBare("summary"), false, "kind"},
		{"room.join/device/kind:summary", d, device(boxA, "room.join:host:box1:tick"), authz.ActionRoomJoin, hostBare("summary"), false, "kind"},
		{"room.join/anonymous/kind:summary", d, authz.Anonymous(boxA, anonScopes), authz.ActionRoomJoin, hostBare("all"), false, "kind"},

		// ---- room.join admin ----
		{"room.join/pb_user/scope:admin", d, adminUser(), authz.ActionRoomJoin, roomAdmin, true, "scope:admin.*"},
		{"room.join/pb_user/scope:admin-explicit", d, user("admin.admin"), authz.ActionRoomJoin, roomAdmin, true, "scope:admin.admin"},
		{"room.join/pb_user/scope:admin-none", d, user("room.join:*"), authz.ActionRoomJoin, roomAdmin, false, "no_scope"},
		{"room.join/machine/kind:admin", d, machine("*"), authz.ActionRoomJoin, roomAdmin, false, "kind"},
		{"room.join/spectator/kind:admin", d, spectator(boxA), authz.ActionRoomJoin, roomAdmin, false, "kind"},
		{"room.join/anonymous/kind:admin", d, authz.Anonymous(boxA, anonScopes), authz.ActionRoomJoin, roomAdmin, false, "kind"},

		// ---- room.join public / other registered ----
		{"room.join/pb_user/pred:always", d, user(), authz.ActionRoomJoin, roomPublic, true, "pred:always"},
		{"room.join/machine/pred:always", d, machine(), authz.ActionRoomJoin, roomPublic, true, "pred:always"},
		{"room.join/spectator/pred:always", d, spectator(""), authz.ActionRoomJoin, roomPublic, true, "pred:always"},
		{"room.join/device/pred:always", d, device(""), authz.ActionRoomJoin, roomPublic, true, "pred:always"},
		{"room.join/anonymous/pred:always", d, authz.Nobody(), authz.ActionRoomJoin, roomPublic, true, "pred:always"},
		{"room.join/anonymous/pred:always-other", d, authz.Nobody(), authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "lobby"}), true, "pred:always"},
		{"room.join/discord/kind:public", d, discord("32", ""), authz.ActionRoomJoin, roomPublic, false, "kind"},

		// ---- ws.send: never a authz.Can action ----
		{"ws.send/pb_user/kind", d, adminUser(), authz.ActionWSSend, authz.Global(), false, "kind"},
		{"ws.send/machine/kind", d, machine("*"), authz.ActionWSSend, authz.Global(), false, "kind"},
		{"ws.send/anonymous/kind", d, authz.Nobody(), authz.ActionWSSend, authz.Global(), false, "kind"},

		// ---- scraper.probe ----
		{"scraper.probe/pb_user/scope", d, adminUser(), authz.ActionScraperProbe, authz.Instance(boxA), true, "scope:scraper.*"},
		{"scraper.probe/pb_user/scope-none", rosteredDeps(), user(), authz.ActionScraperProbe, authz.Instance(boxA), false, "no_scope"},
		{"scraper.probe/machine/scope", d, machine("scraper.probe:*"), authz.ActionScraperProbe, authz.Instance(boxA), true, "scope:scraper.probe:*"},
		{"scraper.probe/machine/scope-none", d, machine("scraper.state:*"), authz.ActionScraperProbe, authz.Instance(boxA), false, "no_scope"},
		{"scraper.probe/spectator/kind", d, spectator(boxA, "scraper.state:box1"), authz.ActionScraperProbe, authz.Instance(boxA), false, "kind"},
		{"scraper.probe/device/kind", d, device(boxA), authz.ActionScraperProbe, authz.Instance(boxA), false, "kind"},
		{"scraper.probe/anonymous/kind", d, authz.Anonymous(boxA, anonScopes), authz.ActionScraperProbe, authz.Instance(boxA), false, "kind"},

		// ---- scraper.events / scraper.state ----
		{"scraper.events/pb_user/scope", d, adminUser(), authz.ActionScraperEvents, authz.Instance(boxA), true, "scope:scraper.*"},
		{"scraper.events/pb_user/pred:rostered-true", rosteredDeps(), user(), authz.ActionScraperEvents, authz.Instance(boxA), true, "pred:rostered"},
		{"scraper.events/pb_user/pred:rostered-false", rosteredDeps(), user(), authz.ActionScraperEvents, authz.Instance(boxB), false, "pred:rostered|box_owner"},
		{"scraper.events/pb_user/pred:box_owner-true", ownerDeps(), user(), authz.ActionScraperEvents, authz.Instance(boxA), true, "pred:box_owner"},
		{"scraper.events/machine/scope", d, machine("scraper.*"), authz.ActionScraperEvents, authz.Instance(boxA), true, "scope:scraper.*"},
		{"scraper.events/spectator/scope+pred:bound-true", d, spectator(boxA, "scraper.events:box1"), authz.ActionScraperEvents, authz.Instance(boxA), true, "pred:bound"},
		{"scraper.events/spectator/scope+pred:bound-false", d, spectator(boxB, "scraper.events:box1"), authz.ActionScraperEvents, authz.Instance(boxA), false, "pred:bound"},
		{"scraper.events/spectator/scope+pred:no-scope", d, spectator(boxA, "scraper.state:box1"), authz.ActionScraperEvents, authz.Instance(boxA), false, "no_scope"},
		{"scraper.events/device/scope+pred:bound-true", d, device(boxA, "scraper.events:box1"), authz.ActionScraperEvents, authz.Instance(boxA), true, "pred:bound"},
		{"scraper.events/anonymous/kind", d, authz.Anonymous(boxA, anonScopes), authz.ActionScraperEvents, authz.Instance(boxA), false, "kind"},
		{"scraper.state/pb_user/scope", d, adminUser(), authz.ActionScraperState, authz.Instance(boxA), true, "scope:scraper.*"},
		{"scraper.state/pb_user/pred:rostered-true", rosteredDeps(), user(), authz.ActionScraperState, authz.Instance(boxA), true, "pred:rostered"},
		{"scraper.state/pb_user/pred:box_owner-false", ownerDeps(), user(), authz.ActionScraperState, authz.Instance(boxB), false, "pred:rostered|box_owner"},
		{"scraper.state/machine/scope", d, machine("scraper.state:box1"), authz.ActionScraperState, authz.Instance(boxA), true, "scope:scraper.state:box1"},
		{"scraper.state/machine/scope-none", d, machine("scraper.state:box2"), authz.ActionScraperState, authz.Instance(boxA), false, "no_scope"},
		{"scraper.state/spectator/scope+pred:bound-true", d, spectator(boxA, "scraper.state:box1"), authz.ActionScraperState, authz.Instance(boxA), true, "pred:bound"},
		{"scraper.state/device/scope+pred:bound-false", d, device(boxB, "scraper.state:box1"), authz.ActionScraperState, authz.Instance(boxA), false, "pred:bound"},
		{"scraper.state/anonymous/kind", d, authz.Anonymous(boxA, anonScopes), authz.ActionScraperState, authz.Instance(boxA), false, "kind"},
		{"scraper.state/discord/kind", d, discord("32", ""), authz.ActionScraperState, authz.Instance(boxA), false, "kind"},

		// ---- overlay.read_state ----
		{"overlay.read_state/pb_user/scope", d, user("overlay.read_state:*"), authz.ActionOverlayReadState, authz.Instance(boxA), true, "scope:overlay.read_state:*"},
		{"overlay.read_state/pb_user/scope-none", rosteredDeps(), user(), authz.ActionOverlayReadState, authz.Instance(boxA), false, "no_scope"},
		{"overlay.read_state/machine/scope", d, machine("overlay.*"), authz.ActionOverlayReadState, authz.Instance(boxA), true, "scope:overlay.*"},
		{"overlay.read_state/spectator/scope+pred:bound-true", d, spectator(boxA, "overlay.read_state:box1"), authz.ActionOverlayReadState, authz.Instance(boxA), true, "pred:bound"},
		{"overlay.read_state/spectator/scope+pred:bound-false", d, spectator(boxB, "overlay.read_state:box1"), authz.ActionOverlayReadState, authz.Instance(boxA), false, "pred:bound"},
		{"overlay.read_state/device/scope+pred:bound-true", d, device(boxA, "overlay.read_state:box1"), authz.ActionOverlayReadState, authz.Instance(boxA), true, "pred:bound"},
		{"overlay.read_state/device/scope+pred:no-scope", d, device(boxA, "box.view:box1"), authz.ActionOverlayReadState, authz.Instance(boxA), false, "no_scope"},
		{"overlay.read_state/anonymous/scope+pred:bound-true", d, authz.Anonymous(boxA, anonScopes), authz.ActionOverlayReadState, authz.Instance(boxA), true, "pred:bound"},
		{"overlay.read_state/anonymous/scope+pred:bound-false", d, authz.Anonymous(boxB, anonScopes), authz.ActionOverlayReadState, authz.Instance(boxA), false, "pred:bound"},
		{"overlay.read_state/anonymous/scope+pred:unbound", d, authz.Anonymous("", anonScopes), authz.ActionOverlayReadState, authz.Instance(boxA), false, "pred:bound"},
		{"overlay.read_state/anonymous/scope+pred:nobody", d, authz.Nobody(), authz.ActionOverlayReadState, authz.Instance(boxA), false, "no_scope"},
		{"overlay.read_state/discord/kind", d, discord("32", ""), authz.ActionOverlayReadState, authz.Instance(boxA), false, "kind"},

		// ---- overlay.list_consoles / overlay.mint ----
		{"overlay.list_consoles/pb_user/scope", d, user(authz.SeedRoles[3].Scopes...), authz.ActionOverlayListConsoles, authz.Global(), true, "scope:overlay.list_consoles"},
		{"overlay.list_consoles/pb_user/scope-none", d, user(), authz.ActionOverlayListConsoles, authz.Global(), false, "no_scope"},
		{"overlay.list_consoles/machine/scope", d, machine("overlay.list_consoles"), authz.ActionOverlayListConsoles, authz.Global(), true, "scope:overlay.list_consoles"},
		{"overlay.list_consoles/spectator/kind", d, spectator(boxA, "overlay.read_state:box1"), authz.ActionOverlayListConsoles, authz.Global(), false, "kind"},
		{"overlay.list_consoles/anonymous/kind", d, authz.Anonymous(boxA, anonScopes), authz.ActionOverlayListConsoles, authz.Global(), false, "kind"},
		{"overlay.mint/pb_user/scope", d, user(authz.SeedRoles[3].Scopes...), authz.ActionOverlayMint, authz.Global(), true, "scope:overlay.mint"},
		{"overlay.mint/pb_user/scope-none", d, user("overlay.read_state:*"), authz.ActionOverlayMint, authz.Global(), false, "no_scope"},
		{"overlay.mint/machine/scope", d, machine("overlay.*"), authz.ActionOverlayMint, authz.Global(), true, "scope:overlay.*"},
		{"overlay.mint/device/kind", d, device(boxA, "box.view:box1"), authz.ActionOverlayMint, authz.Global(), false, "kind"},

		// ---- box.view / box.drive / box.read ----
		{"box.view/pb_user/scope", d, adminUser(), authz.ActionBoxView, authz.Container(boxA), true, "scope:box.*"},
		{"box.view/pb_user/pred:rostered-true", rosteredDeps(), user(), authz.ActionBoxView, authz.Container(boxA), true, "pred:rostered"},
		{"box.view/pb_user/pred:rostered-false", rosteredDeps(), user(), authz.ActionBoxView, authz.Container(boxB), false, "pred:rostered|box_owner"},
		{"box.view/pb_user/pred:box_owner-true", ownerDeps(), user(), authz.ActionBoxView, authz.Container(boxA), true, "pred:box_owner"},
		{"box.view/machine/kind", d, machine("*"), authz.ActionBoxView, authz.Container(boxA), false, "kind"},
		{"box.view/spectator/kind", d, spectator(boxA, "overlay.read_state:box1"), authz.ActionBoxView, authz.Container(boxA), false, "kind"},
		{"box.view/device/scope+pred:bound-true", d, device(boxA, "box.view:box1"), authz.ActionBoxView, authz.Container(boxA), true, "pred:bound"},
		{"box.view/device/scope+pred:bound-false", d, device(boxB, "box.view:box1"), authz.ActionBoxView, authz.Container(boxA), false, "pred:bound"},
		{"box.view/device/scope+pred:bound-case-fold", d, device(boxA, "box.view:box1"), authz.ActionBoxView, authz.Container("Box1"), true, "pred:bound"},
		{"box.view/pb_user/pred:box_owner-case-fold", ownerDeps(), user(), authz.ActionBoxView, authz.Container("BOX1"), true, "pred:box_owner"},
		{"box.view/device/scope+pred:no-scope", d, device(boxA, "box.drive:box1"), authz.ActionBoxView, authz.Container(boxA), false, "no_scope"},
		{"box.view/anonymous/kind", d, authz.Anonymous(boxA, anonScopes), authz.ActionBoxView, authz.Container(boxA), false, "kind"},
		{"box.drive/pb_user/scope", d, adminUser(), authz.ActionBoxDrive, authz.Container(boxA), true, "scope:box.*"},
		{"box.drive/pb_user/pred:rostered-true", rosteredDeps(), user(), authz.ActionBoxDrive, authz.Container(boxA), true, "pred:rostered"},
		{"box.drive/pb_user/pred:box_owner-false", ownerDeps(), user(), authz.ActionBoxDrive, authz.Container(boxB), false, "pred:rostered|box_owner"},
		{"box.drive/device/scope+pred:bound-true", d, device(boxA, "box.drive:box1"), authz.ActionBoxDrive, authz.Container(boxA), true, "pred:bound"},
		{"box.drive/device/scope+pred:bound-false", d, device(boxB, "box.drive:box1"), authz.ActionBoxDrive, authz.Container(boxA), false, "pred:bound"},
		{"box.drive/machine/kind", d, machine("*"), authz.ActionBoxDrive, authz.Container(boxA), false, "kind"},
		{"box.read/pb_user/scope", d, adminUser(), authz.ActionBoxRead, authz.Container(boxA), true, "scope:box.*"},
		{"box.read/pb_user/pred:rostered-true", rosteredDeps(), user(), authz.ActionBoxRead, authz.Container(boxA), true, "pred:rostered"},
		{"box.read/pb_user/pred:box_owner-true", ownerDeps(), user(), authz.ActionBoxRead, authz.Container(boxA), true, "pred:box_owner"},
		{"box.read/pb_user/pred:box_owner-false", ownerDeps(), user(), authz.ActionBoxRead, authz.Container(boxB), false, "pred:rostered|box_owner"},
		{"box.read/device/scope+pred:bound-true", d, device(boxA, "box.read:box1"), authz.ActionBoxRead, authz.Container(boxA), true, "pred:bound"},
		{"box.read/device/scope+pred:bound-false", d, device(boxB, "box.read:box1"), authz.ActionBoxRead, authz.Container(boxA), false, "pred:bound"},
		{"box.read/machine/kind", d, machine("*"), authz.ActionBoxRead, authz.Container(boxA), false, "kind"},
		{"box.read/spectator/kind", d, spectator(boxA), authz.ActionBoxRead, authz.Container(boxA), false, "kind"},

		// ---- box.control / box.teardown: S ∨ box_owner ----
		{"box.control/pb_user/scope", d, adminUser(), authz.ActionBoxControl, authz.Container(boxA), true, "scope:box.*"},
		{"box.control/pb_user/pred:box_owner-true", ownerDeps(), user(), authz.ActionBoxControl, authz.Container(boxA), true, "pred:box_owner"},
		{"box.control/pb_user/pred:box_owner-false", ownerDeps(), user(), authz.ActionBoxControl, authz.Container(boxB), false, "pred:box_owner"},
		{"box.control/pb_user/pred:rostered-not-enough", rosteredDeps(), user(), authz.ActionBoxControl, authz.Container(boxA), false, "pred:box_owner"},
		{"box.control/device/kind", d, device(boxA, "box.control:box1"), authz.ActionBoxControl, authz.Container(boxA), false, "kind"},
		{"box.control/machine/kind", d, machine("*"), authz.ActionBoxControl, authz.Container(boxA), false, "kind"},
		{"box.teardown/pb_user/scope", d, adminUser(), authz.ActionBoxTeardown, authz.Container(boxA), true, "scope:box.*"},
		{"box.teardown/pb_user/pred:box_owner-true", ownerDeps(), user(), authz.ActionBoxTeardown, authz.Container(boxA), true, "pred:box_owner"},
		{"box.teardown/pb_user/pred:box_owner-false", ownerDeps(), user(), authz.ActionBoxTeardown, authz.Container(boxB), false, "pred:box_owner"},
		{"box.teardown/device/kind", d, device(boxA, "box.teardown:box1"), authz.ActionBoxTeardown, authz.Container(boxA), false, "kind"},

		// ---- box.provision: P:authed ----
		{"box.provision/pb_user/pred:authed-true", d, user(), authz.ActionBoxProvision, authz.ISO("iso1"), true, "pred:authed"},
		{"box.provision/pb_user/pred:authed-false", d, authz.Principal{Kind: authz.KindPBUser, ID: uID}, authz.ActionBoxProvision, authz.ISO("iso1"), false, "pred:authed"},
		{"box.provision/pb_user/pred:scope-ignored", d, authz.Principal{Kind: authz.KindPBUser, Scopes: []string{"*"}}, authz.ActionBoxProvision, authz.ISO("iso1"), false, "pred:authed"},
		{"box.provision/machine/kind", d, machine("*"), authz.ActionBoxProvision, authz.ISO("iso1"), false, "kind"},
		{"box.provision/anonymous/kind", d, authz.Nobody(), authz.ActionBoxProvision, authz.ISO("iso1"), false, "kind"},

		// ---- box.provision_named ----
		{"box.provision_named/pb_user/scope", d, adminUser(), authz.ActionBoxProvisionNamed, authz.ISO("iso1"), true, "scope:box.*"},
		{"box.provision_named/pb_user/scope-none", d, user(), authz.ActionBoxProvisionNamed, authz.ISO("iso1"), false, "no_scope"},
		{"box.provision_named/machine/kind", d, machine("*"), authz.ActionBoxProvisionNamed, authz.ISO("iso1"), false, "kind"},

		// ---- container.manage ----
		{"container.manage/pb_user/scope:container", d, adminUser(), authz.ActionContainerManage, authz.Container(boxA), true, "scope:container.manage:*"},
		{"container.manage/pb_user/scope:global", d, adminUser(), authz.ActionContainerManage, authz.Global(), true, "scope:container.manage:*"},
		{"container.manage/pb_user/scope-none", ownerDeps(), user(), authz.ActionContainerManage, authz.Container(boxA), false, "no_scope"},
		{"container.manage/machine/kind", d, machine("*"), authz.ActionContainerManage, authz.Global(), false, "kind"},

		// ---- admin.<group> ----
		{"admin.admin/pb_user/scope", d, adminUser(), authz.ActionAdminAdmin, authz.Global(), true, "scope:admin.*"},
		{"admin.admin/pb_user/scope-none", d, user("admin.users"), authz.ActionAdminAdmin, authz.Global(), false, "no_scope"},
		{"admin.admin/machine/kind", d, machine("*"), authz.ActionAdminAdmin, authz.Global(), false, "kind"},
		{"admin.users/pb_user/scope", d, user("admin.users"), authz.ActionAdminUsers, authz.Global(), true, "scope:admin.users"},
		{"admin.users/machine/kind", d, machine("admin.*"), authz.ActionAdminUsers, authz.Global(), false, "kind"},
		{"admin.containers/pb_user/scope", d, adminUser(), authz.ActionAdminContainers, authz.Global(), true, "scope:admin.*"},
		{"admin.containers/machine/kind", d, machine("*"), authz.ActionAdminContainers, authz.Global(), false, "kind"},
		{"admin.scraper/pb_user/scope", d, adminUser(), authz.ActionAdminScraper, authz.Global(), true, "scope:admin.*"},
		{"admin.scraper/machine/scope", d, machine("admin.scraper"), authz.ActionAdminScraper, authz.Global(), true, "scope:admin.scraper"},
		{"admin.scraper/machine/scope-none", d, machine("scraper.*"), authz.ActionAdminScraper, authz.Global(), false, "no_scope"},
		{"admin.xemu/pb_user/scope", d, adminUser(), authz.ActionAdminXemu, authz.Global(), true, "scope:admin.*"},
		{"admin.xemu/machine/kind", d, machine("*"), authz.ActionAdminXemu, authz.Global(), false, "kind"},
		{"admin.pod/pb_user/scope", d, adminUser(), authz.ActionAdminPod, authz.Global(), true, "scope:admin.*"},
		{"admin.pod/pb_user/scope-none", d, user(), authz.ActionAdminPod, authz.Global(), false, "no_scope"},
		{"admin.pod/anonymous/kind", d, authz.Nobody(), authz.ActionAdminPod, authz.Global(), false, "kind"},

		// ---- library.manage / iso.set_policy ----
		{"library.manage/pb_user/scope:global", d, user(authz.SeedRoles[1].Scopes...), authz.ActionLibraryManage, authz.Global(), true, "scope:library.manage:*"},
		{"library.manage/pb_user/scope:iso", d, adminUser(), authz.ActionLibraryManage, authz.ISO("iso1"), true, "scope:library.manage:*"},
		{"library.manage/pb_user/scope-none", d, user(), authz.ActionLibraryManage, authz.Global(), false, "no_scope"},
		{"library.manage/machine/kind", d, machine("*"), authz.ActionLibraryManage, authz.Global(), false, "kind"},
		{"iso.set_policy/pb_user/scope", d, user(authz.SeedRoles[1].Scopes...), authz.ActionISOSetPolicy, authz.ISO("iso1"), true, "scope:iso.set_policy:*"},
		{"iso.set_policy/pb_user/scope-none", d, user("library.manage:*"), authz.ActionISOSetPolicy, authz.ISO("iso1"), false, "no_scope"},
		{"iso.set_policy/machine/kind", d, machine("*"), authz.ActionISOSetPolicy, authz.ISO("iso1"), false, "kind"},

		// ---- role.grant / role.revoke: S ∧ level_ok, Deny last_admin ----
		{"role.grant/pb_user/scope+pred:level_ok-true", d, adminUser(), authz.ActionRoleGrant, authz.Role("organizer", "u2"), true, "pred:level_ok"},
		{"role.grant/pb_user/scope+pred:level_ok-equal", d, adminUser(), authz.ActionRoleGrant, authz.Role("admin", "u2"), true, "pred:level_ok"},
		{"role.grant/pb_user/scope+pred:level_ok-false", d, levelled(user("role.*"), 50), authz.ActionRoleGrant, authz.Role("admin", "u2"), false, "pred:level_ok"},
		{"role.grant/pb_user/scope+pred:unknown_role", d, adminUser(), authz.ActionRoleGrant, authz.Role("nope", "u2"), false, "pred:unknown_role"},
		{"role.grant/pb_user/scope+pred:no-scope", d, levelled(user(), 100), authz.ActionRoleGrant, authz.Role("member", "u2"), false, "no_scope"},
		{"role.grant/machine/kind", d, machine("*"), authz.ActionRoleGrant, authz.Role("member", "u2"), false, "kind"},
		{"role.revoke/pb_user/scope+pred:level_ok-true", d, adminUser(), authz.ActionRoleRevoke, authz.Role("organizer", "u2"), true, "pred:level_ok"},
		{"role.revoke/pb_user/scope+pred:level_ok-false", d, levelled(user("role.revoke:*"), 10), authz.ActionRoleRevoke, authz.Role("organizer", "u2"), false, "pred:level_ok"},
		{"role.revoke/pb_user/scope+pred:unknown_role", d, adminUser(), authz.ActionRoleRevoke, authz.Role("nope", "u2"), false, "pred:unknown_role"},
		{"role.revoke/pb_user/deny:last_admin", adminsDeps(1), adminUser(), authz.ActionRoleRevoke, authz.Role("admin", "u2"), false, "deny:last_admin"},
		{"role.revoke/pb_user/deny:last_admin-zero", adminsDeps(0), adminUser(), authz.ActionRoleRevoke, authz.Role("admin", "u2"), false, "deny:last_admin"},
		{"role.revoke/pb_user/deny:last_admin-not-last", adminsDeps(2), adminUser(), authz.ActionRoleRevoke, authz.Role("admin", "u2"), true, "pred:level_ok"},
		{"role.revoke/pb_user/deny:last_admin-other-role", adminsDeps(1), adminUser(), authz.ActionRoleRevoke, authz.Role("organizer", "u2"), true, "pred:level_ok"},
		// Revoking admin from a user who does not hold it removes nothing: a
		// no-op, not a last-admin refusal, even with one admin left.
		{"role.revoke/pb_user/deny:last_admin-target-not-admin", adminsDeps(1), adminUser(), authz.ActionRoleRevoke, authz.Role("admin", "u3"), true, "pred:level_ok"},
		{"role.revoke/superuser/deny:last_admin-target-not-admin", adminsDeps(1), authz.Superuser("s"), authz.ActionRoleRevoke, authz.Role("admin", "u3"), true, "superuser"},
		// A role resource without a target falls back to the count alone.
		{"role.revoke/pb_user/deny:last_admin-no-target", adminsDeps(1), adminUser(), authz.ActionRoleRevoke, bareRole("admin"), false, "deny:last_admin"},
		{"role.revoke/pb_user/deny:last_admin-no-target-not-last", adminsDeps(2), adminUser(), authz.ActionRoleRevoke, bareRole("admin"), true, "pred:level_ok"},
		{"role.revoke/superuser/deny:last_admin", adminsDeps(1), authz.Superuser("s"), authz.ActionRoleRevoke, authz.Role("admin", "u2"), false, "deny:last_admin"},
		{"role.revoke/internal/deny:last_admin-bypassed", adminsDeps(1), authz.Internal("users_soft_delete_pii"), authz.ActionRoleRevoke, authz.Role("admin", "u2"), true, "internal"},
		{"role.revoke/machine/kind", d, machine("*"), authz.ActionRoleRevoke, authz.Role("member", "u2"), false, "kind"},
		{"role.revoke/machine/deny:last_admin-before-kind", adminsDeps(1), machine("*"), authz.ActionRoleRevoke, authz.Role("admin", "u2"), false, "deny:last_admin"},

		// ---- user.moderate / user.soft_delete ----
		{"user.moderate/pb_user/scope", d, adminUser(), authz.ActionUserModerate, authz.User("u2"), true, "scope:user.*"},
		{"user.moderate/pb_user/scope-none", d, user(), authz.ActionUserModerate, authz.User("u2"), false, "no_scope"},
		{"user.moderate/machine/kind", d, machine("*"), authz.ActionUserModerate, authz.User("u2"), false, "kind"},
		{"user.soft_delete/pb_user/scope", d, adminUser(), authz.ActionUserSoftDelete, authz.User("u2"), true, "scope:user.*"},
		{"user.soft_delete/pb_user/scope-none", d, user("user.moderate:*"), authz.ActionUserSoftDelete, authz.User("u2"), false, "no_scope"},
		{"user.soft_delete/discord/kind", d, discord("32", uID), authz.ActionUserSoftDelete, authz.User("u2"), false, "kind"},

		// ---- gamertag.moderate / team.moderate / notification.admin_edit ----
		{"gamertag.moderate/pb_user/scope", d, adminUser(), authz.ActionGamertagModerate, authz.Record("gamertags", "g1", "u2"), true, "scope:gamertag.moderate:*"},
		{"gamertag.moderate/pb_user/scope-none", d, user(), authz.ActionGamertagModerate, authz.Record("gamertags", "g1", uID), false, "no_scope"},
		{"gamertag.moderate/machine/kind", d, machine("*"), authz.ActionGamertagModerate, authz.Record("gamertags", "g1", "u2"), false, "kind"},
		{"team.moderate/pb_user/scope", d, adminUser(), authz.ActionTeamModerate, authz.Record("teams", teamID, "u2"), true, "scope:team.*"},
		{"team.moderate/pb_user/scope-none", authorityDeps(), user(), authz.ActionTeamModerate, authz.Record("teams", teamID, uID), false, "no_scope"},
		{"team.moderate/machine/kind", d, machine("*"), authz.ActionTeamModerate, authz.Record("teams", teamID, "u2"), false, "kind"},
		{"notification.admin_edit/pb_user/scope", d, adminUser(), authz.ActionNotifAdminEdit, authz.Record("notifications", "n1", "u2"), true, "scope:notification.admin_edit:*"},
		{"notification.admin_edit/pb_user/scope-none", d, user(), authz.ActionNotifAdminEdit, authz.Record("notifications", "n1", uID), false, "no_scope"},
		{"notification.admin_edit/anonymous/kind", d, authz.Nobody(), authz.ActionNotifAdminEdit, authz.Record("notifications", "n1", "u2"), false, "kind"},

		// ---- team.invite / team.remove / team.decide: S ∨ team_authority ----
		{"team.invite/pb_user/scope", d, adminUser(), authz.ActionTeamInvite, authz.Team(teamID), true, "scope:team.*"},
		{"team.invite/pb_user/pred:team_authority-true", authorityDeps(), user(), authz.ActionTeamInvite, authz.Team(teamID), true, "pred:team_authority"},
		{"team.invite/pb_user/pred:team_authority-false", authorityDeps(), user(), authz.ActionTeamInvite, authz.Team("t2"), false, "pred:team_authority"},
		{"team.invite/machine/kind", d, machine("*"), authz.ActionTeamInvite, authz.Team(teamID), false, "kind"},
		{"team.remove/pb_user/scope", d, user("team.remove:t1"), authz.ActionTeamRemove, authz.Team(teamID), true, "scope:team.remove:t1"},
		{"team.remove/pb_user/pred:team_authority-true", authorityDeps(), user(), authz.ActionTeamRemove, authz.Team(teamID), true, "pred:team_authority"},
		{"team.remove/pb_user/pred:team_authority-false", d, user(), authz.ActionTeamRemove, authz.Team(teamID), false, "pred:team_authority"},
		{"team.remove/discord/kind", d, discord("32", uID), authz.ActionTeamRemove, authz.Team(teamID), false, "kind"},
		{"team.decide/pb_user/scope", d, adminUser(), authz.ActionTeamDecide, authz.Team(teamID), true, "scope:team.*"},
		{"team.decide/pb_user/pred:team_authority-true", authorityDeps(), user(), authz.ActionTeamDecide, authz.Team(teamID), true, "pred:team_authority"},
		{"team.decide/pb_user/pred:team_authority-false", authorityDeps(), user(), authz.ActionTeamDecide, authz.Team("t2"), false, "pred:team_authority"},
		{"team.decide/anonymous/kind", d, authz.Nobody(), authz.ActionTeamDecide, authz.Team(teamID), false, "kind"},

		// ---- lan.saves.* ----
		{"lan.saves.identity/pb_user/scope", d, adminUser(), authz.ActionLANSavesIdentity, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.identity/pb_user/scope-none", d, user(), authz.ActionLANSavesIdentity, authz.Global(), false, "no_scope"},
		{"lan.saves.identity/machine/scope", d, machine("lan.saves.*"), authz.ActionLANSavesIdentity, authz.Global(), true, "scope:lan.saves.*"},
		{"lan.saves.identity/machine/scope-none", d, machine("lan.sync.*"), authz.ActionLANSavesIdentity, authz.Global(), false, "no_scope"},
		{"lan.saves.identity/anonymous/kind", d, authz.Nobody(), authz.ActionLANSavesIdentity, authz.Global(), false, "kind"},
		{"lan.saves.build/pb_user/scope", d, adminUser(), authz.ActionLANSavesBuild, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.build/machine/scope", d, machine("lan.*"), authz.ActionLANSavesBuild, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.build/spectator/kind", d, spectator(boxA), authz.ActionLANSavesBuild, authz.Global(), false, "kind"},
		{"lan.saves.download/pb_user/scope", d, adminUser(), authz.ActionLANSavesDownload, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.download/machine/scope", d, machine("lan.saves.download"), authz.ActionLANSavesDownload, authz.Global(), true, "scope:lan.saves.download"},
		{"lan.saves.manifest/pb_user/scope", d, adminUser(), authz.ActionLANSavesManifest, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.manifest/machine/scope", d, machine("lan.*"), authz.ActionLANSavesManifest, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.meta/pb_user/scope", d, adminUser(), authz.ActionLANSavesMeta, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.meta/machine/scope", d, machine("lan.*"), authz.ActionLANSavesMeta, authz.Global(), true, "scope:lan.*"},
		{"lan.saves.meta/machine/scope-none", d, machine("lan.saves.build"), authz.ActionLANSavesMeta, authz.Global(), false, "no_scope"},
		{"lan.saves.meta/device/kind", d, device(boxA), authz.ActionLANSavesMeta, authz.Global(), false, "kind"},
		// Seed rows: the browser builder pages reach meta / build / download
		// as organizer, only meta as member; the station-only routes reach
		// neither.
		{"lan.saves.meta/pb_user/seed-member", d, user(authz.SeedRoles[2].Scopes...), authz.ActionLANSavesMeta, authz.Global(), true, "scope:lan.saves.meta"},
		{"lan.saves.meta/pb_user/seed-organizer", d, levelled(user(authz.SeedRoles[1].Scopes...), 50), authz.ActionLANSavesMeta, authz.Global(), true, "scope:lan.saves.meta"},
		{"lan.saves.build/pb_user/seed-member-none", d, user(authz.SeedRoles[2].Scopes...), authz.ActionLANSavesBuild, authz.Global(), false, "no_scope"},
		{"lan.saves.build/pb_user/seed-organizer", d, levelled(user(authz.SeedRoles[1].Scopes...), 50), authz.ActionLANSavesBuild, authz.Global(), true, "scope:lan.saves.build"},
		{"lan.saves.download/pb_user/seed-member-none", d, user(authz.SeedRoles[2].Scopes...), authz.ActionLANSavesDownload, authz.Global(), false, "no_scope"},
		{"lan.saves.download/pb_user/seed-organizer", d, levelled(user(authz.SeedRoles[1].Scopes...), 50), authz.ActionLANSavesDownload, authz.Global(), true, "scope:lan.saves.download"},
		{"lan.saves.file/pb_user/seed-organizer-none", d, levelled(user(authz.SeedRoles[1].Scopes...), 50), authz.ActionLANSavesFile, authz.LANFile("gametype", "abc", ""), false, "no_scope"},
		{"lan.saves.manifest/pb_user/seed-organizer-none", d, levelled(user(authz.SeedRoles[1].Scopes...), 50), authz.ActionLANSavesManifest, authz.Global(), false, "no_scope"},
		{"lan.saves.identity/pb_user/seed-organizer-none", d, levelled(user(authz.SeedRoles[1].Scopes...), 50), authz.ActionLANSavesIdentity, authz.Global(), false, "no_scope"},
		{"lan.sync.manifest/pb_user/seed-organizer-none", d, levelled(user(authz.SeedRoles[1].Scopes...), 50), authz.ActionLANSyncManifest, authz.Global(), false, "no_scope"},

		// ---- lan.saves.file: pb_user S; machine S ∧ station_file ----
		{"lan.saves.file/pb_user/scope", d, adminUser(), authz.ActionLANSavesFile, authz.LANFile("gametype", "abc", ""), true, "scope:lan.*"},
		{"lan.saves.file/pb_user/scope-none", d, user(), authz.ActionLANSavesFile, authz.LANFile("gametype", "abc", uID), false, "no_scope"},
		{"lan.saves.file/machine/scope+pred:station_file-unbound", d, machine("lan.*"), authz.ActionLANSavesFile, profileFile("chief"), true, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-non-profile", d, station("chief"), authz.ActionLANSavesFile, authz.LANFile("gametype", "abc", ""), true, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-true", d, station("chief", "arbiter"), authz.ActionLANSavesFile, profileFile("Chief"), true, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-false", d, station("chief"), authz.ActionLANSavesFile, profileFile("arbiter"), false, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-no-owner", d, station("chief"), authz.ActionLANSavesFile, authz.LANFile("h2-profile", "p9", ""), false, "pred:station_file"},
		// The owner side is every usable tag of the owning user: one shared
		// tag is enough, none shared is refused.
		{"lan.saves.file/machine/scope+pred:station_file-owner-list", d, station("chief"), authz.ActionLANSavesFile, profileFile("Arbiter, CHIEF"), true, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-owner-list-miss", d, station("chief"), authz.ActionLANSavesFile, profileFile("arbiter,sarge"), false, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-owner-list-blank", d, station("chief"), authz.ActionLANSavesFile, profileFile(" , "), false, "pred:station_file"},
		// Kind falls back to the ID prefix when Extra["kind"] is missing; a
		// bound key whose kind cannot be determined at all is refused.
		{"lan.saves.file/machine/scope+pred:station_file-kind-from-id-true", d, station("chief"), authz.ActionLANSavesFile, lanFileNoKind("h2-profile/p1", "chief"), true, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-kind-from-id-false", d, station("chief"), authz.ActionLANSavesFile, lanFileNoKind("ce-profile/p1", "arbiter"), false, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-kind-from-id-non-profile", d, station("chief"), authz.ActionLANSavesFile, lanFileNoKind("gametype/abc", ""), true, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-kind-unknown", d, station("chief"), authz.ActionLANSavesFile, lanFileNoKind("p1", "chief"), false, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:station_file-kind-unknown-unbound", d, machine("lan.*"), authz.ActionLANSavesFile, lanFileNoKind("p1", "chief"), true, "pred:station_file"},
		// A bound list with no usable tag is still bound: nothing intersects.
		{"lan.saves.file/machine/scope+pred:station_file-bound-blank", d, station(" "), authz.ActionLANSavesFile, profileFile("chief"), false, "pred:station_file"},
		{"lan.saves.file/machine/scope+pred:no-scope", d, machine("lan.sync.*"), authz.ActionLANSavesFile, authz.LANFile("gametype", "abc", ""), false, "no_scope"},
		{"lan.saves.file/anonymous/kind", d, authz.Nobody(), authz.ActionLANSavesFile, authz.LANFile("gametype", "abc", ""), false, "kind"},

		// ---- lan.sync.* ----
		{"lan.sync.manifest/pb_user/scope", d, adminUser(), authz.ActionLANSyncManifest, authz.Global(), true, "scope:lan.*"},
		{"lan.sync.manifest/pb_user/scope-none", d, user(), authz.ActionLANSyncManifest, authz.Global(), false, "no_scope"},
		{"lan.sync.manifest/machine/scope", d, machine("lan.sync.*"), authz.ActionLANSyncManifest, authz.Global(), true, "scope:lan.sync.*"},
		{"lan.sync.manifest/machine/scope-none", d, machine("lan.saves.*"), authz.ActionLANSyncManifest, authz.Global(), false, "no_scope"},
		{"lan.sync.download_game/pb_user/scope", d, adminUser(), authz.ActionLANSyncDLGame, authz.ISO("iso1"), true, "scope:lan.*"},
		{"lan.sync.download_game/machine/scope", d, machine("lan.sync.download_game:*"), authz.ActionLANSyncDLGame, authz.ISO("iso1"), true, "scope:lan.sync.download_game:*"},
		{"lan.sync.download_game/machine/scope-none", d, machine("lan.sync.download_game:iso2"), authz.ActionLANSyncDLGame, authz.ISO("iso1"), false, "no_scope"},
		{"lan.sync.download_game/spectator/kind", d, spectator(boxA), authz.ActionLANSyncDLGame, authz.ISO("iso1"), false, "kind"},
		{"lan.sync.download_app/pb_user/scope", d, adminUser(), authz.ActionLANSyncDLApp, authz.Global(), true, "scope:lan.*"},
		{"lan.sync.download_app/machine/scope", d, machine("lan.*"), authz.ActionLANSyncDLApp, authz.Global(), true, "scope:lan.*"},
		{"lan.sync.download_app/anonymous/kind", d, authz.Nobody(), authz.ActionLANSyncDLApp, authz.Global(), false, "kind"},

		// ---- token.mint / token.revoke / token.list ----
		{"token.mint/pb_user/scope:machine", d, adminUser(), authz.ActionTokenMint, mint("machine"), true, "scope:token.*"},
		{"token.mint/pb_user/scope:device", d, user("token.mint"), authz.ActionTokenMint, mint("device"), true, "scope:token.mint"},
		{"token.mint/pb_user/scope:spectator-via-overlay.mint", d, user(authz.SeedRoles[3].Scopes...), authz.ActionTokenMint, mint("spectator"), true, "scope:overlay.mint"},
		{"token.mint/pb_user/scope:machine-overlay.mint-not-enough", d, user(authz.SeedRoles[3].Scopes...), authz.ActionTokenMint, mint("machine"), false, "no_scope"},
		{"token.mint/pb_user/scope:device-overlay.mint-not-enough", d, user("overlay.*"), authz.ActionTokenMint, mint("device"), false, "no_scope"},
		{"token.mint/pb_user/scope:kind-selector", d, user("token.mint:device"), authz.ActionTokenMint, mint("device"), true, "scope:token.mint:device"},
		{"token.mint/pb_user/scope:kind-selector-mismatch", d, user("token.mint:device"), authz.ActionTokenMint, mint("machine"), false, "no_scope"},
		{"token.mint/pb_user/scope-none", d, user(), authz.ActionTokenMint, mint("spectator"), false, "no_scope"},
		{"token.mint/machine/scope", d, machine("token.mint"), authz.ActionTokenMint, mint("machine"), true, "scope:token.mint"},
		{"token.mint/machine/scope:spectator-via-overlay.mint", d, machine("overlay.mint"), authz.ActionTokenMint, mint("spectator"), true, "scope:overlay.mint"},
		{"token.mint/machine/scope-none", d, machine("token.list"), authz.ActionTokenMint, mint("machine"), false, "no_scope"},
		{"token.mint/spectator/kind", d, spectator(boxA, "overlay.read_state:box1"), authz.ActionTokenMint, mint("spectator"), false, "kind"},
		{"token.mint/device/kind", d, device(boxA), authz.ActionTokenMint, mint("device"), false, "kind"},
		{"token.mint/anonymous/kind", d, authz.Nobody(), authz.ActionTokenMint, mint("spectator"), false, "kind"},
		{"token.revoke/pb_user/scope", d, adminUser(), authz.ActionTokenRevoke, authz.Token("sp_abc"), true, "scope:token.*"},
		{"token.revoke/pb_user/scope:literal", d, user("token.revoke:sp_abc"), authz.ActionTokenRevoke, authz.Token("sp_abc"), true, "scope:token.revoke:sp_abc"},
		{"token.revoke/pb_user/scope-none", d, user("token.revoke:sp_other"), authz.ActionTokenRevoke, authz.Token("sp_abc"), false, "no_scope"},
		{"token.revoke/machine/scope", d, machine("token.revoke:*"), authz.ActionTokenRevoke, authz.Token("sp_abc"), true, "scope:token.revoke:*"},
		{"token.revoke/spectator/kind", d, spectator(boxA, "overlay.read_state:box1"), authz.ActionTokenRevoke, authz.Token("sp_1"), false, "kind"},
		{"token.list/pb_user/scope", d, adminUser(), authz.ActionTokenList, authz.Global(), true, "scope:token.*"},
		{"token.list/pb_user/scope-none", d, user(), authz.ActionTokenList, authz.Global(), false, "no_scope"},
		{"token.list/machine/scope", d, machine("token.list"), authz.ActionTokenList, authz.Global(), true, "scope:token.list"},
		{"token.list/device/kind", d, device(boxA), authz.ActionTokenList, authz.Global(), false, "kind"},

		// ---- discord.* ----
		{"discord.config/discord/pred:manage_guild-true", d, discord("32", ""), authz.ActionDiscordConfig, authz.Guild("g1"), true, "pred:manage_guild"},
		{"discord.config/discord/pred:manage_guild-admin-bit", d, discord("8", ""), authz.ActionDiscordConfig, authz.Guild("g1"), true, "pred:manage_guild"},
		{"discord.config/discord/pred:manage_guild-combined", d, discord("2147483680", ""), authz.ActionDiscordConfig, authz.Guild("g1"), true, "pred:manage_guild"},
		{"discord.config/discord/pred:manage_guild-false", d, discord("2048", ""), authz.ActionDiscordConfig, authz.Guild("g1"), false, "pred:manage_guild"},
		{"discord.config/discord/pred:manage_guild-garbage", d, discord("lots", ""), authz.ActionDiscordConfig, authz.Guild("g1"), false, "pred:manage_guild"},
		{"discord.config/discord/pred:manage_guild-missing", d, authz.Principal{Kind: authz.KindDiscord, ID: "123"}, authz.ActionDiscordConfig, authz.Guild("g1"), false, "pred:manage_guild"},
		{"discord.config/pb_user/kind", d, adminUser(), authz.ActionDiscordConfig, authz.Guild("g1"), false, "kind"},
		{"discord.config/machine/kind", d, machine("*"), authz.ActionDiscordConfig, authz.Guild("g1"), false, "kind"},
		{"discord.bind_channel/discord/pred:manage_guild-true", d, discord("32", ""), authz.ActionDiscordBindChannel, authz.Guild("g1"), true, "pred:manage_guild"},
		{"discord.bind_channel/discord/pred:manage_guild-false", d, discord("0", ""), authz.ActionDiscordBindChannel, authz.Guild("g1"), false, "pred:manage_guild"},
		{"discord.bind_channel/pb_user/kind", d, adminUser(), authz.ActionDiscordBindChannel, authz.Guild("g1"), false, "kind"},
		{"discord.stats.read/discord/pred:always", d, discord("0", ""), authz.ActionDiscordStatsRead, authz.Guild("g1"), true, "pred:always"},
		{"discord.stats.read/pb_user/kind", d, adminUser(), authz.ActionDiscordStatsRead, authz.Guild("g1"), false, "kind"},
		{"discord.stats.read/anonymous/kind", d, authz.Nobody(), authz.ActionDiscordStatsRead, authz.Guild("g1"), false, "kind"},
		{"discord.box/discord/pred:linked-true", d, discord("0", uID), authz.ActionDiscordBox, authz.Global(), true, "pred:linked"},
		{"discord.box/discord/pred:linked-false", d, discord("32", ""), authz.ActionDiscordBox, authz.Global(), false, "pred:linked"},
		{"discord.box/discord/pred:linked-container", d, discord("0", uID), authz.ActionDiscordBox, authz.Container(boxA), true, "pred:linked"},
		{"discord.box/pb_user/kind", d, adminUser(), authz.ActionDiscordBox, authz.Global(), false, "kind"},
		{"discord.post/discord/kind", d, discord("8", uID), authz.ActionDiscordPost, authz.Global(), false, "kind"},
		{"discord.post/pb_user/kind", d, adminUser(), authz.ActionDiscordPost, authz.Global(), false, "kind"},
		{"discord.post/machine/kind", d, machine("*"), authz.ActionDiscordPost, authz.Global(), false, "kind"},
		{"discord.post/internal/internal", d, authz.Internal("games_discord_post"), authz.ActionDiscordPost, authz.Global(), true, "internal"},
	}

	seen := map[string]bool{}
	for _, c := range cases {
		if seen[c.name] {
			t.Fatalf("duplicate case name %q", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, c.check)
	}
	if len(cases) < 56 {
		t.Fatalf("matrix has %d cases, want >= 56", len(cases))
	}
}

func levelled(p authz.Principal, level int) authz.Principal {
	p.Level = level
	return p
}

// adminsDeps is baseDeps with n admins, "u2" (the revoke pins' target) among
// them; any other target holds nothing.
func adminsDeps(n int) *authztest.FakeDeps {
	d := baseDeps()
	d.Admins = n
	d.Holds = map[string]bool{authztest.Key("u2", "admin"): true}
	return d
}

// bareRole is a role resource built by hand without Role(), so it carries no
// Extra["target_user"].
func bareRole(slug string) authz.Resource {
	return authz.Resource{Kind: authz.ResRole, ID: slug}
}

// lanFileNoKind is a lan_file resource whose kind is only recoverable from
// the ID prefix (no Extra["kind"]); owner is the served profile's gamertag.
func lanFileNoKind(id, owner string) authz.Resource {
	return authz.Resource{Kind: authz.ResLANFile, ID: id, Extra: map[string]string{"gamertag": owner}}
}

func mint(kind string) authz.Resource {
	return authz.Resource{Kind: authz.ResGlobal, Extra: map[string]string{"kind": kind}}
}

// station is a machine key bound to gamertags (PD-8).
func station(tags ...string) authz.Principal {
	p := machine("lan.*")
	p.Extra = map[string]string{"station_id": "station-1", "gamertags": strings.Join(tags, ",")}
	return p
}

// profileFile is a served profile whose owner gamertag is tag.
func profileFile(tag string) authz.Resource {
	r := authz.LANFile("h2-profile", "p1", "u9")
	r.Extra["gamertag"] = tag
	return r
}

// TestCanOrder pins the five §4.3 evaluation-order invariants.
func TestCanOrder(t *testing.T) {
	t.Run("1 last_admin beats superuser", func(t *testing.T) {
		got := authz.CanWith(adminsDeps(1), authz.Superuser("x"), authz.ActionRoleRevoke, authz.Role("admin", "u2"))
		if got.Allow || got.Reason != "deny:last_admin" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("2 unknown action beats superuser", func(t *testing.T) {
		got := authz.CanWith(baseDeps(), authz.Superuser("x"), authz.Action("nope"), authz.Global())
		if got.Allow || got.Reason != "unknown_action" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("3 kind gate beats scope", func(t *testing.T) {
		got := authz.CanWith(baseDeps(), spectator(boxA, "token.mint"), authz.ActionTokenMint, mint("spectator"))
		if got.Allow || got.Reason != "kind" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("4 scope beats predicate", func(t *testing.T) {
		d := baseDeps()
		d.RosteredInFunc = func(string, string) bool { return false }
		got := authz.CanWith(d, user("box.view:*"), authz.ActionBoxView, authz.Container(boxA))
		if !got.Allow || got.Reason != "scope:box.view:*" || got.Matched != "box.view:*" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("5 predicate only when no scope matched", func(t *testing.T) {
		d := baseDeps()
		calls := 0
		d.RosteredInFunc = func(u, inst string) bool { calls++; return u == uID && inst == boxA }
		got := authz.CanWith(d, user(), authz.ActionBoxView, authz.Container(boxA))
		if !got.Allow || got.Reason != "pred:rostered" || calls != 1 {
			t.Fatalf("got %+v (calls=%d)", got, calls)
		}
		calls = 0
		got = authz.CanWith(d, user("box.*"), authz.ActionBoxView, authz.Container(boxA))
		if !got.Allow || got.Reason != "scope:box.*" || calls != 0 {
			t.Fatalf("scope matched but predicate ran: %+v (calls=%d)", got, calls)
		}
	})
}

func TestUnknownActionDenied(t *testing.T) {
	d := baseDeps()
	for _, p := range []authz.Principal{adminUser(), authz.Superuser("x"), authz.Internal("seed"), machine("*"), authz.Nobody()} {
		for _, a := range []authz.Action{"", "nope", "room.joins", "ROOM.JOIN", "lan", "lan.*"} {
			got := authz.CanWith(d, p, a, authz.Global())
			if got.Allow || got.Reason != "unknown_action" {
				t.Fatalf("CanWith(%s, %q) = %+v", p.Kind, a, got)
			}
		}
	}
}

func TestSuperuserAllowAll(t *testing.T) {
	d := baseDeps()
	su := authz.Superuser("s1")
	if !su.IsAdmin() || su.Collection != "_superusers" || su.AuditActorID() != "" {
		t.Fatalf("Superuser() = %+v", su)
	}
	for _, a := range authz.Actions() {
		for _, r := range sampleResources(a) {
			got := authz.CanWith(d, su, a, r)
			if !got.Allow || got.Reason != "superuser" {
				t.Errorf("superuser denied %s on %+v: %+v", a, r, got)
			}
		}
	}
	// ... except the Deny predicates.
	if got := authz.CanWith(adminsDeps(1), su, authz.ActionRoleRevoke, authz.Role("admin", "u2")); got.Allow {
		t.Fatalf("superuser must not revoke the last admin: %+v", got)
	}
	// The guard is about the holder: revoking admin from a user who does not
	// hold it is a no-op the superuser may issue even with one admin left.
	if got := authz.CanWith(adminsDeps(1), su, authz.ActionRoleRevoke, authz.Role("admin", "u3")); !got.Allow || got.Reason != "superuser" {
		t.Fatalf("superuser revoking admin from a non-admin: %+v", got)
	}
}

// TestLastAdminHoldsRole pins denyLastAdmin against the HoldsRole seam: the
// deny needs both "at most one admin" and "the target holds admin"; a
// resource without a target keeps the count-only deny; a target that holds
// admin under a soft-deleted user (AdminCount 0, HoldsRole true) is still
// refused; a holder lookup the adapter could not answer (ok=false) is
// refused rather than read as "does not hold"; and the count is consulted
// before the holder lookup.
func TestLastAdminHoldsRole(t *testing.T) {
	revoke := func(d *authztest.FakeDeps, r authz.Resource) authz.Decision {
		return authz.CanWith(d, adminUser(), authz.ActionRoleRevoke, r)
	}
	for _, tc := range []struct {
		name   string
		admins int
		holds  bool
		known  bool // false: HoldsRole reports ok=false (lookup failed)
		r      authz.Resource
		allow  bool
		reason string
	}{
		{"one admin, target holds", 1, true, true, authz.Role("admin", "u2"), false, "deny:last_admin"},
		{"one admin, target does not hold", 1, false, true, authz.Role("admin", "u2"), true, "pred:level_ok"},
		{"one admin, holder lookup unknown", 1, false, false, authz.Role("admin", "u2"), false, "deny:last_admin"},
		{"zero admins, soft-deleted holder", 0, true, true, authz.Role("admin", "u2"), false, "deny:last_admin"},
		{"zero admins, target does not hold", 0, false, true, authz.Role("admin", "u2"), true, "pred:level_ok"},
		{"zero admins, holder lookup unknown", 0, false, false, authz.Role("admin", "u2"), false, "deny:last_admin"},
		{"two admins, target holds", 2, true, true, authz.Role("admin", "u2"), true, "pred:level_ok"},
		{"one admin, no target", 1, false, true, bareRole("admin"), false, "deny:last_admin"},
		{"one admin, other role", 1, true, true, authz.Role("organizer", "u2"), true, "pred:level_ok"},
		{"one admin, other role, lookup unknown", 1, false, false, authz.Role("organizer", "u2"), true, "pred:level_ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := baseDeps()
			d.Admins = tc.admins
			d.Holds = map[string]bool{authztest.Key("u2", "admin"): tc.holds}
			if !tc.known {
				d.HoldsRoleFunc = func(string, string) (bool, bool) { return false, false }
			}
			got := revoke(d, tc.r)
			if got.Allow != tc.allow || got.Reason != tc.reason {
				t.Fatalf("got %+v, want allow=%v reason=%q", got, tc.allow, tc.reason)
			}
		})
	}
	// An unanswerable holder lookup refuses the superuser too: the Deny
	// predicate runs before the superuser short-circuit.
	d := adminsDeps(1)
	d.HoldsRoleFunc = func(string, string) (bool, bool) { return false, false }
	if got := authz.CanWith(d, authz.Superuser("s1"), authz.ActionRoleRevoke, authz.Role("admin", "u3")); got.Allow || got.Reason != "deny:last_admin" {
		t.Fatalf("superuser with an unanswerable holder lookup: %+v", got)
	}
	// With more than one admin the holder is never looked up.
	d = adminsDeps(2)
	d.HoldsRoleFunc = func(string, string) (bool, bool) { t.Fatal("HoldsRole consulted with two admins"); return false, false }
	if got := revoke(d, authz.Role("admin", "u2")); !got.Allow {
		t.Fatalf("two admins: %+v", got)
	}
	// The holder lookup asks about exactly the target and the admin slug.
	d = adminsDeps(1)
	var askedUser, askedSlug string
	d.HoldsRoleFunc = func(u, s string) (bool, bool) { askedUser, askedSlug = u, s; return false, true }
	if got := revoke(d, authz.Role("admin", "u7")); !got.Allow || askedUser != "u7" || askedSlug != "admin" {
		t.Fatalf("got %+v, asked (%q, %q)", got, askedUser, askedSlug)
	}
}

func TestInternalBypassesLastAdmin(t *testing.T) {
	in := authz.Internal("users_soft_delete_pii")
	for _, n := range []int{0, 1, 2} {
		got := authz.CanWith(adminsDeps(n), in, authz.ActionRoleRevoke, authz.Role("admin", "u2"))
		if !got.Allow || got.Reason != "internal" {
			t.Fatalf("admins=%d: %+v", n, got)
		}
	}
	// authz.Internal is allow-all everywhere else too.
	d := baseDeps()
	for _, a := range authz.Actions() {
		for _, r := range sampleResources(a) {
			got := authz.CanWith(d, in, a, r)
			if !got.Allow || got.Reason != "internal" {
				t.Errorf("internal denied %s on %+v: %+v", a, r, got)
			}
		}
	}
	// But not on an unknown action or a mismatched resource.
	if got := authz.CanWith(d, in, authz.Action("nope"), authz.Global()); got.Allow {
		t.Fatalf("internal on unknown action: %+v", got)
	}
	if got := authz.CanWith(d, in, authz.ActionRoleRevoke, authz.Global()); got.Allow || got.Reason != "resource" {
		t.Fatalf("internal on wrong resource: %+v", got)
	}
}

// sampleResources returns one accepted resource per kind the action admits.
func sampleResources(a authz.Action) []authz.Resource {
	kinds, collection, ok := authz.RuleResources(a)
	if !ok || len(kinds) == 0 {
		return []authz.Resource{authz.Global()}
	}
	out := make([]authz.Resource, 0, len(kinds))
	for _, k := range kinds {
		switch k {
		case authz.ResGlobal:
			if a == authz.ActionTokenMint {
				out = append(out, mint("machine"))
			} else {
				out = append(out, authz.Global())
			}
		case authz.ResInstance:
			out = append(out, authz.Instance(boxA))
		case authz.ResContainer:
			out = append(out, authz.Container(boxA))
		case authz.ResRoom:
			out = append(out, hostBare(boxA), hostClass(boxA, "tick"), hostBare("summary"), roomAdmin, roomPublic)
		case authz.ResUser:
			out = append(out, authz.User("u2"))
		case authz.ResRole:
			out = append(out, authz.Role("organizer", "u2"))
		case authz.ResTeam:
			out = append(out, authz.Team(teamID))
		case authz.ResRecord:
			out = append(out, authz.Record(collection, "r1", "u2"))
		case authz.ResISO:
			out = append(out, authz.ISO("iso1"))
		case authz.ResLANFile:
			out = append(out, authz.LANFile("gametype", "abc", ""))
		case authz.ResToken:
			out = append(out, authz.Token("sp_abc"))
		case authz.ResGuild:
			out = append(out, authz.Guild("g1"))
		}
	}
	return out
}

// TestCanFailsClosed covers the guards that are not in the §2.4 list: nil
// deps, typed-nil deps, mismatched resources, malformed rooms, zero values.
func TestCanFailsClosed(t *testing.T) {
	var nilFake *authztest.FakeDeps
	for _, deps := range []authz.Deps{nil, nilFake} {
		got := authz.CanWith(deps, adminUser(), authz.ActionBoxView, authz.Container(boxA))
		if got.Allow || got.Reason != "no_deps" {
			t.Fatalf("nil deps: %+v", got)
		}
		if authz.Can(deps, authz.Superuser("s"), authz.ActionAdminAdmin, authz.Global()) {
			t.Fatal("nil deps must deny even a superuser")
		}
		// Unknown action still wins over the nil-deps reason.
		if got := authz.CanWith(deps, adminUser(), authz.Action("nope"), authz.Global()); got.Reason != "unknown_action" {
			t.Fatalf("nil deps + unknown action: %+v", got)
		}
	}

	d := baseDeps()
	mismatches := []struct {
		a authz.Action
		r authz.Resource
	}{
		{authz.ActionBoxView, authz.Instance(boxA)},
		{authz.ActionBoxView, authz.Global()},
		{authz.ActionRoomJoin, authz.Global()},
		{authz.ActionRoomJoin, authz.Instance(boxA)},
		{authz.ActionRoleRevoke, authz.User("u2")},
		{authz.ActionGamertagModerate, authz.Record("teams", "t1", "u2")},
		{authz.ActionTeamModerate, authz.Record("", "t1", "u2")},
		{authz.ActionTokenMint, authz.Global()},
		{authz.ActionTokenMint, mint("bogus")},
		{authz.ActionTokenMint, mint("")},
		{authz.ActionLibraryManage, authz.Container(boxA)},
		{authz.ActionDiscordConfig, authz.Global()},
		{authz.ActionAdminAdmin, authz.Container(boxA)},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host"})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host", Instance: "a b"})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host", Instance: boxA, Class: "nope"})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host", Instance: "all", Class: "tick"})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host", Instance: "a:b"})},
		// Hand-built rooms whose fields do not round-trip through ParseRoom
		// must not be re-classified (a "host:x" type would otherwise fall
		// into the everyone-passes public sub-row).
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host:" + boxA})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host:" + boxA + ":tick"})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "admin:x"})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host", Instance: boxA + ":tick"})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "public", Instance: boxA})},
		{authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "admin", Class: "tick"})},
	}
	for _, m := range mismatches {
		for _, p := range []authz.Principal{adminUser(), authz.Superuser("s"), authz.Internal("x"), machine("*")} {
			got := authz.CanWith(d, p, m.a, m.r)
			if got.Allow || got.Reason != "resource" {
				t.Errorf("CanWith(%s, %s, %+v) = %+v, want deny resource", p.Kind, m.a, m.r, got)
			}
		}
	}

	// Zero principal: unknown kind is denied "kind" everywhere.
	for _, a := range authz.Actions() {
		for _, r := range sampleResources(a) {
			if got := authz.CanWith(d, authz.Principal{}, a, r); got.Allow || got.Reason != "kind" {
				t.Errorf("zero principal on %s: %+v", a, got)
			}
			if got := authz.CanWith(d, authz.Principal{Kind: "martian", Scopes: []string{"*"}}, a, r); got.Allow || got.Reason != "kind" {
				t.Errorf("unknown kind on %s: %+v", a, got)
			}
		}
	}

	// authz.Nobody() can only join public rooms.
	nobody := authz.Nobody()
	for _, a := range authz.Actions() {
		for _, r := range sampleResources(a) {
			got := authz.CanWith(d, nobody, a, r)
			public := a == authz.ActionRoomJoin && r.Room.Type != "" && !r.Room.IsHost() && r.Room.Type != "admin"
			if got.Allow != public {
				t.Errorf("Nobody on %s %+v: %+v", a, r, got)
			}
		}
	}

	// Scopes on a token principal never unlock a pb_user-only cell, and a
	// wildcard scope never widens a bound kind past its binding.
	if authz.Can(d, spectator(boxA, "*"), authz.ActionRoomJoin, hostClass(boxB, "tick")) {
		t.Fatal("spectator with * must stay bound")
	}
	if authz.Can(d, device(boxA, "*"), authz.ActionBoxView, authz.Container(boxB)) {
		t.Fatal("device with * must stay bound")
	}
	if authz.Can(d, authz.Anonymous(boxA, []string{"*"}), authz.ActionBoxView, authz.Container(boxA)) {
		t.Fatal("anonymous is never admitted to box.view")
	}
}

func TestPrincipalHelpers(t *testing.T) {
	p := adminUser()
	if !p.Is(authz.KindPBUser) || p.Is(authz.KindMachine) {
		t.Fatal("Is")
	}
	if !p.HasRole("admin") || p.HasRole("organizer") || p.HasRole("") {
		t.Fatal("HasRole")
	}
	if !p.HasScope("admin.users") || p.HasScope("nope") || p.HasScope("") {
		t.Fatal("HasScope")
	}
	if !p.IsAdmin() || user().IsAdmin() {
		t.Fatal("IsAdmin")
	}
	if p.AuditActorID() != uID || machine().AuditActorID() != "" {
		t.Fatal("AuditActorID")
	}
	if p.BoundInstance() != "" || spectator(boxA).BoundInstance() != boxA {
		t.Fatal("BoundInstance")
	}
	anon := authz.Anonymous("", []string{"B", "a", "a"})
	if anon.Bound != nil || len(anon.Scopes) != 2 || anon.Scopes[0] != "a" {
		t.Fatalf("Anonymous = %+v", anon)
	}
	if in := authz.Internal("seed"); in.Kind != authz.KindInternal || in.ID != "seed" || in.IsAdmin() {
		t.Fatalf("Internal = %+v", in)
	}
	if n := authz.Nobody(); n.Kind != authz.KindAnonymous || n.ID != "" || len(n.Scopes) != 0 || n.Bound != nil {
		t.Fatalf("Nobody = %+v", n)
	}
	for _, k := range authz.Kinds() {
		if !k.Known() {
			t.Fatalf("Kinds() contains unknown %q", k)
		}
	}
	if authz.Kind("x").Known() || authz.Kind("").Known() || len(authz.Kinds()) != 8 {
		t.Fatal("Kind.Known / Kinds")
	}
}
