package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/xemu-cartographer/xc-scraper/discovery"
	"github.com/xemu-cartographer/xc-scraper/hostrunner"
	"github.com/xemu-cartographer/xc-scraper/offsets"
	"github.com/xemu-cartographer/xc-scraper/roster"
	scrapermgr "github.com/xemu-cartographer/xc-scraper/runner"
	authzpb "github.com/xemu-cartographer/xemu-cartographer/internal/authz/pb"
	"github.com/xemu-cartographer/xemu-cartographer/internal/guards"
	scraperiface "github.com/xemu-cartographer/xemu-cartographer/internal/guards/interfaces/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/leaguescraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/hooks"
	"github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/migrateconf"
	"github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/oauth"
	"github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/resolvers"
	"github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/routes"
	"github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/routes/containers"
	playroutes "github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/routes/play"
	scraperroutes "github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/routes/scraper"
	"github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase/seed"
	"github.com/xemu-cartographer/xemu-cartographer/internal/podman"
	"github.com/xemu-cartographer/xemu-cartographer/internal/reaper"
	ws "github.com/xemu-cartographer/xemu-cartographer/internal/websocket"

	_ "github.com/xemu-cartographer/xc-scraper/halo2"  // self-registering Halo 2 GameReader (M20)
	_ "github.com/xemu-cartographer/xc-scraper/haloce" // self-registering Halo: CE GameReader
	discordbot "github.com/xemu-cartographer/xemu-cartographer/internal/disgo"
	"github.com/xemu-cartographer/xemu-cartographer/internal/disgo/commands"
	pb "github.com/xemu-cartographer/xemu-cartographer/internal/pocketbase"
	_ "github.com/xemu-cartographer/xemu-cartographer/internal/websocket/handlers" // self-registering WS handlers
	_ "github.com/xemu-cartographer/xemu-cartographer/internal/websocket/rooms"    // self-registering WS room types
	_ "github.com/xemu-cartographer/xemu-cartographer/migrations"                  // self-registering DB migrations (schema source of truth)
)

