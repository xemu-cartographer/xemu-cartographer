// Package pbtest builds a PocketBase test app carrying the collections the
// authz adapter (internal/authz/pb) reads and writes, wired to a *pb.PBDeps
// with a stubbable scraper. The pb tests use it; the later slices (WS guards,
// screen proxy, LAN routes) reuse it so they don't each redeclare the schema.
//
// Typical use:
//
//	app, d := pbtest.NewApp(t)
//	admin := pbtest.NewUser(t, app, "admin@test.dev")
//	pbtest.GrantRole(t, app, admin.Id, "admin")
//	kid, token := pbtest.MintToken(t, app, d, "machine", []string{"lan.saves.*"})
//
// The schema mirrors migrations/1788300001_roles_scopes.go and
// migrations/1788300002_api_tokens.go plus the subset of the collections
// snapshot the adapter touches (user_roles, gamertags, teams, rosters,
// audit_log, the users ban / soft-delete columns). Collection rules are all
// nil — the adapter goes through app.Save / FindRecords, never the rules.
package pbtest

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
)

// DefaultPrefix is the container-name prefix NewApp hands to pb.NewDeps
// (OwnedBox → "xc-play-<user>").
const DefaultPrefix = "xc-"

// Password is the password every NewUser / NewSuperuser record gets.
const Password = "0123456789"

// NewApp is NewAppWith with an empty FakeScraper and DefaultPrefix.
func NewApp(t *testing.T) (core.App, *pb.PBDeps) {
	t.Helper()
	return NewAppWith(t, &FakeScraper{}, func() string { return DefaultPrefix })
}

// NewAppWith creates a tests.TestApp with the authz schema, seeds the five
// built-in roles (authz.SeedRoles), builds a *pb.PBDeps over it, installs it
// as pb.Default() and registers cleanup (pb.SetDefault(nil) + app.Cleanup).
// A nil scraper is allowed — the instance predicates then answer "unknown".
func NewAppWith(t *testing.T, scraper scraperiface.Service, prefix func() string) (core.App, *pb.PBDeps) {
	t.Helper()
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatalf("pbtest: NewTestApp: %v", err)
	}
	t.Cleanup(app.Cleanup)

	ensureSchema(t, app)
	SeedRoles(t, app)

	d := pb.NewDeps(app, scraper, prefix)
	pb.SetDefault(d)
	t.Cleanup(func() { pb.SetDefault(nil) })
	return app, d
}

// SeedRoles creates any of authz.SeedRoles missing from the roles
// collection (idempotent) and invalidates pb.Default()'s role cache.
func SeedRoles(t *testing.T, app core.App) {
	t.Helper()
	col, err := app.FindCollectionByNameOrId("roles")
	if err != nil {
		t.Fatalf("pbtest: roles collection: %v", err)
	}
	for _, seed := range authz.SeedRoles {
		if rec, _ := app.FindFirstRecordByData(col, "slug", seed.Slug); rec != nil {
			continue
		}
		rec := core.NewRecord(col)
		rec.Set("slug", seed.Slug)
		rec.Set("label", seed.Label)
		rec.Set("level", seed.Level)
		rec.Set("scopes", append([]string{}, seed.Scopes...))
		if err := app.Save(rec); err != nil {
			t.Fatalf("pbtest: seed role %s: %v", seed.Slug, err)
		}
	}
	pb.Default().InvalidateRoles()
}

// SetRoleScopes overwrites a seeded role's scopes (e.g. to close the
// anonymous console door) and invalidates pb.Default()'s cache.
func SetRoleScopes(t *testing.T, app core.App, slug string, scopes []string) {
	t.Helper()
	rec, err := app.FindFirstRecordByData("roles", "slug", slug)
	if err != nil {
		t.Fatalf("pbtest: role %s: %v", slug, err)
	}
	rec.Set("scopes", append([]string{}, scopes...))
	if err := app.Save(rec); err != nil {
		t.Fatalf("pbtest: save role %s: %v", slug, err)
	}
	pb.Default().InvalidateRoles()
}

// NewUser creates a users record with the given email and Password.
func NewUser(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()
	return newAuthRecord(t, app, "users", email)
}

// NewSuperuser creates a _superusers record (IsSuperuser() == true).
func NewSuperuser(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()
	return newAuthRecord(t, app, core.CollectionNameSuperusers, email)
}

func newAuthRecord(t *testing.T, app core.App, collection, email string) *core.Record {
	t.Helper()
	col, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("pbtest: %s collection: %v", collection, err)
	}
	rec := core.NewRecord(col)
	rec.Set("email", email)
	rec.Set("password", Password)
	if err := app.Save(rec); err != nil {
		t.Fatalf("pbtest: save %s %s: %v", collection, email, err)
	}
	return rec
}

