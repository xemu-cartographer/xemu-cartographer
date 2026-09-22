// Package pb is the PocketBase adapter for internal/authz: it implements
// authz.Deps over the live app + scraper manager + provisioner (PBDeps),
// resolves principals from HTTP / WebSocket / screen-proxy carriers, and wraps the
// role and api_tokens mutations with their audit rows (design §6).
//
// Import direction: internal/roles and internal/guards import this package,
// so nothing here may import them back. Role slugs are read straight from
// user_roles (userRoleSlugs) and the scraper is taken as the leaf
// scraperiface.Service interface.
package pb

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/gamertags"
	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/rostergrace"
	"github.com/xemu-cartographer/xemu-cartographer/internal/teamperms"
)

// rolesCacheTTL bounds how stale the roles table (levels + scopes) may be
// between InvalidateRoles calls.
const rolesCacheTTL = 60 * time.Second

// lastUsedInterval rate-limits api_tokens.last_used_at writes per kid.
const lastUsedInterval = 60 * time.Second

// roleEntry is the cached view of one roles row.
type roleEntry struct {
	id     string
	level  int
	scopes []string
}

// PBDeps implements authz.Deps over the live PocketBase app. scraper and
// prefix may be nil (the corresponding answers fail closed). Every method is
// safe on a nil receiver: authz.CanWith already denies a typed-nil Deps
// ("no_deps"), and the resolvers call the helpers directly.
type PBDeps struct {
	app     core.App
	scraper scraperiface.Service
	prefix  func() string

	mu       sync.RWMutex
	legacy   *authz.TokenRow      // LAN_SAVES_TOKEN import (PD-12), nil when unset
	webhook  *authz.TokenRow      // XC_SCRAPER_WEBHOOK_TOKEN import (step 8 §7.2), nil when unset
	rolesAt  time.Time            // when roles was loaded
	roles    map[string]roleEntry // slug → row
	lastUsed map[string]time.Time // kid → last last_used_at write
}

// NewDeps builds the adapter and installs its record hooks on app
// (RegisterHooks: roles cache invalidation, binding-delete revocation).
// scraper (nil ok) answers the instance / membership questions; prefix (nil
// ok) is the provisioner's NamePrefix so OwnedBox can derive
// "<prefix>play-<uid>". Build one per app: every call binds hooks again.
func NewDeps(app core.App, scraper scraperiface.Service, prefix func() string) *PBDeps {
	d := newDeps(app, scraper, prefix)
	RegisterHooks(app, d)
	return d
}

// newDeps is NewDeps without the hooks — the ephemeral adapters depsOr and
// withApp build for one call or one transaction.
func newDeps(app core.App, scraper scraperiface.Service, prefix func() string) *PBDeps {
	return &PBDeps{
		app:      app,
		scraper:  scraper,
		prefix:   prefix,
		lastUsed: map[string]time.Time{},
	}
}

var _ authz.Deps = (*PBDeps)(nil)

// withApp returns a fresh, uncached adapter over app (a txApp from
// RunInTransaction) with the same scraper / prefix. The mutation helpers use
// it so a decision made inside a transaction reads the rows the transaction
// sees, not the 60 s roles cache or a snapshot from before it began.
func (d *PBDeps) withApp(app core.App) *PBDeps {
	if d == nil {
		return newDeps(app, nil, nil)
	}
	return newDeps(app, d.scraper, d.prefix)
}

// InvalidateRoles drops the cached roles table. Grant / Revoke call it, as
// do the roles hooks RegisterHooks installs when a roles row changes.
func (d *PBDeps) InvalidateRoles() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.rolesAt = time.Time{}
	d.roles = nil
	d.mu.Unlock()
}

// App returns the app the deps were built over (nil on a nil receiver).
func (d *PBDeps) App() core.App {
	if d == nil {
		return nil
	}
	return d.app
}

// Now implements authz.Deps.
func (d *PBDeps) Now() time.Time { return time.Now() }

