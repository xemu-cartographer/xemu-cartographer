package pb

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
)

// ErrBanned is returned (with authz.Nobody()) when a JWT belongs to a user
// whose row is banned or soft-deleted; the middleware maps it to 401.
var ErrBanned = errors.New("authz: account banned or deleted")

// ErrNoCredential is returned by AuthorizeLAN when the request carries no
// credential at all.
var ErrNoCredential = errors.New("authz: no credential presented")

// screenCookie is the cookie the screen proxy uses to carry the token through
// the iframe's sub-resource requests (containers/auth.go).
const screenCookie = "screen_token"

// principalKey is the request-store key ResolveRequest / AuthorizeLAN save
// the principal under (Get reads it).
const principalKey = "authz.principal"

// tokenSource names where an opaque candidate came from; only the LAN
// carriers accept a raw (dot-less) legacy secret.
type tokenSource uint8

const (
	srcAuthorization tokenSource = iota
	srcAPIKey
	srcLANHeader
	srcQuery
)

// tokenCandidate is one presented credential string.
type tokenCandidate struct {
	value string
	src   tokenSource
}

// ResolveRequest resolves the REST principal (§6.1): e.Auth ⇒ superuser or
// pb_user (re-read; banned / deleted ⇒ Nobody + ErrBanned); else an opaque
// key from Authorization: Bearer / X-Api-Key (bad, revoked or expired ⇒
// Nobody + the authz error); else Nobody. The result is stored on the event
// for Get. It never returns an anonymous principal with a binding — the
// console door is a WS / overlay concern.
func ResolveRequest(app core.App, d *PBDeps, e *core.RequestEvent) (authz.Principal, error) {
	if e == nil {
		return authz.Nobody(), nil
	}
	if app == nil {
		app = e.App
	}
	p, err := resolveEvent(app, d, e, restCandidates(e))
	e.Set(principalKey, p)
	return p, err
}

// restCandidates lists the opaque carriers ResolveRequest accepts.
func restCandidates(e *core.RequestEvent) []tokenCandidate {
	var out []tokenCandidate
	if t := bearerToken(e.Request); t != "" {
		out = append(out, tokenCandidate{t, srcAuthorization})
	}
	if t := strings.TrimSpace(e.Request.Header.Get("X-Api-Key")); t != "" {
		out = append(out, tokenCandidate{t, srcAPIKey})
	}
	return out
}

// lanCandidates lists the carriers AuthorizeLAN accepts: X-LAN-Token and
// ?token= (opaque key or raw legacy secret), then the REST carriers.
func lanCandidates(e *core.RequestEvent) []tokenCandidate {
	var out []tokenCandidate
	if t := strings.TrimSpace(e.Request.Header.Get("X-LAN-Token")); t != "" {
		out = append(out, tokenCandidate{t, srcLANHeader})
	}
	if t := strings.TrimSpace(e.Request.URL.Query().Get("token")); t != "" {
		out = append(out, tokenCandidate{t, srcQuery})
	}
	return append(out, restCandidates(e)...)
}

// bearerToken returns the Authorization header value with an optional
// "Bearer " prefix stripped (PocketBase's own loader does the same).
func bearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(v) >= 7 && strings.EqualFold(v[:7], "bearer ") {
		v = strings.TrimSpace(v[7:])
	}
	return v
}