// GrantRole inserts a user_roles row directly (no authz check, no audit),
// which is how the migrations / dev seeder bootstrap the first admin.
// Idempotent. Invalidates pb.Default()'s role cache.
func GrantRole(t *testing.T, app core.App, userID, slug string) {
	t.Helper()
	role, err := app.FindFirstRecordByData("roles", "slug", slug)
	if err != nil {
		t.Fatalf("pbtest: role %s: %v", slug, err)
	}
	existing, err := app.FindRecordsByFilter("user_roles", "user = {:u} && role = {:r}", "", 1, 0,
		map[string]any{"u": userID, "r": role.Id})
	if err != nil {
		t.Fatalf("pbtest: user_roles lookup: %v", err)
	}
	if len(existing) > 0 {
		return
	}
	col, err := app.FindCollectionByNameOrId("user_roles")
	if err != nil {
		t.Fatalf("pbtest: user_roles collection: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("user", userID)
	rec.Set("role", role.Id)
	if err := app.Save(rec); err != nil {
		t.Fatalf("pbtest: grant %s to %s: %v", slug, userID, err)
	}
	pb.Default().InvalidateRoles()
}

// NewTag creates a gamertags row for the user. sanitized is derived the way
// the gamertags_sanitize hook does (lower + trim), since the hook is not
// bound in a test app. status is one of approved / allowed / pending /
// blocked.
func NewTag(t *testing.T, app core.App, userID, tag, status string) *core.Record {
	t.Helper()
	col, err := app.FindCollectionByNameOrId("gamertags")
	if err != nil {
		t.Fatalf("pbtest: gamertags collection: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("user", userID)
	rec.Set("tag", tag)
	rec.Set("sanitized", scraperiface.SanitizeIdentity(tag))
	rec.Set("status", status)
	if err := app.Save(rec); err != nil {
		t.Fatalf("pbtest: save gamertag %s: %v", tag, err)
	}
	return rec
}

// NewTeam creates a teams row owned (created_by) by userID.
func NewTeam(t *testing.T, app core.App, name, userID string) *core.Record {
	t.Helper()
	col, err := app.FindCollectionByNameOrId("teams")
	if err != nil {
		t.Fatalf("pbtest: teams collection: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("name", name)
	rec.Set("created_by", userID)
	if err := app.Save(rec); err != nil {
		t.Fatalf("pbtest: save team %s: %v", name, err)
	}
	return rec
}

// NewRoster creates an active rosters row (left_at empty) linking a
// gamertag to a team.
func NewRoster(t *testing.T, app core.App, teamID, gamertagID string, owner, manager bool) *core.Record {
	t.Helper()
	col, err := app.FindCollectionByNameOrId("rosters")
	if err != nil {
		t.Fatalf("pbtest: rosters collection: %v", err)
	}
	rec := core.NewRecord(col)
	rec.Set("team", teamID)
	rec.Set("gamertag", gamertagID)
	rec.Set("is_owner", owner)
	rec.Set("is_manager", manager)
	if err := app.Save(rec); err != nil {
		t.Fatalf("pbtest: save roster: %v", err)
	}
	return rec
}

// MintToken mints an api_tokens row through pb.Mint as an internal actor
// and returns (kid, "<kid>.<secret>"). opts tweak the request (label, user,
// container, expiry, gamertags …).
func MintToken(t *testing.T, app core.App, d *pb.PBDeps, kind string, scopes []string, opts ...func(*pb.MintRequest)) (kid, token string) {
	t.Helper()
	req := pb.MintRequest{Kind: kind, Scopes: scopes, Label: "pbtest " + kind}
	for _, o := range opts {
		o(&req)
	}
	res, err := pb.Mint(app, d, authz.Internal("pbtest"), req)
	if err != nil {
		t.Fatalf("pbtest: mint %s %v: %v", kind, scopes, err)
	}
	return res.Kid, res.Token
}

// SetField sets one column on a record of the collection and saves it
// without validation — the shortcut for "revoke this token", "ban this
// user", "delete this user" in tests.
func SetField(t *testing.T, app core.App, collection, id, field string, value any) {
	t.Helper()
	rec, err := app.FindRecordById(collection, id)
	if err != nil {
		t.Fatalf("pbtest: %s/%s: %v", collection, id, err)
	}
	rec.Set(field, value)
	if err := app.SaveNoValidate(rec); err != nil {
		t.Fatalf("pbtest: save %s/%s: %v", collection, id, err)
	}
}

// TokenRecord finds the api_tokens row for a kid.
func TokenRecord(t *testing.T, app core.App, kid string) *core.Record {
	t.Helper()
	rec, err := app.FindFirstRecordByData("api_tokens", "kid", kid)
	if err != nil {
		t.Fatalf("pbtest: api_tokens kid %s: %v", kid, err)
	}
	return rec
}

// ensureSchema adds the collections / fields the adapter reads. Each step is
// guarded so a caller may pre-create part of the schema.
func ensureSchema(t *testing.T, app core.App) {
	t.Helper()

	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("pbtest: users collection: %v", err)
	}
	usersChanged := false
	for _, f := range []core.Field{
		&core.BoolField{Name: "is_banned"},
		&core.DateField{Name: "banned_until"},
		&core.BoolField{Name: "is_deleted"},
		&core.DateField{Name: "deleted_at"},
	} {
		if users.Fields.GetByName(f.GetName()) == nil {
			users.Fields.Add(f)
			usersChanged = true
		}
	}
	if usersChanged {
		if err := app.Save(users); err != nil {
			t.Fatalf("pbtest: extend users: %v", err)
		}
	}

	roles := ensureCollection(t, app, "roles", func(c *core.Collection) {
		c.Fields.Add(
			&core.TextField{Name: "slug", Required: true, Max: 32},
			&core.TextField{Name: "label", Max: 64},
			&core.NumberField{Name: "level", OnlyInt: true},
			&core.JSONField{Name: "scopes", MaxSize: 65536},
		)
		c.AddIndex("idx_roles_slug", true, "slug", "")
	})

	ensureCollection(t, app, "user_roles", func(c *core.Collection) {
		c.Fields.Add(
			&core.RelationField{Name: "user", CollectionId: users.Id, MaxSelect: 1, Required: true, CascadeDelete: true},
			&core.RelationField{Name: "role", CollectionId: roles.Id, MaxSelect: 1, Required: true, CascadeDelete: true},
			&core.RelationField{Name: "granted_by", CollectionId: users.Id, MaxSelect: 1},
			&core.AutodateField{Name: "granted_at", OnCreate: true},
		)
		c.AddIndex("idx_user_roles_user_role", true, "user, role", "")
	})

	gamertags := ensureCollection(t, app, "gamertags", func(c *core.Collection) {
		c.Fields.Add(
			&core.RelationField{Name: "user", CollectionId: users.Id, MaxSelect: 1},
			&core.TextField{Name: "tag", Max: 32},
			&core.TextField{Name: "sanitized", Max: 32},
			&core.SelectField{Name: "status", Values: []string{"approved", "allowed", "pending", "blocked"}, MaxSelect: 1},
		)
	})

	ensureCollection(t, app, "api_tokens", func(c *core.Collection) {
		c.Fields.Add(
			&core.TextField{Name: "kid", Required: true, Max: 64, Presentable: true},
			&core.TextField{Name: "key_hash", Required: true, Max: 128},
			&core.SelectField{Name: "kind", Values: []string{"machine", "spectator", "device"}, MaxSelect: 1, Required: true},
			&core.JSONField{Name: "scopes", Required: true, MaxSize: 65536},
			&core.TextField{Name: "label", Max: 120},
			&core.RelationField{Name: "user", CollectionId: users.Id, MaxSelect: 1},
			&core.TextField{Name: "container", Max: 120},
			&core.TextField{Name: "station_id", Max: 120},
			&core.RelationField{Name: "gamertags", CollectionId: gamertags.Id, MaxSelect: 99},
			&core.RelationField{Name: "minted_by", CollectionId: users.Id, MaxSelect: 1},
			&core.DateField{Name: "expires_at"},
			&core.BoolField{Name: "revoked"},
			&core.DateField{Name: "revoked_at"},
			&core.RelationField{Name: "revoked_by", CollectionId: users.Id, MaxSelect: 1},
			&core.DateField{Name: "last_used_at"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_api_tokens_kid", true, "kid", "")
	})

	ensureCollection(t, app, "audit_log", func(c *core.Collection) {
		c.Fields.Add(
			&core.RelationField{Name: "actor", CollectionId: users.Id, MaxSelect: 1},
			&core.TextField{Name: "target_collection", Required: true, Min: 1},
			&core.TextField{Name: "target_id", Required: true, Min: 1},
			&core.TextField{Name: "action", Required: true, Min: 1},
			&core.JSONField{Name: "payload_json", MaxSize: 1 << 19},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
	})

	teams := ensureCollection(t, app, "teams", func(c *core.Collection) {
		c.Fields.Add(
			&core.TextField{Name: "name", Max: 64},
			&core.RelationField{Name: "created_by", CollectionId: users.Id, MaxSelect: 1},
		)
	})

	ensureCollection(t, app, "rosters", func(c *core.Collection) {
		c.Fields.Add(
			&core.RelationField{Name: "team", CollectionId: teams.Id, MaxSelect: 1},
			&core.RelationField{Name: "gamertag", CollectionId: gamertags.Id, MaxSelect: 1},
			&core.BoolField{Name: "is_owner"},
			&core.BoolField{Name: "is_manager"},
			&core.DateField{Name: "left_at"},
		)
	})
}

// ensureCollection returns the named collection, creating it via build when
// it does not exist yet.
func ensureCollection(t *testing.T, app core.App, name string, build func(c *core.Collection)) *core.Collection {
	t.Helper()
	if c, err := app.FindCollectionByNameOrId(name); err == nil {
		return c
	}
	c := core.NewBaseCollection(name)
	build(c)
	if err := app.Save(c); err != nil {
		t.Fatalf("pbtest: save %s collection: %v", name, err)
	}
	return c
}