// RosteredIn implements authz.Deps: the user's usable gamertags (A.2) ∩ the
// instance's live identity set with the rostergrace TTL — the same ladder
// join_room / the screen proxy / the play resolver use today.
func (d *PBDeps) RosteredIn(userID, instance string) bool {
	if d == nil || d.app == nil || d.scraper == nil || userID == "" || instance == "" {
		return false
	}
	tags, err := gamertags.UsableForUser(d.app, userID)
	if err != nil || len(tags) == 0 {
		return false
	}
	return rostergrace.Default.Allow(d.scraper.Membership(), instance, tags, time.Now())
}

// OwnedBox implements authz.Deps: "<prefix>play-<uid>" by name only (no
// existence check, A.8), "" when no provisioner prefix is wired.
func (d *PBDeps) OwnedBox(userID string) string {
	if d == nil || d.prefix == nil {
		return ""
	}
	uid := sanitizeName(userID)
	if uid == "" {
		return ""
	}
	return d.prefix() + "play-" + uid
}

// InstanceExists implements authz.Deps over scraper.List().
func (d *PBDeps) InstanceExists(name string) bool {
	if d == nil || d.scraper == nil || name == "" {
		return false
	}
	for _, info := range d.scraper.List() {
		if info.Name == name {
			return true
		}
	}
	return false
}

// InstanceByConsole implements authz.Deps: the instance whose runner reports
// the console nickname (XboxName) — never a machine or player name, which is
// why this reads List() and not Membership() (§6.2).
func (d *PBDeps) InstanceByConsole(consoleName string) string {
	if d == nil || d.scraper == nil {
		return ""
	}
	want := scraperiface.SanitizeIdentity(consoleName)
	if want == "" {
		return ""
	}
	for _, info := range d.scraper.List() {
		if info.Name == "" {
			continue
		}
		if scraperiface.SanitizeIdentity(info.XboxName) == want {
			return info.Name
		}
	}
	return ""
}

// TeamAuthority implements authz.Deps: an active owner/manager roster row,
// or teams.created_by when the team has no active owner row (A.8 bootstrap).
func (d *PBDeps) TeamAuthority(userID, teamID string) bool {
	if d == nil || d.app == nil || userID == "" || teamID == "" {
		return false
	}
	ok, err := teamperms.IsOwnerOrManager(d.app, userID, teamID)
	if err != nil {
		return false
	}
	if ok {
		return true
	}
	team, err := d.app.FindRecordById("teams", teamID)
	if err != nil || team.GetString("created_by") != userID {
		return false
	}
	owners, err := d.app.FindRecordsByFilter(
		"rosters",
		"team = {:teamID} && left_at = null && is_owner = true",
		"", 1, 0,
		dbx.Params{"teamID": teamID},
	)
	if err != nil {
		return false
	}
	return len(owners) == 0
}

// ActiveMember implements authz.Deps via teamperms.HasActiveMembership.
func (d *PBDeps) ActiveMember(userID, teamID string) bool {
	if d == nil || d.app == nil {
		return false
	}
	ok, err := teamperms.HasActiveMembership(d.app, userID, teamID)
	return err == nil && ok
}

// ownerFields maps a collection to the field holding its owning users.id.
// rosters own through their gamertag (handled inline).
var ownerFields = map[string]string{
	"gamertags":                "user",
	"notifications":            "user",
	"team_membership_requests": "user",
	"api_tokens":               "user",
	"teams":                    "created_by",
}

// RecordOwner implements authz.Deps for the collections the rule table
// addresses as records; unknown collections and lookup errors answer "".
func (d *PBDeps) RecordOwner(collection, id string) string {
	if d == nil || d.app == nil || collection == "" || id == "" {
		return ""
	}
	rec, err := d.app.FindRecordById(collection, id)
	if err != nil {
		return ""
	}
	if collection == "rosters" {
		return d.RecordOwner("gamertags", rec.GetString("gamertag"))
	}
	field, ok := ownerFields[collection]
	if !ok {
		return ""
	}
	return rec.GetString(field)
}

// RoleLevel implements authz.Deps from the cached roles table.
func (d *PBDeps) RoleLevel(slug string) (int, bool) {
	ent, ok := d.rolesTable()[slug]
	if !ok {
		return 0, false
	}
	return ent.level, true
}