// resolveEvent is the shared body of ResolveRequest / AuthorizeLAN: the
// verified JWT first, then the opaque candidates in order. The first
// candidate that has a recognisable shape decides — a bad secret on a
// recognised kid is an error, not a fall-through to the next carrier.
//
// On the LAN carriers (X-LAN-Token, ?token=) any value that is not an
// opaque key is the raw LAN_SAVES_TOKEN secret, dots included — the legacy
// middleware compared the whole presented string, so an env value with a
// '.' in it is still valid. An opaque-looking value whose kid names no row
// gets the same whole-value fallback: its "<prefix>." head may just be how
// the env secret happens to start.
//
// The REST carriers (Authorization: Bearer, X-Api-Key) get a whole-value
// match against the imported XC_SCRAPER_WEBHOOK_TOKEN row (step 8 §7.2):
// the daemon presents that env value verbatim as a Bearer. A non-match
// changes nothing — the value keeps today's outcome (Nobody, or the opaque
// error) — so an expired JWT or a typo never turns into a bad-secret error.
func resolveEvent(app core.App, d *PBDeps, e *core.RequestEvent, candidates []tokenCandidate) (authz.Principal, error) {
	if e.Auth != nil {
		return principalFromVerifiedAuth(app, d, e.Auth)
	}
	for _, c := range candidates {
		lan := c.src == srcLANHeader || c.src == srcQuery
		if authz.LooksOpaque(c.value) {
			p, err := resolveOpaque(app, d, c.value)
			if errors.Is(err, authz.ErrUnknownKid) {
				if lan && d.legacyRow() != nil {
					return resolveLegacySecret(d, c.value)
				}
				if wp, ok := resolveWebhookSecret(d, c.value); !lan && ok {
					return wp, nil
				}
			}
			return p, err
		}
		if lan {
			return resolveLegacySecret(d, c.value)
		}
		if wp, ok := resolveWebhookSecret(d, c.value); ok {
			return wp, nil
		}
	}
	return authz.Nobody(), nil
}

// principalFromVerifiedAuth re-reads a JWT-authenticated record: superusers
// pass through, users are checked for ban / soft-delete (ErrBanned).
func principalFromVerifiedAuth(app core.App, d *PBDeps, auth *core.Record) (authz.Principal, error) {
	if auth == nil {
		return authz.Nobody(), nil
	}
	if auth.IsSuperuser() {
		return authz.Superuser(auth.Id), nil
	}
	if auth.Collection() == nil || auth.Collection().Name != "users" {
		return authz.Nobody(), nil
	}
	fresh, ok := usableUser(app, auth)
	if !ok {
		return authz.Nobody(), ErrBanned
	}
	return PrincipalFromAuth(app, d, fresh), nil
}

// usableUser re-reads a verified users record and applies userUnusable:
// the fresh row and true when the user may act, false when the row is gone,
// banned or soft-deleted. With no app the record is checked as presented.
// principalFromVerifiedAuth and RejectBannedAuth share it so REST resolution
// and the router-wide guard can never disagree about who is banned.
func usableUser(app core.App, auth *core.Record) (*core.Record, bool) {
	if auth == nil {
		return nil, false
	}
	fresh := auth
	if app != nil {
		rec, err := app.FindRecordById("users", auth.Id)
		if err != nil {
			return nil, false
		}
		fresh = rec
	}
	if userUnusable(fresh, time.Now()) {
		return nil, false
	}
	return fresh, true
}

// userUnusable mirrors the users collection authRule: soft-deleted rows and
// active bans (is_banned with no banned_until, or one still in the future)
// may not act.
func userUnusable(rec *core.Record, now time.Time) bool {
	if rec == nil {
		return true
	}
	if rec.GetBool("is_deleted") || !rec.GetDateTime("deleted_at").IsZero() {
		return true
	}
	if rec.GetBool("is_banned") {
		until := rec.GetDateTime("banned_until")
		if until.IsZero() || !until.Time().Before(now) {
			return true
		}
	}
	return false
}

// resolveOpaque verifies "<kid>.<secret>" against the row LookupToken
// returns (DB or legacy), touches last_used_at and builds the principal.
func resolveOpaque(app core.App, d *PBDeps, token string) (authz.Principal, error) {
	kid, secret, ok := authz.SplitToken(token)
	if !ok {
		return authz.Nobody(), authz.ErrUnknownKid
	}
	row, ok := d.LookupToken(kid)
	if !ok {
		return authz.Nobody(), authz.ErrUnknownKid
	}
	if err := authz.VerifyToken(row, secret, time.Now()); err != nil {
		return authz.Nobody(), err
	}
	p := authz.PrincipalFromToken(row)
	if p.Kind == authz.KindAnonymous {
		return authz.Nobody(), authz.ErrUnknownKid
	}
	d.touchLastUsed(app, kid)
	return p, nil
}

