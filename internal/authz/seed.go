package authz

// RoleSeed is one roles row the migration / dev seeder guarantees exists.
type RoleSeed struct {
	Slug   string
	Label  string
	Level  int
	Scopes []string
}

// SeedRoles is the single source of the five built-in roles (design §5.1).
// migrations/1788300001_roles_scopes.go creates the rows, the dev seeder
// (S6) reuses the literal, and migrations/authz_seed_test.go pins it.
//
// Selectorless exact-action scopes match only the empty selector (§3.2 row
// 15), so the actions the call sites check against an identified resource
// — library.manage on ISO(id) (§2.3), iso.set_policy on ISO(rec.Id) (R-3),
// gamertag.moderate / notification.admin_edit on Record(...) (H-2, H-4),
// container.manage on Container(name) (R-7) — are seeded with the ":*"
// selector so the admin / organizer defaults actually reach them. Family
// patterns (admin.*, role.*, …) already cover every selector.
//
// ws.send appears in no seed — it is not a scope (WSSendAllowed).
var SeedRoles = []RoleSeed{
	{
		Slug:  "admin",
		Label: "Admin",
		Level: 100,
		Scopes: []string{
			"admin.*",
			"library.manage:*",
			"iso.set_policy:*",
			"role.*",
			"user.*",
			"gamertag.moderate:*",
			"team.*",
			"notification.admin_edit:*",
			"container.manage:*",
			"box.*",
			"lan.*",
			"room.join:*",
			"scraper.*",
			"overlay.*",
			"token.*",
		},
	},
	{
		Slug:  "organizer",
		Label: "Organizer",
		Level: 50,
		// Today requireOrganizerOrAdmin admits organizers to every isos route
		// including the policy fields — this is the no-behaviour-change default.
		// The browser gametype / profile builder (meta, build, download) is an
		// organizer page too; the station-only routes (file, manifest, sync,
		// identity) stay off this row.
		Scopes: []string{
			"library.manage:*",
			"iso.set_policy:*",
			"lan.saves.meta",
			"lan.saves.build",
			"lan.saves.download",
		},
	},
	{
		Slug:  "member",
		Label: "Member",
		Level: 10,
		// The settings page reads the save-kind schemas (lan.saves.meta) for
		// every signed-in user; nothing else on /api/lan/* reaches a member.
		Scopes: []string{
			"lan.saves.meta",
		},
	},
	{
		Slug:  "overlay_manager",
		Label: "Overlay manager",
		Level: 40,
		// PD-5
		Scopes: []string{
			"overlay.mint",
			"overlay.list_consoles",
			"overlay.read_state:*",
		},
	},
	{
		Slug:  "anonymous",
		Label: "Anonymous",
		Level: 0,
		// PD-1: the console door. overlay.read_state:* is still gated by
		// P:bound, so it only ever reaches the bound instance.
		Scopes: []string{
			"room.join:host:*:game_filtered",
			"room.join:host:*:tick",
			"room.join:host:*:scenario",
			"room.join:host:*:event_filtered",
			"overlay.read_state:*",
		},
	},
}

// SeedRole returns the seed row for slug.
func SeedRole(slug string) (RoleSeed, bool) {
	for _, r := range SeedRoles {
		if r.Slug == slug {
			return r, true
		}
	}
	return RoleSeed{}, false
}
