package authz

import (
	"errors"
	"fmt"
	"testing"
)

// TestMatch is the §3.2 matching table, one subtest per row.
func TestMatch(t *testing.T) {
	rows := []struct {
		scope, want string
		ok          bool
	}{
		{"overlay.read_state:box1", "overlay.read_state:box1", true},                  // 1
		{"overlay.read_state:box1", "overlay.read_state:box2", false},                 // 2
		{"overlay.read_state:*", "overlay.read_state:box2", true},                     // 3
		{"overlay.*:box1", "overlay.read_state:box1", true},                           // 4
		{"overlay.*:*", "overlay.mint", true},                                         // 5
		{"lan.*", "lan.saves.file:gametype/abc", true},                                // 6
		{"lan.saves.*", "lan.sync.manifest", false},                                   // 7
		{"room.join:host:*:game_filtered", "room.join:host:box1:game_filtered", true}, // 8
		{"room.join:host:*:game_filtered", "room.join:host:box1", false},              // 9
		{"room.join:host:*:game_filtered", "room.join:host:summary", false},           // 10
		{"room.join:host:box1:*", "room.join:host:box1:tick", true},                   // 11
		{"room.join:*", "room.join:host:summary", true},                               // 12
		{"*", "token.mint", true},                                                     // 13
		{"library.manage", "library.manage", true},                                    // 14
		{"library.manage", "library.manage:iso1", false},                              // 15
		{"Overlay.Read_State:Box1", "overlay.read_state:box1", false},                 // 16
		{"overlay.read_state:", "overlay.read_state:box1", false},                     // 17
		{"overlay.read_state:bo*x", "overlay.read_state:box", false},                  // 18
		{"nosuch.action:*", "nosuch.action:x", false},                                 // 19
		{"room.join:host:*:*", "room.join:host:box1:tick", true},                      // 20
	}
	for i, row := range rows {
		t.Run(fmt.Sprintf("row%02d", i+1), func(t *testing.T) {
			if got := Match(row.scope, row.want); got != row.ok {
				t.Fatalf("Match(%q, %q) = %v, want %v", row.scope, row.want, got, row.ok)
			}
		})
	}
}

func TestMatchWantCanonicalised(t *testing.T) {
	// Want selectors are canonicalised (trim, lower-case, collapse spaces);
	// the scope side is strict.
	if !Match("overlay.read_state:box 1", "overlay.read_state:  Box   1 ") {
		t.Fatal("want selector should be canonicalised before comparison")
	}
	if Match("overlay.read_state:box1", "Overlay.Read_State:box1") {
		t.Fatal("want action must be an exact known action")
	}
	if Match("overlay.read_state:box1", "") || Match("", "overlay.read_state:box1") {
		t.Fatal("empty scope / want never match")
	}
	// "*" in a want is a literal, never a wildcard.
	if Match("overlay.read_state:box1", "overlay.read_state:*") {
		t.Fatal("a literal * want must not match a concrete scope")
	}
}

func TestParseScopeRejects(t *testing.T) {
	bad := []string{
		"",
		":",
		":box1",
		"overlay.read_state:",
		"overlay.read_state::box1",
		"Overlay.Read_State",
		"overlay.read_state:Box1",
		"overlay.read_state:bo*x",
		"overlay.read_state:b*",
		"overlay.read_state:box/1",
		"overlay.read_state:box@1",
		"nosuch.action",
		"nosuch.*",
		"overlay",    // a family without ".*"
		"overlay.*x", // malformed family suffix
		"*:box1",     // bare * takes no selector
		"*.*",
		" overlay.read_state",
		"overlay.read_state ",
		"lan.saves.file:gametype/abc", // "/" is outside the segment charset
	}
	for _, s := range bad {
		if _, err := ParseScope(s); !errors.Is(err, ErrScopeSyntax) {
			t.Errorf("ParseScope(%q) = %v, want ErrScopeSyntax", s, err)
		}
	}

	good := map[string]ScopePat{
		"*":                              {Any: true},
		"lan.*":                          {Action: "lan", Family: true},
		"lan.saves.*":                    {Action: "lan.saves", Family: true},
		"overlay.*:box1":                 {Action: "overlay", Family: true, Selector: []string{"box1"}},
		"library.manage":                 {Action: "library.manage"},
		"overlay.read_state:*":           {Action: "overlay.read_state", Selector: []string{"*"}},
		"room.join:host:*:game_filtered": {Action: "room.join", Selector: []string{"host", "*", "game_filtered"}},
		"overlay.read_state:box 1.a-b_c": {Action: "overlay.read_state", Selector: []string{"box 1.a-b_c"}},
	}
	for s, want := range good {
		got, err := ParseScope(s)
		if err != nil {
			t.Errorf("ParseScope(%q): %v", s, err)
			continue
		}
		if got.Any != want.Any || got.Family != want.Family || got.Action != want.Action || fmt.Sprint(got.Selector) != fmt.Sprint(want.Selector) {
			t.Errorf("ParseScope(%q) = %+v, want %+v", s, got, want)
		}
	}
}

func TestCanonIdempotent(t *testing.T) {
	cases := map[string]string{
		"  Overlay.Read_State : Box   1 ": "overlay.read_state:box 1",
		"ROOM.JOIN:HOST:*:TICK":           "room.join:host:*:tick",
		"lan.*":                           "lan.*",
		"":                                "",
		"   ":                             "",
		"a:\tb  c\n":                      "a:b c",
	}
	for in, want := range cases {
		got := Canon(in)
		if got != want {
			t.Errorf("Canon(%q) = %q, want %q", in, got, want)
		}
		if again := Canon(got); again != got {
			t.Errorf("Canon not idempotent: Canon(%q) = %q", got, again)
		}
	}

	// CanonScopes: canon, drop empties, dedupe, sort, never nil.
	got := CanonScopes([]string{"Lan.*", "", "lan.*", "  ", "admin.*"})
	if fmt.Sprint(got) != "[admin.* lan.*]" {
		t.Fatalf("CanonScopes = %v", got)
	}
	if CanonScopes(nil) == nil {
		t.Fatal("CanonScopes(nil) must not be nil")
	}
}

