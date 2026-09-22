package migrations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
)

// Drift tests for authz.SeedRoles (design §5.4): the seed is the one place
// default scopes are written, so it must stay parseable by the engine that
// reads it back.

func TestSeedRolesParse(t *testing.T) {
	for _, r := range authz.SeedRoles {
		if r.Scopes == nil {
			t.Errorf("%s: Scopes must be non-nil (json \"[]\", never null)", r.Slug)
		}
		for _, s := range r.Scopes {
			if _, err := authz.ParseScope(s); err != nil {
				t.Errorf("%s: scope %q: %v", r.Slug, s, err)
			}
			if authz.Canon(s) != s {
				t.Errorf("%s: scope %q is not canonical", r.Slug, s)
			}
		}
	}
}

func TestSeedRolesNoWSSend(t *testing.T) {
	for _, r := range authz.SeedRoles {
		for _, s := range r.Scopes {
			pat, err := authz.ParseScope(s)
			if err != nil {
				continue // TestSeedRolesParse reports it
			}
			if pat.Action == "ws" || strings.HasPrefix(pat.Action, "ws.") {
				t.Errorf("%s: %q — ws.send is not a scope", r.Slug, s)
			}
			if authz.Match(s, string(authz.ActionWSSend)) {
				t.Errorf("%s: %q matches ws.send", r.Slug, s)
			}
		}
	}
}

func TestSeedAnonymousMatchesDecisions(t *testing.T) {
	want := []string{
		"room.join:host:*:game_filtered",
		"room.join:host:*:tick",
		"room.join:host:*:scenario",
		"room.join:host:*:event_filtered",
		"overlay.read_state:*",
	}
	r, ok := authz.SeedRole("anonymous")
	if !ok {
		t.Fatal("no anonymous seed row")
	}
	if r.Level != 0 {
		t.Errorf("anonymous level = %d, want 0", r.Level)
	}
	if len(r.Scopes) != len(want) {
		t.Fatalf("anonymous scopes = %v, want %v", r.Scopes, want)
	}
	for i := range want {
		if r.Scopes[i] != want[i] {
			t.Errorf("anonymous scope[%d] = %q, want %q", i, r.Scopes[i], want[i])
		}
	}
	// The raw game / event / objects / debug rooms must stay closed to the
	// console door.
	for _, closed := range []string{"room.join:host:box1:game", "room.join:host:box1:event", "room.join:host:box1:objects", "room.join:host:box1:debug", "room.join:host:box1", "room.join:host:summary", "box.view:box1"} {
		if _, hit := authz.MatchAny(r.Scopes, closed); hit {
			t.Errorf("anonymous seed grants %q", closed)
		}
	}
}

func TestSeedSlugsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range authz.SeedRoles {
		if seen[r.Slug] {
			t.Errorf("duplicate seed slug %q", r.Slug)
		}
		seen[r.Slug] = true
		if r.Label == "" {
			t.Errorf("%s: empty label", r.Slug)
		}
		if r.Level < 0 || r.Level > 100 {
			t.Errorf("%s: level %d outside the roles.level range 0..100", r.Slug, r.Level)
		}
	}
	for _, slug := range []string{"admin", "organizer", "member", "overlay_manager", "anonymous"} {
		if !seen[slug] {
			t.Errorf("seed is missing %q", slug)
		}
	}
	if _, ok := authz.SeedRole("nope"); ok {
		t.Error("SeedRole(nope) should miss")
	}
}