// resolveLegacySecret compares a raw (dot-less) LAN secret against the
// imported LAN_SAVES_TOKEN row only (constant-time via VerifyToken).
func resolveLegacySecret(d *PBDeps, secret string) (authz.Principal, error) {
	row := d.legacyRow()
	if row == nil {
		return authz.Nobody(), authz.ErrUnknownKid
	}
	if err := authz.VerifyToken(*row, secret, time.Now()); err != nil {
		return authz.Nobody(), err
	}
	return authz.PrincipalFromToken(*row), nil
}

// ResolveWS resolves a WebSocket connect (§6.2): ?token= (opaque key or PB
// JWT), else ?spectator= (opaque key), else ?console=<name> ⇒ the anonymous
// principal bound to the instance whose console nickname matches (may be
// unbound), else Nobody. Anonymous principals carry Extra["console"] so
// ReResolve can re-bind them.
func ResolveWS(app core.App, d *PBDeps, r *http.Request) (authz.Principal, error) {
	if r == nil {
		return authz.Nobody(), nil
	}
	q := r.URL.Query()
	if t := strings.TrimSpace(q.Get("token")); t != "" {
		if authz.LooksOpaque(t) {
			return resolveOpaque(app, d, t)
		}
		return resolveJWT(app, d, t)
	}
	if t := strings.TrimSpace(q.Get("spectator")); t != "" {
		return resolveOpaque(app, d, t)
	}
	if name := strings.TrimSpace(q.Get("console")); name != "" {
		return consolePrincipal(d, name), nil
	}
	return authz.Nobody(), nil
}

// consolePrincipal builds the console-door principal for name.
func consolePrincipal(d *PBDeps, name string) authz.Principal {
	p := authz.Anonymous(d.InstanceByConsole(name), d.AnonymousScopes())
	p.Extra = map[string]string{"console": name}
	return p
}

// resolveJWT validates a PB auth token the way websocket/handler.go does
// today and applies the same ban / soft-delete re-read as REST.
func resolveJWT(app core.App, d *PBDeps, token string) (authz.Principal, error) {
	if app == nil {
		return authz.Nobody(), errors.New("authz: no app to verify the token")
	}
	rec, err := app.FindAuthRecordByToken(token, core.TokenTypeAuth)
	if err != nil {
		return authz.Nobody(), fmt.Errorf("authz: invalid auth token: %w", err)
	}
	return principalFromVerifiedAuth(app, d, rec)
}

// ResolveScreen resolves the screen proxy's carrier — ?token= or the
// screen_token cookie, holding a PB JWT or an opaque (device) key — and
// returns the raw token so the caller can re-set the cookie. Any failure
// yields Nobody and "".
func ResolveScreen(app core.App, d *PBDeps, e *core.RequestEvent) (authz.Principal, string) {
	if e == nil || e.Request == nil {
		return authz.Nobody(), ""
	}
	if app == nil {
		app = e.App
	}
	raw := strings.TrimSpace(e.Request.URL.Query().Get("token"))
	if raw == "" {
		if c, err := e.Request.Cookie(screenCookie); err == nil {
			raw = strings.TrimSpace(c.Value)
		}
	}
	if raw == "" {
		return authz.Nobody(), ""
	}
	var (
		p   authz.Principal
		err error
	)
	if authz.LooksOpaque(raw) {
		p, err = resolveOpaque(app, d, raw)
	} else {
		p, err = resolveJWT(app, d, raw)
	}
	if err != nil {
		return authz.Nobody(), ""
	}
	return p, raw
}

