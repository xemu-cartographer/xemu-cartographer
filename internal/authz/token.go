package authz

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ReResolveInterval is how often the WebSocket handler re-resolves a
// connected principal (banned / deleted / role-stripped users and revoked or
// expired keys are evicted at the next tick; A.1, PD-4).
const ReResolveInterval = 60 * time.Second

// KidPrefix maps an api_tokens kind to the prefix its kids carry. The prefix
// is what lets the resolver classify a presented "<kid>.<secret>" string as
// opaque (LooksOpaque) without touching the database.
var KidPrefix = map[string]string{
	"spectator": "sp_",
	"machine":   "mk_",
	"device":    "dv_",
}

// LegacyKid is the kid of the in-memory machine row imported from
// LAN_SAVES_TOKEN at boot (PD-12).
const LegacyKid = "legacy-env"

// Sentinel errors. The adapter maps them to HTTP statuses (design §6.3, §7
// R-4/R-9): ErrUnknownRole → 400, ErrLevelTooLow → 403, ErrLastAdmin → 409,
// the token errors → 401 (verification) or 400 (ValidateForMint).
var (
	ErrRevoked           = errors.New("authz: token revoked")
	ErrExpired           = errors.New("authz: token expired")
	ErrBadSecret         = errors.New("authz: bad token secret")
	ErrUnknownKid        = errors.New("authz: unknown kid")
	ErrWildcardKind      = errors.New("authz: wildcard scopes are only allowed on machine keys")
	ErrSpectatorInstance = errors.New("authz: scopes must name exactly one instance")
	ErrUnknownRole       = errors.New("authz: unknown role")
	ErrLevelTooLow       = errors.New("authz: role level exceeds the actor's")
	ErrLastAdmin         = errors.New("authz: cannot revoke the last admin")
)

// kidAlphabet is Crockford base32 (no I, L, O, U), lower-cased so a kid is
// already canonical inside a "token.revoke:<kid>" scope selector. 32 symbols
// divide 256 evenly, so a masked random byte is uniform.
const kidAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// kidRandomLen is the number of random symbols after the kind prefix.
const kidRandomLen = 12

// NewKid mints "<prefix><12 Crockford-base32 chars>" for the token kind
// (KidPrefix); unknown kinds are an error.
func NewKid(kind string) (string, error) {
	prefix, ok := KidPrefix[kind]
	if !ok {
		return "", fmt.Errorf("authz: NewKid: unknown token kind %q", kind)
	}
	buf := make([]byte, kidRandomLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("authz: NewKid: %w", err)
	}
	out := make([]byte, 0, len(prefix)+kidRandomLen)
	out = append(out, prefix...)
	for _, b := range buf {
		out = append(out, kidAlphabet[int(b)&31])
	}
	return string(out), nil
}