// TestAuthzMigrationsApply runs 1788300001..3 on a bare PocketBase (system
// collections + minimal roles / user_roles / gamertags stand-ins) and checks
// what they leave behind, then re-runs each up to prove idempotence.
//
// The collections snapshot is deliberately not applied: PocketBase 0.36's
// Collection.UnmarshalJSON recurses without bound under Go's json/v2-backed
// encoding/json, so a tests.NewTestApp() that runs every registered
// migration overflows the stack on toolchains with jsonv2 enabled.
func TestAuthzMigrationsApply(t *testing.T) {
	app := newBareApp(t)

	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	rolesCol := core.NewBaseCollection("roles")
	rolesCol.Fields.Add(
		&core.TextField{Name: "slug", Required: true, Min: 2, Max: 60, Pattern: "^[a-z0-9]+(?:_[a-z0-9]+)*$", Presentable: true},
		&core.TextField{Name: "label", Required: true, Min: 1, Max: 80},
		&core.TextField{Name: "description", Max: 500},
		&core.NumberField{Name: "level", Min: ptrFloat(0), Max: ptrFloat(100), OnlyInt: true},
	)
	rolesCol.AddIndex("idx_roles_slug_unique", true, "slug", "")
	if err := app.Save(rolesCol); err != nil {
		t.Fatalf("roles stand-in: %v", err)
	}
	ur := core.NewBaseCollection("user_roles")
	ur.Fields.Add(
		&core.RelationField{Name: "user", CollectionId: users.Id, MaxSelect: 1, Required: true},
		&core.RelationField{Name: "role", CollectionId: rolesCol.Id, MaxSelect: 1, Required: true},
		// Drifted on purpose: the assert migration must flip it back.
		&core.RelationField{Name: "granted_by", CollectionId: users.Id, MaxSelect: 1, Required: true},
	)
	if err := app.Save(ur); err != nil {
		t.Fatalf("user_roles stand-in: %v", err)
	}
	gt := core.NewBaseCollection("gamertags")
	gt.Fields.Add(&core.TextField{Name: "gamertag", Required: true, Min: 1})
	if err := app.Save(gt); err != nil {
		t.Fatalf("gamertags stand-in: %v", err)
	}
	// A pre-existing operator row (no scopes field exists yet): keeps its id,
	// level and label; only scopes get filled in.
	pre := core.NewRecord(rolesCol)
	pre.Set("slug", "organizer")
	pre.Set("label", "Ops")
	pre.Set("level", 55)
	if err := app.Save(pre); err != nil {
		t.Fatalf("pre-existing organizer: %v", err)
	}
	// A pre-M08 row at level 0 with no scopes: level and scopes get filled.
	member := core.NewRecord(rolesCol)
	member.Set("slug", "member")
	member.Set("label", "Member (legacy)")
	member.Set("level", 0)
	if err := app.Save(member); err != nil {
		t.Fatalf("pre-existing member: %v", err)
	}

	for _, file := range authzMigrationFiles {
		if err := runUp(app, file); err != nil {
			t.Fatalf("%s up: %v", file, err)
		}
	}

	roles, err := app.FindCollectionByNameOrId("roles")
	if err != nil {
		t.Fatal(err)
	}
	f, ok := roles.Fields.GetByName("scopes").(*core.JSONField)
	if !ok {
		t.Fatal("roles.scopes JSON field missing")
	}
	if f.MaxSize != 65536 {
		t.Errorf("roles.scopes MaxSize = %d, want 65536", f.MaxSize)
	}

	for _, seed := range authz.SeedRoles {
		rec := mustRecord(t, app, "roles", "slug", seed.Slug)
		wantLevel, wantLabel, wantScopes := seed.Level, seed.Label, seed.Scopes
		switch seed.Slug {
		case "organizer":
			wantLevel, wantLabel = 55, "Ops"
		case "member":
			wantLabel = "Member (legacy)"
		}
		if got := rec.GetInt("level"); got != wantLevel {
			t.Errorf("role %q level = %d, want %d", seed.Slug, got, wantLevel)
		}
		if got := rec.GetString("label"); got != wantLabel {
			t.Errorf("role %q label = %q, want %q", seed.Slug, got, wantLabel)
		}
		var scopes []string
		if err := json.Unmarshal([]byte(rec.GetString("scopes")), &scopes); err != nil {
			t.Errorf("role %q scopes %q: %v", seed.Slug, rec.GetString("scopes"), err)
			continue
		}
		if scopes == nil {
			t.Errorf("role %q scopes stored as null", seed.Slug)
		}
		if strings.Join(scopes, "\n") != strings.Join(wantScopes, "\n") {
			t.Errorf("role %q scopes = %v, want %v", seed.Slug, scopes, wantScopes)
		}
	}
	if pre2 := mustRecord(t, app, "roles", "slug", "organizer"); pre2.Id != pre.Id {
		t.Error("seed replaced the pre-existing organizer row instead of keeping it")
	}
	if n, err := app.CountRecords("roles"); err != nil || n != int64(len(authz.SeedRoles)) {
		t.Errorf("roles count = %d (%v), want %d", n, err, len(authz.SeedRoles))
	}

	// api_tokens
	tokens, err := app.FindCollectionByNameOrId("api_tokens")
	if err != nil {
		t.Fatalf("api_tokens collection: %v", err)
	}
	if tokens.ListRule != nil || tokens.ViewRule != nil || tokens.CreateRule != nil || tokens.UpdateRule != nil || tokens.DeleteRule != nil {
		t.Error("api_tokens must have nil API rules")
	}
	for _, name := range []string{"kid", "key_hash", "kind", "scopes", "label", "user", "container", "station_id", "gamertags", "minted_by", "expires_at", "revoked", "revoked_at", "revoked_by", "last_used_at"} {
		if tokens.Fields.GetByName(name) == nil {
			t.Errorf("api_tokens missing field %q", name)
		}
	}
	if kind, ok := tokens.Fields.GetByName("kind").(*core.SelectField); !ok || strings.Join(kind.Values, ",") != "machine,spectator,device" || !kind.Required {
		t.Errorf("api_tokens.kind = %+v", tokens.Fields.GetByName("kind"))
	}
	if tags, ok := tokens.Fields.GetByName("gamertags").(*core.RelationField); !ok || !tags.IsMultiple() || tags.CollectionId != gt.Id {
		t.Errorf("api_tokens.gamertags must be a multiple relation to gamertags: %+v", tokens.Fields.GetByName("gamertags"))
	}
	for _, name := range []string{"user", "minted_by", "revoked_by"} {
		if rel, ok := tokens.Fields.GetByName(name).(*core.RelationField); !ok || rel.Required || rel.IsMultiple() || rel.CollectionId != users.Id {
			t.Errorf("api_tokens.%s must be an optional single relation to users: %+v", name, tokens.Fields.GetByName(name))
		}
	}
	if kid, ok := tokens.Fields.GetByName("kid").(*core.TextField); !ok || !kid.Required {
		t.Error("api_tokens.kid must be required")
	}
	if kh, ok := tokens.Fields.GetByName("key_hash").(*core.TextField); !ok || !kh.Required {
		t.Error("api_tokens.key_hash must be required")
	}
	if sc, ok := tokens.Fields.GetByName("scopes").(*core.JSONField); !ok || !sc.Required {
		t.Error("api_tokens.scopes must be required")
	}
	joined := strings.Join(tokens.Indexes, "\n")
	if len(tokens.Indexes) != 2 || !strings.Contains(joined, "idx_api_tokens_kid") || !strings.Contains(joined, "idx_api_tokens_kind_revoked") {
		t.Errorf("api_tokens indexes = %v", tokens.Indexes)
	}
	rec := core.NewRecord(tokens)
	rec.Set("kid", "mk_abc")
	rec.Set("key_hash", authz.HashSecret("s"))
	rec.Set("kind", "machine")
	rec.Set("scopes", []string{"lan.*"})
	if err := app.Save(rec); err != nil {
		t.Fatalf("insert api_tokens row: %v", err)
	}
	dup := core.NewRecord(tokens)
	dup.Set("kid", "mk_abc")
	dup.Set("key_hash", authz.HashSecret("t"))
	dup.Set("kind", "machine")
	dup.Set("scopes", []string{"lan.*"})
	if err := app.Save(dup); err == nil {
		t.Error("duplicate kid must be rejected by idx_api_tokens_kid")
	}
	badKind := core.NewRecord(tokens)
	badKind.Set("kid", "sp_nope")
	badKind.Set("key_hash", authz.HashSecret("u"))
	badKind.Set("kind", "pb_user")
	badKind.Set("scopes", []string{"lan.*"})
	if err := app.Save(badKind); err == nil {
		t.Error("unknown kind must be rejected")
	}
	noScopes := core.NewRecord(tokens)
	noScopes.Set("kid", "sp_none")
	noScopes.Set("key_hash", authz.HashSecret("v"))
	noScopes.Set("kind", "spectator")
	noScopes.Set("scopes", []string{})
	if err := app.Save(noScopes); err == nil {
		t.Error("empty scopes must be rejected")
	}

	// user_roles.granted_by converged to optional.
	ur, _ = app.FindCollectionByNameOrId("user_roles")
	if gb, ok := ur.Fields.GetByName("granted_by").(*core.RelationField); !ok || gb.Required {
		t.Errorf("user_roles.granted_by must be an optional relation: %+v", ur.Fields.GetByName("granted_by"))
	}

	// Every up is idempotent; operator edits survive a re-run.
	admin := mustRecord(t, app, "roles", "slug", "admin")
	admin.Set("scopes", []string{"admin.users"})
	admin.Set("level", 77)
	if err := app.Save(admin); err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(mustRecord(t, app, "roles", "slug", "anonymous")); err != nil {
		t.Fatal(err)
	}
	for _, file := range authzMigrationFiles {
		if err := runUp(app, file); err != nil {
			t.Fatalf("%s second up: %v", file, err)
		}
	}
	admin = mustRecord(t, app, "roles", "slug", "admin")
	if got := admin.GetString("scopes"); got != `["admin.users"]` || admin.GetInt("level") != 77 {
		t.Errorf("re-run overwrote operator edits: scopes=%s level=%d", got, admin.GetInt("level"))
	}
	if anon := mustRecord(t, app, "roles", "slug", "anonymous"); anon.GetInt("level") != 0 || anon.GetString("scopes") == "" {
		t.Errorf("re-run should recreate the deleted anonymous row: %+v", anon)
	}
	if _, err := app.FindFirstRecordByData("api_tokens", "kid", "mk_abc"); err != nil {
		t.Errorf("re-run must not recreate api_tokens: %v", err)
	}

	// Downs.
	for i := len(authzMigrationFiles) - 1; i >= 0; i-- {
		if err := runDown(app, authzMigrationFiles[i]); err != nil {
			t.Fatalf("%s down: %v", authzMigrationFiles[i], err)
		}
	}
	if _, err := app.FindCollectionByNameOrId("api_tokens"); err == nil {
		t.Error("api_tokens should be gone after down")
	}
	roles, _ = app.FindCollectionByNameOrId("roles")
	if roles.Fields.GetByName("scopes") != nil {
		t.Error("roles.scopes should be gone after down")
	}
	if n, _ := app.CountRecords("roles"); n != int64(len(authz.SeedRoles)) {
		t.Errorf("down must keep the role rows, %d left", n)
	}
}

