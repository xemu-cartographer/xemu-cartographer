// Package play exposes the player-scoped /api/play/* endpoints the play tab and
// the Pi controller drive. Unlike /api/admin/containers/* and
// /api/admin/scraper/* (admin-only), these are RequireAuth-but-not-admin: each
// request resolves the CALLER's own container server-side from their gamertag
// against the live container rosters (the M09 scoped pattern —
// gamertags.SanitizedForUser + scraperiface.MatchContainer(Membership())), so a
// player can only see and drive the box their gamertag is currently in. Admins
// may target any container with ?container=<name>.
//
// The admin screen + VNC keyboard path (routes/containers) is untouched; this is
// a parallel, narrower surface onto the same host-runner arbitration the admin
// endpoints use, so an admin take-over (authority=admin) still natively suspends
// whatever a player set here.
package play

import (
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"github.com/xemu-cartographer/xc-scraper/hostrunner"
	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
)

// PlayControl is the subset of *hostrunner.Registry the play endpoints call.
// Kept as an interface so this package has no compile-time dependency on the
// runner wiring — SetHostControl injects the live registry from main.go.
type PlayControl interface {
	Status(instance string) hostrunner.Status
	SetAuthority(instance string, auth hostrunner.Authority) bool
	SetReady(instance string, ready bool) bool
	// SetSelection records the pick with the D-pad Steps the runner navigates to
	// each chosen card (computed from the live carousel index).
	SetSelection(instance, mapName string, mapSteps int, gametypeName string, gametypeSteps int) bool
	ClearSelection(instance string) bool
}

// MapSource is the LIVE per-instance map/gametype list (refinement 2). The
// player API sources the picker from here — the actual game/disc on THAT box —
// never a fixed/stock table. Satisfied structurally by the scraper *Manager.
type MapSource interface {
	AvailableMaps(instance string) scraperiface.MapList
}

// HostProvisioner spins up a fresh xemu instance booting a chosen ISO. It's the
// wire behind POST /api/play/request: a player picks a game from the library and
// the server provisions + starts a box booting straight into it (ADR-0004).
// Kept an interface so this package stays free of a compile-time podman
// dependency (matching Scraper / PlayControl / MapSource); main.go injects an
// adapter over the podman Manager.
type HostProvisioner interface {
	// Provision creates AND starts a container named `name` (the slugified
	// podman name), attaching the shared-library ISO `filename` as its DVD so it
	// boots into that game. `display` is the canonical PRETTY name written as the
	// Xbox console nickname (empty for auto per-player boxes → the name is used).
	Provision(name, filename, display string) (ProvisionResult, error)
	// Exists reports whether a container of this name is already provisioned, so
	// request-instance can fail closed (one hosted box per player) instead of
	// colliding.
	Exists(name string) bool
	// NamePrefix is the deployment's container-name namespace (empty in prod).
	// request-instance prepends it so a beta sharing the host podman daemon
	// gives players "beta-play-<uid>" boxes that can't collide with prod's.
	NamePrefix() string
}

// ProvisionResult is the podman-free slice of the freshly-provisioned container
// the request endpoint echoes back to the player.
type ProvisionResult struct {
	Name    string `json:"name"`
	Index   int    `json:"index"`
	GameISO string `json:"game_iso"`
}

// IdleReporter surfaces the idle-out reaper's countdown for the caller's own
// instance so GET /api/play/current can warn the host before an unused box is
// reclaimed. Kept an interface (returning primitives, not a reaper type) so
// this package stays free of a compile-time dependency on internal/reaper —
// main.go injects an adapter over the live reaper. nil disables the heads-up
// (the reap field is simply omitted).
type IdleReporter interface {
	// IdleInfo reports whether the named instance is currently accruing idle
	// time and, if so, when it will be reaped and whether it's inside the
	// pre-reap warning window. ok=false when the box is active / unknown /
	// reaping disabled.
	IdleInfo(instance string) (idleSince, reapAt time.Time, warning bool, ok bool)
}

// Group is the router group for /api/play endpoints (RequireAuth, no admin).
var Group *router.RouterGroup[*core.RequestEvent]

// Scraper resolves the caller's container from live rosters (Membership). nil
// disables the package (RegisterAll becomes a no-op).
var Scraper scraperiface.Service

// HostRunners is the injected host-runner control surface. nil → endpoints that
// need it return 503 (server still bootable without the subsystem).
var HostRunners PlayControl

// Maps is the injected live map source. nil → the picker reports available:false.
var Maps MapSource

// Provisioner is the injected instance-provisioning surface. nil → request-
// instance returns 503 (server stays bootable without podman).
var Provisioner HostProvisioner

// Idle is the injected reaper heads-up source. nil → /current omits the reap
// countdown (reaping disabled or not wired).
var Idle IdleReporter

// SetScraper wires the scraper manager (for gamertag→container resolution).
func SetScraper(s scraperiface.Service) { Scraper = s }

// SetHostControl wires the host-runner registry. Call before RegisterAll.
func SetHostControl(c PlayControl) { HostRunners = c }

// SetMapSource wires the live per-instance map/gametype source. Call before
// RegisterAll.
func SetMapSource(m MapSource) { Maps = m }

// SetProvisioner wires the instance-provisioning surface (the podman-backed
// adapter). Call before RegisterAll. Safe to leave nil.
func SetProvisioner(p HostProvisioner) { Provisioner = p }

// SetIdleReporter wires the reaper heads-up source. Call before RegisterAll.
// Safe to leave nil (no heads-up).
func SetIdleReporter(i IdleReporter) { Idle = i }

var registry []func()

func register(fn func()) { registry = append(registry, fn) }

// RegisterAll creates the /api/play group and registers all handlers. No-op when
// the scraper manager is nil (keeps the server bootable without the subsystem),
// mirroring routes/scraper.
func RegisterAll(se *core.ServeEvent) {
	if Scraper == nil {
		return
	}
	Group = se.Router.Group("/api/play")
	Group.Bind(apis.RequireAuth())
	for _, fn := range registry {
		fn()
	}
}
