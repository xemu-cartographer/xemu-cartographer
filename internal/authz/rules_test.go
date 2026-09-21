package authz

import (
	"sort"
	"testing"
	"time"
)

// RuleResources exposes a rule's accepted resource kinds and pinned record
// collection to the external can_test package (test-only; the rule table
// itself stays unexported).
func RuleResources(a Action) (resources []ResourceKind, collection string, ok bool) {
	rl, ok := rules[a]
	if !ok {
		return nil, "", false
	}
	return append([]ResourceKind(nil), rl.Resources...), rl.Collection, true
}

var validResourceKinds = map[ResourceKind]bool{
	ResGlobal: true, ResInstance: true, ResContainer: true, ResRoom: true,
	ResUser: true, ResRole: true, ResTeam: true, ResRecord: true, ResISO: true,
	ResLANFile: true, ResToken: true, ResGuild: true,
}

// TestEveryActionHasRule: Actions() and the rule table are the same closed
// set, in both directions.
func TestEveryActionHasRule(t *testing.T) {
	for _, a := range Actions() {
		if _, ok := rules[a]; !ok {
			t.Errorf("action %q has no rule", a)
		}
	}
	var extra []string
	for a := range rules {
		if !a.Known() {
			extra = append(extra, string(a))
		}
	}
	sort.Strings(extra)
	if len(extra) != 0 {
		t.Errorf("rules has entries for unknown actions: %v", extra)
	}
	if len(rules) != len(Actions()) {
		t.Errorf("rules has %d entries, Actions() has %d", len(rules), len(Actions()))
	}
}

// TestScraperIngestRow pins the step-8 ingest row (§7.2): global resource,
// scope-only for users and machine keys (the seeded admin `scraper.*`
// and a minted `scraper.ingest` key), no spectator / device / anonymous
// cell, and it sits in the `scraper.` family so the wildcard covers it.
func TestScraperIngestRow(t *testing.T) {
	rl, ok := rules[ActionScraperIngest]
	if !ok {
		t.Fatalf("scraper.ingest has no rule")
	}
	if len(rl.Resources) != 1 || rl.Resources[0] != ResGlobal {
		t.Errorf("scraper.ingest resources = %v, want [global]", rl.Resources)
	}
	if len(rl.Kinds) != 2 || rl.Kinds[KindPBUser].Mode != modeScope || rl.Kinds[KindMachine].Mode != modeScope {
		t.Errorf("scraper.ingest kinds = %v, want scope-only pb_user + machine", rl.Kinds)
	}
	if ActionScraperIngest.Family() != "scraper" || !ActionScraperIngest.Known() {
		t.Errorf("scraper.ingest family/known = %q/%v", ActionScraperIngest.Family(), ActionScraperIngest.Known())
	}
	deps := stubDeps{}
	for _, tc := range []struct {
		name   string
		p      Principal
		want   bool
		reason string
	}{
		{"machine/ingest", Principal{Kind: KindMachine, Scopes: CanonScopes([]string{"scraper.ingest"})}, true, "scope:scraper.ingest"},
		{"machine/wildcard", Principal{Kind: KindMachine, Scopes: CanonScopes([]string{"scraper.*"})}, true, "scope:scraper.*"},
		{"machine/other", Principal{Kind: KindMachine, Scopes: CanonScopes([]string{"scraper.state:*"})}, false, "no_scope"},
		{"spectator/kind", Principal{Kind: KindSpectator, Scopes: CanonScopes([]string{"scraper.*"})}, false, "kind"},
		{"anonymous", Nobody(), false, "kind"},
	} {
		d := CanWith(deps, tc.p, ActionScraperIngest, Global())
		if d.Allow != tc.want || d.Reason != tc.reason {
			t.Errorf("%s: allow=%v reason=%q, want %v/%q", tc.name, d.Allow, d.Reason, tc.want, tc.reason)
		}
	}
}

// checkCells validates one kind table: known kinds only, never
// superuser/internal (allow-all lives in CanWith), a valid mode, and a
// predicate exactly when the mode needs one.
func checkCells(t *testing.T, label string, cells map[Kind]kindRule) {
	t.Helper()
	for k, cell := range cells {
		if !k.Known() {
			t.Errorf("%s: unknown kind %q", label, k)
		}
		if k == KindSuperuser || k == KindInternal {
			t.Errorf("%s: %s must not appear in the table", label, k)
		}
		switch cell.Mode {
		case modeScope:
			if cell.Pred != nil {
				t.Errorf("%s/%s: S cell carries a predicate", label, k)
			}
		case modeScopeOrPred, modeScopeAndPred, modePred:
			if cell.Pred == nil {
				t.Errorf("%s/%s: mode %d needs a predicate", label, k, cell.Mode)
			}
		default:
			t.Errorf("%s/%s: invalid mode %d", label, k, cell.Mode)
		}
	}
}

