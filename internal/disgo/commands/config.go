package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/handler"
	"github.com/disgoorg/omit"
	"github.com/disgoorg/snowflake/v2"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/discordcfg"
	"github.com/xemu-cartographer/xemu-cartographer/internal/disgo/authzmw"
)

// /config — the shared admin surface for the guild→channel→hook routing table
// (Discord bot spec). A command GROUP (not itself runnable):
//
//   - /config tags       tag the CURRENT channel with hooks (the prod flow) —
//     one string multi-select of all hooks, this channel's
//     current tags pre-selected; diff-on-submit applies the
//     delta and notes any moves.
//   - /config view       show the guild's full hook → channel routing.
//   - /config bootstrap  test-only quickstart: create a category + channels and
//     tag them (the old /setup, demoted).
//
// Permissions: Manage Server via default_member_permissions (the client-side
// default guild admins tune per-command in Discord settings) AND, server-side,
// the authz `discord.config` decision on the caller's discord principal (D-2):
// every handler first checks configAllowed, which needs the Manage Server or
// Administrator bit in the member's effective permissions; the tags submit
// checks `discord.bind_channel` the same way. A caller without it gets the
// ephemeral configDeniedText.
func init() {
	register(Command{
		Create: discord.SlashCommandCreate{
			Name:                     "config",
			Description:              "Configure this server's Cartographer channel routing (admin)",
			DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageGuild),
			Options: []discord.ApplicationCommandOption{
				discord.ApplicationCommandOptionSubCommand{
					Name:        "tags",
					Description: "Tag THIS channel with bot hooks",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "view",
					Description: "Show this server's hook → channel routing",
				},
				discord.ApplicationCommandOptionSubCommand{
					Name:        "bootstrap",
					Description: "Test-only: create + tag the Cartographer channels",
				},
			},
		},
		Handler: handleConfigCommand,
		Group:   "Configuration",
	})

	// The /config tags submit is a string multi-select; disgo routes components
	// by custom_id path. custom_id = "/cfgtags/{channelID}".
	registerComponent(func(m *handler.Mux) {
		m.SelectMenuComponent("/cfgtags/{channelID}", handleConfigTagsSubmit)
	})
}

// configDeniedText is the ephemeral refusal for a caller without Manage Server.
const configDeniedText = "Manage Server required: only members with the Manage Server (or Administrator) permission can change this server's Cartographer routing."

// configAllowed is the D-2 gate: whether principal p may perform a (one of
// discord.config / discord.bind_channel) on guild gid, decided by authz over
// d — the deps the mux middleware resolved for the interaction. A nil gid
// (DM) denies; the callers reply with their "inside a server" text before
// reaching here, so the gate never masks that hint. Pure + unit-testable.
func configAllowed(d authz.Deps, p authz.Principal, a authz.Action, gid *snowflake.ID) bool {
	if gid == nil {
		return false
	}
	return authz.Can(d, p, a, authz.Guild(gid.String()))
}

func handleConfigCommand(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
	sub := ""
	if data.SubCommandName != nil {
		sub = *data.SubCommandName
	}
	switch sub {
	case "tags":
		return handleConfigTags(e)
	case "view":
		return handleConfigView(e)
	case "bootstrap":
		return handleConfigBootstrap(e)
	default:
		return replyEphemeral(e, "Unknown subcommand.")
	}
}