func TestScopeFor(t *testing.T) {
	cases := []struct {
		a    Action
		r    Resource
		want string
	}{
		{ActionOverlayListConsoles, Global(), "overlay.list_consoles"},
		{ActionOverlayReadState, Instance("box1"), "overlay.read_state:box1"},
		{ActionBoxView, Container("box1"), "box.view:box1"},
		{ActionRoomJoin, RoomRes(Room{Type: "host", Instance: "box1"}), "room.join:host:box1"},
		{ActionRoomJoin, RoomRes(Room{Type: "host", Instance: "box1", Class: "tick"}), "room.join:host:box1:tick"},
		{ActionRoomJoin, RoomRes(Room{Type: "host", Instance: "summary"}), "room.join:host:summary"},
		{ActionRoomJoin, RoomRes(Room{Type: "admin"}), "room.join:admin"},
		{ActionRoomJoin, RoomRes(Room{Type: "public"}), "room.join:public"},
		{ActionRoleGrant, Role("admin", "u1"), "role.grant:admin"},
		{ActionGamertagModerate, Record("gamertags", "g1", "u1"), "gamertag.moderate:g1"},
		{ActionLANSavesFile, LANFile("gametype", "abc", ""), "lan.saves.file:gametype/abc"},
		{ActionLibraryManage, ISO("iso1"), "library.manage:iso1"},
		{ActionTokenRevoke, Token("sp_abc"), "token.revoke:sp_abc"},
		{ActionDiscordConfig, Guild("123"), "discord.config:123"},
		{ActionTeamInvite, Team("t1"), "team.invite:t1"},
		{ActionUserModerate, User("u1"), "user.moderate:u1"},
	}
	for _, c := range cases {
		if got := ScopeFor(c.a, c.r); got != c.want {
			t.Errorf("ScopeFor(%s, %+v) = %q, want %q", c.a, c.r, got, c.want)
		}
	}
}

func TestNamesOneInstance(t *testing.T) {
	yes := []string{
		"overlay.read_state:box1",
		"room.join:host:box1",
		"room.join:host:box1:tick",
		"room.join:host:box1:*",
		"box.view:box1",
	}
	no := []string{
		"overlay.read_state:*",
		"overlay.read_state",
		"room.join:host:*:tick",
		"room.join:host:summary",
		"room.join:host:all:tick",
		"room.join:*",
		"room.join:foo:box1",
		"room.join:host:box1:tick:extra",
		"*",
		"lan.*",
		"nosuch.action:box1",
		"",
	}
	for _, s := range yes {
		if !NamesOneInstance(s) {
			t.Errorf("NamesOneInstance(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if NamesOneInstance(s) {
			t.Errorf("NamesOneInstance(%q) = true, want false", s)
		}
	}
}

func TestIsWildcard(t *testing.T) {
	yes := []string{"*", "lan.*", "overlay.read_state:*", "room.join:host:*:tick", "overlay.*:box1"}
	no := []string{"overlay.read_state:box1", "library.manage", "room.join:host:box1:tick", "", "bogus"}
	for _, s := range yes {
		if !IsWildcard(s) {
			t.Errorf("IsWildcard(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsWildcard(s) {
			t.Errorf("IsWildcard(%q) = true, want false", s)
		}
	}
}

func TestMatchAny(t *testing.T) {
	scopes := []string{"admin.*", "overlay.read_state:box1", "room.join:host:*:tick"}
	if s, ok := MatchAny(scopes, "overlay.read_state:box1"); !ok || s != "overlay.read_state:box1" {
		t.Fatalf("MatchAny = %q,%v", s, ok)
	}
	if s, ok := MatchAny(scopes, "admin.users"); !ok || s != "admin.*" {
		t.Fatalf("MatchAny = %q,%v", s, ok)
	}
	if _, ok := MatchAny(scopes, "token.mint"); ok {
		t.Fatal("MatchAny should miss")
	}
	if _, ok := MatchAny(nil, "token.mint"); ok {
		t.Fatal("MatchAny(nil) should miss")
	}
}

func TestActionFamilyAndKnown(t *testing.T) {
	if ActionLANSavesFile.Family() != "lan.saves" || ActionRoomJoin.Family() != "room" {
		t.Fatal("Family should be everything before the last dot")
	}
	if !ActionTokenMint.Known() || Action("nope").Known() || Action("").Known() {
		t.Fatal("Known mismatch")
	}
	acts := Actions()
	if len(acts) != len(allActions) {
		t.Fatalf("Actions() has %d entries, want %d", len(acts), len(allActions))
	}
	for i := 1; i < len(acts); i++ {
		if acts[i-1] >= acts[i] {
			t.Fatalf("Actions() not sorted / not unique at %d: %s >= %s", i, acts[i-1], acts[i])
		}
	}
	for _, f := range []string{"lan", "lan.saves", "lan.sync", "room", "admin", "discord.stats"} {
		if !isActionFamily(f) {
			t.Errorf("isActionFamily(%q) = false", f)
		}
	}
	for _, f := range []string{"", "la", "lan.saves.file", "nosuch", "discord.stats.read"} {
		if isActionFamily(f) {
			t.Errorf("isActionFamily(%q) = true", f)
		}
	}
}
