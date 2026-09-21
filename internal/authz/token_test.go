package authz

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSplitToken(t *testing.T) {
	cases := []struct {
		in          string
		kid, secret string
		ok          bool
	}{
		{"sp_abc.secret", "sp_abc", "secret", true},
		{"legacy-env.s3cr3t", "legacy-env", "s3cr3t", true},
		{"nodot", "", "", false},
		{"a.b.c", "", "", false}, // two dots (JWT shape)
		{".secret", "", "", false},
		{"kid.", "", "", false},
		{".", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		kid, secret, ok := SplitToken(c.in)
		if kid != c.kid || secret != c.secret || ok != c.ok {
			t.Errorf("SplitToken(%q) = %q,%q,%v want %q,%q,%v", c.in, kid, secret, ok, c.kid, c.secret, c.ok)
		}
	}
}

func TestLooksOpaque(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJpZCI6IngifQ.sig"
	cases := map[string]bool{
		jwt:                  false,
		"sp_x.y":             true,
		"mk_x.y":             true,
		"dv_x.y":             true,
		"legacy-env.s":       true,
		"xx_x.y":             false, // unknown prefix
		"sp_x":               false, // no secret
		"sp_x.y.z":           false,
		"":                   false,
		"legacy-env":         false,
		"SP_X.y":             false, // prefixes are lower-case
		"sp_.y":              true,  // prefix alone is still "looks opaque"; lookup decides
		"legacy-env-2.s":     false,
		"mk_abcdefghjkmnp.s": true,
	}
	for in, want := range cases {
		if got := LooksOpaque(in); got != want {
			t.Errorf("LooksOpaque(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestVerifyTokenConstantTime(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	secret := "correct-horse"
	row := TokenRow{Kid: "sp_abc", KeyHash: HashSecret(secret), Kind: "spectator"}

	if err := VerifyToken(row, secret, now); err != nil {
		t.Fatalf("ok row: %v", err)
	}
	if err := VerifyToken(row, "wrong", now); !errors.Is(err, ErrBadSecret) {
		t.Fatalf("bad secret: %v", err)
	}
	if err := VerifyToken(row, "", now); !errors.Is(err, ErrBadSecret) {
		t.Fatalf("empty secret: %v", err)
	}
	if err := VerifyToken(TokenRow{Kid: "sp_abc"}, "", now); !errors.Is(err, ErrBadSecret) {
		t.Fatalf("empty hash + empty secret must not verify: %v", err)
	}
	if err := VerifyToken(TokenRow{Kid: "sp_abc", KeyHash: secret}, secret, now); !errors.Is(err, ErrBadSecret) {
		t.Fatalf("plaintext stored as hash must not verify: %v", err)
	}

	revoked := row
	revoked.Revoked = true
	if err := VerifyToken(revoked, secret, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked: %v", err)
	}
	// A bad secret on a revoked row is still ErrBadSecret — the secret is
	// checked first so the row's state never leaks.
	if err := VerifyToken(revoked, "wrong", now); !errors.Is(err, ErrBadSecret) {
		t.Fatalf("revoked + bad secret: %v", err)
	}

	expired := row
	expired.ExpiresAt = now.Add(-time.Second)
	if err := VerifyToken(expired, secret, now); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	exact := row
	exact.ExpiresAt = now
	if err := VerifyToken(exact, secret, now); !errors.Is(err, ErrExpired) {
		t.Fatalf("expiry at now is expired: %v", err)
	}
	future := row
	future.ExpiresAt = now.Add(time.Hour)
	if err := VerifyToken(future, secret, now); err != nil {
		t.Fatalf("future expiry: %v", err)
	}
	// Revoked beats expired.
	both := expired
	both.Revoked = true
	if err := VerifyToken(both, secret, now); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked + expired: %v", err)
	}
}

func TestHashSecret(t *testing.T) {
	// sha256("abc")
	if got := HashSecret("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("HashSecret(abc) = %s", got)
	}
	if HashSecret("a") == HashSecret("b") {
		t.Fatal("distinct secrets must hash differently")
	}
}

func TestNewKidPrefix(t *testing.T) {
	seen := map[string]bool{}
	for kind, prefix := range KidPrefix {
		for i := 0; i < 8; i++ {
			kid, err := NewKid(kind)
			if err != nil {
				t.Fatalf("NewKid(%s): %v", kind, err)
			}
			if !strings.HasPrefix(kid, prefix) {
				t.Fatalf("NewKid(%s) = %q, want prefix %q", kind, kid, prefix)
			}
			if len(kid) != len(prefix)+kidRandomLen {
				t.Fatalf("NewKid(%s) = %q, want %d chars after the prefix", kind, kid, kidRandomLen)
			}
			for _, r := range kid[len(prefix):] {
				if !strings.ContainsRune(kidAlphabet, r) {
					t.Fatalf("NewKid(%s) = %q: %q outside the Crockford alphabet", kind, kid, r)
				}
			}
			if seen[kid] {
				t.Fatalf("NewKid produced a duplicate %q", kid)
			}
			seen[kid] = true
			if !LooksOpaque(kid + ".secret") {
				t.Fatalf("minted kid %q should look opaque", kid)
			}
			// A kid must be a valid literal scope segment (token.revoke:<kid>).
			if _, err := ParseScope("token.revoke:" + kid); err != nil {
				t.Fatalf("kid %q is not a valid scope segment: %v", kid, err)
			}
		}
	}
	if _, err := NewKid("bogus"); err == nil {
		t.Fatal("NewKid(bogus) should fail")
	}
	if _, err := NewKid(""); err == nil {
		t.Fatal("NewKid(\"\") should fail")
	}
}

func TestNewSecret(t *testing.T) {
	a, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("secrets must differ")
	}
	if len(a) != 43 { // 32 bytes, base64url, no padding
		t.Fatalf("NewSecret length %d, want 43", len(a))
	}
	if strings.ContainsAny(a, "=+/.") {
		t.Fatalf("NewSecret %q must be unpadded base64url with no '.'", a)
	}
}

// TestValidateForMint is the §3.4 table, one subtest per row.
func TestValidateForMint(t *testing.T) {
	rows := []struct {
		kind   string
		scopes []string
		want   error // nil = ok
	}{
		{"machine", []string{"lan.*"}, nil},                                    // 1
		{"machine", []string{"*"}, nil},                                        // 2
		{"machine", []string{"lan.saves.file:*", "overlay.read_state:*"}, nil}, // 3
		{"spectator", []string{"overlay.read_state:box1", "room.join:host:box1:game_filtered", "room.join:host:box1:tick"}, nil}, // 4
		{"spectator", []string{"overlay.read_state:*"}, ErrWildcardKind},                                                         // 5
		{"spectator", []string{"overlay.*:box1"}, ErrWildcardKind},                                                               // 6
		{"spectator", []string{"overlay.read_state:box1", "room.join:host:box2:tick"}, ErrSpectatorInstance},                     // 7
		{"spectator", []string{"room.join:host:box1"}, ErrSpectatorInstance},                                                     // 8
		{"spectator", []string{"token.mint"}, ErrScopeSyntax},                                                                    // 9
		{"device", []string{"box.view:box1", "box.drive:box1"}, nil},                                                             // 10
		{"device", []string{"box.*:box1"}, ErrWildcardKind},                                                                      // 11
		{"device", []string{"box.view:box1", "box.read:box2"}, ErrSpectatorInstance},                                             // 12
		{"machine", []string{}, ErrScopeSyntax},                                                                                  // 13
		{"bogus", []string{"*"}, ErrScopeSyntax},                                                                                 // 14
	}
	for i, row := range rows {
		t.Run(fmt.Sprintf("row%02d", i+1), func(t *testing.T) {
			err := ValidateForMint(row.kind, row.scopes)
			if row.want == nil {
				if err != nil {
					t.Fatalf("ValidateForMint(%s, %v) = %v, want ok", row.kind, row.scopes, err)
				}
				return
			}
			if !errors.Is(err, row.want) {
				t.Fatalf("ValidateForMint(%s, %v) = %v, want %v", row.kind, row.scopes, err, row.want)
			}
			if i == 11 && !strings.Contains(err.Error(), "device") {
				t.Fatalf("row 12 message should mention device: %v", err)
			}
		})
	}

	// Extra edges beyond the table.
	extra := []struct {
		name   string
		kind   string
		scopes []string
		want   error
	}{
		{"empty spectator list", "spectator", nil, ErrScopeSyntax},
		{"empty device list", "device", []string{}, ErrScopeSyntax},
		{"unknown kind empty list", "bogus", nil, ErrScopeSyntax},
		{"machine bad grammar", "machine", []string{"Lan.*"}, ErrScopeSyntax},
		{"spectator bad grammar", "spectator", []string{"overlay.read_state:"}, ErrScopeSyntax},
		{"spectator selectorless", "spectator", []string{"overlay.read_state"}, ErrSpectatorInstance},
		{"spectator class wildcard", "spectator", []string{"room.join:host:box1:*"}, ErrWildcardKind},
		{"spectator aggregate", "spectator", []string{"room.join:host:summary:tick"}, ErrSpectatorInstance},
		{"spectator two-seg non-host", "spectator", []string{"scraper.state:box1:tick"}, ErrSpectatorInstance},
		{"spectator unknown class", "spectator", []string{"room.join:host:box1:nope"}, ErrScopeSyntax},
		{"device room bare", "device", []string{"room.join:host:box1"}, ErrSpectatorInstance},
		{"device room class ok", "device", []string{"room.join:host:box1:tick", "box.read:box1"}, nil},
		{"device not in allow-list", "device", []string{"overlay.read_state:box1"}, ErrScopeSyntax},
		{"spectator not in allow-list", "spectator", []string{"box.view:box1"}, ErrScopeSyntax},
		{"spectator bare star", "spectator", []string{"*"}, ErrWildcardKind},
	}
	for _, c := range extra {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateForMint(c.kind, c.scopes)
			if c.want == nil {
				if err != nil {
					t.Fatalf("got %v, want ok", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

// TestOverlayMintCovers pins the reading of overlay.mint (PD-5): exactly the
// overlay spectator grant on one literal instance — read_state plus the four
// OverlayClasses rooms — and nothing wider or of another action.
func TestOverlayMintCovers(t *testing.T) {
	if strings.Join(OverlayClasses, ",") != "game_filtered,tick,scenario,event_filtered" {
		t.Fatalf("OverlayClasses = %v, must match the console door (PD-1)", OverlayClasses)
	}
	covered := []string{
		"overlay.read_state:box1",
		"room.join:host:box1:game_filtered",
		"room.join:host:box1:tick",
		"room.join:host:box1:scenario",
		"room.join:host:box1:event_filtered",
	}
	for _, s := range covered {
		if !OverlayMintCovers(s) {
			t.Errorf("OverlayMintCovers(%q) = false, want true", s)
		}
	}
	notCovered := []string{
		"",
		"overlay.read_state",           // no instance
		"overlay.read_state:*",         // wildcard instance
		"overlay.read_state:box1:tick", // too many segments
		"overlay.*:box1",
		"overlay.mint",
		"overlay.list_consoles",
		"room.join:host:box1",         // bare host room (A.10)
		"room.join:host:box1:game",    // unfiltered class
		"room.join:host:box1:objects", // not an overlay class
		"room.join:host:box1:*",
		"room.join:host:*:tick",
		"room.join:host:all:tick",     // aggregate
		"room.join:host:summary:tick", // aggregate
		"room.join:admin",
		"room.join:*",
		"room.*",
		"scraper.state:box1",
		"scraper.events:box1",
		"box.view:box1",
		"token.mint:spectator",
		"*",
		"Room.Join:host:box1:tick", // ParseScope is case-sensitive; Mint lowercases first
	}
	for _, s := range notCovered {
		if OverlayMintCovers(s) {
			t.Errorf("OverlayMintCovers(%q) = true, want false", s)
		}
	}
}

func TestPrincipalFromToken(t *testing.T) {
	spectator := TokenRow{
		Kid:    "sp_abc",
		Kind:   "spectator",
		Label:  "OBS 1",
		UserID: "u1",
		Scopes: []string{"Room.Join:host:box1:tick", "overlay.read_state:box1", "room.join:host:box1:tick"},
	}
	p := PrincipalFromToken(spectator)
	if p.Kind != KindSpectator || p.ID != "sp_abc" || p.UserID != "u1" {
		t.Fatalf("spectator principal = %+v", p)
	}
	if fmt.Sprint(p.Scopes) != "[overlay.read_state:box1 room.join:host:box1:tick]" {
		t.Fatalf("scopes not canonicalised: %v", p.Scopes)
	}
	if p.BoundInstance() != "box1" {
		t.Fatalf("spectator should bind to box1, got %q", p.BoundInstance())
	}
	if p.Extra["label"] != "OBS 1" || p.Extra["token_user"] != "u1" {
		t.Fatalf("extra = %v", p.Extra)
	}
	if p.Collection != "" || p.AuditActorID() != "" {
		t.Fatal("token principals are never audit actors")
	}

	// A spectator row whose scopes disagree on the instance binds nothing.
	mixed := spectator
	mixed.Scopes = []string{"overlay.read_state:box1", "overlay.read_state:box2"}
	if got := PrincipalFromToken(mixed).BoundInstance(); got != "" {
		t.Fatalf("mixed spectator bound = %q, want none", got)
	}

	device := TokenRow{Kid: "dv_1", Kind: "device", Container: "box7", Scopes: []string{"box.view:box7"}}
	if got := PrincipalFromToken(device); got.Kind != KindDevice || got.BoundInstance() != "box7" {
		t.Fatalf("device principal = %+v", got)
	}

	machine := TokenRow{
		Kid:       "mk_1",
		Kind:      "machine",
		StationID: "station-3",
		Scopes:    []string{"lan.*"},
		Gamertags: []string{" Chief ", "chief", "Arbiter", ""},
	}
	m := PrincipalFromToken(machine)
	if m.Kind != KindMachine || m.BoundInstance() != "" {
		t.Fatalf("machine principal = %+v", m)
	}
	if m.Extra["station_id"] != "station-3" || m.Extra["gamertags"] != "chief,arbiter" {
		t.Fatalf("machine extra = %v", m.Extra)
	}
	if _, ok := m.Extra["token_user"]; ok {
		t.Fatal("no token_user without a UserID")
	}

	if got := PrincipalFromToken(TokenRow{Kid: "x", Kind: "bogus", Scopes: []string{"*"}}); got.Kind != KindAnonymous || len(got.Scopes) != 0 {
		t.Fatalf("unknown kind must yield Nobody(), got %+v", got)
	}
}
