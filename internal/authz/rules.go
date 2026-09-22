package authz

import (
	"strconv"
	"strings"
)

// predicate is a rule predicate (design §4). It returns its verdict and its
// stable name — the "pred:<name>" suffix in Decision.Reason on both outcomes.
type predicate func(deps Deps, p Principal, r Resource) (ok bool, name string)

// mode is how a principal kind passes a rule.
type mode uint8

const (
	modeScope        mode = iota + 1 // S: a scope match on ScopeFor(a, r) suffices
	modeScopeOrPred                  // S ∨ P: scope, else the predicate
	modeScopeAndPred                 // S ∧ P: scope required, then the predicate
	modePred                         // P: predicate only, scopes ignored
)

// kindRule is one cell of the §4 table.
type kindRule struct {
	Mode mode
	Pred predicate // nil for modeScope
}

// rule is one row of the §4 table (for room.join the kind table is the
// sub-row roomJoinRules picks by room shape, see kindsFor).
type rule struct {
	// Resources lists the resource kinds the action accepts (§2.3
	// annotations); a mismatch is denied "resource" before anything else.
	// Empty = any kind.
	Resources []ResourceKind
	// Collection pins ResRecord resources to one collection ("" = any).
	Collection string
	// Kinds is the admitted principal kinds and how each passes. superuser
	// and internal never appear (allow-all after Deny).
	Kinds map[Kind]kindRule
	// Deny predicates run first for every kind, including superuser
	// (last_admin lives here; the predicate itself exempts internal, §4.4).
	Deny []predicate
	// Accept, when set, is an extra resource validation (token.mint needs a
	// known kind in Extra["kind"]).
	Accept func(r Resource) bool
	// Wants, when set, replaces the single ScopeFor(a, r) want with a list
	// (token.mint: "overlay.mint" also grants spectator keys).
	Wants func(a Action, r Resource) []string
}

// Cell shorthands.
var (
	cellS        = kindRule{Mode: modeScope}
	cellAlways   = kindRule{Mode: modePred, Pred: predAlways}
	cellBound    = kindRule{Mode: modeScopeAndPred, Pred: predBound}
	cellRostered = kindRule{Mode: modeScopeOrPred, Pred: anyOf(predRostered, predBoxOwner)}
	cellBoxOwner = kindRule{Mode: modeScopeOrPred, Pred: predBoxOwner}
)

// roomJoinRules are the room.join sub-rows of §4, keyed by room shape
// (roomShape). rules_test iterates them alongside the main table.
var roomJoinRules = map[string]map[Kind]kindRule{
	// host:<inst> — bare per-instance room: pb_user only (A.10).
	"host_bare": {
		KindPBUser: cellRostered,
	},
	// host:<inst>:<class>
	"host_class": {
		KindPBUser:    cellRostered,
		KindMachine:   cellS,
		KindSpectator: cellBound,
		KindDevice:    cellBound,
		KindAnonymous: cellBound,
	},
	// host:all / host:summary — scope only (room.join:* on admin, machine keys).
	"host_aggregate": {
		KindPBUser:  cellS,
		KindMachine: cellS,
	},
	// admin — via the admin.admin scope.
	"admin": {
		KindPBUser: cellS,
	},
	// public / any other registered type — everyone including Nobody().
	"public": {
		KindPBUser:    cellAlways,
		KindMachine:   cellAlways,
		KindSpectator: cellAlways,
		KindDevice:    cellAlways,
		KindAnonymous: cellAlways,
	},
}

// roomShape classifies a room for roomJoinRules.
func roomShape(rm Room) string {
	switch {
	case !rm.IsHost():
		if rm.Type == "admin" {
			return "admin"
		}
		return "public"
	case rm.IsHostAggregate():
		return "host_aggregate"
	case rm.Class == "":
		return "host_bare"
	default:
		return "host_class"
	}
}

// roomJoinWants: the admin room is granted by the admin.admin scope, not by
// a room.join selector.
func roomJoinWants(a Action, r Resource) []string {
	if !r.Room.IsHost() && r.Room.Type == "admin" {
		return []string{string(ActionAdminAdmin)}
	}
	return []string{ScopeFor(a, r)}
}

