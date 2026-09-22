package scraper

import (
	"errors"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xc-scraper/hosthealth"
	"github.com/xemu-cartographer/xc-scraper/hostrunner"
	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/xcclient"
)

// MapSource supplies the LIVE-enumerated map/gametype carousel for a box (the same
// list the /play picker uses). Injected from cmd/server/main.go (the scraper
// manager). An interface so this package keeps no compile-time dependency on the
// manager; nil until wired (the endpoint then omits the enumerated lists).
type MapSource interface {
	AvailableMaps(name string) scraperiface.MapList
}

var Maps MapSource

// SetMapSource wires the live map source. Call before RegisterAll.
func SetMapSource(m MapSource) { Maps = m }

// ReadoutSource supplies the instance's CURRENT per-tick scraper readout — the same
// one the host runner ticks on and navfp logs from. Injected from cmd/server/main.go
// (the scraper manager). Without it the panel would fall back to the host Registry's
// last event, which only exists while a runner is attached and ticking — that is what
// froze the panel at tick 0 on an observed-only box.
type ReadoutSource interface {
	Readout(name string) (hostrunner.ScraperReadout, bool)
}

var Readouts ReadoutSource

// SetReadoutSource wires the live per-tick readout source. Call before RegisterAll.
func SetReadoutSource(r ReadoutSource) { Readouts = r }

// HealthSource supplies the instance's rolling observed-vs-expected engine tick
// rate. Injected from cmd/server/main.go (the scraper manager); an interface so this
// package keeps no compile-time dependency on the manager. Nil until wired — the
// panel then simply has no host-health rows rather than failing the request.
type HealthSource interface {
	HostHealth(name string) (hosthealth.Health, bool)
}

var Health HealthSource

// SetHealthSource wires the host-health source. Call before RegisterAll.
func SetHealthSource(h HealthSource) { Health = h }

// diagnosticsResponse is the admin diagnostics-panel payload: the live per-tick
// scraper reads plus the enumerated map/gametype NAMES and the HIGHLIGHTED (live
// carousel-selection) names resolved from the cursor index — so the panel can show
// the three DISTINCT parameters Stewart called out: what's HIGHLIGHTED (the live
// selection being driven), what's PICKED (the player's target, Diagnostics.Selected*),
// and what's LOADED (Diagnostics.Map/Gametype — read-only, updated only when A commits).
type diagnosticsResponse struct {
	hostrunner.Diagnostics
	HighlightedMap      string   `json:"highlighted_map"`      // enumerated name at the live map cursor index
	HighlightedGametype string   `json:"highlighted_gametype"` // enumerated name at the live gametype cursor index
	EnumeratedMaps      []string `json:"enumerated_maps"`
	EnumeratedGametypes []string `json:"enumerated_gametypes"`

	// HostHealth answers "is this box sustaining its engine tick rate?" — the
	// question the panel could not previously answer, since Tick is a raw
	// counter with nothing comparing it to wall clock. Null when the source
	// isn't wired or the instance has no runner. HostHealthAgeMs mirrors the
	// ReadoutAgeMs idiom: the reading is a snapshot, and a wedged runner stops
	// refreshing it while its last-known values keep looking healthy.
	HostHealth      *hosthealth.Health `json:"host_health"`
	HostHealthAgeMs int64              `json:"host_health_age_ms"`
}

func optionNames(opts []scraperiface.MapOption) []string {
	out := make([]string, len(opts))
	for i, o := range opts {
		out[i] = o.Name
	}
	return out
}

// nameAtCursor resolves the WIDGET-space carousel index to an enumerated name.
// Maps have no prefix (widget index == enumeration index). Gametypes PREPEND the
// user's custom variants ahead of the built-ins, so the enumeration (built-in) list
// starts at prefix = liveCount − len(names); a cursor sitting in that prefix region
// is a custom variant not in this list. "" when unresolvable.
func nameAtCursor(names []string, cursorIndex, cursorCount int) string {
	prefix := cursorCount - len(names)
	if prefix < 0 {
		prefix = 0
	}
	if ai := cursorIndex - prefix; ai >= 0 && ai < len(names) {
		return names[ai]
	}
	if cursorIndex >= 0 && cursorIndex < prefix {
		return "(custom variant)"
	}
	return ""
}