// NewSecret returns 32 random bytes as unpadded base64url — the part of the
// token shown once at mint and never stored (HashSecret is).
func NewSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("authz: NewSecret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashSecret returns the sha256 hex digest stored in api_tokens.key_hash.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// SplitToken splits "<kid>.<secret>": exactly one '.', both halves non-empty.
func SplitToken(s string) (kid, secret string, ok bool) {
	if strings.Count(s, ".") != 1 {
		return "", "", false
	}
	kid, secret, _ = strings.Cut(s, ".")
	if kid == "" || secret == "" {
		return "", "", false
	}
	return kid, secret, true
}

// LooksOpaque reports whether s has the shape of an opaque key: SplitToken
// ok and the kid carries a KidPrefix value or equals LegacyKid. A PocketBase
// JWT has two dots and therefore never looks opaque.
func LooksOpaque(s string) bool {
	kid, _, ok := SplitToken(s)
	if !ok {
		return false
	}
	return kidHasKnownPrefix(kid)
}

func kidHasKnownPrefix(kid string) bool {
	if kid == LegacyKid {
		return true
	}
	for _, prefix := range KidPrefix {
		if strings.HasPrefix(kid, prefix) {
			return true
		}
	}
	return false
}

// VerifyToken checks a presented secret against the row. The secret is
// compared first (constant-time, against the stored hash) so a caller that
// does not hold it learns nothing about the row's state; then revocation,
// then expiry (ExpiresAt zero = never). An empty stored hash never verifies.
func VerifyToken(row TokenRow, secret string, now time.Time) error {
	if row.KeyHash == "" || secret == "" {
		return ErrBadSecret
	}
	if subtle.ConstantTimeCompare([]byte(HashSecret(secret)), []byte(row.KeyHash)) != 1 {
		return ErrBadSecret
	}
	if row.Revoked {
		return ErrRevoked
	}
	if !row.ExpiresAt.IsZero() && !now.Before(row.ExpiresAt) {
		return ErrExpired
	}
	return nil
}

// tokenKinds maps api_tokens.kind to the principal kind it produces.
var tokenKinds = map[string]Kind{
	"machine":   KindMachine,
	"spectator": KindSpectator,
	"device":    KindDevice,
}

// PrincipalFromToken builds the principal for a verified row: Kind from
// row.Kind, ID = kid, Scopes canonicalised, Bound["instance"] = the container
// for device rows and the single instance the scopes name for spectator rows,
// Extra station_id / label / token_user / gamertags. A row whose kind is not
// machine / spectator / device yields Nobody() (fail-closed).
func PrincipalFromToken(row TokenRow) Principal {
	kind, ok := tokenKinds[row.Kind]
	if !ok {
		return Nobody()
	}
	p := Principal{
		Kind:   kind,
		ID:     row.Kid,
		UserID: row.UserID,
		Scopes: CanonScopes(row.Scopes),
		Extra:  map[string]string{},
	}
	if row.StationID != "" {
		p.Extra["station_id"] = row.StationID
	}
	if row.Label != "" {
		p.Extra["label"] = row.Label
	}
	if row.UserID != "" {
		p.Extra["token_user"] = row.UserID
	}
	if tags := sanitizedTags(row.Gamertags); len(tags) > 0 {
		p.Extra["gamertags"] = strings.Join(tags, ",")
	}

	var bound string
	switch kind {
	case KindDevice:
		bound = row.Container
	case KindSpectator:
		bound, _ = scopesInstance(p.Scopes)
	}
	if bound != "" {
		p.Bound = map[string]string{"instance": bound}
	}
	return p
}

// sanitizedTags lower-cases, trims and de-duplicates gamertags (the
// gamertags.sanitized convention), dropping empties.
func sanitizedTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		s := strings.ToLower(strings.TrimSpace(t))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// scopesInstance returns the single instance every scope in scopes names
// (via mintInstance); ok is false when the scopes name none, several, or
// contain an unparseable / wildcard / non-instance selector.
func scopesInstance(scopes []string) (string, bool) {
	inst := ""
	for _, s := range scopes {
		pat, err := ParseScope(s)
		if err != nil {
			return "", false
		}
		named, ok := mintInstance(pat)
		if !ok {
			return "", false
		}
		if inst != "" && named != inst {
			return "", false
		}
		inst = named
	}
	return inst, inst != ""
}

// mintInstance is the spectator/device reading of a selector: "room.join"
// scopes must be "host:<inst>:<class>" (the bare host room is not mintable,
// A.10); every other action must carry a single literal segment.
func mintInstance(pat ScopePat) (string, bool) {
	if pat.Any || pat.Family || pat.isWildcard() {
		return "", false
	}
	if Action(pat.Action) == ActionRoomJoin {
		if len(pat.Selector) != 3 {
			return "", false
		}
		return pat.namedInstance()
	}
	if len(pat.Selector) != 1 {
		return "", false
	}
	return pat.namedInstance()
}

// mintAllowList is the set of actions a non-machine key may carry (PD-13).
var mintAllowList = map[string]map[Action]bool{
	"spectator": {
		ActionOverlayReadState: true,
		ActionRoomJoin:         true,
		ActionScraperEvents:    true,
		ActionScraperState:     true,
	},
	"device": {
		ActionBoxView:       true,
		ActionBoxDrive:      true,
		ActionBoxRead:       true,
		ActionRoomJoin:      true,
		ActionScraperState:  true,
		ActionScraperEvents: true,
	},
}

// OverlayClasses is the server-held class set an overlay browser source
// subscribes to on its instance: the console door's four classes (A.3, the
// anonymous seed row) and what Studio mints into a spectator key (§9).
var OverlayClasses = []string{"game_filtered", "tick", "scenario", "event_filtered"}

// overlayClasses is OverlayClasses as a set.
var overlayClasses = func() map[string]bool {
	set := map[string]bool{}
	for _, c := range OverlayClasses {
		set[c] = true
	}
	return set
}()

// OverlayMintCovers reports whether s is one of the scopes an overlay
// spectator key carries — "overlay.read_state:<inst>" or
// "room.join:host:<inst>:<class>" with the class in OverlayClasses, <inst> a
// literal instance name — i.e. what the overlay.mint grant lets its holder
// mint on any instance (PD-5) without holding the scope itself. Another
// action, another class, the bare host room or any wildcard is not covered:
// the minter needs the scope in its own list for those.
func OverlayMintCovers(s string) bool {
	pat, err := ParseScope(s)
	if err != nil || pat.isWildcard() {
		return false
	}
	if _, ok := pat.namedInstance(); !ok {
		return false
	}
	switch Action(pat.Action) {
	case ActionOverlayReadState:
		return len(pat.Selector) == 1
	case ActionRoomJoin:
		return len(pat.Selector) == 3 && overlayClasses[pat.Selector[2]]
	}
	return false
}

// ValidateForMint applies the PD-13 minting rules to a kind + scope list
// (design §3.4). Scopes are expected canonical (CanonScopes) — upper-case
// or padded patterns are ErrScopeSyntax like any other grammar violation.
//
//   - empty list or unknown kind ⇒ ErrScopeSyntax
//   - any scope outside the grammar ⇒ ErrScopeSyntax
//   - machine: anything parseable, including "*" and "<family>.*"
//   - spectator / device: no wildcard of any form (ErrWildcardKind); every
//     action in the kind's allow-list and every room.join class a known
//     scraper class (else ErrScopeSyntax); every selector naming one literal
//     instance, all the same one, and "room.join" only as
//     "host:<inst>:<class>" (else ErrSpectatorInstance — the same error for
//     device rows, with "device" in the message)
func ValidateForMint(kind string, scopes []string) error {
	if _, ok := tokenKinds[kind]; !ok {
		return fmt.Errorf("%w: unknown token kind %q", ErrScopeSyntax, kind)
	}
	if len(scopes) == 0 {
		return fmt.Errorf("%w: at least one scope is required", ErrScopeSyntax)
	}
	pats := make([]ScopePat, 0, len(scopes))
	for _, s := range scopes {
		pat, err := ParseScope(s)
		if err != nil {
			return err
		}
		pats = append(pats, pat)
	}
	if kind == "machine" {
		return nil
	}

	allow := mintAllowList[kind]
	inst := ""
	for i, pat := range pats {
		if pat.isWildcard() {
			return fmt.Errorf("%w: %s key scope %q", ErrWildcardKind, kind, scopes[i])
		}
		if !allow[Action(pat.Action)] {
			return fmt.Errorf("%w: action %q is not mintable on a %s key", ErrScopeSyntax, pat.Action, kind)
		}
		if Action(pat.Action) == ActionRoomJoin && len(pat.Selector) == 3 && !scraperClasses[pat.Selector[2]] {
			return fmt.Errorf("%w: %q is not a scraper class", ErrScopeSyntax, pat.Selector[2])
		}
		named, ok := mintInstance(pat)
		if !ok {
			return fmt.Errorf("%w: %s key scope %q must name one instance (room.join only as host:<inst>:<class>)", ErrSpectatorInstance, kind, scopes[i])
		}
		if inst != "" && named != inst {
			return fmt.Errorf("%w: %s key scopes name both %q and %q", ErrSpectatorInstance, kind, inst, named)
		}
		inst = named
	}
	return nil
}
