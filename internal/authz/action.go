package authz

import (
	"sort"
	"strings"
)

// Action is one verb of the closed authorization vocabulary. Can denies any
// Action that is not one of the constants below (Decision.Reason
// "unknown_action"), and ParseScope only accepts scopes whose action-pat
// names a known Action or a dotted prefix of one.
type Action string

const (
	// WS
	ActionRoomJoin      Action = "room.join"      // ResRoom
	ActionWSSend        Action = "ws.send"        // ResGlobal; message type gated by WSSendAllowed, never a scope
	ActionScraperProbe  Action = "scraper.probe"  // ResInstance
	ActionScraperEvents Action = "scraper.events" // ResInstance
	ActionScraperState  Action = "scraper.state"  // ResInstance
	ActionScraperIngest Action = "scraper.ingest" // ResGlobal; POST /api/xc/finished_game (daemon webhook)

	// Overlay
	ActionOverlayReadState    Action = "overlay.read_state"    // ResInstance
	ActionOverlayListConsoles Action = "overlay.list_consoles" // ResGlobal
	ActionOverlayMint         Action = "overlay.mint"          // ResGlobal (mint spectator keys)

	// Box: screen (view/drive) + lifecycle
	ActionBoxView           Action = "box.view"            // ResContainer
	ActionBoxDrive          Action = "box.drive"           // ResContainer
	ActionBoxRead           Action = "box.read"            // ResContainer
	ActionBoxControl        Action = "box.control"         // ResContainer
	ActionBoxTeardown       Action = "box.teardown"        // ResContainer
	ActionBoxProvision      Action = "box.provision"       // ResISO
	ActionBoxProvisionNamed Action = "box.provision_named" // ResISO
	ActionContainerManage   Action = "container.manage"    // ResContainer or ResGlobal (list/create/cleanup)

	// Admin route groups (one action per group, replaces middleware.RequireAdmin)
	ActionAdminAdmin      Action = "admin.admin"      // /api/admin/* stats
	ActionAdminUsers      Action = "admin.users"      // /api/admin/users/*
	ActionAdminContainers Action = "admin.containers" // /api/admin/containers/*
	ActionAdminScraper    Action = "admin.scraper"    // /api/admin/scraper/*
	ActionAdminXemu       Action = "admin.xemu"       // /api/admin/xemu/*
	ActionAdminPod        Action = "admin.pod"        // /api/admin/pod/*

	// Library / ISO
	ActionLibraryManage Action = "library.manage" // ResGlobal or ResISO
	ActionISOSetPolicy  Action = "iso.set_policy" // ResISO (role / allow_on_xbox fields)

	// Roles / moderation
	ActionRoleGrant        Action = "role.grant"              // ResRole (Extra target_user)
	ActionRoleRevoke       Action = "role.revoke"             // ResRole
	ActionUserModerate     Action = "user.moderate"           // ResUser (ban/unban/timeout)
	ActionUserSoftDelete   Action = "user.soft_delete"        // ResUser
	ActionGamertagModerate Action = "gamertag.moderate"       // ResRecord gamertags
	ActionTeamModerate     Action = "team.moderate"           // ResRecord teams (status)
	ActionNotifAdminEdit   Action = "notification.admin_edit" // ResRecord notifications

	// Teams (non-admin)
	ActionTeamInvite Action = "team.invite" // ResTeam
	ActionTeamRemove Action = "team.remove" // ResTeam
	ActionTeamDecide Action = "team.decide" // ResTeam (accept/decline a join request)

	// LAN
	ActionLANSavesIdentity Action = "lan.saves.identity"
	ActionLANSavesFile     Action = "lan.saves.file" // ResLANFile
	ActionLANSavesBuild    Action = "lan.saves.build"
	ActionLANSavesDownload Action = "lan.saves.download"
	ActionLANSavesManifest Action = "lan.saves.manifest"
	ActionLANSavesMeta     Action = "lan.saves.meta"
	ActionLANSyncManifest  Action = "lan.sync.manifest"
	ActionLANSyncDLGame    Action = "lan.sync.download_game" // ResISO
	ActionLANSyncDLApp     Action = "lan.sync.download_app"

	// Tokens
	ActionTokenMint   Action = "token.mint"   // ResGlobal; Extra["kind"] on the resource
	ActionTokenRevoke Action = "token.revoke" // ResToken
	ActionTokenList   Action = "token.list"   // ResGlobal

	// Discord
	ActionDiscordConfig      Action = "discord.config"       // ResGuild
	ActionDiscordBindChannel Action = "discord.bind_channel" // ResGuild
	ActionDiscordStatsRead   Action = "discord.stats.read"   // ResGuild
	ActionDiscordBox         Action = "discord.box"          // ResContainer
	ActionDiscordPost        Action = "discord.post"         // internal only
)