func init() {
	register(func() {
		// GET /api/admin/scraper/{name}/diagnostics — the LIVE scraper-read snapshot
		// (dela path, resolved MenuItem + screen classification, map/gametype cursors,
		// game_connection, pregame sentinel) + enumerated + selected map/gametype
		// names. Admin-gated (the group binds RequireAuth + RequireAdmin). Feeds the
		// admin screen diagnostics panel so an operator watches the box AND its live
		// reads side-by-side, reporting a dela/menu_item fingerprint for any screen
		// without grepping beta.log.
		Group.GET("/{name}/diagnostics", func(e *core.RequestEvent) error {
			if HostRunners == nil {
				return e.JSON(http.StatusServiceUnavailable, map[string]string{"error": "host-runner subsystem not enabled"})
			}
			name := e.Request.PathValue("name")
			if name == "" {
				return e.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
			}
			if ctl := Wire(HostRunners); ctl != nil {
				return wireDiagnostics(e, ctl, name)
			}
			resp := diagnosticsResponse{Diagnostics: HostRunners.Diagnostics(name)}
			// Overlay the CURRENT tick's reads so the panel is live even when no host
			// runner is attached (registry events only flow while a runner ticks).
			if Readouts != nil {
				if ro, ok := Readouts.Readout(name); ok {
					resp.Diagnostics.ApplyReadout(ro)
					resp.Present = true
				}
			}
			if Health != nil {
				if hh, ok := Health.HostHealth(name); ok {
					resp.HostHealth = &hh
					resp.HostHealthAgeMs = hh.Age(time.Now()).Milliseconds()
				}
			}
			if Maps != nil {
				ml := Maps.AvailableMaps(name)
				resp.EnumeratedMaps = optionNames(ml.Maps)
				resp.EnumeratedGametypes = optionNames(ml.Gametypes)
				// The HIGHLIGHTED (live carousel-selection) name — distinct from the
				// LOADED map (resp.Map) and the player's PICK (resp.SelectedMap).
				resp.HighlightedMap = nameAtCursor(resp.EnumeratedMaps, resp.MapCursor.Index, resp.MapCursor.Count)
				resp.HighlightedGametype = nameAtCursor(resp.EnumeratedGametypes, resp.GametypeCursor.Index, resp.GametypeCursor.Count)
			}
			return e.JSON(http.StatusOK, resp)
		})
	})
}

// wireDiagnostics is the wire-mode branch: the daemon composes the same
// snapshot server-side (GET /api/instances/{n}/diagnostics — registry
// snapshot → live readout overlay → health → map list, §6.1), so one
// proxied call replaces the four source reads above. The JSON shape stays
// diagnosticsResponse. 503 while the daemon is away, 404 when nothing is
// attached under name (the daemon's not_found), 502 for any other failure.
func wireDiagnostics(e *core.RequestEvent, ctl *xcclient.Ctl, name string) error {
	d, err := ctl.Diagnostics(e.Request.Context(), name)
	if err != nil {
		if IsUpstreamDown(err) {
			return Unavailable(e)
		}
		if errors.Is(err, &xcclient.Error{Status: http.StatusNotFound}) {
			return e.JSON(http.StatusNotFound, map[string]string{"error": "no runner attached for " + name})
		}
		return e.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	resp := diagnosticsResponse{Diagnostics: d.HostRunner}
	if resp.Instance == "" {
		resp.Instance = name
	}
	// The daemon already applied the readout overlay to host_runner; Present
	// travels alongside it.
	resp.Present = resp.Present || d.Present
	if d.Health != nil {
		hh := *d.Health
		resp.HostHealth = &hh
		resp.HostHealthAgeMs = d.HealthAgeMs
	}
	resp.EnumeratedMaps = optionNames(d.Maps.Maps)
	resp.EnumeratedGametypes = optionNames(d.Maps.Gametypes)
	resp.HighlightedMap = nameAtCursor(resp.EnumeratedMaps, resp.MapCursor.Index, resp.MapCursor.Count)
	resp.HighlightedGametype = nameAtCursor(resp.EnumeratedGametypes, resp.GametypeCursor.Index, resp.GametypeCursor.Count)
	return e.JSON(http.StatusOK, resp)
}
