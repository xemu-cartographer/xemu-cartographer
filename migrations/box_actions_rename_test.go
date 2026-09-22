package migrations

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

const boxRenameMigration = "1788300004_box_actions_rename.go"

func TestRenameKioskScopes(t *testing.T) {
	cases := []struct {
		in      []string
		want    []string
		changed bool
	}{
		{nil, []string{}, false},
		{[]string{"box.*", "lan.*"}, []string{"box.*", "lan.*"}, false},
		{[]string{"kiosk.view:box1", "kiosk.input:box1"}, []string{"box.view:box1", "box.drive:box1"}, true},
		{[]string{"kiosk.view", "kiosk.input"}, []string{"box.view", "box.drive"}, true},
		// family → the two exact actions, every selector; never box.*
		{[]string{"kiosk.*"}, []string{"box.view:*", "box.drive:*"}, true},
		{[]string{"kiosk.*:box1"}, []string{"box.view:box1", "box.drive:box1"}, true},
		// duplicates the rewrite would create collapse, order kept
		{[]string{"box.view:box1", "kiosk.view:box1", "kiosk.*:box1"}, []string{"box.view:box1", "box.drive:box1"}, true},
		// admin seed shape: the bare box.* family now covers view/drive, so the
		// kiosk grants are dropped rather than duplicated as exact actions
		{[]string{"admin.*", "box.*", "kiosk.*", "lan.*"}, []string{"admin.*", "box.*", "lan.*"}, true},
		{[]string{"box.*", "kiosk.view:box1"}, []string{"box.*"}, true},
		// a selector-bound family does not subsume: rewrite as usual
		{[]string{"box.*:box1", "kiosk.*:box1"}, []string{"box.*:box1", "box.view:box1", "box.drive:box1"}, true},
		// unknown kiosk.* member passes through (was invalid before too)
		{[]string{"kiosk.other:box1"}, []string{"kiosk.other:box1"}, false},
	}
	for _, c := range cases {
		got, changed := renameKioskScopes(c.in)
		if !reflect.DeepEqual(got, c.want) || changed != c.changed {
			t.Errorf("renameKioskScopes(%v) = %v,%v want %v,%v", c.in, got, changed, c.want, c.changed)
		}
	}
	backCases := []struct {
		in      []string
		want    []string
		changed bool
	}{
		{[]string{"box.view:*", "box.drive:*", "box.read:box1"}, []string{"kiosk.view:*", "kiosk.input:*", "box.read:box1"}, true},
		// bare box.* gets its kiosk.* companion back (the pre-rename admin seed)
		{[]string{"admin.*", "box.*", "lan.*"}, []string{"admin.*", "box.*", "kiosk.*", "lan.*"}, true},
		// already there → no change reported
		{[]string{"box.*", "kiosk.*"}, []string{"box.*", "kiosk.*"}, false},
		{[]string{"lan.saves.meta"}, []string{"lan.saves.meta"}, false},
	}
	for _, c := range backCases {
		got, changed := renameBoxScopesBack(c.in)
		if !reflect.DeepEqual(got, c.want) || changed != c.changed {
			t.Errorf("renameBoxScopesBack(%v) = %v,%v want %v,%v", c.in, got, changed, c.want, c.changed)
		}
	}
}

