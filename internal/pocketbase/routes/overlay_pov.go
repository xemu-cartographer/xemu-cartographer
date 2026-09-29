package routes

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/xemu-cartographer/xc-scraper/roster"
	sc "github.com/xemu-cartographer/xc-scraper/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz"
	"github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/leaguescraper"
	scraperroutes "github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/routes/scraper"
)

// Target an overlay purely by CONSOLE NAME (no instance / container id). Given
// a console name, this finds which running host box currently sees that
// console — either as its OWN console (xbox_name) or as a peer in its System
// Link lobby (game_data.machines) — and returns that host's live snapshot plus
// which machine matched. An OBS overlay polls this by console name and
// RE-RESOLVES every tick, so it survives the container being recreated (the
// console name is the stable constant; the instance id churns).
//
// Built on the M09 identity mechanic (the host already scrapes peer console
// names into game_data.machines — see xc-scraper/runner/membership.go).
//
// Authorization (design §7.1 R-14, PD-1 "console door"):
//
//   - GET /api/overlay/console/{name} — a caller presenting no credential is
//     the anonymous principal bound to the instance whose OWN console
//     nickname (xbox_name) is the path name, carrying the `anonymous` role's
//     scopes. A lobby-peer name still resolves an instance below but binds
//     nothing, so anonymous is refused there — the door only ever opens onto
//     the box whose console you named. Presented credentials (users JWT,
//     spectator / device / machine key) resolve as on any REST route and are
//     checked with overlay.read_state on the resolved instance. A bound
//     principal resolves the name within its own box first (see
//     resolveConsoleFor) so a shared lobby, where every host sees every
//     peer, still lands on the box the principal may read.
//   - GET /api/overlay/consoles — never anonymous: overlay.list_consoles
//     (seeded on admin + overlay_manager, mintable on machine keys).
func init() {
	register(registerOverlayConsole)
	register(registerOverlayConsoleList)
}

// consoleEntry is one selectable console for the Studio picker: its name, which
// host currently sees it, whether it's a local/host console, and its lobby
// machine index (-1 = the host's own console with no live lobby).
type consoleEntry struct {
	Console      string `json:"console"`
	Instance     string `json:"instance"`
	IsLocal      bool   `json:"is_local"`
	MachineIndex int    `json:"machine_index"`
}

// listConsoles aggregates every console name currently visible across all
// running hosts — each host's own console (xbox_name) plus every System Link
// lobby peer (game_data.machines) — deduped by name (a lobby-machine entry,
// which carries a machine index, wins over the bare xbox_name). This is the
// console index the overlays resolve against; Studio lists it so the operator
// targets by name.
func listConsoles(mgr scraperiface.Inspect) []consoleEntry {
	seen := map[string]consoleEntry{}
	order := []string{}
	add := func(e consoleEntry) {
		k := ovSanitize(e.Console)
		if k == "" {
			return
		}
		if existing, ok := seen[k]; ok {
			if existing.MachineIndex < 0 && e.MachineIndex >= 0 {
				seen[k] = e // prefer the lobby-machine entry (has an index)
			}
			return
		}
		seen[k] = e
		order = append(order, k)
	}
	for _, info := range mgr.List() {
		st, ok := mgr.Inspect(info.Name)
		if !ok {
			continue
		}
		if info.XboxName != "" {
			add(consoleEntry{Console: info.XboxName, Instance: info.Name, IsLocal: true, MachineIndex: -1})
		}
		if st.GameData != nil {
			for _, m := range st.GameData.Machines {
				add(consoleEntry{
					Console:      m.Name,
					Instance:     info.Name,
					IsLocal:      m.IsLocal != nil && *m.IsLocal,
					MachineIndex: m.Index,
				})
			}
		}
	}
	out := make([]consoleEntry, 0, len(order))
	for _, k := range order {
		out = append(out, seen[k])
	}
	return out
}

// requireOverlay gates a selectorless overlay route on a. pb.Default() is
// read per request rather than at registration (same shape as
// middleware.RequireAdmin) so the route works regardless of whether the
// adapter was installed before or after the router was built.
func requireOverlay(a authz.Action) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return pb.Require(pb.Default(), a, nil)(e)
	}
}

func registerOverlayConsoleList(se *core.ServeEvent) {
	// Every console name currently visible, for the Studio picker. Anonymous
	// callers get 401 from the middleware; a credential without
	// overlay.list_consoles gets 403.
	se.Router.GET("/api/overlay/consoles", handleOverlayConsoleList).
		BindFunc(requireOverlay(authz.ActionOverlayListConsoles))
}

func handleOverlayConsoleList(e *core.RequestEvent) error {
	mgr := scraperroutes.Manager
	if mgr == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{"error": "scraper not running"})
	}
	return e.JSON(http.StatusOK, map[string]any{"consoles": listConsoles(mgr)})
}

func ovSanitize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// findMachineByName returns the index of the lobby machine whose name matches
// want (case-insensitive) — this is the console's per-console filter target.
func findMachineByName(machines []sc.GameMachine, want string) (idx int, name string, ok bool) {
	w := ovSanitize(want)
	for _, m := range machines {
		if ovSanitize(m.Name) == w {
			return m.Index, m.Name, true
		}
	}
	return 0, "", false
}