// handleConfigTags replies (ephemeral) with a single string multi-select listing
// all of this bot's hooks, with the hooks currently pointing at THIS channel
// pre-selected (Default: true). The admin adds/removes and submits.
func handleConfigTags(e *handler.CommandEvent) error {
	app := commandApp()
	if app == nil {
		return replyEphemeral(e, "Config is unavailable right now (PocketBase not wired).")
	}
	gid := e.GuildID()
	if gid == nil {
		return replyEphemeral(e, "Run `/config tags` inside a server.")
	}
	if !configAllowed(authzmw.Deps(e.Ctx), authzmw.From(e.Ctx), authz.ActionDiscordConfig, gid) {
		return replyEphemeral(e, configDeniedText)
	}
	channelID := e.Channel().ID()

	bindings, _ := discordcfg.GetBindings(app, gid.String())
	opts := make([]discord.StringSelectMenuOption, 0, len(discordcfg.PostHooks))
	for _, hook := range discordcfg.PostHooks {
		opts = append(opts, discord.StringSelectMenuOption{
			Label:   discordcfg.HookLabel(hook),
			Value:   hook,
			Default: bindings[hook] == channelID.String(), // currently points here
		})
	}

	minZero := 0
	menu := discord.StringSelectMenuComponent{
		CustomID:    "/cfgtags/" + channelID.String(),
		Placeholder: "Select the hooks this channel serves",
		MinValues:   &minZero, // allow clearing all tags
		MaxValues:   len(opts),
		Options:     opts,
	}
	return e.CreateMessage(discord.MessageCreate{
		Content:    fmt.Sprintf("Tag <#%s> with the bot functions it should serve, then submit:", channelID),
		Flags:      discord.MessageFlagEphemeral,
		Components: []discord.LayoutComponent{discord.NewActionRow(menu)},
	})
}

// handleConfigTagsSubmit diffs the submitted selection against the stored
// bindings for THIS channel and applies the delta, noting any moves.
func handleConfigTagsSubmit(data discord.SelectMenuInteractionData, e *handler.ComponentEvent) error {
	app := commandApp()
	if app == nil {
		return e.UpdateMessage(clearComponents("Config is unavailable right now."))
	}
	gid := e.GuildID()
	if gid == nil {
		return e.UpdateMessage(clearComponents("Run this inside a server."))
	}
	if !configAllowed(authzmw.Deps(e.Ctx), authzmw.From(e.Ctx), authz.ActionDiscordBindChannel, gid) {
		return e.UpdateMessage(clearComponents(configDeniedText))
	}
	channelID := e.Vars["channelID"]
	selected := selectedValues(data)

	bindings, err := discordcfg.GetBindings(app, gid.String())
	if err != nil {
		return e.UpdateMessage(clearComponents("Couldn't read the current routing."))
	}

	plan := diffTags(bindings, channelID, selected)

	var notes []string
	for _, hook := range plan.toSet {
		if err := discordcfg.SetBinding(app, gid.String(), hook, channelID); err != nil {
			return e.UpdateMessage(clearComponents("Failed to save `" + hook + "`: " + err.Error()))
		}
		if from, moved := plan.movedFrom[hook]; moved {
			notes = append(notes, fmt.Sprintf("`%s` moved from <#%s> → here", hook, from))
		} else {
			notes = append(notes, fmt.Sprintf("`%s` added", hook))
		}
	}
	for _, hook := range plan.toDelete {
		if err := discordcfg.DeleteBinding(app, gid.String(), hook); err != nil {
			return e.UpdateMessage(clearComponents("Failed to remove `" + hook + "`: " + err.Error()))
		}
		notes = append(notes, fmt.Sprintf("`%s` removed", hook))
	}

	summary := "✅ No changes."
	if len(notes) > 0 {
		summary = "✅ " + strings.Join(notes, " · ")
	}
	return e.UpdateMessage(clearComponents(summary))
}

// handleConfigView shows the guild's full hook → channel routing.
func handleConfigView(e *handler.CommandEvent) error {
	app := commandApp()
	if app == nil {
		return replyEphemeral(e, "Config is unavailable right now.")
	}
	gid := e.GuildID()
	if gid == nil {
		return replyEphemeral(e, "Run `/config view` inside a server.")
	}
	if !configAllowed(authzmw.Deps(e.Ctx), authzmw.From(e.Ctx), authz.ActionDiscordConfig, gid) {
		return replyEphemeral(e, configDeniedText)
	}
	bindings, err := discordcfg.GetBindings(app, gid.String())
	if err != nil {
		return replyEphemeral(e, "Couldn't read the routing.")
	}
	emb := discord.Embed{Title: "Channel routing", Color: 0x5865f2}
	if len(bindings) == 0 {
		emb.Description = "No hooks configured yet. Use `/config tags` in a channel, or `/config bootstrap`."
		return replyEmbedEphemeral(e, emb)
	}
	// Deterministic order: hooks in the known order, then any unknown.
	hooks := orderedHooks(bindings)
	var lines []string
	for _, hook := range hooks {
		lines = append(lines, fmt.Sprintf("**%s** → <#%s>", discordcfg.HookLabel(hook), bindings[hook]))
	}
	emb.Description = strings.Join(lines, "\n")
	return replyEmbedEphemeral(e, emb)
}