// PrincipalFromAuth maps an auth record to a principal: nil ⇒
// authz.Internal("hook") (in-process writers), _superusers ⇒ Superuser,
// users ⇒ pb_user carrying its sorted role slugs, the union of those roles'
// scopes and the max level. Any other auth collection ⇒ Nobody.
func PrincipalFromAuth(app core.App, d *PBDeps, auth *core.Record) authz.Principal {
	if auth == nil {
		return authz.Internal("hook")
	}
	if auth.IsSuperuser() {
		return authz.Superuser(auth.Id)
	}
	if auth.Collection() == nil || auth.Collection().Name != "users" {
		return authz.Nobody()
	}
	if app == nil && d != nil {
		app = d.app
	}
	slugs := userRoleSlugs(app, auth.Id)
	var table map[string]roleEntry
	if d != nil {
		table = d.rolesTable()
	} else {
		table = loadRoles(app)
	}
	p := authz.Principal{
		Kind:       authz.KindPBUser,
		ID:         auth.Id,
		UserID:     auth.Id,
		Collection: "users",
		Roles:      slugs,
	}
	var scopes []string
	for _, slug := range slugs {
		ent, ok := table[slug]
		if !ok {
			continue
		}
		scopes = append(scopes, ent.scopes...)
		if ent.level > p.Level {
			p.Level = ent.level
		}
	}
	p.Scopes = authz.CanonScopes(scopes)
	return p
}

// ReResolve re-derives a live principal on the WS tick (A.1): pb_user rows
// are re-read (missing / banned / deleted ⇒ false), superusers re-read,
// opaque keys re-looked-up (missing / revoked / expired ⇒ false), anonymous
// console principals re-bound via InstanceByConsole + AnonymousScopes,
// internal / discord returned unchanged. false means evict.
func ReResolve(app core.App, d *PBDeps, p authz.Principal) (authz.Principal, bool) {
	if app == nil && d != nil {
		app = d.app
	}
	switch p.Kind {
	case authz.KindPBUser:
		if app == nil || p.UserID == "" {
			return authz.Nobody(), false
		}
		rec, err := app.FindRecordById("users", p.UserID)
		if err != nil || userUnusable(rec, time.Now()) {
			return authz.Nobody(), false
		}
		return PrincipalFromAuth(app, d, rec), true
	case authz.KindSuperuser:
		if app == nil || p.ID == "" {
			return authz.Nobody(), false
		}
		if _, err := app.FindRecordById(core.CollectionNameSuperusers, p.ID); err != nil {
			return authz.Nobody(), false
		}
		return authz.Superuser(p.ID), true
	case authz.KindMachine, authz.KindSpectator, authz.KindDevice:
		row, ok := d.LookupToken(p.ID)
		if !ok || row.Revoked {
			return authz.Nobody(), false
		}
		if !row.ExpiresAt.IsZero() && !time.Now().Before(row.ExpiresAt) {
			return authz.Nobody(), false
		}
		np := authz.PrincipalFromToken(row)
		if np.Kind != p.Kind {
			return authz.Nobody(), false
		}
		return np, true
	case authz.KindAnonymous:
		if name := p.Extra["console"]; name != "" {
			return consolePrincipal(d, name), true
		}
		return authz.Nobody(), true
	case authz.KindInternal, authz.KindDiscord:
		return p, true
	}
	return authz.Nobody(), false
}

// classifyToken names the path a presented string takes: "jwt" (two dots —
// FindAuthRecordByToken), "opaque" (<kid>.<secret> with a known prefix —
// LookupToken), "legacy" (no dot — the LAN_SAVES_TOKEN row, LAN carriers
// only; on those carriers resolveEvent also tries a dotted non-opaque value
// as the legacy secret) or "" (unrecognised). Exposed for tests.
func classifyToken(s string) string {
	switch {
	case s == "":
		return ""
	case authz.LooksOpaque(s):
		return "opaque"
	case strings.Count(s, ".") == 2:
		return "jwt"
	case !strings.Contains(s, "."):
		return "legacy"
	}
	return ""
}