func main() {
	app := pocketbase.New()

	var bot *discordbot.Bot
	var hub *ws.Hub
	var watcherCancel context.CancelFunc
	var reaperCancel context.CancelFunc
	// feed is the R1 scraper feed (wire or in-process); scrMgr is its
	// embedded manager, nil in wire mode.
	var feed *scraperFeed
	var scrMgr *scrapermgr.Manager
	// podMgr is set below only when CONTAINERS_ENABLED; the host-runner URL
	// resolver reads it at call time (through a getter) so it can be wired before
	// podman is constructed. Atomic because the getters run on scraper /
	// request goroutines that may already be up when the store happens.
	var podMgr atomic.Pointer[podman.Manager]

	// Database migrations are the SOURCE OF TRUTH for schema (docs/MIGRATIONS.md).
	// Pending migrations in migrations/ are applied automatically on boot — BEFORE
	// OnServe — and tracked in the _migrations table, so routes/hooks/the scraper
	// can assume the schema exists. Automigrate writes a migration file whenever
	// the schema changes in the admin UI, but ONLY in dev builds (`-tags dev`);
	// beta/prod snapshots apply reviewed migrations and never author new ones.
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		Dir:          "migrations",
		Automigrate:  migrateconf.Automigrate,
		TemplateLang: migratecmd.TemplateLangGo,
	})

	// Register record lifecycle hooks (callback registration, fires later).
	hooks.RegisterAll(app)

	// OnServe: register routes (needs running DB / ServeEvent).
	//
	// NOTE: collections are created by MIGRATIONS (migrations/), applied on boot
	// BEFORE this hook — there is no schema-as-code step any more (the retired
	// internal/pocketbase/schema package). Anything here may assume the schema
	// exists. See docs/MIGRATIONS.md.
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		if err := oauth.RegisterAll(app); err != nil {
			return err
		}

		// Build the Services skeleton early so subsystems that need to broadcast
		// (the scraper manager) can hold a stable pointer to it. Per-system
		// fields (svc.WS, svc.Discord) are populated as those subsystems come up
		// later in this OnServe block — Go's pointer semantics mean the scraper
		// sees the live values without needing a SetServices callback.
		pbSvc := pb.NewService(app)
		svc := &guards.Services{
			App: app,
			PB:  pbSvc,
		}

		// Imported offset sets (Offsets page): ids the embedded registry doesn't
		// know resolve through the offset_sets collection — the stored offsetmap
		// JSON parses through the same path as an embedded file at bind time.
		// Wired before the scraper manager exists so no bind can race it.
		offsets.SetDynamicSource(func(id string) ([]byte, bool) {
			rec, err := app.FindFirstRecordByData("offset_sets", "set_id", id)
			if err != nil || rec == nil {
				return nil, false
			}
			fsys, err := app.NewFilesystem()
			if err != nil {
				return nil, false
			}
			defer fsys.Close()
			r, err := fsys.GetReader(rec.BaseFilesPath() + "/" + rec.GetString("file"))
			if err != nil {
				return nil, false
			}
			defer r.Close()
			raw, err := io.ReadAll(r)
			if err != nil {
				return nil, false
			}
			return raw, true
		})

		// WebSocket hub — constructed here (Run + /api/ws mount happen below,
		// after the scraper feed exists for the hello hook) so wire mode can
		// hand it to xcclient as its rebroadcast / eviction port.
		hub = ws.NewHub(app)

		// Scraper feed (DESIGN-STEP8 D-4, R1 dual mode): XC_SCRAPER_URL set ⇒
		// wire mode — the league consumes the xc-scraper daemon through
		// internal/xcclient + the leaguescraper Adapter and never builds a
		// runner, a discovery watcher or a host-runner registry; unset ⇒ the
		// in-process manager exactly as before (the league glue plugs the hub
		// in through the Emitter / Demand ports, the roster filter through
		// RosterFilter and the games persistence chain through OnGameEnd; the
		// WS adapters read svc.WS at call time, so broadcasts safely no-op
		// until svc.WS is populated below). Either way the adapter is what
		// every scraperiface.Service consumer sees (step 7 part 3c). The blank
		// import of xc-scraper/haloce above triggers haloce.init(), which
		// registers Halo: CE's title ID with scraper.Lookup so the embedded
		// runner.Start() can detect it.
		booted, err := bootScraperFeed(app, svc, hub, os.Getenv, podMgr.Load)
		if err != nil {
			return err
		}
		feed = booted
		scrMgr = feed.mgr // nil in wire mode
		scrAdapter := feed.adapter
		svc.Scraper = scrAdapter
		scraperroutes.SetManager(scrAdapter)

		// Containers config is read here (pure env, no side effects) because the
		// authz adapter below needs the provisioner's name prefix; the podman
		// manager itself is built further down under the same config.
		podmanCfg := podman.LoadFromEnv()

		// Authorization adapter (DESIGN-STEP6 §7.4 B-1): ONE process-wide
		// authz.Deps over the live app + scraper, published through
		// authzpb.SetDefault so hooks, guards and route groups — which all read
		// authzpb.Default() at request time — share it. Installed BEFORE the
		// seeder runs (the hooks that fire during seeding decide through it; a
		// nil default denies) and before any route is bound. The prefix closure
		// is left nil when the provisioner is off so OwnedBox answers "" (no
		// per-user box can exist), matching the play resolver's derivation.
		var prefixFn func() string
		if podmanCfg.Enabled {
			prefixFn = func() string { return podmanCfg.NamePrefix }
		}
		authzDeps := authzpb.NewDeps(app, scrAdapter, prefixFn)
		authzpb.SetDefault(authzDeps)
		svc.Authz = authzDeps
		// Legacy LAN_SAVES_TOKEN (PD-12): imported as the in-memory "legacy-env"
		// machine key carrying lan.saves.* + lan.sync.* so existing LAN stations
		// keep working; the boot report below nags to rotate it.
		authzpb.ImportLegacyEnv(authzDeps, os.Getenv)
		// XC_SCRAPER_WEBHOOK_TOKEN (step 8 §7.2): imported as the in-memory
		// "webhook-env" machine key carrying scraper.ingest so the xc-scraper
		// daemon's finished_game webhook (POST /api/xc/finished_game) works in
		// every tier without UI minting; the boot report nags to mint a real key.
		webhookImported := authzpb.ImportWebhookEnv(authzDeps, os.Getenv)

		if err := seed.Run(app); err != nil {
			return err
		}

		// Env-driven superuser bootstrap (all builds, incl. prod-style beta) —
		// creates a superuser from SEED_SUPERUSER_EMAIL/PASSWORD if set and none
		// with that email exists yet. No-op when unset.
		if err := seed.EnsureEnvSuperuser(app); err != nil {
			return err
		}

		// Register snapshot hooks AFTER seeding so the seeder's own writes don't
		// overwrite the snapshot mid-run.
		seed.RegisterContainerSnapshotHooks(app)

		// The dev seeder writes roles rows with app.Save (not authzpb.Grant), so
		// drop the adapter's roles cache before the first decision reads it; then
		// print the §8.2 boot report — after seeding so the admin count and the
		// anonymous-scopes row reflect what this boot actually ends up with.
		authzDeps.InvalidateRoles()
		authzpb.LogStartup(authzpb.Inspect(app, authzDeps), log.Printf)
		authzpb.LogWebhookEnv(webhookImported, log.Printf)
		log.Print(feed.line) // leaguescraper: mode=wire … | mode=in-process (§12)

		playroutes.SetScraper(scrAdapter)
		// Player-hosting (ADR-0003) + the play / diagnostics sources. hostReg
		// exists only in in-process mode; wire mode's Adapter proxies the same
		// surfaces to the daemon (hostrunner runs inside the daemon, D-9).
		var hostReg *hostrunner.Registry
		if feed.wire != nil {
			a := feed.wire.Adapter
			scraperroutes.SetHostControl(a)
			playroutes.SetHostControl(a)
			playroutes.SetMapSource(a)
			scraperroutes.SetMapSource(a)
			scraperroutes.SetReadoutSource(a)
			scraperroutes.SetHealthSource(a)
		} else {
			// The host-runner Registry owns the per-instance state-aware runners
			// and fans their observable stream to the admin WS room. It's wired
			// to both the admin arbitration endpoints (/api/admin/scraper/
			// {name}/host) and the player-scoped /api/play/* group. The Manager
			// attaches a runner + vncinput input pump per instance on Start when
			// HOSTRUNNER_ENABLED — resolving each container's websockify URL
			// through the podman manager (nil-safe: no URL → runner ticks +
			// emits state but presses nothing).
			hostReg = hostrunner.NewRegistry(newHostRunnerSink(svc))
			scraperroutes.SetHostControl(hostReg)
			playroutes.SetHostControl(hostReg)
			// The play map picker is sourced LIVE per instance from the scraper
			// (never a stock table) — the Manager satisfies playroutes.MapSource.
			playroutes.SetMapSource(scrMgr)
			// The admin diagnostics panel shows the same enumerated carousel, and
			// reads the LIVE per-tick readout (not the host runner's last event)
			// so it stays live on an observed-only box.
			scraperroutes.SetMapSource(scrMgr)
			scraperroutes.SetReadoutSource(scrMgr)
			// ...and the rolling observed-vs-expected engine tick rate, so "is
			// this host keeping up?" is answerable from the panel instead of a
			// manual capture.
			scraperroutes.SetHealthSource(scrMgr)
			scrMgr.SetHostRunner(
				hostReg,
				hostRunnerURLResolver(podMgr.Load),
				envBool("HOSTRUNNER_ENABLED", false),
			)
			// Host/client scoping (pod-hijack fix): AUTO-DRIVE only player-hosted
			// boxes — the ones /api/play/request provisions as "<prefix>play-<uid>"
			// (the same "play-" marker the reaper scopes idle-out to). Every other
			// box (admin/manual — e.g. a client pod created to JOIN a System Link
			// lobby) attaches observe-only and is never driven until an admin
			// promotes it via the host control endpoint. Marker is env-tunable
			// for non-standard deployments (wire mode pushes the same marker to
			// the daemon per instance, §10).
			driveMarker := envStr("HOSTRUNNER_DRIVE_MARKER", "play-")
			scrMgr.SetHostDrivePolicy(func(name string) bool {
				return strings.Contains(name, driveMarker)
			})
			// Host-side custom gametype variant enumeration (part C): resolve a
			// box's overlay qcow2 so the runner can read its saved variants off
			// disk (the SELECT GAMETYPE carousel keeps only rendered cards
			// resident). nil-safe — no podman manager (CONTAINERS_ENABLED off)
			// → built-in gametypes only.
			scrMgr.SetOverlayResolver(func(name string) (string, bool) {
				pm := podMgr.Load()
				if pm == nil {
					return "", false
				}
				return pm.OverlayPath(name)
			})

			// Capture-policy loader: read the persisted (instance, class) rows
			// now so runners started immediately after this (auto-start via the
			// discovery watcher, manual /api/admin/scraper/start) inherit the
			// current snapshot. The hook bind keeps them in sync as operators
			// edit policies through the PB dashboard. (Wire mode registers the
			// same hooks against its event writer inside leaguescraper.Boot.)
			//
			// pb: sink scheme must register BEFORE the initial reload — a
			// policy carrying "pb:game_events" loaded against an empty registry
			// would error with "unknown scheme" and silently drop captures.
			leaguescraper.RegisterPBSink(app)
			leaguescraper.RegisterCapturePolicyHooks(app, scrMgr)
			if err := leaguescraper.ReloadCapturePolicies(app, scrMgr); err != nil {
				log.Printf("scraper: initial capture-policy load: %v", err)
			}
		}

		// WebSocket hub — run and published on svc.WS BEFORE the discovery
		// watcher below starts. The watcher's goroutines (scrMgr.Start → runner
		// loop / aggregator → leaguescraper emitter + demand) read svc.WS at
		// call time; the `go w.Run(ctx)` statement is the happens-before edge
		// that makes this write visible to them without a lock. Wire mode's
		// room observer (demand-gated upstream joins, §8.3) must be installed
		// before Run.
		if feed.wire != nil {
			hub.SetRoomObserver(feed.wire.Demand.Observe)
		}
		go hub.Run()
		ws.SetInstance(hub)
		se.Router.GET("/api/ws", ws.NewHandler(hub, app, feed.hello))
		svc.WS = hub
		hub.SetServices(svc)

		// Containers (optional): start podman manager + socket watcher when
		// CONTAINERS_ENABLED=true (podmanCfg was loaded above, ahead of the authz
		// adapter). The route group registers itself as a no-op when Manager is
		// nil, so a fresh checkout boots cleanly.
		if podmanCfg.Enabled {
			containersStore := resolvers.NewContainersStore(app)
			mgr, err := podman.NewManager(podmanCfg, containersStore)
			if err != nil {
				return err
			}
			podMgr.Store(mgr) // host-runner URL resolver reads this to find websockify ports

			// Offset-set selection (offset versioning): map an instance to the
			// offset-set id its catalog row assigns. Under the managed ingest
			// model the attached disc is <isos-record-id>.iso, so the podman
			// GameISO basename IS the record id — no extra linkage. Empty (or
			// any lookup miss) means the detected game's baseline; boxes whose
			// GameISO is unknown (e.g. attached before a server restart) also
			// fall back to the baseline. Fail-soft by design. Wire mode pushes
			// the same mapping to the daemon per instance (§10): the pusher's
			// pod hooks PUT the instance config at Create and drop it at Remove.
			if feed.wire != nil {
				mgr.SetHooks(feed.wire.Pusher.PodHooks())
			} else {
				scrMgr.SetOffsetSetResolver(func(instance string) string {
					info, ok := mgr.Get(instance)
					if !ok || info.GameISO == "" {
						return ""
					}
					id := strings.TrimSuffix(filepath.Base(info.GameISO), ".iso")
					rec, err := app.FindRecordById("isos", id)
					if err != nil {
						return ""
					}
					return rec.GetString("offset_set")
				})
			}
			containers.SetManager(mgr)
			containers.SetServices(svc)
			// Player request-instance flow: provisions a fresh box booting the
			// chosen catalog ISO. Additive to the untouched admin screen/VNC path.
			playroutes.SetProvisioner(podmanProvisioner{m: mgr})

			// Idle-out reaper (optional, REAPER_ENABLED): reclaim player-hosted
			// boxes that nobody joins — an empty lobby with no live match and no
			// guest machine for the idle window is torn down (stop scraper +
			// podman remove). Scoped to the "play-" prefix so admin/manual boxes
			// are never auto-reaped. Reads activity from the scraper + host-runner;
			// removes through this podman manager. A disabled config makes Run a
			// no-op, so the wiring is unconditional and just doesn't spin a
			// goroutine. Wire mode reads phase / machines from the mirror and
			// the host-runner machine count through the daemon's /host route
			// (once per poll); the remover is podman.Remove alone — the daemon
			// detaches on its own when the QMP socket disappears.
			reap := reaper.New(
				reaperConfigFromEnv(),
				feed.reaperSource(hostReg),
				feed.reaperRemover(mgr),
				reaper.WithLogger(log.Printf),
			)
			if reap.Enabled() {
				// Surface the per-instance countdown on /api/play/current so the
				// host gets a heads-up before an idle box is reclaimed.
				playroutes.SetIdleReporter(reaperIdleReporter{r: reap})
				rctx, rcancel := context.WithCancel(context.Background())
				reaperCancel = rcancel
				go reap.Run(rctx)
				log.Printf("reaper: idle-out enabled (REAPER_* env controls timeout/prefix)")
			}

			if feed.wantsDiscovery(podmanCfg.SocketDir) {
				ctx, cancel := context.WithCancel(context.Background())
				watcherCancel = cancel

				// Per-name dedup of repeated identical auto-start errors. After
				// M5 stage 5a, scraper.Manager.Start no longer rejects unknown
				// titles — that path is handled inside the runner's Idle phase.
				// Start only errors here on QMP init failure (xemu container
				// still booting / socket not yet ready), which is also a state
				// that resolves on its own. Dedup keeps the log clean during
				// the boot retry window.
				var (
					lastErrMu sync.Mutex
					lastErr   = map[string]string{}
				)
				var w *discovery.Watcher
				w = discovery.NewWatcher(podmanCfg.SocketDir, 2*time.Second,
					func(name, sock string) {
						go func() {
							err := scrMgr.Start(name, sock)
							if err == nil {
								lastErrMu.Lock()
								delete(lastErr, name)
								lastErrMu.Unlock()
								return
							}
							msg := err.Error()
							lastErrMu.Lock()
							prev := lastErr[name]
							lastErr[name] = msg
							lastErrMu.Unlock()
							if prev != msg {
								log.Printf("discovery: auto-start scraper %s: %v", name, err)
							}
							// Drop from the watcher's known set so the next poll
							// retries — typical case is xemu still booting / on the
							// dashboard, which resolves once a game is loaded.
							w.Forget(name)
						}()
					},
					func(name string) {
						lastErrMu.Lock()
						delete(lastErr, name)
						lastErrMu.Unlock()
						if err := scrMgr.Stop(name); err != nil {
							log.Printf("discovery: auto-stop scraper %s: %v", name, err)
						}
					},
				)
				go w.Run(ctx)
			}
		}

		// Wire mode: dial the daemon now that the hub runs and the podman
		// manager (if any) exists — the first connect pushes the config
		// document (§10) and evicts host:* downstream for a clean replay.
		feed.start()

		se.Router.BindFunc(authzpb.RejectBannedAuth) // banned / soft-deleted JWT ⇒ guest on every route
		routes.RegisterAll(se)

		// Start Disgo bot (non-blocking)
		bot, err = discordbot.NewBot()
		if err != nil {
			log.Printf("Warning: Discord bot not started: %v", err)
		} else {
			if err := bot.OpenGateway(context.Background()); err != nil {
				log.Printf("Warning: Discord gateway failed: %v", err)
				bot = nil
			} else {
				discordbot.SetInstance(bot)
				svc.Discord = bot
				bot.SetServices(svc)
			}
		}

		hooks.SetServices(svc)
		commands.SetServices(svc)

		return se.Next()
	})

	// OnTerminate: cleanup.
	app.OnTerminate().BindFunc(func(te *core.TerminateEvent) error {
		if watcherCancel != nil {
			watcherCancel()
		}
		// Stop the reaper before the scrapers so no reap pass fires against a
		// manager that's tearing down.
		if reaperCancel != nil {
			reaperCancel()
		}

		// Stop scrapers BEFORE the hub so in-flight tick broadcasts don't try to
		// write to a closing channel. Manager.Stop blocks until each runner's
		// tick goroutine exits. After all runners are gone, Close() stops the
		// host:all aggregator goroutine so the process can exit cleanly. Wire
		// mode instead closes the upstream stream (the daemon keeps running —
		// nothing is stopped remotely) before the hub goes away.
		if feed != nil {
			feed.stop()
		}

		if hub != nil {
			hub.Stop()
		}

		if bot != nil {
			bot.Close(context.Background())
		}

		log.Println("Server shutting down...")
		return te.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// scraperFeed is what the OnServe block builds for the scraper feed in
// either R1 mode (DESIGN-STEP8 D-4). Exactly one of mgr / wire is set; the
// adapter is the scraperiface.Service every consumer sees and hello the
// per-principal handshake hook for /api/ws.
type scraperFeed struct {
	mode    leaguescraper.Mode
	mgr     *scrapermgr.Manager // in-process only
	wire    *leaguescraper.Wire // wire only
	adapter scraperiface.Service
	hello   ws.ConnectHook
	line    string // the §12 boot line
	// hostrunner is HOSTRUNNER_ENABLED: in wire mode the client joins
	// xc:hostrunner and the reaper polls the daemon's /host route.
	hostrunner bool
}

// feedEnv are the league-side wire-mode variables (§11); HOSTRUNNER_* are
// shared with the in-process path and read for the config push.
const (
	envScraperToken        = "XC_SCRAPER_TOKEN"
	envScraperControlToken = "XC_SCRAPER_CONTROL_TOKEN"
	envScraperStaleAfter   = "XC_SCRAPER_STALE_AFTER"
)

// bootScraperFeed picks the mode from env — XC_SCRAPER_URL set ⇒ wire (a
// malformed URL is a boot error, §11), unset ⇒ in-process — and builds the
// feed. hub is the league hub (constructed, not yet running); pods
// late-binds the podman manager for wire mode's config push. Nothing
// touches the network until start.
func bootScraperFeed(app core.App, svc *guards.Services, hub *ws.Hub, env func(string) string, pods func() *podman.Manager) (*scraperFeed, error) {
	rawURL := strings.TrimSpace(env(leaguescraper.EnvUpstreamURL))
	if rawURL == "" {
		mgr := scrapermgr.New(scrapermgr.Options{
			Emitter:   leaguescraper.NewEmitter(svc),
			Demand:    leaguescraper.NewDemand(svc),
			OnGameEnd: leaguescraper.GameEndHook(app),
			RosterFilter: func(inst string) roster.Config {
				return leaguescraper.LoadRosterConfig(app, inst)
			},
		})
		// The league adapter frames the manager's bare reply envelopes for the
		// WS rooms and filters the hello per principal.
		a := leaguescraper.NewWireAdapter(mgr)
		return &scraperFeed{
			mode:    leaguescraper.ModeInProcess,
			mgr:     mgr,
			adapter: a,
			hello:   a.SendHelloOn,
			line:    leaguescraper.BootLine(leaguescraper.ModeInProcess, "", "", ""),
		}, nil
	}

	token := env(envScraperToken)
	control := env(envScraperControlToken)
	var stale time.Duration
	if v := strings.TrimSpace(env(envScraperStaleAfter)); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("leaguescraper: %s must be a positive duration (got %q)", envScraperStaleAfter, v)
		}
		stale = d
	}
	hostRunner := parseBool(env("HOSTRUNNER_ENABLED"), false)
	w, err := leaguescraper.Boot(app, leaguescraper.BootConfig{
		URL:             rawURL,
		Token:           token,
		ControlToken:    control,
		StaleAfter:      stale,
		Hostrunner:      hostRunner,
		HostDriveMarker: firstNonEmpty(env("HOSTRUNNER_DRIVE_MARKER"), "play-"),
		Hub:             hub,
		Pods: func() leaguescraper.PodSource {
			if pods == nil {
				return nil
			}
			if m := pods(); m != nil {
				return m
			}
			return nil
		},
		Logf: log.Printf,
	})
	if err != nil {
		return nil, err
	}
	return &scraperFeed{
		mode:       leaguescraper.ModeWire,
		wire:       w,
		adapter:    w.Adapter,
		hello:      w.Adapter.SendHelloOn,
		line:       leaguescraper.BootLine(leaguescraper.ModeWire, rawURL, token, control),
		hostrunner: hostRunner,
	}, nil
}