// tagsPlan is the applied delta of a /config tags submit (pure result).
type tagsPlan struct {
	toSet     []string          // hooks to bind to this channel
	toDelete  []string          // hooks to remove (were here, now unselected)
	movedFrom map[string]string // hook → old channel, for toSet hooks bound elsewhere
}

// diffTags computes the delta between the submitted `selected` hooks and the
// current `bindings`, for `channelID`. Pure + unit-testable.
//
//   - a selected hook not currently pointing here → toSet (moved, if it pointed
//     at another channel).
//   - a hook currently pointing here but not selected → toDelete.
func diffTags(bindings map[string]string, channelID string, selected []string) tagsPlan {
	sel := make(map[string]bool, len(selected))
	for _, h := range selected {
		sel[h] = true
	}
	plan := tagsPlan{movedFrom: map[string]string{}}
	for _, hook := range selected {
		if bindings[hook] == channelID {
			continue // already here
		}
		plan.toSet = append(plan.toSet, hook)
		if old, ok := bindings[hook]; ok && old != "" {
			plan.movedFrom[hook] = old
		}
	}
	// Remove hooks that currently point HERE but weren't re-selected.
	var here []string
	for hook, ch := range bindings {
		if ch == channelID && !sel[hook] {
			here = append(here, hook)
		}
	}
	sort.Strings(here)
	plan.toDelete = here
	sort.Strings(plan.toSet)
	return plan
}

// selectedValues extracts the chosen values from a string select interaction.
func selectedValues(data discord.SelectMenuInteractionData) []string {
	if s, ok := data.(discord.StringSelectMenuInteractionData); ok {
		return s.Values
	}
	return nil
}

// clearComponents builds a MessageUpdate that sets the content and removes the
// select menu (the interaction is done).
func clearComponents(content string) discord.MessageUpdate {
	empty := []discord.LayoutComponent{}
	return discord.MessageUpdate{Content: &content, Components: &empty}
}