// allActions is the closed vocabulary. rules.go keys its table by these and
// TestEveryActionHasRule pins the two lists together.
var allActions = []Action{
	ActionRoomJoin,
	ActionWSSend,
	ActionScraperProbe,
	ActionScraperEvents,
	ActionScraperState,
	ActionScraperIngest,
	ActionOverlayReadState,
	ActionOverlayListConsoles,
	ActionOverlayMint,
	ActionBoxView,
	ActionBoxDrive,
	ActionBoxRead,
	ActionBoxControl,
	ActionBoxTeardown,
	ActionBoxProvision,
	ActionBoxProvisionNamed,
	ActionContainerManage,
	ActionAdminAdmin,
	ActionAdminUsers,
	ActionAdminContainers,
	ActionAdminScraper,
	ActionAdminXemu,
	ActionAdminPod,
	ActionLibraryManage,
	ActionISOSetPolicy,
	ActionRoleGrant,
	ActionRoleRevoke,
	ActionUserModerate,
	ActionUserSoftDelete,
	ActionGamertagModerate,
	ActionTeamModerate,
	ActionNotifAdminEdit,
	ActionTeamInvite,
	ActionTeamRemove,
	ActionTeamDecide,
	ActionLANSavesIdentity,
	ActionLANSavesFile,
	ActionLANSavesBuild,
	ActionLANSavesDownload,
	ActionLANSavesManifest,
	ActionLANSavesMeta,
	ActionLANSyncManifest,
	ActionLANSyncDLGame,
	ActionLANSyncDLApp,
	ActionTokenMint,
	ActionTokenRevoke,
	ActionTokenList,
	ActionDiscordConfig,
	ActionDiscordBindChannel,
	ActionDiscordStatsRead,
	ActionDiscordBox,
	ActionDiscordPost,
}

// actionSet is the lookup form of allActions.
var actionSet = func() map[Action]bool {
	m := make(map[Action]bool, len(allActions))
	for _, a := range allActions {
		m[a] = true
	}
	return m
}()

// Actions returns the closed vocabulary sorted by name (a fresh slice; for
// tests and reports).
func Actions() []Action {
	out := make([]Action, len(allActions))
	copy(out, allActions)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Known reports whether a is one of the closed vocabulary.
func (a Action) Known() bool {
	return actionSet[a]
}

// Family is everything before the LAST dot: "lan.saves" for "lan.saves.file",
// "room" for "room.join". An action without a dot has an empty family.
func (a Action) Family() string {
	s := string(a)
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return ""
	}
	return s[:i]
}

// isActionFamily reports whether family is a dotted prefix of at least one
// known action, i.e. "<family>.*" is a meaningful scope pattern ("lan",
// "lan.saves", "room", ...).
func isActionFamily(family string) bool {
	if family == "" {
		return false
	}
	prefix := family + "."
	for _, a := range allActions {
		if strings.HasPrefix(string(a), prefix) {
			return true
		}
	}
	return false
}