// resolveConsole locates the instance currently hosting a console name. A live
// lobby-machine match wins (it yields a machine filter); otherwise an instance
// whose own xbox_name matches is the fallback (machine_index -1 = the whole
// instance / its own local players).
func resolveConsole(mgr scraperiface.Inspect, console string) (instance string, machineIndex int, machineName string, st scraperiface.InspectState, ok bool) {
	want := ovSanitize(console)
	var fbInstance, fbName string
	var fbState scraperiface.InspectState
	fbFound := false
	for _, info := range mgr.List() {
		state, present := mgr.Inspect(info.Name)
		if !present {
			continue
		}
		if state.GameData != nil {
			if i, n, found := findMachineByName(state.GameData.Machines, want); found {
				return info.Name, i, n, state, true
			}
		}
		if !fbFound && ovSanitize(info.XboxName) == want {
			fbInstance, fbName, fbState, fbFound = info.Name, info.XboxName, state, true
		}
	}
	if fbFound {
		return fbInstance, -1, fbName, fbState, true
	}
	return "", 0, "", scraperiface.InspectState{}, false
}

// resolveConsoleIn is resolveConsole restricted to one instance: a lobby-
// machine match yields the machine filter, else the instance's own xbox_name
// matching yields -1 (the whole instance). Not found ⇒ ok=false.
func resolveConsoleIn(mgr scraperiface.Inspect, instance, console string) (machineIndex int, machineName string, st scraperiface.InspectState, ok bool) {
	want := ovSanitize(console)
	for _, info := range mgr.List() {
		if info.Name != instance {
			continue
		}
		state, present := mgr.Inspect(info.Name)
		if !present {
			break
		}
		if state.GameData != nil {
			if i, n, found := findMachineByName(state.GameData.Machines, want); found {
				return i, n, state, true
			}
		}
		if ovSanitize(info.XboxName) == want {
			return -1, info.XboxName, state, true
		}
		break
	}
	return 0, "", scraperiface.InspectState{}, false
}

// resolveConsoleFor resolves console for principal p. A principal bound to an
// instance (the anonymous door, a spectator / device key) is resolved within
// that instance first: in a shared System Link lobby every host scrapes every
// peer, so the global scan would pick whichever host mgr.List() names first
// even when the name is another box's OWN console — the box the door binds
// to — and P:bound would then refuse a legitimate own-console overlay. When
// the bound box doesn't see the name (or p is unbound) the global scan
// applies and authz.Can decides on whatever it returns; no access is granted
// here, only which instance the check runs against.
func resolveConsoleFor(mgr scraperiface.Inspect, p authz.Principal, console string) (instance string, machineIndex int, machineName string, st scraperiface.InspectState, ok bool) {
	if bound := p.BoundInstance(); bound != "" {
		if i, n, state, found := resolveConsoleIn(mgr, bound, console); found {
			return bound, i, n, state, true
		}
	}
	return resolveConsole(mgr, console)
}

// overlayConsolePrincipal is the caller of GET /api/overlay/console/{name}: the
// resolved REST principal when a credential was presented (pb.Get — a users
// JWT or an opaque key; unresolvable ⇒ Nobody), else the anonymous door
// principal — bound via d.InstanceByConsole, which matches the instance's
// own xbox_name ONLY (never a lobby-peer or player name), and carrying the
// anonymous role's scopes (empty when the row is missing: door closed).
func overlayConsolePrincipal(d *pb.PBDeps, e *core.RequestEvent, name string) authz.Principal {
	p := pb.Get(e)
	if p.Kind == authz.KindAnonymous {
		p = authz.Anonymous(d.InstanceByConsole(name), d.AnonymousScopes())
	}
	return p
}

// v2Roster reshapes the reader GamePlayer roster into the v2 GameRosterPlayer
// JSON the overlay client already parses (field names match; this is a pass-
// through of the overlay-relevant subset).
func v2Roster(players []sc.GamePlayer, accum map[int]sc.PlayerAccum) []map[string]any {
	out := make([]map[string]any, 0, len(players))
	for _, p := range players {
		acc := accum[p.Index]
		out = append(out, map[string]any{
			"index": p.Index, "name": p.Name, "team": p.Team, "armor_color": p.ArmorColor,
			"score": p.Score, "kills": p.Kills, "deaths": p.Deaths, "assists": p.Assists,
			"team_kills": p.TeamKills, "suicides": p.Suicides,
			"kill_streak": p.KillStreak, "shots_fired": p.ShotsFired, "shots_hit": p.ShotsHit,
			"is_local": p.IsLocal, "local_index": p.LocalIndex, "machine_index": p.MachineIndex,
			// Accumulated match stats (same names as the WS game class).
			"acc_shots_fired": acc.ShotsFired, "acc_grenade_throws": acc.GrenadeThrows,
			"acc_melees": acc.Melees, "acc_damage_dealt": acc.DamageDealt,
			"acc_damage_received": acc.DamageReceived, "acc_camo_pickups": acc.CamoPickups,
			"acc_overshield_pickups": acc.OvershieldPickups, "best_kill_streak": acc.BestKillStreak,
		})
	}
	return out
}