// orderedHooks returns the guild's configured hooks in a stable display order
// (known hooks first in canonical order, then any unknown alphabetically).
func orderedHooks(bindings map[string]string) []string {
	known := append([]string{discordcfg.HookCategory}, discordcfg.PostHooks...)
	var out []string
	seen := map[string]bool{}
	for _, h := range known {
		if _, ok := bindings[h]; ok {
			out = append(out, h)
			seen[h] = true
		}
	}
	var extra []string
	for h := range bindings {
		if !seen[h] {
			extra = append(extra, h)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// ---- /config bootstrap (the demoted /setup) ----

// bootstrapCategoryName is the category the bot channels live under.
const bootstrapCategoryName = "Cartographer"

// channelSpec is one provisioned channel: its Discord name + the hook it binds.
type channelSpec struct {
	name string
	hook string
}

// bootstrapChannelSpec is the fixed set of function channels /config bootstrap
// provisions. Pure (no Discord/DB), so the shape is unit-testable.
func bootstrapChannelSpec() []channelSpec {
	return []channelSpec{
		{"container-status", discordcfg.HookContainerStatus},
		{"play-links", discordcfg.HookPlayLinks},
		{"announcements", discordcfg.HookAnnouncements},
		{"bot-log", discordcfg.HookBotLog},
	}
}

// handleConfigBootstrap is the old /setup: idempotent (reuse-by-stored-id then
// by-name) creation of a Cartographer category + one channel per bot function,
// persisting each channel→hook binding. Test-only quickstart — the prod flow is
// `/config tags` per channel.
func handleConfigBootstrap(e *handler.CommandEvent) error {
	gid := e.GuildID()
	if gid == nil {
		return replyEphemeral(e, "Run `/config bootstrap` inside a server.")
	}
	if !configAllowed(authzmw.Deps(e.Ctx), authzmw.From(e.Ctx), authz.ActionDiscordConfig, gid) {
		return replyEphemeral(e, configDeniedText)
	}
	app := commandApp()
	if app == nil {
		return replyEphemeral(e, "Bootstrap is unavailable right now (PocketBase not wired).")
	}

	existing, err := e.Client().Rest.GetGuildChannels(*gid)
	if err != nil {
		return replyEphemeral(e, "Couldn't read this server's channels — does the bot have Manage Channels?")
	}

	bindings, _ := discordcfg.GetBindings(app, gid.String())
	var log []string

	catID, made, err := ensureCategory(e, *gid, existing, bindings[discordcfg.HookCategory])
	if err != nil {
		return replyEphemeral(e, "Failed to create the category: "+err.Error())
	}
	if err := discordcfg.SetBinding(app, gid.String(), discordcfg.HookCategory, catID.String()); err != nil {
		return replyEphemeral(e, "Category ready but saving the binding failed: "+err.Error())
	}
	log = append(log, statusLine(bootstrapCategoryName, made))

	for _, spec := range bootstrapChannelSpec() {
		id, made, err := ensureTextChannel(e, *gid, existing, spec.name, catID)
		if err != nil {
			return replyEphemeral(e, fmt.Sprintf("Failed to create #%s: %s", spec.name, err.Error()))
		}
		if err := discordcfg.SetBinding(app, gid.String(), spec.hook, id.String()); err != nil {
			return replyEphemeral(e, fmt.Sprintf("#%s ready but saving its binding failed: %s", spec.name, err.Error()))
		}
		log = append(log, statusLine("#"+spec.name, made))
	}

	emb := discord.Embed{
		Title:       "Cartographer bootstrap",
		Color:       0x57f287,
		Description: "Provisioned + saved this server's channel routing:\n" + strings.Join(log, "\n"),
	}
	return replyEmbedEphemeral(e, emb)
}

// ensureCategory returns the category ID, preferring a still-valid persisted id,
// then a category matching bootstrapCategoryName, else creating one.
func ensureCategory(e *handler.CommandEvent, gid snowflake.ID, existing []discord.GuildChannel, storedID string) (snowflake.ID, bool, error) {
	if id, ok := findChannel(existing, storedID, discord.ChannelTypeGuildCategory); ok {
		return id, false, nil
	}
	if id, ok := findChannelByName(existing, bootstrapCategoryName, discord.ChannelTypeGuildCategory); ok {
		return id, false, nil
	}
	c, err := e.Client().Rest.CreateGuildChannel(gid, discord.GuildCategoryChannelCreate{Name: bootstrapCategoryName})
	if err != nil {
		return 0, false, err
	}
	return c.ID(), true, nil
}

// ensureTextChannel returns the ID of the named text channel under parent,
// reusing an existing one (by name) or creating it.
func ensureTextChannel(e *handler.CommandEvent, gid snowflake.ID, existing []discord.GuildChannel, name string, parent snowflake.ID) (snowflake.ID, bool, error) {
	if id, ok := findChannelByName(existing, name, discord.ChannelTypeGuildText); ok {
		return id, false, nil
	}
	c, err := e.Client().Rest.CreateGuildChannel(gid, discord.GuildTextChannelCreate{Name: name, ParentID: parent})
	if err != nil {
		return 0, false, err
	}
	return c.ID(), true, nil
}

// findChannel looks up a channel by (non-empty) stored ID string and type.
func findChannel(chans []discord.GuildChannel, storedID string, typ discord.ChannelType) (snowflake.ID, bool) {
	if storedID == "" {
		return 0, false
	}
	id, err := snowflake.Parse(storedID)
	if err != nil {
		return 0, false
	}
	for _, c := range chans {
		if c.ID() == id && c.Type() == typ {
			return id, true
		}
	}
	return 0, false
}

// findChannelByName matches a channel by case-insensitive name + type.
func findChannelByName(chans []discord.GuildChannel, name string, typ discord.ChannelType) (snowflake.ID, bool) {
	for _, c := range chans {
		if c.Type() == typ && strings.EqualFold(c.Name(), name) {
			return c.ID(), true
		}
	}
	return 0, false
}

func statusLine(name string, created bool) string {
	if created {
		return "• " + name + " — created"
	}
	return "• " + name + " — reused"
}
