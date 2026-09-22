package pb_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb/pbtest"
)

func captureStartup(r pb.Report) []string {
	var lines []string
	pb.LogStartup(r, func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	})
	return lines
}

func TestLogStartupEightLines(t *testing.T) {
	// The worst-case boot: everything missing, origins unset.
	r := pb.Report{
		MissingRoles:    []string{"admin", "anonymous"},
		TokenCounts:     map[string]int{},
		AnonymousScopes: []string{},
		WSOriginsUnset:  true,
	}
	lines := captureStartup(r)
	if len(lines) != 8 {
		t.Fatalf("lines = %d, want 8:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	wantPrefix := []string{
		"authz: roles MISSING: [admin, anonymous]",
		"authz: WARNING no admin users",
		"authz: console door CLOSED (anonymous role has no scopes)",
		"authz: console door CLOSED (?console= connects but can join nothing)",
		"authz: LAN_SAVES_TOKEN unset",
		"authz: api_tokens machine=0 spectator=0 device=0 (revoked=0, expired=0)",
		"authz: WARNING /api/lan/* has no valid key",
		"authz: WS_ALLOWED_ORIGINS unset",
	}
	for i, want := range wantPrefix {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d = %q, want prefix %q", i+1, lines[i], want)
		}
	}

	// The healthy boot: origins set ⇒ seven lines, all positive.
	r = pb.Report{
		RolesOK:         true,
		TokenCounts:     map[string]int{"machine": 2, "spectator": 1, "device": 0, "revoked": 3, "expired": 1},
		LegacyImported:  true,
		ConsoleDoorOpen: true,
		AnonymousScopes: []string{"overlay.read_state:*", "room.join:*"},
		AdminCount:      1,
	}
	lines = captureStartup(r)
	if len(lines) != 7 {
		t.Fatalf("lines = %d, want 7:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	wantPrefix = []string{
		"authz: roles ok (admin, organizer, member, overlay_manager, anonymous)",
		"authz: admins=1",
		"authz: anonymous scopes=[overlay.read_state:*, room.join:*]",
		"authz: console door OPEN",
		"authz: LAN_SAVES_TOKEN imported as kid=legacy-env scopes=[lan.saves.*, lan.sync.*] — WARNING",
		"authz: api_tokens machine=2 spectator=1 device=0 (revoked=3, expired=1)",
		"authz: /api/lan/* fail-closed: 2 live machine keys",
	}
	for i, want := range wantPrefix {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d = %q, want prefix %q", i+1, lines[i], want)
		}
	}
	// nil logger is a no-op.
	pb.LogStartup(r, nil)
}

func TestInspect(t *testing.T) {
	t.Setenv("WS_ALLOWED_ORIGINS", "")
	app, d := pbtest.NewApp(t)

	r := pb.Inspect(app, d)
	if !r.RolesOK || len(r.MissingRoles) != 0 || r.AdminCount != 0 || r.LegacyImported || !r.WSOriginsUnset {
		t.Fatalf("fresh report = %s", r)
	}
	if !r.ConsoleDoorOpen || len(r.AnonymousScopes) == 0 {
		t.Fatalf("seed anonymous scopes should open the door: %s", r)
	}
	for k, n := range r.TokenCounts {
		if n != 0 {
			t.Fatalf("fresh token count %s = %d", k, n)
		}
	}

	admin := pbtest.NewUser(t, app, "a@test.dev")
	pbtest.GrantRole(t, app, admin.Id, "admin")
	pbtest.MintToken(t, app, d, "machine", []string{"lan.*"})
	pbtest.MintToken(t, app, d, "spectator", []string{"overlay.read_state:xc-1"})
	rkid, _ := pbtest.MintToken(t, app, d, "device", []string{"box.view:xc-1"})
	pbtest.SetField(t, app, "api_tokens", pbtest.TokenRecord(t, app, rkid).Id, "revoked", true)
	ekid, _ := pbtest.MintToken(t, app, d, "machine", []string{"lan.*"})
	pbtest.SetField(t, app, "api_tokens", pbtest.TokenRecord(t, app, ekid).Id, "expires_at", time.Now().Add(-time.Hour))
	pb.ImportLegacyEnv(d, func(string) string { return "x" })
	pbtest.SetRoleScopes(t, app, "anonymous", nil)
	t.Setenv("WS_ALLOWED_ORIGINS", "https://example.test")

	r = pb.Inspect(app, d)
	if r.AdminCount != 1 || !r.LegacyImported || r.WSOriginsUnset || r.ConsoleDoorOpen {
		t.Fatalf("report = %s", r)
	}
	want := map[string]int{"machine": 1, "spectator": 1, "device": 0, "revoked": 1, "expired": 1}
	for k, n := range want {
		if r.TokenCounts[k] != n {
			t.Errorf("TokenCounts[%s] = %d, want %d", k, r.TokenCounts[k], n)
		}
	}

	// A missing seed role is reported.
	role, _ := app.FindFirstRecordByData("roles", "slug", "overlay_manager")
	if err := app.Delete(role); err != nil {
		t.Fatalf("delete role: %v", err)
	}
	d.InvalidateRoles()
	r = pb.Inspect(app, d)
	if r.RolesOK || len(r.MissingRoles) != 1 || r.MissingRoles[0] != "overlay_manager" {
		t.Fatalf("missing role report = %s", r)
	}
	if lines := captureStartup(r); !strings.HasPrefix(lines[0], "authz: roles MISSING: [overlay_manager]") {
		t.Fatalf("line 1 = %q", lines[0])
	}
}