// v2Tick reshapes the reader tick roster into the slim per-index live-state the
// overlay client reads (alive / health / shields / camo / respawn).
func v2Tick(t *sc.TickPayload) []map[string]any {
	if t == nil {
		return nil
	}
	out := make([]map[string]any, 0, len(t.Players))
	for _, p := range t.Players {
		out = append(out, map[string]any{
			"index": p.Index, "alive": p.Alive, "health": p.Health, "shields": p.Shields,
			"has_camo": p.HasCamo, "respawn_in_ticks": p.RespawnInTicks,
		})
	}
	return out
}

// activeFromAccum projects an accumulator snapshot into the filter's
// index→latched-Active map (mirror of the manager's activeLocals — kept local
// to avoid exporting a one-liner across the scraperiface boundary).
func activeFromAccum(snap map[int]sc.PlayerAccum) map[int]bool {
	if len(snap) == 0 {
		return nil
	}
	out := make(map[int]bool, len(snap))
	for idx, st := range snap {
		if st.Active {
			out[idx] = true
		}
	}
	return out
}

func registerOverlayConsole(se *core.ServeEvent) {
	se.Router.GET("/api/overlay/console/{name}", handleOverlayConsole)
}

func handleOverlayConsole(e *core.RequestEvent) error {
	mgr := scraperroutes.Manager
	if mgr == nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{"error": "scraper not running"})
	}
	// Upstream-down is not console-absent. In wire mode every Inspect fails
	// fast with xcclient.ErrUpstreamDown while the daemon stream is away, and
	// the bool-returning Inspect port cannot carry that error, so the resolver
	// below would answer "not found" for a console that is merely
	// unobservable — telling the caster "start a game" when the truth is
	// "the scraper is down". Gate on the same check the scraper routes use
	// (always false in-process) and say so: 503 {"error":"upstream"}.
	if scraperroutes.UpstreamDown(mgr) {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{"error": "upstream"})
	}
	name := e.Request.PathValue("name")
	if strings.TrimSpace(name) == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "console name required"})
	}
	d := pb.Default()
	p := overlayConsolePrincipal(d, e, name)
	instance, machineIndex, machineName, st, ok := resolveConsoleFor(mgr, p, name)
	if !ok {
		return e.JSON(http.StatusNotFound, map[string]any{"error": "console not found in any live lobby", "console": name})
	}
	// overlay.read_state on the instance the name resolved to. For the
	// anonymous door this is P:bound — the bound instance (xbox_name match)
	// must be the resolved one, so a peer name (or a name whose instance
	// isn't the one hosting the lobby) is refused. A nil adapter denies.
	if !authz.Can(d, p, authz.ActionOverlayReadState, authz.Instance(instance)) {
		return e.JSON(http.StatusForbidden, map[string]any{"error": "forbidden", "console": name})
	}
	gd := st.GameData
	// Snapshot roster is filtered server-side with the SAME unified rule as
	// the scraper's game_filtered broadcast: local seats hidden until the
	// accumulator latches them Active; is_neutral_host = hard override;
	// dummy_gamertags allowlist. This snapshot only serves the HTTP-poll
	// fallback; the WS-push path gets the game_filtered class.
	cfg := leaguescraper.LoadRosterConfig(e.App, instance)
	cfg.HideInactiveLocals = true
	cfg.ActiveLocals = activeFromAccum(st.PlayerAccum)
	// engine_tick (0x0C) is the MATCH CLOCK — starts ~0 at match start and
	// counts up at 30Hz (live-verified 2026-08-08); the scorebug renders it.
	// game_elapsed_ticks (0x10) is a DEAD VALUE (stuck at ~1), kept on the
	// wire only for payload-shape compatibility.
	game := map[string]any{"phase": st.Phase, "engine_tick": st.Tick}
	scenario := map[string]any{}
	if gd != nil {
		game["game_elapsed_ticks"] = gd.ElapsedTicks
		game["config"] = map[string]any{
			"gametype": gd.Gametype, "is_team_game": gd.IsTeamGame, "score_limit": gd.ScoreLimit,
		}
		game["team_scores"] = gd.TeamScores
		game["players"] = v2Roster(roster.FilterRoster(gd.Players, cfg), st.PlayerAccum)
		game["machines"] = gd.Machines
		scenario["map"] = gd.Map
	}
	// The client uses this purely to resolve console→instance, then opens a
	// WS with ?console=NAME (the same anonymous door, resolved by ResolveWS)
	// and subscribes to host:<instance>: game_filtered/tick/scenario.
	// Filtering is server-side (game_filtered class).
	return e.JSON(http.StatusOK, map[string]any{
		"console":       name,
		"instance":      instance,
		"machine_index": machineIndex,
		"machine_name":  machineName,
		"game":          game,
		"tick":          map[string]any{"players": v2Tick(st.LatestTick)},
		"scenario":      scenario,
	})
}