// AnonymousScopes implements authz.Deps: the "anonymous" roles row's scopes
// (canonical; empty when the row is missing — console door closed, PD-1).
func (d *PBDeps) AnonymousScopes() []string {
	ent, ok := d.rolesTable()["anonymous"]
	if !ok {
		return []string{}
	}
	return authz.CanonScopes(ent.scopes)
}

// AdminCount implements authz.Deps (§6.4): user_roles rows on the admin role
// whose user still exists and is not soft-deleted (users.deleted_at empty;
// when the users collection has no such field every row counts).
func (d *PBDeps) AdminCount() int {
	if d == nil || d.app == nil {
		return 0
	}
	return adminCount(d.app)
}

func adminCount(app core.App) int {
	rows, err := app.FindRecordsByFilter(
		"user_roles",
		"role.slug = 'admin'",
		"", 0, 0,
		dbx.Params{},
	)
	if err != nil || len(rows) == 0 {
		return 0
	}
	hasDeletedAt := false
	if users, err := app.FindCollectionByNameOrId("users"); err == nil {
		hasDeletedAt = users.Fields.GetByName("deleted_at") != nil
	}
	seen := map[string]bool{}
	n := 0
	for _, row := range rows {
		uid := row.GetString("user")
		if uid == "" || seen[uid] {
			continue
		}
		user, err := app.FindRecordById("users", uid)
		if err != nil {
			continue
		}
		if hasDeletedAt && !user.GetDateTime("deleted_at").IsZero() {
			continue
		}
		seen[uid] = true
		n++
	}
	return n
}

// LookupToken implements authz.Deps: the in-memory legacy row for LegacyKid,
// else the api_tokens row by kid with its gamertags relation projected to
// sanitized tags (PD-8).
func (d *PBDeps) LookupToken(kid string) (authz.TokenRow, bool) {
	if d == nil || kid == "" {
		return authz.TokenRow{}, false
	}
	if kid == authz.LegacyKid {
		if row := d.legacyRow(); row != nil {
			return *row, true
		}
		return authz.TokenRow{}, false
	}
	if d.app == nil {
		return authz.TokenRow{}, false
	}
	rec, err := findTokenRecord(d.app, kid)
	if err != nil {
		return authz.TokenRow{}, false
	}
	return tokenRowFromRecord(d.app, rec), true
}

// legacyRow returns a copy of the imported LAN_SAVES_TOKEN row, nil when
// none was imported.
func (d *PBDeps) legacyRow() *authz.TokenRow {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.legacy == nil {
		return nil
	}
	row := *d.legacy
	row.Scopes = append([]string(nil), d.legacy.Scopes...)
	return &row
}

func (d *PBDeps) setLegacy(row *authz.TokenRow) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.legacy = row
	d.mu.Unlock()
}

// rolesTable returns the slug → row map, reloading it after rolesCacheTTL
// or an InvalidateRoles. A nil receiver / app yields an empty table.
func (d *PBDeps) rolesTable() map[string]roleEntry {
	if d == nil || d.app == nil {
		return map[string]roleEntry{}
	}
	d.mu.RLock()
	table, at := d.roles, d.rolesAt
	d.mu.RUnlock()
	if table != nil && time.Since(at) < rolesCacheTTL {
		return table
	}
	table = loadRoles(d.app)
	d.mu.Lock()
	d.roles, d.rolesAt = table, time.Now()
	d.mu.Unlock()
	return table
}

// loadRoles reads every roles row (slug, level, scopes). Lookup errors
// yield an empty table (fail closed: unknown roles, no anonymous scopes).
func loadRoles(app core.App) map[string]roleEntry {
	table := map[string]roleEntry{}
	if app == nil {
		return table
	}
	rows, err := app.FindAllRecords("roles")
	if err != nil {
		return table
	}
	for _, r := range rows {
		slug := r.GetString("slug")
		if slug == "" {
			continue
		}
		var scopes []string
		_ = r.UnmarshalJSONField("scopes", &scopes)
		table[slug] = roleEntry{id: r.Id, level: r.GetInt("level"), scopes: authz.CanonScopes(scopes)}
	}
	return table
}

