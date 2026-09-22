package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Authz step 1 (design §5.2): `api_tokens` holds every non-user credential —
// machine keys (`mk_…`, LAN stations / host runners), spectator keys (`sp_…`,
// overlay browser sources) and device keys (`dv_…`, station tablets). Only the
// sha256 of the secret is stored; the secret itself is shown once at mint.
//
// All API rules are nil on purpose: minting, listing and revoking go through
// /api/admin/tokens, which is gated by authz (token.mint / token.list /
// token.revoke) and never returns key_hash. `scopes` is validated by
// authz.ValidateForMint before a row is written.
func init() {
	m.Register(func(app core.App) error {
		if _, err := app.FindCollectionByNameOrId("api_tokens"); err == nil {
			return nil // idempotent
		}
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		gamertags, err := app.FindCollectionByNameOrId("gamertags")
		if err != nil {
			return err
		}

		c := core.NewBaseCollection("api_tokens")
		c.Fields.Add(
			&core.TextField{Name: "kid", Required: true, Max: 64, Presentable: true},
			&core.TextField{Name: "key_hash", Required: true, Max: 128},
			&core.SelectField{Name: "kind", Values: []string{"machine", "spectator", "device"}, MaxSelect: 1, Required: true},
			&core.JSONField{Name: "scopes", Required: true, MaxSize: 65536},
			&core.TextField{Name: "label", Max: 120},
			&core.RelationField{Name: "user", CollectionId: users.Id, MaxSelect: 1},
			&core.TextField{Name: "container", Max: 120},
			&core.TextField{Name: "station_id", Max: 120},
			// PD-8 station binding: a machine key may only serve the profiles of
			// these gamertags. PocketBase treats MaxSelect <= 1 as single, so
			// "many" needs an explicit cap.
			&core.RelationField{Name: "gamertags", CollectionId: gamertags.Id, MaxSelect: 99},
			&core.RelationField{Name: "minted_by", CollectionId: users.Id, MaxSelect: 1},
			&core.DateField{Name: "expires_at"},
			&core.BoolField{Name: "revoked"},
			&core.DateField{Name: "revoked_at"},
			&core.RelationField{Name: "revoked_by", CollectionId: users.Id, MaxSelect: 1},
			&core.DateField{Name: "last_used_at"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		c.AddIndex("idx_api_tokens_kid", true, "kid", "")
		c.AddIndex("idx_api_tokens_kind_revoked", false, "kind, revoked", "")
		return app.Save(c)
	}, func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("api_tokens")
		if err != nil {
			return nil
		}
		return app.Delete(c)
	})
}