// TestBoxActionsRenameMigration runs 1788300004 up (twice — idempotent) and
// down on stand-in roles / api_tokens / discord_routes collections.
func TestBoxActionsRenameMigration(t *testing.T) {
	app := newBareApp(t)

	roles := core.NewBaseCollection("roles")
	roles.Fields.Add(
		&core.TextField{Name: "slug", Required: true},
		&core.JSONField{Name: "scopes", MaxSize: 65536},
	)
	if err := app.Save(roles); err != nil {
		t.Fatal(err)
	}
	tokens := core.NewBaseCollection("api_tokens")
	tokens.Fields.Add(
		&core.TextField{Name: "kid", Required: true},
		&core.JSONField{Name: "scopes", MaxSize: 65536},
	)
	if err := app.Save(tokens); err != nil {
		t.Fatal(err)
	}
	routes := core.NewBaseCollection("discord_routes")
	routes.Fields.Add(
		&core.TextField{Name: "guild_id", Required: true},
		&core.TextField{Name: "hook", Required: true},
		&core.TextField{Name: "channel_id"},
	)
	routes.AddIndex("idx_test_routes_guild_hook", true, "guild_id, hook", "")
	if err := app.Save(routes); err != nil {
		t.Fatal(err)
	}

	newRow := func(col *core.Collection, kv map[string]any) *core.Record {
		t.Helper()
		r := core.NewRecord(col)
		for k, v := range kv {
			r.Set(k, v)
		}
		if err := app.Save(r); err != nil {
			t.Fatalf("seed %s: %v", col.Name, err)
		}
		return r
	}
	newRow(roles, map[string]any{"slug": "admin", "scopes": []string{"admin.*", "box.*", "kiosk.*"}})
	newRow(roles, map[string]any{"slug": "usher", "scopes": []string{"kiosk.view:*"}})
	newRow(roles, map[string]any{"slug": "member", "scopes": []string{"lan.saves.meta"}})
	newRow(tokens, map[string]any{"kid": "dv_1", "scopes": []string{"kiosk.view:xc-1", "kiosk.input:xc-1", "box.read:xc-1"}})
	newRow(routes, map[string]any{"guild_id": "g1", "hook": "kiosk_links", "channel_id": "c-old"})
	// g2 already has a play_links row: its kiosk_links row must be dropped, not collide.
	newRow(routes, map[string]any{"guild_id": "g2", "hook": "kiosk_links", "channel_id": "c-stale"})
	newRow(routes, map[string]any{"guild_id": "g2", "hook": "play_links", "channel_id": "c-new"})
	newRow(routes, map[string]any{"guild_id": "g1", "hook": "bot_log", "channel_id": "c-log"})

	scopesOf := func(collection, key, value string) []string {
		t.Helper()
		var s []string
		rec := mustRecord(t, app, collection, key, value)
		if err := json.Unmarshal([]byte(rec.GetString("scopes")), &s); err != nil {
			t.Fatalf("%s %s scopes: %v", collection, value, err)
		}
		return s
	}
	hooksOf := func(guild string) map[string]string {
		t.Helper()
		rows, err := app.FindRecordsByFilter("discord_routes", "guild_id = {:g}", "", 0, 0, map[string]any{"g": guild})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, r := range rows {
			out[r.GetString("hook")] = r.GetString("channel_id")
		}
		return out
	}

	for i := 0; i < 2; i++ { // second pass proves idempotence
		if err := runUp(app, boxRenameMigration); err != nil {
			t.Fatalf("up #%d: %v", i+1, err)
		}
		if got, want := scopesOf("roles", "slug", "admin"), []string{"admin.*", "box.*"}; !reflect.DeepEqual(got, want) {
			t.Errorf("up #%d admin scopes = %v, want %v", i+1, got, want)
		}
		if got, want := scopesOf("roles", "slug", "usher"), []string{"box.view:*"}; !reflect.DeepEqual(got, want) {
			t.Errorf("up #%d usher scopes = %v, want %v", i+1, got, want)
		}
		if got, want := scopesOf("api_tokens", "kid", "dv_1"), []string{"box.view:xc-1", "box.drive:xc-1", "box.read:xc-1"}; !reflect.DeepEqual(got, want) {
			t.Errorf("up #%d dv_1 scopes = %v, want %v", i+1, got, want)
		}
		if got, want := hooksOf("g1"), (map[string]string{"play_links": "c-old", "bot_log": "c-log"}); !reflect.DeepEqual(got, want) {
			t.Errorf("up #%d g1 hooks = %v, want %v", i+1, got, want)
		}
		if got, want := hooksOf("g2"), (map[string]string{"play_links": "c-new"}); !reflect.DeepEqual(got, want) {
			t.Errorf("up #%d g2 hooks = %v, want %v", i+1, got, want)
		}
	}
	if got, want := scopesOf("roles", "slug", "member"), []string{"lan.saves.meta"}; !reflect.DeepEqual(got, want) {
		t.Errorf("member scopes = %v, want %v (untouched)", got, want)
	}

	if err := runDown(app, boxRenameMigration); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got, want := scopesOf("roles", "slug", "admin"), []string{"admin.*", "box.*", "kiosk.*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("down admin scopes = %v, want %v", got, want)
	}
	if got, want := scopesOf("roles", "slug", "usher"), []string{"kiosk.view:*"}; !reflect.DeepEqual(got, want) {
		t.Errorf("down usher scopes = %v, want %v", got, want)
	}
	if got, want := scopesOf("api_tokens", "kid", "dv_1"), []string{"kiosk.view:xc-1", "kiosk.input:xc-1", "box.read:xc-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("down dv_1 scopes = %v, want %v", got, want)
	}
	if got, want := hooksOf("g1"), (map[string]string{"kiosk_links": "c-old", "bot_log": "c-log"}); !reflect.DeepEqual(got, want) {
		t.Errorf("down g1 hooks = %v, want %v", got, want)
	}
}
