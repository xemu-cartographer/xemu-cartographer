// Package lansync exposes /api/lan/sync/* — the LAN box-provisioning contract
// the norcal-og-xbox client consumes to push player profiles + games + apps onto
// LAN Xboxes from cartographer.
//
// It is the SERVER half of a client↔server system (SCAFFOLD — heavy resolution
// is stubbed with TODOs):
//
//   - GET /api/lan/sync/manifest?preset=<id|active>
//     resolves the active (or a named) sync_preset into the concrete set of
//     profiles + games + apps, in priority order, with per-category
//     conflict/prune flags + ready-to-GET download URLs. THIS is the contract
//     the client consumes (shape reconciled against the client session).
//   - GET /api/lan/sync/games/{id}/download
//   - GET /api/lan/sync/apps/{id}/download
//     station-scoped pulls of the derived EXTRACTED trees for a game (iso) / app.
//     (Profiles + gametypes already have /api/lan/saves — this group adds only
//     games + apps.)
//
// Access mirrors /api/lan/saves: the group is bound to pb.AuthorizeLAN, so a
// caller presents a machine key (X-LAN-Token / ?token= / Authorization:
// Bearer / X-Api-Key), the raw legacy secret, or a PB session and each route
// is checked against its lan.sync.* verb (syncVerb). There is no open mode —
// an anonymous station is refused with 401.
package lansync

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/lansync"
)

// groupPrefix is the mount point of the LAN sync group.
const groupPrefix = "/api/lan/sync"

// Group is the router group for /api/lan/sync. Access is governed by
// pb.AuthorizeLAN (machine key or PB session, checked per verb), NOT
// RequireAdmin, because the on-Xbox client cannot present a browser JWT.
var Group *router.RouterGroup[*core.RequestEvent]

// cfg holds the resolved LAN-sync paths + client-facing dir names (SPEC dest_dir
// roots, FATX cluster). Loaded once from env in RegisterAll.
var cfg lansync.Config

var registry []func()

func register(fn func()) { registry = append(registry, fn) }

// RegisterAll creates the group + registers all handlers.
func RegisterAll(se *core.ServeEvent) {
	cfg = lansync.Load()
	Group = se.Router.Group(groupPrefix)
	Group.BindFunc(authorizeLAN)
	for _, fn := range registry {
		fn()
	}
}

// authorizeLAN is the group middleware: pb.AuthorizeLAN over the deps
// installed at boot (pb.Default — nil until then, which denies every verb).
func authorizeLAN(e *core.RequestEvent) error {
	return pb.AuthorizeLAN(pb.Default(), syncVerb)(e)
}

// syncActionUnmapped is the verb a path outside the table maps to. It is not
// part of the action vocabulary, so no rule grants it — a route added to this
// group without an entry in syncVerbFor is denied rather than open.
const syncActionUnmapped authz.Action = "lan.sync.unmapped"

// syncVerb names the action + resource pb.AuthorizeLAN checks for a request,
// keyed on the path relative to the group.
func syncVerb(e *core.RequestEvent) (authz.Action, authz.Resource) {
	return syncVerbFor(strings.TrimPrefix(e.Request.URL.Path, groupPrefix))
}

// syncVerbFor is the pure group-relative path → verb map (unit-tested):
//
//	/manifest        → lan.sync.manifest       (global)
//	/dl/game/{id}    → lan.sync.download_game  (iso {id})
//	/dl/app/{id}     → lan.sync.download_app   (global)
//
// Anything else maps to syncActionUnmapped.
func syncVerbFor(path string) (authz.Action, authz.Resource) {
	path = "/" + strings.Trim(path, "/")
	switch {
	case path == "/manifest":
		return authz.ActionLANSyncManifest, authz.Global()
	case strings.HasPrefix(path, "/dl/game/"):
		id := strings.TrimPrefix(path, "/dl/game/")
		if id == "" || strings.Contains(id, "/") {
			return syncActionUnmapped, authz.Global()
		}
		return authz.ActionLANSyncDLGame, authz.ISO(id)
	case strings.HasPrefix(path, "/dl/app/"):
		id := strings.TrimPrefix(path, "/dl/app/")
		if id == "" || strings.Contains(id, "/") {
			return syncActionUnmapped, authz.Global()
		}
		return authz.ActionLANSyncDLApp, authz.Global()
	}
	return syncActionUnmapped, authz.Global()
}