// TestRuleKindsClosed: every rule (and every room.join sub-row) only names
// known kinds, valid resource kinds and well-formed cells; predicates return
// a non-empty name; Deny predicates are non-nil.
func TestRuleKindsClosed(t *testing.T) {
	for a, rl := range rules {
		label := string(a)
		checkCells(t, label, rl.Kinds)
		for _, rk := range rl.Resources {
			if !validResourceKinds[rk] {
				t.Errorf("%s: invalid resource kind %q", label, rk)
			}
		}
		if rl.Collection != "" {
			pinned := false
			for _, rk := range rl.Resources {
				if rk == ResRecord {
					pinned = true
				}
			}
			if !pinned {
				t.Errorf("%s: Collection set but ResRecord not accepted", label)
			}
		}
		for i, d := range rl.Deny {
			if d == nil {
				t.Errorf("%s: Deny[%d] is nil", label, i)
			}
		}
		if a == ActionRoomJoin {
			if len(rl.Kinds) != 0 {
				t.Errorf("room.join must use roomJoinRules, not Kinds")
			}
			if len(rl.Resources) != 1 || rl.Resources[0] != ResRoom {
				t.Errorf("room.join must accept exactly ResRoom, got %v", rl.Resources)
			}
		}
	}
	for shape, cells := range roomJoinRules {
		checkCells(t, "room.join/"+shape, cells)
	}
	for _, shape := range []string{"host_bare", "host_class", "host_aggregate", "admin", "public"} {
		if _, ok := roomJoinRules[shape]; !ok {
			t.Errorf("roomJoinRules missing shape %q", shape)
		}
	}
	if len(roomJoinRules) != 5 {
		t.Errorf("roomJoinRules has %d shapes, want 5", len(roomJoinRules))
	}

	// Every predicate reports a non-empty name on both verdicts so the
	// Decision.Reason is always "pred:<name>".
	named := map[string]predicate{
		"always": predAlways, "authed": predAuthed, "linked": predLinked,
		"rostered": predRostered, "box_owner": predBoxOwner, "bound": predBound,
		"level_ok": predLevelOK, "team_authority": predTeamAuthority,
		"station_file": predStationFile, "manage_guild": predManageGuild,
		"last_admin": denyLastAdmin,
	}
	deps := stubDeps{}
	for want, pred := range named {
		_, name := pred(deps, Principal{}, Resource{})
		if name == "" {
			t.Errorf("predicate %s returned an empty name", want)
		}
		if want != "level_ok" && name != want {
			t.Errorf("predicate %s reports name %q", want, name)
		}
	}
	// level_ok reports unknown_role for a slug the deps do not know.
	if ok, name := predLevelOK(deps, Principal{Level: 100}, Role("nope", "u")); ok || name != "unknown_role" {
		t.Errorf("level_ok on unknown role = %v,%q", ok, name)
	}
	// anyOf: first pass wins with its name, all-fail joins the names.
	if ok, name := anyOf(predRostered, predBoxOwner)(deps, Principal{}, Container("box1")); ok || name != "rostered|box_owner" {
		t.Errorf("anyOf all-fail = %v,%q", ok, name)
	}
	if ok, name := anyOf(predAlways, predRostered)(deps, Principal{}, Container("box1")); !ok || name != "always" {
		t.Errorf("anyOf first-pass = %v,%q", ok, name)
	}
	if ok, name := anyOf()(deps, Principal{}, Container("box1")); ok || name != "" {
		t.Errorf("anyOf() = %v,%q, want false", ok, name)
	}
}