// tokenMintResource: the kind being minted must be a known token kind.
func tokenMintResource(r Resource) bool {
	_, ok := tokenKinds[r.Extra["kind"]]
	return ok
}

// tokenMintWants: "token.mint" (or "token.mint:<kind>") for every kind;
// "overlay.mint" additionally grants spectator keys (PD-5).
func tokenMintWants(a Action, r Resource) []string {
	kind := r.Extra["kind"]
	wants := []string{string(a), string(a) + ":" + kind}
	if kind == "spectator" {
		wants = append(wants, string(ActionOverlayMint))
	}
	return wants
}

// Row shorthands for the table below.
var (
	rowScopeUser        = map[Kind]kindRule{KindPBUser: cellS}
	rowScopeUserMachine = map[Kind]kindRule{KindPBUser: cellS, KindMachine: cellS}
	rowBoxAccess        = map[Kind]kindRule{KindPBUser: cellRostered, KindDevice: cellBound}
	rowBoxOwner         = map[Kind]kindRule{KindPBUser: cellBoxOwner}
	rowRole             = map[Kind]kindRule{KindPBUser: {Mode: modeScopeAndPred, Pred: predLevelOK}}
	rowTeam             = map[Kind]kindRule{KindPBUser: {Mode: modeScopeOrPred, Pred: predTeamAuthority}}
)