var authzMigrationFiles = []string{
	"1788300001_roles_scopes.go",
	"1788300002_api_tokens.go",
	"1788300003_user_roles_granted_by_optional.go",
}

// newBareApp boots PocketBase on a scratch data dir with only the system
// collections (Bootstrap runs core.SystemMigrations, never AppMigrations).
func newBareApp(t *testing.T) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), EncryptionEnv: "pb_test_env"})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	return app
}

func ptrFloat(f float64) *float64 { return &f }

func mustRecord(t *testing.T, app core.App, collection, key, value string) *core.Record {
	t.Helper()
	rec, err := app.FindFirstRecordByData(collection, key, value)
	if err != nil {
		t.Fatalf("%s %s=%q: %v", collection, key, value, err)
	}
	return rec
}

// runUp / runDown invoke one registered migration outside the runner so the
// test controls ordering and can re-apply.
func runUp(app core.App, file string) error {
	mig := findMigration(file)
	if mig == nil {
		return errNoMigration(file)
	}
	return mig.Up(app)
}

func runDown(app core.App, file string) error {
	mig := findMigration(file)
	if mig == nil {
		return errNoMigration(file)
	}
	return mig.Down(app)
}

func findMigration(file string) *core.Migration {
	for _, mig := range core.AppMigrations.Items() {
		if mig.File == file {
			return mig
		}
	}
	return nil
}

type errNoMigration string

func (e errNoMigration) Error() string { return "no registered migration " + string(e) }