// touchLastUsed stamps api_tokens.last_used_at for kid at most once per
// lastUsedInterval. Best effort: write errors are ignored (the request is
// already authenticated) and the legacy row has no record to stamp.
//
// The write is a column-scoped UPDATE, not a record save: a full-row save of
// a record loaded before the write would put back every other column as it
// was at load time and so clobber a revoke that raced in between. The
// "revoked = false" guard means a revoked key is never stamped at all.
func (d *PBDeps) touchLastUsed(app core.App, kid string) {
	if d == nil || app == nil || kid == "" || kid == authz.LegacyKid {
		return
	}
	now := time.Now()
	d.mu.Lock()
	if last, ok := d.lastUsed[kid]; ok && now.Sub(last) < lastUsedInterval {
		d.mu.Unlock()
		return
	}
	if d.lastUsed == nil {
		d.lastUsed = map[string]time.Time{}
	}
	d.lastUsed[kid] = now
	d.mu.Unlock()

	stamp, err := types.ParseDateTime(now.UTC())
	if err != nil {
		return
	}
	_, _ = app.DB().NewQuery(
		"UPDATE {{api_tokens}} SET [[last_used_at]] = {:t} WHERE [[kid]] = {:kid} AND [[revoked]] = false",
	).Bind(dbx.Params{
		"t":   stamp.String(),
		"kid": kid,
	}).Execute()
}

// findTokenRecord loads the api_tokens row by kid.
func findTokenRecord(app core.App, kid string) (*core.Record, error) {
	return app.FindFirstRecordByData("api_tokens", "kid", kid)
}

// tokenRowFromRecord projects an api_tokens record to the Deps view. The
// gamertags relation is expanded to sanitized tag strings.
func tokenRowFromRecord(app core.App, rec *core.Record) authz.TokenRow {
	var scopes []string
	_ = rec.UnmarshalJSONField("scopes", &scopes)
	row := authz.TokenRow{
		Kid:       rec.GetString("kid"),
		KeyHash:   rec.GetString("key_hash"),
		Kind:      rec.GetString("kind"),
		Label:     rec.GetString("label"),
		UserID:    rec.GetString("user"),
		Container: rec.GetString("container"),
		StationID: rec.GetString("station_id"),
		Scopes:    authz.CanonScopes(scopes),
		Revoked:   rec.GetBool("revoked"),
	}
	if exp := rec.GetDateTime("expires_at"); !exp.IsZero() {
		row.ExpiresAt = exp.Time()
	}
	row.Gamertags = sanitizedTagsByID(app, rec.GetStringSlice("gamertags"))
	return row
}

// sanitizedTagsByID resolves gamertags record ids to their sanitized tag
// strings (falling back to lower/trim of the raw tag). Unknown ids are
// skipped; the result is sorted.
func sanitizedTagsByID(app core.App, ids []string) []string {
	if app == nil || len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		rec, err := app.FindRecordById("gamertags", id)
		if err != nil {
			continue
		}
		s := rec.GetString("sanitized")
		if s == "" {
			s = strings.ToLower(strings.TrimSpace(rec.GetString("tag")))
		}
		if s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// userRoleSlugs returns the sorted role slugs userID holds (user_roles with
// the role relation expanded). Mirrors roles.Slugs, which this package
// cannot import.
func userRoleSlugs(app core.App, userID string) []string {
	if app == nil || userID == "" {
		return nil
	}
	rows, err := app.FindRecordsByFilter(
		"user_roles",
		"user = {:userID}",
		"", 0, 0,
		dbx.Params{"userID": userID},
	)
	if err != nil || len(rows) == 0 {
		return nil
	}
	for _, expandErr := range app.ExpandRecords(rows, []string{"role"}, nil) {
		if expandErr != nil {
			return nil
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		role := r.ExpandedOne("role")
		if role == nil {
			continue
		}
		if slug := role.GetString("slug"); slug != "" {
			out = append(out, slug)
		}
	}
	sort.Strings(out)
	return out
}

// sanitizeName mirrors play's box-name sanitizer: lower-case, trimmed, and
// only podman-safe characters ([a-z0-9_.-]) kept.
func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}
