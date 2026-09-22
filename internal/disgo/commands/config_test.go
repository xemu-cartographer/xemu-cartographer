package commands

import (
	"sort"
	"strconv"
	"testing"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/authztest"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/discordcfg"
)

// TestBootstrapChannelSpec verifies the fixed channel set + that each Discord
// channel name maps to a distinct, valid post-hook (the pure part of
// /config bootstrap; the Discord REST provisioning + binding writes are thin
// plumbing verified live).
func TestBootstrapChannelSpec(t *testing.T) {
	spec := bootstrapChannelSpec()
	want := map[string]string{
		"container-status": discordcfg.HookContainerStatus,
		"play-links":       discordcfg.HookPlayLinks,
		"announcements":    discordcfg.HookAnnouncements,
		"bot-log":          discordcfg.HookBotLog,
	}
	if len(spec) != len(want) {
		t.Fatalf("spec has %d channels, want %d", len(spec), len(want))
	}
	postHooks := map[string]bool{}
	for _, h := range discordcfg.PostHooks {
		postHooks[h] = true
	}
	seen := map[string]bool{}
	for _, s := range spec {
		if want[s.name] != s.hook {
			t.Errorf("channel %q → hook %q, want %q", s.name, s.hook, want[s.name])
		}
		if !postHooks[s.hook] {
			t.Errorf("hook %q for %q is not in PostHooks", s.hook, s.name)
		}
		if seen[s.hook] {
			t.Errorf("hook %q bound more than once", s.hook)
		}
		seen[s.hook] = true
	}
}

// TestDiffTags covers the pure /config tags diff: add, remove, move, no-op.
func TestDiffTags(t *testing.T) {
	const here = "chan-here"
	const other = "chan-other"

	t.Run("add new hook", func(t *testing.T) {
		plan := diffTags(map[string]string{}, here, []string{"announcements"})
		if len(plan.toSet) != 1 || plan.toSet[0] != "announcements" {
			t.Fatalf("toSet = %v, want [announcements]", plan.toSet)
		}
		if len(plan.toDelete) != 0 {
			t.Errorf("toDelete = %v, want []", plan.toDelete)
		}
		if _, moved := plan.movedFrom["announcements"]; moved {
			t.Errorf("new hook should not be a move")
		}
	})

	t.Run("move hook from another channel", func(t *testing.T) {
		plan := diffTags(map[string]string{"announcements": other}, here, []string{"announcements"})
		if len(plan.toSet) != 1 {
			t.Fatalf("toSet = %v, want [announcements]", plan.toSet)
		}
		if plan.movedFrom["announcements"] != other {
			t.Errorf("movedFrom = %v, want announcements→%s", plan.movedFrom, other)
		}
	})

	t.Run("remove hook that pointed here", func(t *testing.T) {
		plan := diffTags(map[string]string{"bot_log": here, "play_links": other}, here, nil)
		if len(plan.toDelete) != 1 || plan.toDelete[0] != "bot_log" {
			t.Errorf("toDelete = %v, want [bot_log] (play_links points elsewhere, untouched)", plan.toDelete)
		}
		if len(plan.toSet) != 0 {
			t.Errorf("toSet = %v, want []", plan.toSet)
		}
	})

	t.Run("no-op: already bound here + reselected", func(t *testing.T) {
		plan := diffTags(map[string]string{"announcements": here}, here, []string{"announcements"})
		if len(plan.toSet) != 0 || len(plan.toDelete) != 0 {
			t.Errorf("expected no changes, got set=%v del=%v", plan.toSet, plan.toDelete)
		}
	})

	t.Run("combined add + remove", func(t *testing.T) {
		plan := diffTags(
			map[string]string{"bot_log": here, "announcements": other},
			here,
			[]string{"container_status", "announcements"},
		)
		sort.Strings(plan.toSet)
		want := []string{"announcements", "container_status"}
		if len(plan.toSet) != 2 || plan.toSet[0] != want[0] || plan.toSet[1] != want[1] {
			t.Errorf("toSet = %v, want %v", plan.toSet, want)
		}
		if len(plan.toDelete) != 1 || plan.toDelete[0] != "bot_log" {
			t.Errorf("toDelete = %v, want [bot_log]", plan.toDelete)
		}
		if plan.movedFrom["announcements"] != other {
			t.Errorf("announcements should be a move from %s", other)
		}
	})
}

// TestConfigAllowedManageGuild pins the D-2 gate every /config handler runs
// first: the caller's server-side member permissions must carry Manage
// Server (or Administrator) for discord.config and discord.bind_channel; a
// DM (nil guild), a request the mux middleware did not see (Nobody) and a
// missing adapter all deny.
func TestConfigAllowedManageGuild(t *testing.T) {
	deps := &authztest.FakeDeps{}
	gid := snowflake.ID(123456789)
	member := func(perms discord.Permissions) authz.Principal {
		return authz.Principal{
			Kind: authz.KindDiscord,
			ID:   "456",
			Extra: map[string]string{
				"guild_id":           gid.String(),
				"member_permissions": strconv.FormatUint(uint64(perms), 10),
			},
		}
	}

	cases := []struct {
		name string
		deps authz.Deps
		p    authz.Principal
		gid  *snowflake.ID
		want bool
	}{
		{"manage guild", deps, member(discord.PermissionManageGuild), &gid, true},
		{"administrator", deps, member(discord.PermissionAdministrator), &gid, true},
		{"manage guild among others", deps, member(discord.PermissionManageGuild | discord.PermissionSendMessages), &gid, true},
		{"send messages only", deps, member(discord.PermissionSendMessages), &gid, false},
		{"manage channels only", deps, member(discord.PermissionManageChannels), &gid, false},
		{"no permissions", deps, member(0), &gid, false},
		{"dm", deps, member(discord.PermissionManageGuild), nil, false},
		{"no middleware", deps, authz.Nobody(), &gid, false},
		{"no deps", nil, member(discord.PermissionManageGuild), &gid, false},
		{"nil adapter", (*pb.PBDeps)(nil), member(discord.PermissionManageGuild), &gid, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, a := range []authz.Action{authz.ActionDiscordConfig, authz.ActionDiscordBindChannel} {
				if got := configAllowed(c.deps, c.p, a, c.gid); got != c.want {
					t.Errorf("%s: configAllowed = %v, want %v", a, got, c.want)
				}
			}
		})
	}
}
