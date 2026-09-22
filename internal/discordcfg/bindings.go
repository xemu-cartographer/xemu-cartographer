package discordcfg

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Channel-routing hooks — the bot function each guild channel serves. Stored one
// row per (guild, hook) in the canonical `discord_routes` collection (shared
// shape + name with the norcal-halo-site bot per the Discord bot spec), so a new
// hook is a new ROW (data), never a schema change. `/config` writes these;
// commands/events read them to know where to post.
//
// Hooks are APP-OWNED string keys: this constant list is cartographer's hook
// set, and the `/config tags` UI only offers these. The `hook` column is
// free-text (the two bots have different hook sets) — validation lives here.
const (
	HookCategory        = "category"         // structural parent /config bootstrap nests channels under
	HookContainerStatus = "container_status" // live "who's playing"
	HookPlayLinks       = "play_links"       // per-player play links
	HookAnnouncements   = "announcements"    // results / tournament posts (folded from the old results_channel)
	HookTournament      = "tournament"       // tournament posts (folded from the old tournament_channel)
	HookBotLog          = "bot_log"          // bot ops log
)

// PostHooks are the postable hooks the `/config tags` picker offers (everything
// except the structural `category`, which /config bootstrap sets on the parent
// Discord category, not a text channel you tag).
var PostHooks = []string{
	HookContainerStatus,
	HookPlayLinks,
	HookAnnouncements,
	HookTournament,
	HookBotLog,
}

// hookLabels are the human-friendly labels the /config UI shows per hook.
var hookLabels = map[string]string{
	HookCategory:        "Category (structural)",
	HookContainerStatus: "Container status",
	HookPlayLinks:       "Play links",
	HookAnnouncements:   "Announcements / results",
	HookTournament:      "Tournament posts",
	HookBotLog:          "Bot log",
}

// HookLabel returns a human label for a hook (the raw key when unknown).
func HookLabel(hook string) string {
	if l, ok := hookLabels[hook]; ok {
		return l
	}
	return hook
}

// RoutesCollection is the canonical routing-table collection name.
const RoutesCollection = "discord_routes"

// GetBinding returns the channel ID bound to (guild, hook), or ("", false).
func GetBinding(app core.App, guildID, hook string) (string, bool) {
	r, err := app.FindFirstRecordByFilter(RoutesCollection,
		"guild_id = {:g} && hook = {:h}", dbx.Params{"g": guildID, "h": hook})
	if err != nil || r == nil {
		return "", false
	}
	return r.GetString("channel_id"), true
}

// GetBindings returns a guild's full hook→channel map (empty when unconfigured).
func GetBindings(app core.App, guildID string) (map[string]string, error) {
	rows, err := app.FindRecordsByFilter(RoutesCollection,
		"guild_id = {:g}", "", 0, 0, dbx.Params{"g": guildID})
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.GetString("hook")] = r.GetString("channel_id")
	}
	return out, nil
}

// SetBinding upserts the channel bound to (guild, hook) — moves the hook here if
// it pointed elsewhere (unique on (guild, hook)).
func SetBinding(app core.App, guildID, hook, channelID string) error {
	r, err := app.FindFirstRecordByFilter(RoutesCollection,
		"guild_id = {:g} && hook = {:h}", dbx.Params{"g": guildID, "h": hook})
	if err != nil || r == nil {
		col, cerr := app.FindCollectionByNameOrId(RoutesCollection)
		if cerr != nil {
			return cerr
		}
		r = core.NewRecord(col)
		r.Set("guild_id", guildID)
		r.Set("hook", hook)
	}
	r.Set("channel_id", channelID)
	return app.Save(r)
}

// DeleteBinding removes the (guild, hook) route if present. No-op when absent.
func DeleteBinding(app core.App, guildID, hook string) error {
	r, err := app.FindFirstRecordByFilter(RoutesCollection,
		"guild_id = {:g} && hook = {:h}", dbx.Params{"g": guildID, "h": hook})
	if err != nil || r == nil {
		return nil
	}
	return app.Delete(r)
}
