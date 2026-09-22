// Package isos exposes /api/admin/isos/* — the ISO/game-catalog management
// surface (inbox scan + ingest, metadata/server_iso/offset_set edits, delete).
//
// Access is gated by the `library.manage` scope (design §7.1 R-3): the seeded
// organizer and admin roles both carry it, so the population matches the
// organizer-gated /organizer/games page and the isos collection's own PB
// rules (organizer-or-admin create/delete). The `role` / `allow_on_xbox`
// policy fields on PATCH additionally require `iso.set_policy` (also seeded on
// both roles — remove it from a row to lock policy edits to admins). The route
// path keeps its historical /api/admin/ prefix — renaming it would churn every
// client for no behavioral gain. The player-scoped picker lives in routes/play
// (GET /api/play/isos + POST /api/play/request); the admin screen + VNC path
// (routes/containers) is untouched.
//
// Under the ingest model the catalog is not a scan of arbitrary files: each
// row OWNS a managed <record-id>.iso produced by the inbox→ingest pipeline
// (internal/isoingest), so no podman manager is needed here at all.
package isos

import (
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
)

// Group is the router group for /api/admin/isos endpoints.
// All routes inherit RequireAuth + the library.manage gate below.
var Group *router.RouterGroup[*core.RequestEvent]

var registry []func()

func register(fn func()) { registry = append(registry, fn) }

// requireLibraryManage admits superusers and any principal whose scopes
// cover `library.manage` on the global resource (seeded on admin and
// organizer). The deps are read at request time through pb.Default() and a
// nil default fails closed.
func requireLibraryManage() func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return pb.Require(pb.Default(), authz.ActionLibraryManage, nil)(e)
	}
}

// RegisterAll creates the isos group and registers all handlers. Always active —
// the catalog + ingest pipeline need only PocketBase and the configured dirs.
func RegisterAll(se *core.ServeEvent) {
	Group = se.Router.Group("/api/admin/isos")
	Group.Bind(apis.RequireAuth())
	Group.BindFunc(requireLibraryManage())

	for _, fn := range registry {
		fn()
	}
}