// rules is the §4 table as data. Every Action has exactly one entry
// (rules_test pins that), and every rule's kinds are a subset of Kinds()
// minus superuser/internal.
var rules = map[Action]rule{
	// WS
	ActionRoomJoin: {Resources: []ResourceKind{ResRoom}, Wants: roomJoinWants},
	// ws.send is not a Can action at runtime — WSSendAllowed(kind, type) is
	// the whole check; the rule exists so Actions() is closed. No kind passes.
	ActionWSSend:        {Resources: []ResourceKind{ResGlobal}},
	ActionScraperProbe:  {Resources: []ResourceKind{ResInstance}, Kinds: rowScopeUserMachine},
	ActionScraperEvents: {Resources: []ResourceKind{ResInstance}, Kinds: map[Kind]kindRule{KindPBUser: cellRostered, KindMachine: cellS, KindSpectator: cellBound, KindDevice: cellBound}},
	ActionScraperState:  {Resources: []ResourceKind{ResInstance}, Kinds: map[Kind]kindRule{KindPBUser: cellRostered, KindMachine: cellS, KindSpectator: cellBound, KindDevice: cellBound}},
	ActionScraperIngest: {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	// Overlay
	ActionOverlayReadState:    {Resources: []ResourceKind{ResInstance}, Kinds: map[Kind]kindRule{KindPBUser: cellS, KindMachine: cellS, KindSpectator: cellBound, KindDevice: cellBound, KindAnonymous: cellBound}},
	ActionOverlayListConsoles: {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionOverlayMint:         {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	// Box: screen (view/drive) + lifecycle
	ActionBoxView:           {Resources: []ResourceKind{ResContainer}, Kinds: rowBoxAccess},
	ActionBoxDrive:          {Resources: []ResourceKind{ResContainer}, Kinds: rowBoxAccess},
	ActionBoxRead:           {Resources: []ResourceKind{ResContainer}, Kinds: rowBoxAccess},
	ActionBoxControl:        {Resources: []ResourceKind{ResContainer}, Kinds: rowBoxOwner},
	ActionBoxTeardown:       {Resources: []ResourceKind{ResContainer}, Kinds: rowBoxOwner},
	ActionBoxProvision:      {Resources: []ResourceKind{ResISO}, Kinds: map[Kind]kindRule{KindPBUser: {Mode: modePred, Pred: predAuthed}}},
	ActionBoxProvisionNamed: {Resources: []ResourceKind{ResISO}, Kinds: rowScopeUser},
	ActionContainerManage:   {Resources: []ResourceKind{ResContainer, ResGlobal}, Kinds: rowScopeUser},
	// Admin route groups — admin.scraper alone admits machine keys (host-runner keys later).
	ActionAdminAdmin:      {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUser},
	ActionAdminUsers:      {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUser},
	ActionAdminContainers: {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUser},
	ActionAdminScraper:    {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionAdminXemu:       {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUser},
	ActionAdminPod:        {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUser},
	// Library / ISO
	ActionLibraryManage: {Resources: []ResourceKind{ResGlobal, ResISO}, Kinds: rowScopeUser},
	ActionISOSetPolicy:  {Resources: []ResourceKind{ResISO}, Kinds: rowScopeUser},
	// Roles / moderation
	ActionRoleGrant:        {Resources: []ResourceKind{ResRole}, Kinds: rowRole},
	ActionRoleRevoke:       {Resources: []ResourceKind{ResRole}, Kinds: rowRole, Deny: []predicate{denyLastAdmin}},
	ActionUserModerate:     {Resources: []ResourceKind{ResUser}, Kinds: rowScopeUser},
	ActionUserSoftDelete:   {Resources: []ResourceKind{ResUser}, Kinds: rowScopeUser},
	ActionGamertagModerate: {Resources: []ResourceKind{ResRecord}, Collection: "gamertags", Kinds: rowScopeUser},
	ActionTeamModerate:     {Resources: []ResourceKind{ResRecord}, Collection: "teams", Kinds: rowScopeUser},
	ActionNotifAdminEdit:   {Resources: []ResourceKind{ResRecord}, Collection: "notifications", Kinds: rowScopeUser},
	// Teams (non-admin)
	ActionTeamInvite: {Resources: []ResourceKind{ResTeam}, Kinds: rowTeam},
	ActionTeamRemove: {Resources: []ResourceKind{ResTeam}, Kinds: rowTeam},
	ActionTeamDecide: {Resources: []ResourceKind{ResTeam}, Kinds: rowTeam},
	// LAN
	ActionLANSavesIdentity: {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionLANSavesFile:     {Resources: []ResourceKind{ResLANFile}, Kinds: map[Kind]kindRule{KindPBUser: cellS, KindMachine: {Mode: modeScopeAndPred, Pred: predStationFile}}},
	ActionLANSavesBuild:    {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionLANSavesDownload: {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionLANSavesManifest: {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionLANSavesMeta:     {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionLANSyncManifest:  {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	ActionLANSyncDLGame:    {Resources: []ResourceKind{ResISO}, Kinds: rowScopeUserMachine},
	ActionLANSyncDLApp:     {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	// Tokens
	ActionTokenMint:   {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine, Accept: tokenMintResource, Wants: tokenMintWants},
	ActionTokenRevoke: {Resources: []ResourceKind{ResToken}, Kinds: rowScopeUserMachine},
	ActionTokenList:   {Resources: []ResourceKind{ResGlobal}, Kinds: rowScopeUserMachine},
	// Discord
	ActionDiscordConfig:      {Resources: []ResourceKind{ResGuild}, Kinds: map[Kind]kindRule{KindDiscord: {Mode: modePred, Pred: predManageGuild}}},
	ActionDiscordBindChannel: {Resources: []ResourceKind{ResGuild}, Kinds: map[Kind]kindRule{KindDiscord: {Mode: modePred, Pred: predManageGuild}}},
	ActionDiscordStatsRead:   {Resources: []ResourceKind{ResGuild}, Kinds: map[Kind]kindRule{KindDiscord: cellAlways}},
	ActionDiscordBox:         {Resources: []ResourceKind{ResContainer, ResGlobal}, Kinds: map[Kind]kindRule{KindDiscord: {Mode: modePred, Pred: predLinked}}},
	// discord.post is internal only: no kind is admitted (superuser/internal
	// pass as allow-all).
	ActionDiscordPost: {},
}

// accepts reports whether the rule admits the resource: kind in Resources,
// pinned collection for records, a well-formed room for rooms, a known token
// kind for token.mint.
func (rl rule) accepts(r Resource) bool {
	if len(rl.Resources) > 0 {
		found := false
		for _, k := range rl.Resources {
			if k == r.Kind {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if r.Kind == ResRecord && rl.Collection != "" && r.Collection != rl.Collection {
		return false
	}
	if r.Kind == ResRoom && !roomCanonical(r.Room) {
		return false
	}
	if rl.Accept != nil && !rl.Accept(r) {
		return false
	}
	return true
}

// roomCanonical reports whether rm is exactly what ParseRoom yields for its
// own selector. A hand-built Room whose fields do not round-trip (Type
// "host:x", Instance "x:tick", a class on a non-host room, …) would otherwise
// be classified by roomShape into a sub-row — possibly the everyone-passes
// public one — that its selector does not address. Raw is not compared: it is
// the client's spelling and carries no meaning for the rule.
func roomCanonical(rm Room) bool {
	parsed, err := ParseRoom(rm.Selector())
	if err != nil {
		return false
	}
	return parsed.Type == rm.Type && parsed.Instance == rm.Instance && parsed.Class == rm.Class
}

// kindsFor returns the kind table for (rule, resource): the room.join
// sub-row for rooms, rl.Kinds otherwise.
func (rl rule) kindsFor(r Resource) map[Kind]kindRule {
	if r.Kind == ResRoom {
		return roomJoinRules[roomShape(r.Room)]
	}
	return rl.Kinds
}

// wantsFor returns the scope wants to try for (a, r).
func (rl rule) wantsFor(a Action, r Resource) []string {
	if rl.Wants != nil {
		return rl.Wants(a, r)
	}
	return []string{ScopeFor(a, r)}
}

// ---- predicates (names are the Decision.Reason suffixes) ----

func predAlways(Deps, Principal, Resource) (bool, string) { return true, "always" }

func predAuthed(_ Deps, p Principal, _ Resource) (bool, string) {
	return p.UserID != "", "authed"
}

func predLinked(_ Deps, p Principal, _ Resource) (bool, string) {
	return p.UserID != "", "linked"
}

// predRostered: the principal's user has a usable gamertag in the
// instance's live roster (rostergrace TTL applies inside deps, A.2).
func predRostered(deps Deps, p Principal, r Resource) (bool, string) {
	inst := r.instance()
	if p.UserID == "" || inst == "" {
		return false, "rostered"
	}
	return deps.RosteredIn(p.UserID, inst), "rostered"
}

// sameInstance compares two instance names under the scope segment fold
// (canonSegment): scopes are Canon-lowercased before they are matched, so a
// binding or box name must be compared the same way or a principal admitted
// by "…:Box1:…" would fail the literal check against "box1". Empty never
// matches anything.
func sameInstance(a, b string) bool {
	a, b = canonSegment(a), canonSegment(b)
	return a != "" && b != "" && a == b
}

// predBoxOwner: the resource is the box the user provisioned (A.8; no
// rostergrace — ownership is by name and the reaper is the TTL).
func predBoxOwner(deps Deps, p Principal, r Resource) (bool, string) {
	inst := r.instance()
	if p.UserID == "" || inst == "" {
		return false, "box_owner"
	}
	return sameInstance(deps.OwnedBox(p.UserID), inst), "box_owner"
}

// predBound: the principal's bound instance is the resource's instance.
func predBound(_ Deps, p Principal, r Resource) (bool, string) {
	return sameInstance(p.BoundInstance(), r.instance()), "bound"
}

// predLevelOK: the actor's level reaches the role's (A.7). An unknown slug
// is false with the name "unknown_role" (S2 → ErrUnknownRole → 400).
func predLevelOK(deps Deps, p Principal, r Resource) (bool, string) {
	lvl, ok := deps.RoleLevel(r.ID)
	if !ok {
		return false, "unknown_role"
	}
	return p.Level >= lvl, "level_ok"
}

func predTeamAuthority(deps Deps, p Principal, r Resource) (bool, string) {
	if p.UserID == "" || r.ID == "" {
		return false, "team_authority"
	}
	return deps.TeamAuthority(p.UserID, r.ID), "team_authority"
}

// profileFileKind reports whether a LAN save kind is a per-player profile
// (lansaves serveKinds: h2-profile, ce-profile; gametype and game are not).
func profileFileKind(kind string) bool {
	return strings.HasSuffix(kind, "-profile")
}

// lanFileKind is the LAN save kind of a lan_file resource: Extra["kind"] when
// the constructor set it, else the "<kind>/" prefix of the ID (LANFile builds
// ID = kind + "/" + id); "" when neither says.
func lanFileKind(r Resource) string {
	if kind := r.Extra["kind"]; kind != "" {
		return kind
	}
	kind, _, ok := strings.Cut(r.ID, "/")
	if !ok {
		return ""
	}
	return kind
}

// splitTags splits a comma-separated gamertag list into its non-empty
// entries, each trimmed and lower-cased so two sanitized tags compare equal
// regardless of how the caller cased them.
func splitTags(list string) []string {
	var out []string
	for _, t := range strings.Split(list, ",") {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// predStationFile (PD-8): a station key bound to gamertags (a non-empty
// Extra["gamertags"] list) may only serve profile files whose owner has one
// of those tags; unbound stations pass, as do non-profile kinds. The owner
// side, Extra["gamertag"], is the comma-separated list of every usable tag
// the owning user holds, so the check is a set intersection. A bound key
// whose resource kind cannot be determined is refused rather than assumed
// non-profile.
func predStationFile(_ Deps, p Principal, r Resource) (bool, string) {
	if p.Extra["gamertags"] == "" {
		return true, "station_file"
	}
	kind := lanFileKind(r)
	if kind == "" {
		return false, "station_file"
	}
	if !profileFileKind(kind) {
		return true, "station_file"
	}
	tags := splitTags(p.Extra["gamertags"])
	for _, owner := range splitTags(r.Extra["gamertag"]) {
		for _, t := range tags {
			if t == owner {
				return true, "station_file"
			}
		}
	}
	return false, "station_file"
}

// Discord permission bits (A.9): MANAGE_GUILD and ADMINISTRATOR.
const (
	discordPermManageGuild   uint64 = 1 << 5
	discordPermAdministrator uint64 = 1 << 3
)

// predManageGuild: the interaction member's server-side permissions carry
// MANAGE_GUILD or ADMINISTRATOR.
func predManageGuild(_ Deps, p Principal, _ Resource) (bool, string) {
	perms, err := strconv.ParseUint(strings.TrimSpace(p.Extra["member_permissions"]), 10, 64)
	if err != nil {
		return false, "manage_guild"
	}
	return perms&(discordPermManageGuild|discordPermAdministrator) != 0, "manage_guild"
}

// denyLastAdmin refuses revoking the admin role from a user who holds it
// while at most one admin remains. Revoking admin from a user who does not
// hold it removes nothing, so it is not refused; a role resource with no
// Extra["target_user"] (not built by Role) falls back to the count alone so
// the guard cannot be dodged by omitting the target, and a holder lookup the
// adapter could not complete (ok=false) refuses too — an unanswerable
// question never opens the gate. internal actors are exempt (§4.4: the
// soft-delete cascade must not be refused; the adapter logs the zero-admin
// state).
func denyLastAdmin(deps Deps, p Principal, r Resource) (bool, string) {
	if p.Kind == KindInternal || r.ID != "admin" {
		return false, "last_admin"
	}
	if deps.AdminCount() > 1 {
		return false, "last_admin"
	}
	target := r.Extra["target_user"]
	if target == "" {
		return true, "last_admin"
	}
	holds, ok := deps.HoldsRole(target, r.ID)
	return holds || !ok, "last_admin"
}

// anyOf chains predicates: the first that passes decides (its name is
// reported); when none does the verdict is false with the names joined by
// "|" so the log line shows everything that was tried.
func anyOf(preds ...predicate) predicate {
	return func(deps Deps, p Principal, r Resource) (bool, string) {
		names := make([]string, 0, len(preds))
		for _, pred := range preds {
			ok, name := pred(deps, p, r)
			if ok {
				return true, name
			}
			names = append(names, name)
		}
		return false, strings.Join(names, "|")
	}
}