// TestRoomShape pins the room.join sub-row selection.
func TestRoomShape(t *testing.T) {
	cases := map[string]Room{
		"host_bare":      {Type: "host", Instance: "box1"},
		"host_class":     {Type: "host", Instance: "box1", Class: "tick"},
		"host_aggregate": {Type: "host", Instance: "summary"},
		"admin":          {Type: "admin"},
		"public":         {Type: "public"},
	}
	for want, rm := range cases {
		if got := roomShape(rm); got != want {
			t.Errorf("roomShape(%+v) = %q, want %q", rm, got, want)
		}
	}
	if got := roomShape(Room{Type: "host", Instance: "all"}); got != "host_aggregate" {
		t.Errorf("host:all shape = %q", got)
	}
	if got := roomShape(Room{Type: "lobby"}); got != "public" {
		t.Errorf("unregistered non-host room shape = %q, want public", got)
	}
}

// TestWants pins the multi-want rows.
func TestWants(t *testing.T) {
	admin := RoomRes(Room{Type: "admin"})
	if got := rules[ActionRoomJoin].wantsFor(ActionRoomJoin, admin); len(got) != 1 || got[0] != "admin.admin" {
		t.Errorf("admin room wants = %v", got)
	}
	host := RoomRes(Room{Type: "host", Instance: "box1", Class: "tick"})
	if got := rules[ActionRoomJoin].wantsFor(ActionRoomJoin, host); len(got) != 1 || got[0] != "room.join:host:box1:tick" {
		t.Errorf("host room wants = %v", got)
	}
	mint := func(kind string) Resource { return Resource{Kind: ResGlobal, Extra: map[string]string{"kind": kind}} }
	got := rules[ActionTokenMint].wantsFor(ActionTokenMint, mint("spectator"))
	if len(got) != 3 || got[0] != "token.mint" || got[1] != "token.mint:spectator" || got[2] != "overlay.mint" {
		t.Errorf("spectator mint wants = %v", got)
	}
	got = rules[ActionTokenMint].wantsFor(ActionTokenMint, mint("machine"))
	if len(got) != 2 || got[0] != "token.mint" || got[1] != "token.mint:machine" {
		t.Errorf("machine mint wants = %v", got)
	}
	// Plain rows want exactly ScopeFor.
	if got := rules[ActionBoxView].wantsFor(ActionBoxView, Container("box1")); len(got) != 1 || got[0] != "box.view:box1" {
		t.Errorf("box.view wants = %v", got)
	}
	// token.mint accepts only known token kinds.
	for kind, ok := range map[string]bool{"machine": true, "spectator": true, "device": true, "": false, "pb_user": false, "MACHINE": false} {
		if got := rules[ActionTokenMint].accepts(mint(kind)); got != ok {
			t.Errorf("token.mint accepts kind %q = %v, want %v", kind, got, ok)
		}
	}
}

// TestSameInstance pins the instance compare to Canon's segment fold: two
// names are the same instance exactly when Canon says their single-segment
// scopes are, and empty never matches.
func TestSameInstance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"box1", "box1", true},
		{"box1", "Box1", true},
		{"BOX1", "box1", true},
		{" box1 ", "box1", true},
		{"box1", "box2", false},
		{"box1", "box11", false},
		{"", "", false},
		{"box1", "", false},
		{"", "box1", false},
		{"   ", "box1", false},
	} {
		if got := sameInstance(tc.a, tc.b); got != tc.want {
			t.Errorf("sameInstance(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := sameInstance(tc.b, tc.a); got != tc.want {
			t.Errorf("sameInstance(%q, %q) = %v, want %v (symmetry)", tc.b, tc.a, got, tc.want)
		}
		if canonA, canonB := Canon(tc.a), Canon(tc.b); tc.want != (canonA != "" && canonA == canonB) {
			t.Errorf("sameInstance(%q, %q) = %v disagrees with Canon (%q vs %q)", tc.a, tc.b, tc.want, canonA, canonB)
		}
	}
	for _, s := range []string{"box1", "Box 1", "  Two  Words ", "MiXeD"} {
		if got, want := canonSegment(s), Canon(s); got != want {
			t.Errorf("canonSegment(%q) = %q, Canon = %q", s, got, want)
		}
	}
}

// TestLANFileKind pins the kind fallback: Extra["kind"] wins, else the ID
// prefix before "/", else "".
func TestLANFileKind(t *testing.T) {
	for _, tc := range []struct {
		r    Resource
		want string
	}{
		{LANFile("h2-profile", "p1", ""), "h2-profile"},
		{Resource{Kind: ResLANFile, ID: "ce-profile/p1"}, "ce-profile"},
		{Resource{Kind: ResLANFile, ID: "gametype/abc", Extra: map[string]string{"kind": "h2-profile"}}, "h2-profile"},
		{Resource{Kind: ResLANFile, ID: "p1"}, ""},
		{Resource{Kind: ResLANFile, ID: "/p1"}, ""},
		{Resource{Kind: ResLANFile}, ""},
		{Resource{}, ""},
	} {
		if got := lanFileKind(tc.r); got != tc.want {
			t.Errorf("lanFileKind(%+v) = %q, want %q", tc.r, got, tc.want)
		}
	}
}

