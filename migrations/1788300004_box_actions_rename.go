package migrations

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Rename the box's remote-screen actions: `kiosk.view` → `box.view` and
// `kiosk.input` → `box.drive` (the `kiosk.*` family folds into `box.*`; the
// flagship no longer says "kiosk" for the screen so the word is free for the
// station product). Scopes are stored data — `roles.scopes` (seeded once by
// 1788300001, admin-editable since) and `api_tokens.scopes` (minted keys) —
// and ParseScope rejects the old names now, so without this rewrite every
// principal that relied on a kiosk.* grant would fail closed after the deploy.
//
// Also renames the Discord routing hook `kiosk_links` → `play_links` in
// `discord_routes` (rows keyed (guild_id, hook); the bound channel is kept).
//
// Idempotent: rows are saved only when a scope or hook actually changes;
// collections that don't exist yet are skipped. Down maps the names back
// (`box.view` / `box.drive` → `kiosk.view` / `kiosk.input`, and a bare `box.*`
// regains the `kiosk.*` companion the pre-rename admin seed carried).
func init() {
	m.Register(func(app core.App) error {
		if err := rewriteScopes(app, "roles", renameKioskScopes); err != nil {
			return err
		}
		if err := rewriteScopes(app, "api_tokens", renameKioskScopes); err != nil {
			return err
		}
		return rewriteHook(app, "kiosk_links", "play_links")
	}, func(app core.App) error {
		if err := rewriteScopes(app, "roles", renameBoxScopesBack); err != nil {
			return err
		}
		if err := rewriteScopes(app, "api_tokens", renameBoxScopesBack); err != nil {
			return err
		}
		return rewriteHook(app, "play_links", "kiosk_links")
	})
}

// renameKioskScopes maps the pre-rename scope strings onto the new action
// names, preserving selectors. `kiosk.*[:sel]` becomes the two exact actions
// it covered (`box.view[:sel]` + `box.drive[:sel]`, with a bare family pattern
// expanding to the every-selector form ":*"), never `box.*` — that would also
// grant read/control/teardown/provision. A row that already holds the bare
// `box.*` family (the admin seed) subsumes every rewrite, so the kiosk scopes
// are simply dropped there. Duplicates the rewrite would create are dropped;
// every other scope passes through untouched, in order.
func renameKioskScopes(in []string) (out []string, changed bool) {
	hasBoxFamily := false
	for _, s := range in {
		if s == "box.*" {
			hasBoxFamily = true
		}
	}
	return mapScopes(in, func(action, selector string) []string {
		var repl []string
		switch action {
		case "kiosk.view":
			repl = []string{"box.view" + selector}
		case "kiosk.input":
			repl = []string{"box.drive" + selector}
		case "kiosk.*":
			if selector == "" {
				selector = ":*"
			}
			repl = []string{"box.view" + selector, "box.drive" + selector}
		default:
			return nil
		}
		if hasBoxFamily {
			return []string{}
		}
		return repl
	})
}

// renameBoxScopesBack is the Down direction (see the migration comment). The
// post-rename `box.*` family covers view/drive, which the pre-rename `box.*`
// did not, so a bare `box.*` gets its `kiosk.*` companion back (that is what
// the admin seed held before 1788300004 dropped it).
func renameBoxScopesBack(in []string) (out []string, changed bool) {
	return mapScopes(in, func(action, selector string) []string {
		switch action {
		case "box.view":
			return []string{"kiosk.view" + selector}
		case "box.drive":
			return []string{"kiosk.input" + selector}
		case "box.*":
			return []string{"box.*" + selector, "kiosk.*" + selector}
		}
		return nil
	})
}

// mapScopes applies f to each scope split as (action-pat, ":selector" or "").
// A nil result keeps the scope as is. Output is de-duplicated, first wins;
// changed reports whether the result differs from the input.
func mapScopes(in []string, f func(action, selector string) []string) (out []string, changed bool) {
	seen := make(map[string]bool, len(in))
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, s := range in {
		action, selector := s, ""
		if i := strings.IndexByte(s, ':'); i >= 0 {
			action, selector = s[:i], s[i:]
		}
		repl := f(action, selector)
		if repl == nil {
			add(s)
			continue
		}
		for _, r := range repl {
			add(r)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out, !equalStrings(out, in)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rewriteScopes runs f over every row's `scopes` JSON list in collection,
// saving only rows that change. A missing collection is not an error (a bare
// app that never ran the roles / api_tokens migrations).
func rewriteScopes(app core.App, collection string, f func([]string) ([]string, bool)) error {
	c, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if c.Fields.GetByName("scopes") == nil {
		return nil
	}
	rows, err := app.FindAllRecords(c)
	if err != nil {
		return err
	}
	for _, rec := range rows {
		var scopes []string
		if err := rec.UnmarshalJSONField("scopes", &scopes); err != nil || len(scopes) == 0 {
			continue
		}
		next, changed := f(scopes)
		if !changed {
			continue
		}
		rec.Set("scopes", next)
		if err := app.Save(rec); err != nil {
			return err
		}
	}
	return nil
}

// rewriteHook renames every discord_routes row with hook=from to hook=to. When
// a guild already has a `to` row (unique on (guild_id, hook)) the `from` row is
// deleted instead, so the newer binding wins.
func rewriteHook(app core.App, from, to string) error {
	c, err := app.FindCollectionByNameOrId("discord_routes")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	rows, err := app.FindRecordsByFilter(c, "hook = {:h}", "", 0, 0, dbx.Params{"h": from})
	if err != nil {
		return err
	}
	for _, rec := range rows {
		dup, err := app.FindFirstRecordByFilter(c, "guild_id = {:g} && hook = {:h}",
			dbx.Params{"g": rec.GetString("guild_id"), "h": to})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if dup != nil {
			if err := app.Delete(rec); err != nil {
				return err
			}
			continue
		}
		rec.Set("hook", to)
		if err := app.Save(rec); err != nil {
			return err
		}
	}
	return nil
}