// wantsDiscovery reports whether main should run the league-side socket
// watcher: in-process only, and only with a socket dir. In wire mode the
// daemon's --watch-dir attaches (§9); CONTAINERS_SOCKET_DIR stays for the
// podman bind mount.
func (f *scraperFeed) wantsDiscovery(socketDir string) bool {
	return f.wire == nil && f.mgr != nil && socketDir != ""
}

// start dials the daemon in wire mode; a no-op in-process (runners start
// through discovery / the admin routes).
func (f *scraperFeed) start() {
	if f.wire != nil {
		f.wire.Start()
	}
}

// stop tears the feed down at OnTerminate: wire mode closes the upstream
// stream + demand/pusher timers; in-process stops every runner, then the
// manager's aggregator.
func (f *scraperFeed) stop() {
	if f.wire != nil {
		f.wire.Close()
		return
	}
	if f.mgr == nil {
		return
	}
	for _, info := range f.mgr.List() {
		if err := f.mgr.Stop(info.Name); err != nil {
			log.Printf("scraper: stop %s on shutdown: %v", info.Name, err)
		}
	}
	f.mgr.Close()
}

// parseBool is envBool over an explicit value (tests hand in their own env).
func parseBool(v string, def bool) bool {
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func firstNonEmpty(v, def string) string {
	if v != "" {
		return v
	}
	return def
}