// TestPredStationFile pins PD-8 on the predicate alone: unbound passes
// everything; bound passes non-profile kinds and profile files sharing a tag
// with the key (both sides comma lists, trimmed and case-folded); bound is
// refused on a profile with no shared tag, and on any resource whose kind
// cannot be determined.
func TestPredStationFile(t *testing.T) {
	key := func(tags string) Principal {
		return Principal{Kind: KindMachine, ID: "mk_1", Extra: map[string]string{"gamertags": tags}}
	}
	profile := func(id, owner string) Resource {
		return Resource{Kind: ResLANFile, ID: id, Extra: map[string]string{"gamertag": owner}}
	}
	for _, tc := range []struct {
		name string
		p    Principal
		r    Resource
		want bool
	}{
		{"unbound/profile", key(""), LANFile("h2-profile", "p1", ""), true},
		{"unbound/no-kind", key(""), Resource{Kind: ResLANFile, ID: "p1"}, true},
		{"unbound/no-extra", Principal{Kind: KindMachine}, Resource{}, true},
		{"bound/non-profile", key("chief"), LANFile("gametype", "abc", ""), true},
		{"bound/non-profile-from-id", key("chief"), profile("game/abc", ""), true},
		{"bound/profile-match", key("chief"), profile("h2-profile/p1", "chief"), true},
		{"bound/profile-match-case", key("Chief"), profile("h2-profile/p1", " CHIEF "), true},
		{"bound/profile-match-lists", key("arbiter, chief"), profile("ce-profile/p1", "sarge,Chief"), true},
		{"bound/profile-miss", key("chief"), profile("h2-profile/p1", "arbiter"), false},
		{"bound/profile-miss-lists", key("arbiter,chief"), profile("h2-profile/p1", "sarge, cortana"), false},
		{"bound/profile-no-owner", key("chief"), profile("h2-profile/p1", ""), false},
		{"bound/profile-blank-owner", key("chief"), profile("h2-profile/p1", " , ,"), false},
		{"bound/profile-no-owner-extra", key("chief"), Resource{Kind: ResLANFile, ID: "h2-profile/p1"}, false},
		{"bound/kind-unknown", key("chief"), profile("p1", "chief"), false},
		{"bound/kind-unknown-no-extra", key("chief"), Resource{Kind: ResLANFile}, false},
		{"bound/kind-unknown-zero-resource", key("chief"), Resource{}, false},
		{"bound-blank/profile", key(" , "), profile("h2-profile/p1", "chief"), false},
		{"bound-blank/non-profile", key(" , "), LANFile("gametype", "abc", ""), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, name := predStationFile(stubDeps{}, tc.p, tc.r)
			if got != tc.want || name != "station_file" {
				t.Fatalf("predStationFile = %v,%q, want %v,\"station_file\"", got, name, tc.want)
			}
		})
	}
}

// stubDeps is a fail-closed Deps for predicate unit checks (authztest would
// create an import cycle from inside the authz package).
type stubDeps struct{}

func (stubDeps) RosteredIn(string, string) bool        { return false }
func (stubDeps) OwnedBox(string) string                { return "" }
func (stubDeps) InstanceExists(string) bool            { return false }
func (stubDeps) InstanceByConsole(string) string       { return "" }
func (stubDeps) TeamAuthority(string, string) bool     { return false }
func (stubDeps) ActiveMember(string, string) bool      { return false }
func (stubDeps) RecordOwner(string, string) string     { return "" }
func (stubDeps) RoleLevel(string) (int, bool)          { return 0, false }
func (stubDeps) AdminCount() int                       { return 0 }
func (stubDeps) HoldsRole(string, string) (bool, bool) { return false, false }
func (stubDeps) AnonymousScopes() []string             { return nil }
func (stubDeps) LookupToken(string) (TokenRow, bool)   { return TokenRow{}, false }
func (stubDeps) Now() time.Time                        { return time.Time{} }
