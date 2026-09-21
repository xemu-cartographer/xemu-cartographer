package pb_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xemu-cartographer/xemu-cartographer/internal/audit"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb/pbtest"
)

func TestMintValidates(t *testing.T) {
	app, d := pbtest.NewApp(t)
	internal := authz.Internal("test")

	cases := []struct {
		name   string
		req    pb.MintRequest
		wantIs error
	}{
		{"unknown kind", pb.MintRequest{Kind: "robot", Scopes: []string{"lan.*"}}, authz.ErrScopeSyntax},
		{"no scopes", pb.MintRequest{Kind: "machine"}, authz.ErrScopeSyntax},
		{"bad grammar", pb.MintRequest{Kind: "machine", Scopes: []string{"nope.nothing"}}, authz.ErrScopeSyntax},
		{"spectator wildcard", pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.*"}}, authz.ErrWildcardKind},
		{"spectator two instances", pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state:a", "overlay.read_state:b"}}, authz.ErrSpectatorInstance},
		{"spectator no instance", pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state"}}, authz.ErrSpectatorInstance},
		{"spectator wrong action", pb.MintRequest{Kind: "spectator", Scopes: []string{"lan.saves.file:x"}}, authz.ErrScopeSyntax},
		{"device wildcard", pb.MintRequest{Kind: "device", Scopes: []string{"box.view:*"}}, authz.ErrWildcardKind},
		{"past expiry", pb.MintRequest{Kind: "machine", Scopes: []string{"lan.*"}, ExpiresAt: ptrTime(time.Now().Add(-time.Hour))}, pb.ErrMintRequest},
		{"unknown user", pb.MintRequest{Kind: "machine", Scopes: []string{"lan.*"}, UserID: "nosuchuser00000"}, pb.ErrMintRequest},
		{"unknown gamertag", pb.MintRequest{Kind: "machine", Scopes: []string{"lan.*"}, Gamertags: []string{"ghost"}}, pb.ErrMintRequest},
	}
	for _, tc := range cases {
		_, err := pb.Mint(app, d, internal, tc.req)
		if !errors.Is(err, tc.wantIs) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantIs)
		}
	}
	if rows, _ := app.FindAllRecords("api_tokens"); len(rows) != 0 {
		t.Fatalf("refused mints wrote %d rows", len(rows))
	}

	// Valid machine key, bound to a user + gamertag, minted by a pb_user.
	admin := pbtest.NewUser(t, app, "admin@test.dev")
	pbtest.GrantRole(t, app, admin.Id, "admin")
	owner := pbtest.NewUser(t, app, "owner@test.dev")
	tag := pbtest.NewTag(t, app, owner.Id, "Chief 117", "approved")
	actor := pb.PrincipalFromAuth(app, d, admin)
	res, err := pb.Mint(app, d, actor, pb.MintRequest{
		Kind: "Machine", Label: " station A ", Scopes: []string{"LAN.SAVES.*", "lan.saves.*", "lan.sync.manifest"},
		UserID: owner.Id, StationID: "stn-a", Gamertags: []string{tag.Id},
	})
	if err != nil {
		t.Fatalf("mint machine: %v", err)
	}
	if !strings.HasPrefix(res.Kid, "mk_") || !strings.HasPrefix(res.Token, res.Kid+".") {
		t.Fatalf("kid/token = %q / %q", res.Kid, res.Token)
	}
	if res.Row.KeyHash != "" {
		t.Fatalf("MintResult leaks the hash")
	}
	if res.Row.Label != "station A" || res.Row.UserID != owner.Id || res.Row.StationID != "stn-a" {
		t.Fatalf("row = %+v", res.Row)
	}
	if got := strings.Join(res.Row.Scopes, ","); got != "lan.saves.*,lan.sync.manifest" {
		t.Fatalf("scopes not canonical: %q", got)
	}
	if len(res.Row.Gamertags) != 1 || res.Row.Gamertags[0] != "chief 117" {
		t.Fatalf("gamertags = %v", res.Row.Gamertags)
	}
	rec := pbtest.TokenRecord(t, app, res.Kid)
	_, secret, _ := authz.SplitToken(res.Token)
	if rec.GetString("key_hash") != authz.HashSecret(secret) {
		t.Fatalf("stored hash does not match the secret")
	}
	if rec.GetString("minted_by") != admin.Id {
		t.Fatalf("minted_by = %q", rec.GetString("minted_by"))
	}
	if !rec.GetDateTime("expires_at").IsZero() {
		t.Fatalf("machine key must not expire by default")
	}
	rows := auditRows(t, app, audit.ActionTokenMint)
	if len(rows) != 1 || rows[0].GetString("actor") != admin.Id || rows[0].GetString("target_id") != rec.Id {
		t.Fatalf("mint audit = %d rows", len(rows))
	}
	var payload audit.TokenMintPayload
	payloadOf(t, rows[0], &payload)
	if payload.Kid != res.Kid || payload.Kind != "machine" || len(payload.Scopes) != 2 {
		t.Fatalf("payload = %+v", payload)
	}

	// The minted key resolves and carries the PD-8 binding.
	p, err := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/", map[string]string{"X-Api-Key": res.Token}))
	if err != nil || p.Kind != authz.KindMachine || p.Extra["gamertags"] != "chief 117" || p.Extra["station_id"] != "stn-a" {
		t.Fatalf("resolved = %+v err=%v", p, err)
	}

	// Spectator: default expiry, bound to the one instance.
	sres, err := pb.Mint(app, d, actor, pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state:xc-1", "room.join:host:xc-1:game"}})
	if err != nil {
		t.Fatalf("mint spectator: %v", err)
	}
	if sres.Row.ExpiresAt.IsZero() || time.Until(sres.Row.ExpiresAt) > pb.SpectatorTTL+time.Minute {
		t.Fatalf("spectator expiry = %v", sres.Row.ExpiresAt)
	}
	if !strings.HasPrefix(sres.Kid, "sp_") {
		t.Fatalf("spectator kid = %q", sres.Kid)
	}
	// Device: container defaults to the scoped instance.
	dres, err := pb.Mint(app, d, actor, pb.MintRequest{Kind: "device", Scopes: []string{"box.view:xc-2"}})
	if err != nil {
		t.Fatalf("mint device: %v", err)
	}
	if dres.Row.Container != "xc-2" || !strings.HasPrefix(dres.Kid, "dv_") {
		t.Fatalf("device row = %+v kid=%q", dres.Row, dres.Kid)
	}

	// Authorization: a member with no token scope is refused; an
	// overlay_manager may mint spectator keys but not machine keys.
	member := pbtest.NewUser(t, app, "member@test.dev")
	pbtest.GrantRole(t, app, member.Id, "member")
	_, err = pb.Mint(app, d, pb.PrincipalFromAuth(app, d, member), pb.MintRequest{Kind: "machine", Scopes: []string{"lan.*"}})
	if !errors.Is(err, pb.ErrForbidden) {
		t.Fatalf("member mint: %v", err)
	}
	om := pbtest.NewUser(t, app, "om@test.dev")
	pbtest.GrantRole(t, app, om.Id, "overlay_manager")
	omp := pb.PrincipalFromAuth(app, d, om)
	if _, err := pb.Mint(app, d, omp, pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state:xc-1"}}); err != nil {
		t.Fatalf("overlay_manager spectator mint: %v", err)
	}
	if _, err := pb.Mint(app, d, omp, pb.MintRequest{Kind: "machine", Scopes: []string{"lan.*"}}); !errors.Is(err, pb.ErrForbidden) {
		t.Fatalf("overlay_manager machine mint: %v", err)
	}
	// A machine key holding token.mint may mint too (rowScopeUserMachine) —
	// within the scopes it holds itself (the delegation ceiling).
	_, mtok := pbtest.MintToken(t, app, d, "machine", []string{"token.mint:machine", "lan.saves.*"})
	mp, _ := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/", map[string]string{"X-Api-Key": mtok}))
	if _, err := pb.Mint(app, d, mp, pb.MintRequest{Kind: "machine", Scopes: []string{"lan.saves.*"}}); err != nil {
		t.Fatalf("machine minting machine: %v", err)
	}
	if _, err := pb.Mint(app, d, mp, pb.MintRequest{Kind: "device", Scopes: []string{"box.view:xc-1"}}); !errors.Is(err, pb.ErrForbidden) {
		t.Fatalf("token.mint:machine minting device: %v", err)
	}
}

// TestMintCeiling pins the delegation ceiling: a minter hands down at most
// what it holds. Superusers / internal callers and admins are exempt (PD-13
// puts wildcard machine keys in admin hands); everyone else is refused with
// ErrMintCeiling for any scope its own list does not cover, and a literal
// scope never covers a wildcard. The seed roles are used as shipped.
func TestMintCeiling(t *testing.T) {
	app, d := pbtest.NewApp(t)

	// Machine key with token.mint:machine only: "*" and every scope it does
	// not hold are refused; the ceiling stops the key from listing every
	// token through a minted-up child.
	_, mtok := pbtest.MintToken(t, app, d, "machine", []string{"token.mint:machine", "lan.saves.file:*"})
	mp, err := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/", map[string]string{"X-Api-Key": mtok}))
	if err != nil || mp.Kind != authz.KindMachine {
		t.Fatalf("machine principal = %+v err=%v", mp, err)
	}
	for _, scopes := range [][]string{
		{"*"},
		{"token.*"},
		{"lan.*"},
		{"lan.saves.*"},
		{"lan.saves.file:*", "lan.sync.manifest"},
		{"token.mint:*"},
		{"token.list"},
	} {
		_, err := pb.Mint(app, d, mp, pb.MintRequest{Kind: "machine", Scopes: scopes})
		if !errors.Is(err, pb.ErrMintCeiling) {
			t.Errorf("machine key minting %v: err = %v, want ErrMintCeiling", scopes, err)
		}
	}
	// What it holds — exactly, narrower, or the same literal — is fine.
	for _, scopes := range [][]string{
		{"token.mint:machine"},
		{"lan.saves.file:*"},
		{"lan.saves.file:abc"},
		{"lan.saves.file:*", "token.mint:machine"},
	} {
		if _, err := pb.Mint(app, d, mp, pb.MintRequest{Kind: "machine", Scopes: scopes}); err != nil {
			t.Errorf("machine key minting %v: %v", scopes, err)
		}
	}
	if rows, err := pb.ListTokens(app, d, authz.Internal("t"), "machine"); err != nil || len(rows) != 5 {
		t.Fatalf("machine rows = %d err=%v, want 5 (1 + 4 allowed mints)", len(rows), err)
	}

	// Admin (role) and superuser mint "*" freely.
	admin := pbtest.NewUser(t, app, "admin@test.dev")
	pbtest.GrantRole(t, app, admin.Id, "admin")
	if _, err := pb.Mint(app, d, pb.PrincipalFromAuth(app, d, admin), pb.MintRequest{Kind: "machine", Scopes: []string{"*"}}); err != nil {
		t.Fatalf("admin minting *: %v", err)
	}
	su := pbtest.NewSuperuser(t, app, "root@test.dev")
	if _, err := pb.Mint(app, d, pb.PrincipalFromAuth(app, d, su), pb.MintRequest{Kind: "machine", Scopes: []string{"*"}}); err != nil {
		t.Fatalf("superuser minting *: %v", err)
	}
	// A wildcard-holding machine key (admin-minted) may delegate a narrower
	// wildcard, but not a wider one.
	_, wtok := pbtest.MintToken(t, app, d, "machine", []string{"token.mint:machine", "lan.saves.*"})
	wp, _ := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/", map[string]string{"X-Api-Key": wtok}))
	if _, err := pb.Mint(app, d, wp, pb.MintRequest{Kind: "machine", Scopes: []string{"lan.saves.file:*"}}); err != nil {
		t.Fatalf("lan.saves.* delegating lan.saves.file:*: %v", err)
	}
	if _, err := pb.Mint(app, d, wp, pb.MintRequest{Kind: "machine", Scopes: []string{"lan.*"}}); !errors.Is(err, pb.ErrMintCeiling) {
		t.Fatalf("lan.saves.* delegating lan.*: %v", err)
	}

	// overlay_manager (seed: overlay.mint, no room.join): overlay.mint covers
	// the overlay spectator grant on any instance — read_state plus the four
	// OverlayClasses rooms — and nothing beyond it.
	om := pbtest.NewUser(t, app, "om@test.dev")
	pbtest.GrantRole(t, app, om.Id, "overlay_manager")
	omp := pb.PrincipalFromAuth(app, d, om)
	for _, class := range authz.OverlayClasses {
		if _, err := pb.Mint(app, d, omp, pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state:xc-1", "room.join:host:xc-1:" + class}}); err != nil {
			t.Errorf("overlay_manager spectator (%s): %v", class, err)
		}
	}
	for _, scope := range []string{
		"room.join:host:xc-1:game",    // unfiltered class
		"room.join:host:xc-1:objects", // not an overlay class
		"scraper.state:xc-1",          // spectator-mintable action outside the grant
		"scraper.events:xc-1",
	} {
		_, err := pb.Mint(app, d, omp, pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state:xc-1", scope}})
		if !errors.Is(err, pb.ErrMintCeiling) || !strings.Contains(err.Error(), scope) {
			t.Errorf("overlay_manager spectator (%s): err = %v, want ErrMintCeiling naming it", scope, err)
		}
	}
	// A machine key licensed for spectator keys through token.mint:spectator
	// rather than overlay.mint gets no such reading: its own list is the
	// ceiling, class by class.
	_, stok := pbtest.MintToken(t, app, d, "machine", []string{"token.mint:spectator", "overlay.read_state:*", "room.join:host:*:tick"})
	sp, _ := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/", map[string]string{"X-Api-Key": stok}))
	if _, err := pb.Mint(app, d, sp, pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state:xc-1", "room.join:host:xc-1:tick"}}); err != nil {
		t.Fatalf("token.mint:spectator key minting held classes: %v", err)
	}
	_, err = pb.Mint(app, d, sp, pb.MintRequest{Kind: "spectator", Scopes: []string{"overlay.read_state:xc-1", "room.join:host:xc-1:game_filtered"}})
	if !errors.Is(err, pb.ErrMintCeiling) || !strings.Contains(err.Error(), "room.join:host:xc-1:game_filtered") {
		t.Fatalf("token.mint:spectator key minting an unheld class: %v", err)
	}
}

// TestOverlayManagerMintsStudioSpectator pins the PD-5 flow end to end with
// the seed role exactly as shipped: an overlay_manager mints the scope list
// Studio requests (tokens-api.ts spectatorScopesFor) and the resulting key
// is usable — it reads the instance's state and joins the four overlay
// rooms of that instance, nothing else.
func TestOverlayManagerMintsStudioSpectator(t *testing.T) {
	app, d := pbtest.NewApp(t)
	seed, ok := authz.SeedRole("overlay_manager")
	if !ok || strings.Join(seed.Scopes, ",") != "overlay.mint,overlay.list_consoles,overlay.read_state:*" {
		t.Fatalf("overlay_manager seed changed (%v) — re-check the Studio mint flow", seed.Scopes)
	}
	om := pbtest.NewUser(t, app, "om@test.dev")
	pbtest.GrantRole(t, app, om.Id, "overlay_manager")
	omp := pb.PrincipalFromAuth(app, d, om)
	if omp.IsAdmin() || omp.Level >= 100 {
		t.Fatalf("overlay_manager must not be ceiling-exempt: %+v", omp)
	}

	studio := []string{
		"overlay.read_state:xc-1",
		"room.join:host:xc-1:game_filtered",
		"room.join:host:xc-1:tick",
		"room.join:host:xc-1:scenario",
		"room.join:host:xc-1:event_filtered",
	}
	res, err := pb.Mint(app, d, omp, pb.MintRequest{Kind: "spectator", Label: "obs xc-1", Scopes: studio})
	if err != nil {
		t.Fatalf("overlay_manager minting the Studio spectator key: %v", err)
	}
	if len(res.Row.Scopes) != len(studio) {
		t.Fatalf("minted scopes = %v, want %v", res.Row.Scopes, studio)
	}
	for _, s := range studio {
		if !slices.Contains(res.Row.Scopes, s) {
			t.Fatalf("minted scopes = %v, missing %q", res.Row.Scopes, s)
		}
	}

	sp, err := pb.ResolveWS(app, d, httptest.NewRequest(http.MethodGet, "/api/ws?spectator="+res.Token, nil))
	if err != nil || sp.Kind != authz.KindSpectator || sp.BoundInstance() != "xc-1" {
		t.Fatalf("spectator resolve = %+v err=%v", sp, err)
	}
	if dec := authz.CanWith(d, sp, authz.ActionOverlayReadState, authz.Instance("xc-1")); !dec.Allow {
		t.Fatalf("spectator overlay.read_state xc-1: %+v", dec)
	}
	for _, class := range authz.OverlayClasses {
		if dec := authz.CanWith(d, sp, authz.ActionRoomJoin, authz.RoomRes(authz.Room{Type: "host", Instance: "xc-1", Class: class})); !dec.Allow {
			t.Errorf("spectator join host:xc-1:%s: %+v", class, dec)
		}
	}
	for _, rm := range []authz.Room{
		{Type: "host", Instance: "xc-1"},                // bare room (A.10)
		{Type: "host", Instance: "xc-1", Class: "game"}, // class not minted
		{Type: "host", Instance: "xc-2", Class: "tick"}, // other instance
		{Type: "host", Instance: "summary"},             // aggregate
	} {
		if dec := authz.CanWith(d, sp, authz.ActionRoomJoin, authz.RoomRes(rm)); dec.Allow {
			t.Errorf("spectator join %s allowed: %+v", rm.Selector(), dec)
		}
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestListNeverReturnsHash(t *testing.T) {
	app, d := pbtest.NewApp(t)
	pbtest.MintToken(t, app, d, "machine", []string{"lan.*"}, func(r *pb.MintRequest) { r.Label = "m1" })
	pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:xc-1"})
	dkid, _ := pbtest.MintToken(t, app, d, "device", []string{"box.view:xc-1"})
	if err := pb.RevokeToken(app, d, authz.Internal("t"), dkid, "done"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	pb.ImportLegacyEnv(d, func(string) string { return "legacy-secret" })

	rows, err := pb.ListTokens(app, d, authz.Internal("t"), "")
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (3 + legacy)", len(rows))
	}
	if rows[0].Kid != authz.LegacyKid {
		t.Fatalf("legacy row must lead: %+v", rows[0])
	}
	for _, r := range rows {
		if r.KeyHash != "" {
			t.Fatalf("row %s leaks key_hash", r.Kid)
		}
	}
	details, err := pb.ListTokenDetails(app, d, authz.Internal("t"), "device")
	if err != nil {
		t.Fatalf("ListTokenDetails: %v", err)
	}
	if len(details) != 1 || details[0].Kid != dkid || !details[0].Revoked || details[0].RevokedAt.IsZero() || details[0].KeyHash != "" {
		t.Fatalf("device details = %+v", details)
	}
	if m, _ := pb.ListTokens(app, d, authz.Internal("t"), "machine"); len(m) != 2 || m[0].Kid != authz.LegacyKid || m[1].Label != "m1" {
		t.Fatalf("machine rows = %+v", m)
	}

	// token.list is required.
	member := pbtest.NewUser(t, app, "m@test.dev")
	pbtest.GrantRole(t, app, member.Id, "member")
	if _, err := pb.ListTokens(app, d, pb.PrincipalFromAuth(app, d, member), ""); !errors.Is(err, pb.ErrForbidden) {
		t.Fatalf("member list: %v", err)
	}
	admin := pbtest.NewUser(t, app, "a@test.dev")
	pbtest.GrantRole(t, app, admin.Id, "admin")
	if _, err := pb.ListTokens(app, d, pb.PrincipalFromAuth(app, d, admin), ""); err != nil {
		t.Fatalf("admin list: %v", err)
	}
}

func TestRevokeLegacyRefused(t *testing.T) {
	app, d := pbtest.NewApp(t)
	pb.ImportLegacyEnv(d, func(string) string { return "legacy-secret" })
	admin := pbtest.NewUser(t, app, "a@test.dev")
	pbtest.GrantRole(t, app, admin.Id, "admin")
	actor := pb.PrincipalFromAuth(app, d, admin)

	if err := pb.RevokeToken(app, d, actor, authz.LegacyKid, ""); !errors.Is(err, pb.ErrLegacyToken) {
		t.Fatalf("legacy revoke: %v", err)
	}
	if !d.LegacyImported() {
		t.Fatalf("legacy row dropped by the refused revoke")
	}
	if err := pb.RevokeToken(app, d, actor, "mk_missing", ""); !errors.Is(err, authz.ErrUnknownKid) {
		t.Fatalf("unknown kid: %v", err)
	}
	// Can runs before the kid lookup: an unauthorised caller learns nothing.
	member := pbtest.NewUser(t, app, "m@test.dev")
	pbtest.GrantRole(t, app, member.Id, "member")
	if err := pb.RevokeToken(app, d, pb.PrincipalFromAuth(app, d, member), "mk_missing", ""); !errors.Is(err, pb.ErrForbidden) {
		t.Fatalf("member revoke of unknown kid: %v", err)
	}
	if err := pb.RevokeToken(app, d, pb.PrincipalFromAuth(app, d, member), authz.LegacyKid, ""); !errors.Is(err, pb.ErrForbidden) {
		t.Fatalf("member revoke of legacy kid: %v", err)
	}

	// A real revoke: flags the row, stamps revoked_by, audits, and the key
	// stops resolving. Second revoke is a no-op.
	kid, token := pbtest.MintToken(t, app, d, "machine", []string{"lan.*"})
	if err := pb.RevokeToken(app, d, actor, kid, "leaked"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rec := pbtest.TokenRecord(t, app, kid)
	if !rec.GetBool("revoked") || rec.GetString("revoked_by") != admin.Id || rec.GetDateTime("revoked_at").IsZero() {
		t.Fatalf("revoked row = revoked:%v by:%q at:%v", rec.GetBool("revoked"), rec.GetString("revoked_by"), rec.GetDateTime("revoked_at"))
	}
	if _, err := pb.ResolveRequest(app, d, newEvent(app, nil, http.MethodGet, "/", map[string]string{"X-Api-Key": token})); !errors.Is(err, authz.ErrRevoked) {
		t.Fatalf("revoked key resolve: %v", err)
	}
	if err := pb.RevokeToken(app, d, actor, kid, "again"); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	rows := auditRows(t, app, audit.ActionTokenRevoke)
	if len(rows) != 1 || rows[0].GetString("actor") != admin.Id {
		t.Fatalf("revoke audit rows = %d", len(rows))
	}
	var payload audit.TokenRevokePayload
	payloadOf(t, rows[0], &payload)
	if payload.Kid != kid || payload.Reason != "leaked" {
		t.Fatalf("payload = %+v", payload)
	}
}
