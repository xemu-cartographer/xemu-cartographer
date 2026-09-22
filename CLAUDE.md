# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

> See also: [README.md](README.md) for project overview, tech stack, architecture diagram, and quick-start guide.

## Reference docs

Before writing or reviewing code that touches a third-party library where the API may have drifted from your training data, consult up-to-date docs rather than guessing.

- **Skeleton UI v5** — [sveltekit/docs/skeleton-llms.txt](sveltekit/docs/skeleton-llms.txt) is a table of contents of Skeleton's official docs (components, theming, Tailwind v4 integration). Read it first to locate the right page, then WebFetch the specific page under `https://www.skeleton.dev/` (e.g. `https://www.skeleton.dev/docs/svelte/framework-components/app-bar.md`, `https://www.skeleton.dev/docs/svelte/tailwind-components/buttons`). Always use the **Svelte** section, not React. Note the TOC file predates v5 and still lists the v2/v3 migration pages; the v4→v5 guide is at `https://www.skeleton.dev/docs/svelte/get-started/migrate-from-v4.md`.
- **SvelteKit, PocketBase JS SDK, Disgo, Tailwind v4** — WebFetch the official docs site (`kit.svelte.dev`, `pocketbase.io/docs`, `disgo.dev`, `tailwindcss.com`) rather than inventing an API.

## Project meta-docs

[`docs/README.md`](docs/README.md) is the source of truth for the project's meta-docs convention. Follow it when adding planning, status, or decision documents.

- New milestone → copy [`docs/milestones/_template.md`](docs/milestones/_template.md) to `docs/milestones/M??-kebab-name.md`, then add a row to `docs/milestones/README.md`.
- New decision → copy [`docs/decisions/_template.md`](docs/decisions/_template.md) to `docs/decisions/????-kebab-name.md`, then add a row to `docs/decisions/README.md`. ADRs are immutable once Accepted — supersede with a new ADR rather than editing.
- Update [`docs/STATUS.md`](docs/STATUS.md) whenever the "Now" set of work changes.
- Add user-visible changes to `CHANGELOG.md` under `[Unreleased]`; cut a versioned section when shipping (SemVer, Keep-a-Changelog format).
- Dates are always absolute (`YYYY-MM-DD`). Milestone `Log` sections and the ADR index are append-only.

## Development Commands

```sh

# Install task runner and hot reload (to /usr/local/bin so both `task dev` and `sudo task dev` work; see README Prerequisites for the rationale and opt-out)

sudo env GOBIN=/usr/local/bin go install github.com/go-task/task/v3/cmd/task@latest
sudo env GOBIN=/usr/local/bin go install github.com/air-verse/air@latest

# Run the three dev processes in parallel: the xc-scraper daemon (Air in
# ../xc-scraper, port 8992), the Go backend (Air, -tags dev) and the SvelteKit
# dev server. `task dev:scraper` alone runs just the daemon.

task dev

# Backend only (hot reload). A league binary BUILT by the local go1.27
# toolchain with its default experiments crashes at boot (jsonv2 makes
# PocketBase's Collection.UnmarshalJSON recurse: "fatal error: stack
# overflow" in migrations.init) — export GOEXPERIMENT=nodwarf5,nojsonv2 in
# the shell that runs Air / `task build` / `go build` for anything you will
# boot. Tests do not need it, and neither does the xc-scraper daemon (no
# PocketBase). Nothing in the repo sets it (2026-09-07 finding, D1).

task dev:backend

# Frontend only (run from sveltekit/)

task dev:frontend

# Build for production: bin/server (league) + bin/xc-scraper (the daemon, via
# build:scraper from ../xc-scraper with -X main.version=<git describe>;
# `bin/xc-scraper --version` prints it). The frontend step needs
# PUBLIC_PB_PORT in the environment (root .env or exported). If .task/ is
# root-owned from an earlier `sudo task dev`, run with TASK_TEMP_DIR=/tmp/xc-task.

task build
task build:scraper      # just the daemon

# Build and run container (the backend stage needs ../xc-scraper: task passes it
# as a named build context; XC_SCRAPER_DIR overrides the path. .containerignore
# trims the context — node_modules / pb_data / .env* never enter the image)

task container:build
task container:run

# Clean build artifacts

task clean

# Run server directly (no Task/Air)

go run ./cmd/server serve
./bin/server serve

# Backend tests (Go) — CI runs the same scope (go vet + go test ./cmd/... ./internal/... + build)

go test ./cmd/... ./internal/...         # what CI and `task test` run (skips the root-owned containers/ tree)
go test ./internal/leaguescraper/...     # single package
go vet ./...
go test -tags dev ./internal/pocketbase/seed/...   # the dev-only seeder (Air builds with -tags dev)

# The scraper runner + leaf packages + the wire contract live in the SIBLING
# module ../xc-scraper (github.com/xemu-cartographer/xc-scraper). go.mod has
# `replace github.com/xemu-cartographer/xc-scraper => ../xc-scraper`, so
# ../xc-scraper MUST exist next to this checkout or nothing here builds
# (CI checks both repos out side by side). Keep both green:

cd ../xc-scraper && go build ./... && go vet ./... && go test ./...

# Step 8 R1 (DESIGN-STEP8 D-4): the league can consume the xc-scraper DAEMON
# instead of the embedded runner. `task dev` already runs one (dev:scraper =
# `air -c .air.toml` in ../xc-scraper with XC_SCRAPER_LISTEN=127.0.0.1:8992,
# XC_SCRAPER_WATCH_DIR=<repo>/containers/xemu/qmp, XC_SCRAPER_GAME_WEBHOOK=
# http://127.0.0.1:$PUBLIC_PB_PORT/api/xc/finished_game, state under
# ../xc-scraper/tmp/xc-scraper-state; tokens/--hostrunner come from the
# XC_SCRAPER_* env twins in .env). Production: `task build` → bin/xc-scraper,
# systemd unit template + install steps in ../xc-scraper/deploy/README.md
# (ports 8990 prod / 8991 pre / 8992 dev). The league consumes it only when
# booted with XC_SCRAPER_URL=http://127.0.0.1:<port> (+ XC_SCRAPER_TOKEN,
# XC_SCRAPER_CONTROL_TOKEN, XC_SCRAPER_WEBHOOK_TOKEN, XC_SCRAPER_STALE_AFTER —
# see .env.example). Unset XC_SCRAPER_URL ⇒ in-process, as before — but with
# CONTAINERS_ENABLED=true the dev daemon and the league would then BOTH attach
# the qmp dir (every finished game persisted twice): set XC_SCRAPER_URL in
# .env, or run `task dev:backend` + `task dev:frontend` without the daemon.
# The boot line `leaguescraper: mode=wire|in-process …` + the rollback order
# (daemon off FIRST) are in docs/RUNBOOK.md; the daemon's HTTP surfaces are
# ../xc-scraper/docs/wire.md appendix A + ../xc-scraper/docs/control.md.

# Wire contract artifacts are vendored from ../xc-scraper/wire into
# sveltekit/src/lib/types/ (scraper-v2.ts + wire-fixtures/*.json). Re-run
# after every wire change; the check is also a CI step and fails on drift.

task sync-wire          # copy ../xc-scraper/wire/{ts/scraper-v2.ts,testdata/*.json} into sveltekit/src/lib/types/
task sync-wire:check    # diff the vendored copies against ../xc-scraper (XC_SCRAPER_DIR overrides the path)

# Frontend type-check, lint, format (run from sveltekit/)

cd sveltekit
pnpm check          # svelte-check + TypeScript
pnpm lint           # prettier + eslint
pnpm format         # prettier --write

# Frontend tests (run from sveltekit/)

pnpm test           # vitest run — unit tests
pnpm test:watch     # vitest watch
pnpm test:e2e       # playwright — e2e tests in sveltekit/e2e/

# Generate PocketBase TypeScript types (requires running dev server)

task typegen
```

## Architecture

Single Go binary (`cmd/server`) runs four concurrent systems plus an optional fifth:

1. **PocketBase** — REST API, auth (JWT), SQLite database, static file server (serves `pb_public/`), uses `net/http.ServeMux` router
2. **Disgo Discord bot** — connects via gateway in PocketBase's OnServe hook, non-blocking
3. **WebSocket handler** (`coder/websocket`) — custom route on PocketBase's router with optional JWT auth, Hub for managing clients/rooms/broadcasting
4. **Scraper runner** (`xc-scraper/runner`, league-free — lives in the sibling module `github.com/xemu-cartographer/xc-scraper`, consumed through `go.mod`'s `replace => ../xc-scraper`; `cmd/server` still calls it `scrapermgr`) — owns per-xemu-instance memory-scraper goroutines; emits per-class `wire.Envelope`s through its `Emitter` port, which the league glue in `internal/leaguescraper` routes to per-instance `host:<name>:<class>` rooms and a cross-instance `host:summary` room (M5 stages 5c–5e — see `envelopeType*` constants in `xc-scraper/runner/classes.go`; on-demand event log via the `request_events` WS handler + `EventsReply()` framed by the `WireAdapter`; system-snapshot identity sourced from xc-scraper's `xbox` package). **R1 dual mode (step 8 D-4):** with `XC_SCRAPER_URL` set the runner is NOT built — `leaguescraper.Boot` assembles the daemon consumer instead (`internal/xcclient` stream client + mirror, `leaguescraper.Adapter` as the `scraperiface.Service`, demand observer + byte-identical rebroadcast into the same rooms, config pusher, `pb:` event writer); discovery, the host-runner registry and the in-process capture hooks are skipped, the reaper reads the mirror and only removes pods
5. **Podman containers + discovery watcher** (optional, gated by `CONTAINERS_ENABLED=true`) — `internal/podman` shells out to provision xemu+browser pairs; `xc-scraper/discovery` polls the QMP socket dir and auto-Start/Stops scrapers as instances appear/disappear

The SvelteKit frontend is built with `@sveltejs/adapter-static` into `pb_public/`, which PocketBase serves automatically. The `fallback: 'index.html'` config enables SPA-style client-side routing.

Protected pages can be served through custom PocketBase routes that validate JWT auth before serving the static file, while public pages are served directly from `pb_public/`. The kiosk noVNC UI is fronted by a same-origin reverse proxy (`/api/admin/containers/{name}/kiosk/...`, see [internal/pocketbase/routes/containers/proxy.go](internal/pocketbase/routes/containers/proxy.go)) so only PocketBase's port needs to be public — per-container ports stay bound to 127.0.0.1. M09 widens the proxy + VNC-relay auth from admin-only to `authorizeKioskAccess` ([internal/pocketbase/routes/containers/auth.go](internal/pocketbase/routes/containers/auth.go)): admins reach any container, and a non-admin reaches the one container their gamertag is currently rostered in (re-checked per request), which is what lets the player-facing `/play/` page embed the kiosk + controller.

## Backend Structure

### Startup sequence (`cmd/server/main.go`)

1. Create PocketBase app instance
2. Register `migratecmd` (`Dir: "migrations"`, `Automigrate: migrateconf.Automigrate` — true only under `-tags dev`). **Pending migrations apply on boot, BEFORE OnServe** — collections are defined by [`migrations/`](migrations), NOT by code. See [docs/MIGRATIONS.md](docs/MIGRATIONS.md).
3. Register record lifecycle hooks — `hooks.RegisterAll(app)` (callback registration, fires later)
4. In OnServe hook (the schema already exists at this point), in this order:
   - Register OAuth2 providers (`oauth.RegisterAll(app)`)
   - Build `*Services` skeleton with `App` + `PB` populated; later subsystems mutate the same pointer as they come up (no `SetServices` callback needed for systems that already hold the pointer). Wire the dynamic offset-set source (`offsets.SetDynamicSource` over the `offset_sets` collection) before any scraper can bind.
   - Build the scraper feed via `bootScraperFeed(app, svc, hub, os.Getenv, pods)` (`cmd/server/main.go`, unit-tested in `wire_mode_test.go`): **`XC_SCRAPER_URL` unset ⇒ in-process** — construct the scraper `Manager` from its ports (`scrapermgr.New(scrapermgr.Options{Emitter: leaguescraper.NewEmitter(svc), Demand: leaguescraper.NewDemand(svc), OnGameEnd: leaguescraper.GameEndHook(app), RosterFilter: …})`), wrap it in the league adapter `leaguescraper.NewWireAdapter(scrMgr)` and attach the **adapter** to `svc.Scraper` + the scraper route group (`scraperroutes.SetManager`) — the adapter is the `scraperiface.Service`; the bare `*Manager` is what the later `Set*` calls, the reaper and discovery use. **`XC_SCRAPER_URL` set ⇒ wire mode** — `leaguescraper.Boot(app, BootConfig{URL, Token, ControlToken, StaleAfter, Hostrunner, HostDriveMarker, Hub, Pods, Logf})` validates the URL (`http(s)://host[:port]` only, else `leaguescraper: XC_SCRAPER_URL must be http(s)://host[:port]`) and returns a `*leaguescraper.Wire` (`Client`, `Ctl`, `Adapter`, `Demand`, `Rebroadcast`, `Pusher`, `Events`); the `Adapter` is the `scraperiface.Service` and every route source. The hub is constructed (`ws.NewHub`, pure) before the feed so the client can rebroadcast into it. After the authz report the boot prints `leaguescraper: mode=wire url=… token=set|unset control=set|unset` or `leaguescraper: mode=in-process (XC_SCRAPER_URL unset)` (docs/RUNBOOK.md)
   - Read the containers config (`podman.LoadFromEnv()`, pure env) — the authz adapter needs the provisioner's name prefix
   - Install the **authz adapter**: `authzpb.NewDeps(app, scrAdapter, prefixFn)` → `authzpb.SetDefault` + `svc.Authz` (before the seeder, so hooks firing during seeding decide through it — a nil default denies), then `authzpb.ImportLegacyEnv` (PD-12 `LAN_SAVES_TOKEN` → in-memory "legacy-env" machine key)
   - Run `seed.Run(app)` (no-op in production builds) and `seed.EnsureEnvSuperuser` (`SEED_SUPERUSER_EMAIL/PASSWORD`, all builds), then `seed.RegisterContainerSnapshotHooks` (mounted _after_ seeding so the seeder's writes don't trip the snapshot); `authzDeps.InvalidateRoles()` (the seeder writes roles with `app.Save`) and print the eight `authz:` boot lines (`authzpb.LogStartup`)
   - In-process only: host-runner registry (ADR-0003) + play-route wiring (`playroutes.SetScraper/SetHostControl/SetMapSource`), scraper route sources, host-drive policy, overlay resolver, capture-policy provider (`leaguescraper.RegisterPBSink` → `leaguescraper.RegisterCapturePolicyHooks(app, scrMgr)` → `leaguescraper.ReloadCapturePolicies(app, scrMgr)`; the manager itself only exposes `SetCapturePolicies`). Wire mode instead points `playroutes` / `scraperroutes` (`SetHostControl/SetMapSource/SetReadoutSource/SetHealthSource`) at the `Adapter`; the host runner lives in the daemon (D-9), the capture-policy hooks feed the `EventWriter` (pb: rows) and the config pusher (everything else, §10)
   - WebSocket Hub: in wire mode `hub.SetRoomObserver(wire.Demand.Observe)` first (room occupancy → upstream join/leave with linger), then start its Run() goroutine, mount `/api/ws` endpoint (`ws.NewHandler(hub, app, feed.hello)` — the per-principal hello handshake is the adapter's in either mode), populate `svc.WS`. This happens **before** the containers/discovery block: the watcher's goroutines (`scrMgr.Start` → runner loop / aggregator → `leaguescraper` emitter + demand) read `svc.WS` at call time, and the `go w.Run(ctx)` statement is the happens-before edge that makes the write visible to them
   - If `CONTAINERS_ENABLED=true`: build podman `Manager`, wire it into the containers + play route groups (offset-set resolver, provisioner), start the idle-out reaper when `REAPER_ENABLED` (`feed.reaperSource/reaperRemover`: in-process = manager + host registry + `Stop`+`Remove`; wire = phase/machines from the mirror's summary + game frames, `GET …/host` via the `Adapter` per poll only when `HOSTRUNNER_ENABLED`, remover = `podman.Remove` only), and (if `CONTAINERS_SOCKET_DIR` is set, in-process only) start the discovery watcher with `onAdd → scrMgr.Start` and `onRemove → scrMgr.Stop`; wire mode instead hands the podman `Manager`'s create/remove hooks to the config pusher and the daemon's own `--watch-dir` attaches. Then `feed.start()` runs the wire stream (reconnect loop) — after the containers block so the first connect's config push already sees the pod list
   - Bind `authzpb.RejectBannedAuth` on the router (a banned / soft-deleted JWT resolves as a guest on every route), then register custom API routes (`routes.RegisterAll(se)`)
   - Start Disgo bot gateway connection (non-blocking); on success populate `svc.Discord`
   - Hand `*Services` to hooks + Disgo commands via `SetServices`
5. Register OnTerminate hook: cancel discovery watcher → cancel reaper → `feed.stop()` (in-process: stop every runner + close the manager; wire: `Wire.Close` = stop the stream, demand timers and pusher; must precede hub shutdown so in-flight tick broadcasts don't write to a closing channel) → stop hub → close Disgo bot
6. Call `app.Start()` (blocking)

### Key packages

The README's [Project Structure](README.md#project-structure) tree covers the directory layout. The table below highlights packages with cross-cutting roles or non-obvious behavior.

| Package                                 | Purpose                                                                                                                                                                                                                                                          |
| --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/guards`                       | Unified cross-system guards, `Services` struct, `GuardFunc` type                                                                                                                                                                                                 |
| `internal/guards/interfaces/`           | Per-method interfaces, one file each, grouped by system (`discord/`, `websocket/`, `pocketbase/`, `scraper/`). Compose into aggregate `Service` interfaces via embedding.                                                                                        |
| `internal/pocketbase`                   | PB service wrapper — implements `pbiface.Service`                                                                                                                                                                                                                |
| `internal/pocketbase/routes/containers` | `/api/admin/containers/*` CRUD + kiosk noVNC reverse proxy + VNC pass-through                                                                                                                                                                                    |
| `internal/pocketbase/routes/scraper`    | `/api/admin/scraper/*` — list/start/stop scraper runners (typed against `scraperiface.Service`; imports `xc-scraper/runner` only for its `ErrAlreadyRunning` / `ErrInvalidName` sentinels)                                                                                                                              |
| `internal/pocketbase/routes/xemu`       | `/api/admin/xemu/*` — memory-bridge probe/smoke + live diagnostics (`probe.go`, `probe_title.go`, `sample_deltas.go`, `scan_string.go`)                                                                                                                          |
| `internal/pocketbase/routes/teams`      | M23c — `/api/teams/{teamId}/invite` (owner/manager → user) + `/api/teams/{teamId}/join-requests` (user → team). RequireAuth; per-handler guards via `internal/teamperms`.                                                                                                                                                                                                                                                                                                                                                  |
| `internal/pocketbase/routes/adminusers` | M08g — `/api/admin/users/*` admin moderation routes. RequireAuth + RequireAdmin. `POST /{id}/roles` + `DELETE /{id}/roles/{slug}` delegate to `internal/roles.Grant/Revoke`. `POST /{id}/{ban,unban,timeout}` `app.Save` the users record and emit the matching audit row directly (bypassing the `users_ban_transitions` hook to avoid double-audit) so the request body's `reason` lands on the audit log.                                                                                                                                                                                                                                                                                                                                                  |
| `internal/pocketbase/routes/team_membership_requests` | M23d — `/api/team-membership-requests/{id}/{accept,decline,cancel}`. Accept resolves the recipient's `default_gamertag` server-side and creates the roster row.                                                                                                                                                                                                                                                                                                                                |
| `internal/pocketbase/routes/rosters`    | M23d — `/api/rosters/{id}/{leave,remove}`. Self-leave stamps `left_at` quietly; owner/manager remove notifies the kicked user.                                                                                                                                                                                                                                                                                                                                                                                  |
| `internal/pocketbase/seed`              | In-process dev seeder — `seed.go` (`//go:build dev`) + `stub.go` (`//go:build !dev`) + `data.go` defines seed vars; `containers_snapshot.go` keeps a record-backed snapshot of live podman state                                                                 |
| `internal/websocket`                    | WebSocket Hub, client management, message routing, JWT auth upgrade                                                                                                                                                                                              |
| `internal/websocket/rooms`              | Room type definitions with guard lists — one per file (`admin`, `public`, `host:<name>` / `host:all` / `host:summary`)                                                                                                                                                  |
| `xc-scraper/scraper` (sibling module)   | `GameReader` interface + game registry; `Detect()` picks a plugin by xemu title ID                                                                                                                                                                               |
| `xc-scraper/runner` (sibling module)    | The scraper runner (moved out of `internal/scraper/manager` in step 7 part 4; `cmd/server` imports it as `scrapermgr`): opens `xemu.Instance`, runs the phase-driven tick goroutine and emits bare `wire.Envelope` bytes through its **ports** (`Options{Emitter, Demand, OnGameEnd, RosterFilter, …}`, `ports.go`) — it imports nothing from the league server or PocketBase and no third-party module beyond `coder/websocket` (pinned by `xc-scraper/runner/deps_test.go`). Owns the view types (`Info`, `InspectState`, `InstanceState`, `MapList`, `ContainerMembership` + `SanitizeIdentity`/`MatchContainer`/`ContainerHasGamertag`; `scraperiface` aliases them) and returns request/reply envelopes as `runner.Reply{Instance, Class, Envelope}` (`JoinReplay*`, `EventsReply`, `ProbeReply`) plus the unfiltered hello (`BuildHelloPayload` / `HelloPayloadFiltered` / `HelloEnvelope`). M09 `Membership()` projects each runner's roster (console / machine / player names) into a `ContainerMembership` view for gamertag→container match routing.                                                                                          |
| `internal/leaguescraper`                | The league side of the scraper ports: `NewEmitter`/`NewDemand` (WS hub room fan-out + subscriber demand), `GameEndHook` (→ `internal/games`), `LoadRosterConfig`, the capture-policy provider, the PB sink, and `WireAdapter` (`NewWireAdapter(mgr)`) — embeds `*runner.Manager`, frames each `runner.Reply` as `wire.Message{Type:"scraper", Room:…}` (per-class `host:<inst>:<class>` for state classes, legacy `host:<inst>` for the events/probe replies, `host:summary` for the summary) and narrows the hello per principal (`HelloPayloadFor`, `SendHelloOn`); it is the `scraperiface.Service` that `svc.Scraper`, the routes and the authz adapter see.                                                                                          |
| `xc-scraper/haloce` (sibling module)    | Halo: CE `GameReader` (offsets, game-data / tick / event readers); self-registers via blank import in `main.go`                                                                                                                                                  |
| `xc-scraper/xbox` (sibling module)      | Title-agnostic Xbox console primitives — one file per scan target: `xbe_certificate.go`, `console_name.go`, `serial.go`, `mac.go`, `clock.go`, `timezone.go`, `video_standard.go`; plus `memory.go` (kernel helpers), `offsets.go`, `encoding.go` (Xbox strings) |
| `xc-scraper/roster` (sibling module)    | M10d dummy-player / neutral-host filter. `FilterRoster(players, Config{IsNeutralHost, DummyGamertags})` is the pure, shared cleaning step that drops a neutral host's local out-of-bounds dummy + globally-allowlisted dummy gamertags from viewer-facing surfaces (overlays / minimaps / stats). Config sources: the `is_neutral_host` bool on the `containers` collection + the admin-gated `dummy_gamertags` collection. Raw debug views keep the unfiltered roster.                                                                                                                              |
| `internal/games`                        | M13 games-persistence path + the M13/14/15/18 game-end chain. `PersistFinishedGame(app, FinishedGame)` writes a `games` row + N `game_players`, auto-creates a 1-game `series` if none supplied, then: stamps this instance's in-window `game_events` rows with the game id (option a — idempotent on `game=''`), advances the series (`series.Progress`, stamps `ended_at`), and applies a per-game-type two-team Elo update into the `ratings` collection (`rating.Update`). `SuggestCategory` is the 13c heuristic. These collections (`series`/`games`/`game_players`/`game_events`, plus `ratings`) come from the migration baseline. Triggered in prod by the scraper manager's `GameEnd` port: `runLive`'s Live→Ready defer (`xc-scraper/runner/finished_game.go`) distils the capture into a `wire.FinishedGame` and the league adapter `leaguescraper.GameEndHook(app)` (`internal/leaguescraper/gameend.go`) maps it onto `games.FinishedGame` and runs the chain — the live `GameData→FinishedGame` mapping is the one unverified-without-xemu piece. See the M13 Log (2026-06-18).                                                                                                                              |
| `internal/series`                       | M14 series-format logic. `Progress(format, targetN, teamWins) Standing` is the pure decision of whether a series is complete + who won (`single` / `exact-n` / `best-of-n` / `first-to-x`). The 14d game-end wiring + M14c in-progress UI consume it; no PB/IO so it's fully unit-tested. Format constants are the source of truth the `series.format` SelectField mirrors.                                                                                                                              |
| `internal/stats`                        | M15a stats layer. `aggregate.go` is the pure roll-up (`Roll` / `RollByGametype` / `Totals.KD`/`KDA`/`WinRate`, dummy-line exclusion); `query.go` projects `game_players` (joined to game + series) into `[]PlayerGame` via `PlayerGamesForGamertag(s)`. Per-user = roll a user's gamertag lines; per-team = roll a roster's. The M15 UIs (profile/team stats, match history) consume these.                                                                                                                              |
| `internal/bracket`                      | M16b tournament-structure generators. `SingleElim(participants)` (standard seeding + first-round byes) + `RoundRobin(participants)` (circle method). Pure combinatorics, fully unit-tested. The M16 schema/UI/series-spawning consume these; double-elim + Swiss are follow-ups.                                                                                                                              |
| `internal/rating`                       | M18 rating + leaderboard core. `elo.go` — Elo `Expected` + zero-sum `Update` (chosen over Glicko-2/TrueSkill for v1, trade-off in code); `leaderboard.go` — `Rank` / `TopN` / `RankOf`. Pure, fully unit-tested. Per-game-type is the caller's key; the M18 recompute hook + leaderboard pages + Discord cmds consume these.                                                                                                                              |
| `xc-scraper/xemu` (sibling module)      | xemu QMP client + memory bridge — `Instance.Init`, GVA→GPA translation, `ReadBytes`/`ReadAt`                                                                                                                                                                     |
| `internal/podman`                       | Podman CLI wrapper — container pair lifecycle, stride-wise port allocation, state tracking. M26 `overlay.go` provisions each instance's copy-on-write HDD overlay (`qemu-img create -b _default.qcow2`, relative backing) on `Create` + freezes the shared root read-only (`freezeRoot` 0444); `console_name.go` stamps the container name into the overlay's Xbox console name (`E:\UDATA\NICKNAME.XBN`, via qemu-storage-daemon FUSE export + pyfatx) before first boot; `Remove` → `removeContainerFiles` does a symmetric teardown (all per-instance files, shared base untouched). **Replacing the root requires rebuilding all overlays.** `browser_cert.go` (`provisionBrowserTrust`) pre-seeds the firefox-kiosk profile's NSS trust store with the instance CA on `Create` (host `certutil`) so the kiosk loads xemu's HTTPS view with no TLS warning — see the kiosk-TLS-trust note under Containers.                                                                                                                                                                       |
| `xc-scraper/discovery` (sibling module) | Polling watcher over `CONTAINERS_SOCKET_DIR`; emits `onAdd(name, sock)` / `onRemove(name)` so the scraper manager can attach/detach as containers come and go                                                                                                    |
| `internal/audit`                        | Shared audit-log writer per [ADR-0002](docs/decisions/0002-unified-audit-log-collection.md). `Write(app, actor, action, target, payload)` + `WriteRef(...)` (post-delete / synthetic rows). One file per action constant — `action_<verb>.go` pairs `ActionXxx` with `XxxPayload`. M22 lands block / unblock / approve / edit / rename; M08 adds role_grant / role_revoke / ban / unban / timeout. `Write` skips the `actor` relation when the supplied record isn't from the `users` collection (PB superusers + nil actors record as `actor=null`).                                                                |
| `internal/roles`                        | M08 roles + user_roles read/write helpers, now thin wrappers over the authz adapter. `Has(app, userID, slug)` / `Slugs(app, userID)` for reads; `Grant(app, userID, slug, grantedBy)` / `Revoke(app, userID, slug, by, reason)` resolve `grantedBy`/`by` to a principal and delegate to `pb.Grant` / `pb.Revoke` (`internal/authz/pb`), which decide `role.grant` / `role.revoke` — an actor may only grant or revoke roles at or below their own max level (`authz.ErrLevelTooLow`), the last admin cannot be revoked by a non-internal actor (`authz.ErrLastAdmin`), the `user_roles` mutation is paired with the matching `audit_log` row, and the adapter's 60 s roles cache is invalidated. A nil actor is `authz.Internal` (the migration backfill, the `users_default_role` hook, the dev seeder): no tier check, "system" provenance. `IsAdminAuth(app, auth)` survives only as a deprecated shim over `pb.IsAdmin` — new gates use `authz.Can` with an explicit action (`middleware.RequireAdmin(action)`, `pb.Check`). The five built-in roles + default scopes live in one literal, `authz.SeedRoles` (migration `1788300001_roles_scopes` creates them; `seed.Run` and `TestSeedRolesMatchAuthz` pin it). |
| `internal/reservednames`                | M22e reserved-name pre-list. `Match(app, candidate)` does substring-on-lowercase against the `reserved_names` collection; consumed by the `gamertags_reserved_name` and `teams_reserved_name` hooks to flip new submissions to `status="pending"` before the status-transition hooks fire.                                                                                                          |
| `internal/notifications`                | M23a notification writer. `Notify(app, user, type, payload)` mirrors `audit.Write`. One file per type — `notification_<event>.go` pairs `NotifTypeXxx` with `XxxPayload`. Reads through the `notifications` collection per the M23a schema; rules gate ownership and the `notifications_field_lock` hook restricts non-admin updates to the `read` field.                                                                                                                                                       |
| `internal/teamlog`                      | M23c team activity log writer. `Write(app, team, actor, event, subjectUser, subjectGamertag, payload)` writes to the `team_log` collection. One file per event — `event_<verb>.go` pairs `EventXxx` with `XxxPayload`. Twin-write target for renames + status changes (the M22d `audit_log.ActionRename` row stays); canonical surface for membership churn so the public team page reads activity without admin-only audit_log access.                                                                                              |
| `internal/teamperms`                    | M23c cross-cutting team-permission helpers. `IsOwnerOrManager`, `HasActiveMembership`, `OwnersAndManagers` (notification fan-out target), `FindPendingRequest`. Direct `core.App` calls; no interface boundary — scraper + Discord don't need team perms. Consumed by `routes/teams/`, `routes/team_membership_requests/`, `routes/rosters/`, and the `users_soft_delete_pii` cascade.                                                                                                                              |
| `internal/gamertags`                    | M09 gamertag→roster match helper. `SanitizedForUser(app, userID)` returns a user's non-blocked, lowercased+trimmed gamertag strings (the `gamertags.sanitized` column) — still what the play resolver and the Discord `/box` command use to *find* the box a player is in. Access is a separate question: `UsableForUser(app, userID)` is the same query restricted to `UsableStatuses` (`approved`, `allowed` — pending and blocked excluded), and it is what the authz adapter's `RosteredIn` predicate (`internal/authz/pb`) intersects with live rosters + the `rostergrace` window. Every rostered-player gate — the `host:<name>` room join, the kiosk/VNC proxy, the `box.read`/`kiosk.*` checks behind `/api/play/*` (`box.control`/`box.teardown` are owner-only) — decides through that one predicate, so a pending tag can locate a box but never unlock it. Leaf package (`core.App` + dbx); flip `UsableStatuses` to change the policy in one place. |
| `internal/rostergrace`                  | M09 roster-access grace window. The `Default` tracker keeps `(container, gamertag)` last-seen times; `Allow(view, container, tags, now)` grants access while a tag is in the roster (refreshing the window) or within `DefaultTTL` (5 min) of its last live sighting — only live presence refreshes, so a removed gamertag fails closed after the TTL. Shared by `authorizeKioskAccess` (kiosk/VNC proxy) + the `join_room` `host:<name>` gate so a transient roster drop doesn't instantly kick a player. Admins bypass before it. Pure logic w/ injected clock → fully unit-tested. **Deliberate security trade-off — see the M09 Log.**                                                                                                                              |
| `internal/discordcfg`                    | M17a per-guild Discord config. `Get`/`Upsert`/`All` over the `discord_guilds` collection + the pure category filter (`GuildConfig.ResultsTarget` / `ResultsTargets` fan-out — opt-in `posted_categories`, empty = post nothing). Written by the `/cartographer config` slash command; read by the `games_discord_post` hook. Unit-tested.                                                                                                                              |

## Frontend Structure

- **UI framework:** Skeleton UI v5 (Svelte 5 + Tailwind CSS v4); eight bundled themes in [sveltekit/src/lib/themes/](sveltekit/src/lib/themes/) (`theme-{default,forerunner,midnight,norcal,phosphor,xbox,debug,starcommand}.css`), one is selected via `data-theme="..."` on `<html>` in [sveltekit/src/app.html](sveltekit/src/app.html) (**`starcommand` is the default**). Per-theme body decorations (e.g. xbox's warped hex mesh + jewel glow, starcommand's starfield) live behind `[data-theme='...']` selectors in [sveltekit/src/routes/layout.css](sveltekit/src/routes/layout.css). Each theme implements the full 247-var Skeleton v5 token contract — when adding one, copy an existing file rather than an upstream v4 theme, since v4's typography tokens (`--base-font-*` / `--heading-font-*` / `--anchor-*`) and `--body-background-color` were renamed to `--typo-{base,heading,anchor}--*` and `--color-root-bg-{light,dark}`.
- **⚠️ Full-viewport backdrops must not sit under an opaque `body`.** Skeleton v5 paints the root background on `html` (`background-color: light-dark(var(--color-root-bg-light), var(--color-root-bg-dark))`); v4 painted it on `body`. Because `html` is no longer transparent, `body`'s background **stops propagating to the canvas** and paints as an ordinary block background — which per CSS painting order lands *above* every negative-`z-index` descendant. Any theme adding a fixed backdrop (xbox's `body::before` mesh at `z-index:-1`, starcommand's `StarfieldBackground` at `-10`) must therefore keep `body`'s `background-color: transparent` and let `html` supply the base color, or the decoration is invisible. This silently broke the xbox mesh during the v4→v5 migration.
- **stew-kit (motion + fx):** [sveltekit/docs/stew-kit.md](sveltekit/docs/stew-kit.md) is the usage reference. 13 theme-token-driven components in [sveltekit/src/lib/components/fx/](sveltekit/src/lib/components/fx/) (ParticleField, MouseGlow, AnimateIn, CountUp, TypeCycle, TiltCard, Sparkline, SkeletonBlock, EmptyState, ConfettiBurst, PageTransition, ScrollProgress, BuildStamp) plus motion utilities (`hover-lift` / `press` / `icon-slide` / `glow` / `shimmer` / `bg-aurora` / `bg-dotgrid`) in [sveltekit/src/lib/styles/motion.css](sveltekit/src/lib/styles/motion.css). All honor `prefers-reduced-motion`. Only the shell pieces are wired so far — `PageTransition` wraps both `+layout.svelte` branches, `ScrollProgress` targets the inner `#page` `<main>` (the app shell is `h-screen`/`overflow-hidden`, so the window never scrolls), and `BuildStamp` anchors on the NavToggle logo. The rest are opt-in per page.
- **starcommand kit:** six theme-token-driven components in [sveltekit/src/lib/components/starcommand/](sveltekit/src/lib/components/starcommand/) (`Panel`, `StatusDot`, `Eyebrow`, `AccentChip`, `NorcalLockup`, `StarfieldBackground`) plus `.sc-scanlines` / `.sc-vignette` / `.sc-mono` in [sveltekit/src/lib/styles/starcommand-utilities.css](sveltekit/src/lib/styles/starcommand-utilities.css). They read theme tokens only, so they render under any theme — they just look *right* under `starcommand`. Only `StarfieldBackground` is wired (mounted once in `+layout.svelte`, CSS-gated to `html.dark[data-theme='starcommand']` and skipped on `hideLayout` routes so OBS browser sources stay transparent); the rest are opt-in per page. House rules: one accent leads per surface (blue = LAN/ops/stats, orange = events/CTAs), green only for status, **never recolor the star emblem** ([sveltekit/src/lib/assets/star.png](sveltekit/src/lib/assets/star.png)), Orbitron never at body sizes.
- **Build version stamp:** `svelte.config.js` bakes the short git commit into `kit.version` with a 60s `pollInterval` (`Date.now()` fallback when `.git` is absent, e.g. container builds). `BuildStamp` reads it via `$app/environment`'s `version` and flags newer deploys through SvelteKit's `updated` store.
- **API client:** PocketBase JS SDK (`pocketbase` npm package) — singleton in `src/lib/pocketbase.ts`; in dev points to `http://localhost:PORT`, in production passes `undefined` (same-origin relative)
- **Auth store:** `src/lib/stores/auth.svelte.ts` — uses Svelte 5 runes (`$state`/`$derived`), not writable stores
- **Mode store:** `src/lib/stores/mode.svelte.ts` — dark/light mode toggle, persisted in `localStorage`; call `mode.toggle()` or `mode.set('dark'|'light')`
- **Toaster:** `src/lib/stores/toaster.ts` — global Skeleton toast singleton (`toaster`); import and call `toaster.trigger(...)` from any component
- **Navigation:** `src/lib/config/navigation.ts` — central nav link config consumed by all four layout nav components; edit here to add/remove nav links
- **App config:** `src/lib/config/app.ts` — exports `APP_NAME` (displayed app name) and `OAUTH_PROVIDERS` (display labels + icons per provider); actual enabled providers are discovered at runtime from PocketBase's `listAuthMethods()` API
- **WebSocket:** Browser native `WebSocket` API connecting to `/api/ws?token=PB_JWT`
- **Wire types (vendored, do not edit by hand):** [sveltekit/src/lib/types/scraper-v2.ts](sveltekit/src/lib/types/scraper-v2.ts) is a byte-for-byte copy of `../xc-scraper/wire/ts/scraper-v2.ts`, and [sveltekit/src/lib/types/wire-fixtures/](sveltekit/src/lib/types/wire-fixtures/) mirrors `../xc-scraper/wire/testdata/*.json` (the Go golden fixtures). `task sync-wire` refreshes both; `task sync-wire:check` / the CI "Wire sync check" step fail on drift. [wire-fixtures.test.ts](sveltekit/src/lib/types/wire-fixtures.test.ts) imports every fixture and `satisfies`-checks it against the TS payload types (through a literal-widening `Loose<T>` because JSON modules type `"live"` as `string`), so a Go-side wire change that breaks the TS mirror fails `pnpm check`. Change the contract in xc-scraper (`wire/*.go` + `wire/ts/scraper-v2.ts` + `go test ./wire -update`), then sync here.
- **Routing:** SvelteKit file-based routing in `sveltekit/src/routes/`; `+layout.ts` sets `ssr = false`, `prerender = true`, `trailingSlash = 'always'` globally
- **Admin pages:** Under `sveltekit/src/routes/admin/`: `/admin/` dashboard, `/admin/pod/` listing + `/admin/pod/[name]/` per-pod hub (View / Debug / Probe sub-pages), `/admin/capture-policies/`, `/admin/players/` (gamertag 4-state moderation queue — M07/M22), `/admin/rosters/` (Teams + Rosters tabs, with the Teams tab carrying the same 4-state queue — M07/M22), `/admin/reserved-names/` (M22e pre-list curation). `/admin/roles/` (M08 role grants + ban moderation) is live. The route group is gated by `requireAdmin` hoisted into `routes/admin/+layout.ts`.
- **Player "my match" page:** [sveltekit/src/routes/play/](sveltekit/src/routes/play/) (M09, RequireAuth not admin) polls `GET /api/play/current` (server resolves the caller's gamertag against live container rosters and decides `box.read` through the authz adapter — `host:summary` is admin-only so this can't be a client-side match) and renders [PlayKiosk](sveltekit/src/lib/components/play/PlayKiosk.svelte) (reuses `KioskFrame` + `XboxController` + `VNCKeyboard`) for the matched container, or an idle state. The pure resolve→phase logic lives in [sveltekit/src/lib/utils/play-match.ts](sveltekit/src/lib/utils/play-match.ts) (unit-tested).
- **Admin debug page:** [sveltekit/src/routes/admin/pod/[name]/debug/](sveltekit/src/routes/admin/pod/[name]/debug/) renders per-instance tabs (Overview / Xbox / Scenario / Game / Tick / Objects / Debug / Previous game / Events). Sub-components live in [sveltekit/src/lib/components/debug/](sveltekit/src/lib/components/debug/), one folder per tab plus `shared/`. The Probe tab consumes `GameReader.BuildScoreProbe()` and `LastStateInputs()` from `xc-scraper/scraper/scraper.go` — when adding diagnostics to a game plugin, surface them through these methods.
- **Tables:** use `<DataTable>` from [sveltekit/src/lib/components/ui/DataTable.svelte](sveltekit/src/lib/components/ui/DataTable.svelte) for any list of records. Conventions (required props, density, cell patterns, order stability) live in [sveltekit/docs/TABLES.md](sveltekit/docs/TABLES.md).
- **Nav layout & Skeleton rail-gap override:** [sveltekit/docs/NAV.md](sveltekit/docs/NAV.md) — Skeleton's rail mode stretches `Navigation.Menu` via `flex: 1` and centers items, producing a large gap between groups; the doc records the targeted override and the Tailwind v4 `!` suffix needed to win on specificity. Still required under v5 (the `[data-part=menu] { flex: 1 }` rule is unchanged). Re-apply this override in any template-derived repo.
- **Build:** adapter-static outputs directly to `pb_public/` with SPA fallback
- **Env:** `vite.config.ts` uses `envDir: '..'` to read from root `.env` — no separate `sveltekit/.env`
- **Package manager:** pnpm

### Responsive layout

The root layout (`+layout.svelte`) implements a 3-mode navigation system driven by a single `NavPanel` component:

| Breakpoint       | Nav mode                                                             |
| ---------------- | -------------------------------------------------------------------- |
| Mobile (`< sm`)  | Bottom bar (`MobileNav`) + slide-in overlay drawer (`NavPanel`)      |
| Desktop (`< lg`) | Rail sidebar — icons only (`NavPanel layout="rail"`)                 |
| Desktop (`≥ lg`) | Toggle between rail and full sidebar via `NavToggle` in the `Header` |

`NavToggle` toggles `navOpen`, which controls both the desktop rail↔sidebar expansion and the mobile overlay open/close state. `NavPanel` derives its Skeleton `layout` prop (`"rail"` | `"sidebar"`) from `open` and `isDesktop`.

## Cross-System Architecture

The main systems (PocketBase, Disgo, WebSocket, Scraper) never import each other. Cross-system communication is mediated through:

1. **Interfaces** (`internal/guards/interfaces/`) — one interface per file, organized in per-system subdirectories (`discord/`, `websocket/`, `pocketbase/`, `scraper/`). Small interfaces compose into aggregate `Service` interfaces via embedding.
2. **Services struct** (`internal/guards/services.go`) — bundles all system references (`App`, `PB`, `WS`, `Discord`, `Scraper`). Fields are nil if the system is not running.
3. **Dependency injection** — `main.go` builds the `Services` struct early and either passes the pointer at construction time or calls `SetServices()` once subsystems come up. Subsystems that hold the `*Services` pointer (e.g. the league-side scraper glue in `internal/leaguescraper` — the emitter/demand ports and the `WireAdapter`; the scraper manager itself holds no `Services`) see fields like `svc.WS` populate later in OnServe without needing a setter.

Handler flow: **Trigger → Resolve → Guard → Action**

- **Resolvers** stay in their own package (`pocketbase/resolvers/`, `disgo/resolvers/`, `websocket/resolvers/`) and only talk to their own system
- **Guards** (`internal/guards/`) take `*Services` and check cross-system permissions
- **Actions** are called through `Services` interfaces (e.g., `svc.Discord.SendNotification()`, `svc.WS.BroadcastRaw()`, `svc.Scraper.JoinReplayForInstance()`)

Handlers orchestrate by calling resolvers/guards/actions from multiple systems — no resolver or guard calls sideways into another package's resolvers.

The scraper manager is special the other way round: it holds **no** `*guards.Services` and imports nothing from the league server (it lives in the sibling module as `xc-scraper/runner`; `xc-scraper/runner/deps_test.go` pins its dependency closure to xc-scraper + stdlib + `coder/websocket`). It talks outward only through the ports in `runner.Options` — `Emitter.Emit(instance, class, envBytes)` for broadcasts, `Demand.Wants(instance, class)` for subscriber gating, `OnGameEnd(wire.FinishedGame)` for persistence, `RosterFilter` for the dummy filter — and every request/reply path (`JoinReplay*`, `EventsReply`, `ProbeReply`) returns bare `runner.Reply{Instance, Class, Envelope}` values. The league glue lives in `internal/leaguescraper`: `NewEmitter`/`NewDemand` route to the per-instance `host:<name>:<class>` rooms + the cross-instance `host:summary` room via `svc.WS.SendToRoomRaw`, and `WireAdapter` (embeds `*runner.Manager`) frames each `Reply` as a `wire.Message` for its room and sends the per-principal hello — the adapter, not the manager, is the `scraperiface.Service` on `svc.Scraper`. Late-joining clients are caught up two ways: (1) `JoinReplayMessages()` / `JoinReplayForInstance*()`, called from the `join_room` WS handler, replay the latest per-class envelopes so the UI can render immediately; (2) `EventsReply()`, called from the `request_events` WS handler, returns the recent event log filtered by `since_tick` + `types`. Without join replay, a client that connects mid-match never gets map/players/power-item-spawn data; without events reply, on-demand event-log requests would have no path back through the import-cycle-free `scraperiface` boundary.

## Conventions

- **Adding new routes/hooks/commands/WS handlers:** create a new file in the relevant package, define a function, and call `register(fn)` from `init()`. No other file needs to change — the package-level `init()` runs automatically on import.
- PocketBase v0.36.7 — uses `net/http.ServeMux`, NOT Echo. Hooks use `OnServe` not `OnBeforeServe`.
- PocketBase extensions follow a registration pattern: hooks register before OnServe, routes register inside OnServe via `RegisterAll()`. **Collections are NOT registered in code** — they come from `migrations/` (applied before OnServe); see [docs/MIGRATIONS.md](docs/MIGRATIONS.md)
- One `.go` file per logical domain in `hooks/`, `routes/`, and `commands/` (the old `schema/` package is retired — schema changes are migrations now)
- PB record hooks use `routine.FireAndForget` for async external calls (Discord API)
- Clone record data into local variables before goroutines — event objects are not concurrent-safe
- WebSocket auth: validate `?token=` query param, attach user if valid, allow anonymous if not
- WebSocket Hub supports: Broadcast (all clients), SendToUser (by user ID), SendToRoom (room members), plus `*Raw` variants taking `[]byte` for cross-system use via interfaces
- Disgo uses `discord.SlashCommandCreate` for slash commands, raw event listeners for gateway events
- Disgo actions take `*bot.Client` as first param — also exposed as methods on `Bot` for interface compliance
- Disgo components are pure builder functions (no registry, no init) — one file per button/embed/row
- M17 Discord: slash-command **handlers stay thin** (read options → call a testable resolver → `replyEmbed`); the resolution + embed building live in unit-tested helpers (`commands/` resolvers, `components/embeds/` builders) since handlers only run on a live interaction. Event posting goes through a PB hook (`games_discord_post`) using `routine.FireAndForget` + `svc.Discord.PostEmbed`, **no-op when the bot is nil** (offline/dev/tests). Per-guild posting config + the opt-in category filter is `internal/discordcfg`. Never construct the `Bot` in tests — `NewBot()` calls `syncCommands` (a live REST call).
- Cross-system guards in `internal/guards/` take `*Services` + `*core.Record`, usable from any system — one `require_*.go` file per guard (see `require_admin.go`, `require_auth.go`, `require_connected.go`, etc.); compose them with `compose.go`
- **M08 admin gating:** PB collection rules embed the admin subquery `@collection.user_roles.user ?= @request.auth.id && @collection.user_roles.role.slug ?= "admin"` — wrap in parens when composing with `||`/`&&`. The rules live in the migration snapshot (`migrations/*_collections_snapshot.go`), not in a Go constant; change them via a new migration. **Go-side gates go through `internal/authz`** (DESIGN-STEP6): one process-wide adapter (`pb.NewDeps` over the live app + scraper) is installed in `OnServe` before the seeder and before any route binds (`pb.SetDefault`, also on `guards.Services.Authz`), and every gate is `authz.Can(deps, principal, action, resource)` against the rule table in [internal/authz/rules.go](internal/authz/rules.go) — route groups bind `middleware.RequireAdmin(authz.ActionAdmin<Group>)`, handlers call `pb.Check(d, e, action, res)`, hooks resolve `pb.PrincipalFromAuth` and decide `user.moderate` / `gamertag.moderate` / `notification.admin_edit`, `guards.RequireAdmin` is the `room.join` decision on the admin room. Everything reads `pb.Default()` at request time and a nil adapter denies (fail-closed), superusers included; a PB superuser or an `internal` actor otherwise passes every rule. Scopes are `family.action[:selector]` strings on `roles.scopes` (seeded from `authz.SeedRoles`: the `admin` role carries `admin.*`, `role.*`, `room.join:*`, …) or on `api_tokens` rows (machine / spectator / device keys, `/api/admin/tokens`); `roles.IsAdminAuth` is a deprecated shim. Boot prints eight `authz:` lines (roles present, admin count, anonymous scopes / console door, `LAN_SAVES_TOKEN` import, token counts, `/api/lan/*` key health, `WS_ALLOWED_ORIGINS`) — see [docs/RUNBOOK.md](docs/RUNBOOK.md). The pre-M08 `users.isAdmin` boolean is gone. Adding a new gated collection: create it in a migration (dev Automigrate writes one when you add it in the admin UI). The `roles` + `user_roles` collections use nil mutate rules because of the first-boot circular reference; admin mutations on them flow through the M08g routes under `/api/admin/users/*` (which `app.Save` to bypass rules) and editing a role's `scopes` (e.g. the PD-1 console-door flip on `anonymous`) is a superuser dashboard edit.
- Interface files use one-interface-per-file convention for merge-safe parallel development
- Custom routes registered in OnServe take priority over pb_public/ static file serving
- `PUBLIC_PB_PORT` in root `.env` — single port variable used by Taskfile, compose, Containerfile, and SvelteKit (via `$env/static/public`). The `PUBLIC_` prefix is required by SvelteKit for client-side access
- SvelteKit `trailingSlash = 'always'` is set globally — all route hrefs must end with `/` (e.g. `/login/`, not `/login`), otherwise navigation breaks with the static adapter
- **Seeding:** Air (`task dev`) builds with `-tags dev`, causing `seed.Run(app)` to execute automatically at server startup using `internal/pocketbase/seed/data.go`. Edit `data.go` to change seed data.
- **Dev vs prod builds:** `air` (dev) compiles with `-tags dev`; `task build:backend` compiles without it. The `//go:build dev` constraint in `internal/pocketbase/seed/` means the seeder is a no-op in production binaries.
- **Dev DB is ephemeral:** Air compiles the server to `tmp/server.exe` and `clean_on_exit = true` wipes `tmp/` on exit — including `tmp/pb_data/` where PocketBase stores its dev database. This is intentional: each `task dev` session starts with a clean slate. TypeScript type generation (`task typegen`) therefore uses `--url` mode against the live server rather than reading the DB file directly.
- **`atlas/` directory:** Snapshots of predecessor projects (`HaloCaster`, `xemu-cartographer-legacy`) kept as porting reference. **Treat every artifact here as unverified** — offsets, patterns, and APIs must be re-confirmed against current xemu/library behavior before being copied into the live tree. Not part of the build, not imported, not modified. When in doubt, read `atlas/README.md` first. Contents are gitignored (local-only) by default.
- **CI** ([.github/workflows/ci.yml](.github/workflows/ci.yml)): three jobs gate `main` + `beta`. Every job checks this repo out under `xemu-cartographer/`, and **frontend** + **backend** also check out `xemu-cartographer/xc-scraper@main` under `xc-scraper/` so `go.mod`'s `replace => ../xc-scraper` resolves. That sibling repo is **private**, so both checkouts pass the `XC_SCRAPER_CHECKOUT_TOKEN` repository secret (a fine-grained PAT owned by the `xemu-cartographer` org with repository access limited to `xc-scraper` and Contents: read-only); until the owner creates that secret, those two jobs fail at the checkout step. **frontend** runs the wire sync check (`diff` of the vendored `scraper-v2.ts` + `wire-fixtures/` against `../xc-scraper/wire`, same commands as `task sync-wire:check`), then `pnpm lint`, `pnpm check`, `pnpm test`, `pnpm build`; **backend** runs `go vet ./...`, `go test ./cmd/... ./internal/...`, `go test -race ./internal/websocket/... ./internal/xcclient/...` (the two packages with real goroutine choreography, booted against an in-process xc daemon), `go build` and the daemon build (`bin/xc-scraper` from the sibling checkout, same as `task build:scraper`); **e2e** downloads the built `pb_public/` and runs Playwright. CI does not run xc-scraper's own tests — that repo has its own workflow (`../xc-scraper/.github/workflows/ci.yml`: gofmt, vet, build, test, race, dependency pin); run `cd ../xc-scraper && go test ./...` before pushing changes there.
- **pnpm pinned to v11:** both `Containerfile` (`corepack prepare pnpm@11`) and `.github/workflows/ci.yml` (`pnpm/action-setup` `version: 11`); pnpm v11 requires Node 22+ (already the baseline). The dependency build-script allowlist lives in `sveltekit/pnpm-workspace.yaml` under v11's `allowBuilds` map (`esbuild: true`, `sqlite3: true`), which replaced v10's `onlyBuiltDependencies` list; pnpm ignores a `pnpm` block in `package.json`, and the `Containerfile`'s frontend stage must copy this file into the build context. `pnpm-lock.yaml` (`lockfileVersion: '9.0'`) is unchanged — v11 reads the existing lockfile as-is, so there's no lockfile churn — but because build-script approvals now use v11's `allowBuilds` syntax (which v10 doesn't recognize), the project targets v11. The pin is major-only, so CI floats to the newest 11.x on every run: pnpm 11.27.1 (2026-09-20) started running `pnpm exec` commands in their own process group, which left Playwright's webServer alive after its group-kill and hung the e2e job for 6 h — `sveltekit/playwright.config.ts` therefore starts sirv via `node node_modules/sirv-cli/bin.js`, not `pnpm exec`, and the e2e job has `timeout-minutes: 15`.

## Containers (xemu + browser pairs)

The `internal/podman/` package shells out to the `podman` CLI to provision xemu + Firefox-kiosk container pairs. Routes live under `/api/admin/containers/*` (admin auth required). Disabled by default; opt in by setting `CONTAINERS_ENABLED=true` in `.env`.

In-container boot logic lives in [containers/xemu/init/](containers/xemu/init/) (numbered shell scripts run in order: `01-setup-toml.sh`, `02-patch-toml.sh`, `03-setup-hdd.sh`, `04-patch-startwm.sh` — the last wraps labwc to try the GPU first and fall back to the pixman software renderer if wlroots can't initialize a renderer on its DRM node, e.g. a driverless GPU in passthrough, instead of crash-looping the desktop into a black screen). `03-setup-hdd.sh` no longer copies the disk — it just injects the `hdd_path` of the per-instance qcow2 **overlay** the host provisioner created (see HDD overlays below). QMP sockets are bind-mounted into [containers/xemu/qmp/](containers/xemu/qmp/), which is what the discovery watcher polls.

### Prerequisites

- **Rooted Podman + crun.** `/dev/kvm` + `/dev/dri` device passthrough and `NET_ADMIN`/`NET_RAW` caps don't work rootless. On CachyOS: `sudo pacman -S podman crun`, then `sudo systemctl enable --now podman.socket`. The Go binary itself doesn't need to run as root — `podman` does (sudo or rootful service). The `crun` runtime is non-optional: with the default `runc` (1.4.x) on some hosts, the jlesage/firefox kiosk's Xvnc rejects all X clients ("Authorization required, but no authorization protocol specified") and the noVNC view stays black. [.env.example](.env.example) defaults `CONTAINERS_PODMAN_CMD=sudo -n podman --runtime=crun` to select it.
- **Pre-pull images** (auto-pulls on first start, but pre-pulling avoids surprises):

  ```sh
  sudo podman pull lscr.io/linuxserver/xemu:latest
  sudo podman pull docker.io/jlesage/firefox
  ```

- **`qemu-img` on the host.** The host running the server must have `qemu-img` (the `qemu-img` / `qemu-utils` package) — the podman provisioner shells it to create each instance's copy-on-write HDD overlay. Configurable via `CONTAINERS_QEMU_IMG_CMD` (default `qemu-img`).
- **`qemu-storage-daemon` + `python3` + `pyfatx` (optional — console naming).** For the create-time Xbox console-name write (`E:\UDATA\NICKNAME.XBN`), the host needs `qemu-storage-daemon` (same qemu package; FUSE export of the overlay), plus `python3` and `pip install pyfatx`. Missing any of these is non-fatal — the instance just keeps the Xbox's random name. Disable entirely with `CONTAINERS_SET_CONSOLE_NAME=false`; overrides: `CONTAINERS_QEMU_STORAGE_DAEMON_CMD`, `CONTAINERS_PYTHON_CMD`, `CONTAINERS_FATX_TOOL`.
- **`_default.qcow2` baseline = the read-only ROOT.** Drop the canonical Halo-installed disk at `containers/xemu/shared/hdds/_default.qcow2` before the first `Start` (configurable via `CONTAINERS_ROOT_HDD`; keep it in sync with `DEFAULT_HDD_NAME` in [containers/xemu/init/.env](containers/xemu/init/.env)). Without it, create fails. Easiest paths: `qemu-img create -f qcow2 ./containers/xemu/shared/hdds/_default.qcow2 8G` for a blank image, or copy a pre-configured xemu HDD. See **HDD overlays** below for why this file is frozen.

### HDD overlays (copy-on-write) — M26

Each container gets a **thin qcow2 overlay** backed by the shared, read-only root (`_default.qcow2`), not a full copy. `internal/podman` `Manager.Create` → `provisionOverlay` ([overlay.go](internal/podman/overlay.go)) runs `qemu-img create -f qcow2 -b _default.qcow2 -F qcow2 <name>.qcow2` in `hdds/`, so the instance writes only its deltas; xemu (a qemu fork, stock qcow2 block driver) reads the canonical disk from the root through the backing chain.

- **Root is frozen read-only (0444)** by `freezeRoot` on first overlay create — modifying a backing file corrupts every overlay above it. `CleanupOrphans` skips `_`-prefixed baselines so the root is never auto-deleted. **⚠️ If you replace the root, you MUST delete every overlay first and re-create the instances — there is no in-place root edit that preserves overlays.**
- **Backing path is stored RELATIVE** (bare `_default.qcow2`), on purpose: the `hdds` dir is bind-mounted at `/shared/hdds` in the container but `./containers/xemu/shared/hdds` on the host, so a relative backing resolves in both and survives the dir moving — as long as root + overlays stay co-located.
- Pre-existing **full-copy** instance disks still work standalone; delete them to reclaim space and re-create as overlays.

**Symmetric teardown (per-instance vs shared).** Firefox is its **own** podman container per instance (`<name>-browser`, `jlesage/firefox`), distinct from the xemu container. `Manager.Remove` stops + `rm -f -v` **both** containers — dropping each container, its read-write layer, and any anonymous volume (defensive: neither image declares a `VOLUME` today, and we only use host-path bind mounts so no named volumes are created) — then `removeContainerFiles` ([podman.go](internal/podman/podman.go)) removes *every* per-instance file Create generated: the overlay (`hdds/<name>.qcow2`), the xemu config dir (`configs/<name>/`, which holds the **per-instance eeprom** at `/config/.local/share/xemu/xemu/eeprom.bin`, plus toml/ssl/shaders/X state), and the browser dir (`browser/config-<name>/`). The browser runs with `HOME=/config` bind-mounted there, so the **whole Firefox footprint** — `profile/` (incl. `cache2`), `.mozilla/`, `xdg/` cache, `downloads/`, lock files (`.parentlock`/`lock`), logs, `machine-id` — is inside that one dir and goes with it. Teardown never touches **shared** files: the root qcow2 (+ any `_`-prefixed baseline), the bootrom/MCPX + flashrom/BIOS under `shared/bios/`, the default toml, the shared `browser/init/` scripts. `DeleteFiles` reuses the same file remover (after a running-check); `containerOwnedPaths` is the single source of truth for "per-instance".

**Console name (E:\UDATA\NICKNAME.XBN) — M26.** At create time, before first boot, `Manager.Create` → `writeConsoleName` ([console_name.go](internal/podman/console_name.go)) stamps the container name into the instance's Xbox console name **inside its overlay**, so instances are distinguishable on system link / the dashboard. The write goes through **qemu-storage-daemon's FUSE export** of the overlay's merged view (rootless — no `/dev/nbd`/kernel module; fits an unprivileged server) so it lands in the CoW layer; a small **pyfatx** helper ([containers/xemu/tools/fatx_console_name.py](containers/xemu/tools/fatx_console_name.py)) creates `NICKNAME.XBN` in the existing `E:\UDATA` (FATX write-CREATE) — the root leaves the file **absent on purpose**, which is what makes unnamed/root instances get the Xbox's *random* name. We never `qemu-img convert` an overlay to raw (that would flatten the backing chain). All FATX/Xbox format knowledge (header `04 00 'SM'`, UTF-16LE NUL-terminated name, 3400-byte fixed size; cap 15 chars) lives in Go (`buildNicknameXBN`, unit-tested); the helper just writes the bytes. **Best-effort:** missing `qemu-storage-daemon`/`python3`/`pyfatx` → logged warning, instance keeps the random name. Disable via `CONTAINERS_SET_CONSOLE_NAME=false`. Format ref: [halo-offset-mapper `docs/SYSTEM-INFO.md` §7].

### Kiosk HTTPS trust (no "risky connection" warning)

The Firefox-kiosk container loads `https://localhost:<XemuHTTPS>` — xemu's noVNC view. linuxserver/xemu's nginx serves `/config/ssl/cert.pem` **only if it exists**, otherwise it generates a `CN=*`, **no-SAN** self-signed cert that modern Firefox rejects. `Manager.Create` → `generateXemuCerts` ([cert.go](internal/podman/cert.go)) pre-writes a proper chain there first: a per-instance **CA** → **SAN-pinned `localhost` leaf** (`cert.pem` = leaf+CA bundle, `cert.key`, `ca.pem`, `ca.key`). Two certs, not one self-signed leaf-as-its-own-root, because NSS refuses to validate a single cert for both CA and server roles.

Trust is then established **host-side** (the reliable, verifiable path): `Manager.Create` → `provisionBrowserTrust` ([browser_cert.go](internal/podman/browser_cert.go)) runs the host's `certutil` to import the CA (`C,,` = trusted TLS root) into the kiosk profile's NSS DB (`browser/config-<name>/profile/cert9.db`) **before the container starts**. jlesage/firefox launches with `-profile /config/profile` (bind-mounted, its cont-init only `mkdir -p`s it, never wipes), so Firefox reads the pre-seeded trust on first launch. This avoids depending on the image having `nss-tools` (it doesn't) or on Alpine Firefox honoring a dropped-in policies.json. The in-container [`60-trust-xemu-cert.sh`](containers/browser/init/60-trust-xemu-cert.sh) is now just the **durable belt**: a Mozilla enterprise policy (`policies.json` with `Certificates.Install`, referencing the bind-mounted `/xemu-cert/ca.pem`), which covers hosts without `certutil` and the `DELETE /files`-then-restart profile-reset path, and survives cert rotation.

**Best-effort:** missing host `certutil` → logged warning, kiosk falls back to the policy belt. Disable the host pre-seed with `CONTAINERS_SET_BROWSER_TRUST=false`; override the binary with `CONTAINERS_CERTUTIL_CMD`. **Verifiable off-box** (no running container needed): `certutil -A -n ca -t C,, -i ca.pem -d sql:<tmp>` then import the leaf and `certutil -V -u V -n leaf -d sql:<tmp>` → "certificate is valid" (exactly Firefox's check); automated as `TestProvisionBrowserTrustImportsCA`. **Pre-existing instances** created before this fix must be re-created (or `DELETE /files` + restart) to pick up the host pre-seed.

### Endpoints

All routes inherit `RequireAuth + RequireAdmin` middleware (see [internal/pocketbase/routes/containers/](internal/pocketbase/routes/containers/)).

| Method | Path                                 | Body             | Returns                         |
| ------ | ------------------------------------ | ---------------- | ------------------------------- |
| GET    | `/api/admin/containers`              | —                | `200` array of `ContainerInfo`  |
| POST   | `/api/admin/containers`              | `{"name":"..."}` | `201` new `ContainerInfo`       |
| GET    | `/api/admin/containers/{name}`       | —                | `200 {"status":"running"\|...}` |
| POST   | `/api/admin/containers/{name}/start` | —                | `204`                           |
| POST   | `/api/admin/containers/{name}/stop`  | —                | `204`                           |
| DELETE | `/api/admin/containers/{name}`       | —                | `204`                           |
| DELETE | `/api/admin/containers/{name}/files` | —                | `204` (or `409` if running)     |
| POST   | `/api/admin/containers/cleanup`      | —                | `200 {"deleted":[…]}`           |

`DELETE /{name}/files` wipes the on-disk bind-mount sources (xemu config + Firefox profile) without touching the container record — useful for resetting a corrupted profile (e.g. stale Firefox `.parentlock`) without losing the entry. Refuses if either container half is currently `running`/`paused`/`restarting` so we don't yank the rug under a live X session.

`POST /cleanup` walks `containers/browser/config-*`, `containers/xemu/configs/*`, and `containers/xemu/shared/hdds/*` and removes any entry that doesn't correspond to a live container record. **Files in `containers/xemu/shared/hdds/` whose basename starts with `_` (e.g. `_default.qcow2`) are treated as protected baselines and skipped.** Both halves rely on `Manager.runSudo` to escalate when the entries are root-owned (which is the default — see below).

Both halves of the container pair run as **root inside the container** (`PUID=PGID=os.Getuid()`, `USER_ID=GROUP_ID=os.Getuid()`). xemu needs that for `NET_ADMIN`/`NET_RAW` (pcap netplay binds raw sockets on the host interface, and Linux projects container caps into the effective set only for root). The browser side matches for symmetry. With `task dev` typically run under `sudo` (so the scraper can read host-side process state), this means the in-container init writes back into bind mounts as host root — leaving config dirs and HDD files root-owned on the host. The two cleanup endpoints above (and the `Cleanup files` button on `/containers/`) are the operator's path to remove those without manually escalating in a shell.

Per-instance ports are allocated stride-wise from `CONTAINERS_PORT_BASE` (default 3100). For `index=0`: HTTP=3100, HTTPS=3101, WS=3102, BrowserWeb=3103, VNC=3104. The browser container points its kiosk Firefox at `https://localhost:<XemuHTTPS>` so the UI is visible at `http://localhost:<BrowserWeb>`.

### Smoke test

1. `task dev` (with `CONTAINERS_ENABLED=true` in `.env`).
2. Authenticate as a superuser (or any user with `isAdmin=true`) via the PocketBase admin UI at `http://localhost:8090/_/`, grab the JWT.
3. `curl -X POST http://localhost:8090/api/admin/containers -H "Authorization: $JWT" -d '{"name":"smoke"}'` → `201` with allocated ports.
4. `curl -X POST http://localhost:8090/api/admin/containers/smoke/start -H "Authorization: $JWT"` → `204`.
5. Browse `http://localhost:3103` → Firefox kiosk → xemu Selkies stream.
6. Server stdout shows `discovery: socket up name=smoke path=...` once the QMP socket appears in `containers/xemu/qmp/`.
7. `curl -X POST .../smoke/stop` then `curl -X DELETE .../smoke` to tear down.

### Discovery watcher

When `CONTAINERS_ENABLED=true` and `CONTAINERS_SOCKET_DIR` is set, the server starts a goroutine that polls the directory every 2s for new `.sock` files:

- `onAdd(name, sock)` → `scrMgr.Start(name, sock)` in a goroutine. Failures (xemu init error, unknown title ID from `scraper.Detect`) are logged but don't kill the watcher.
- `onRemove(name)` → `scrMgr.Stop(name)` (idempotent — silent no-op if the runner is already gone).

So once a smoke-test container's QMP socket appears in `containers/xemu/qmp/`, a scraper auto-attaches; once it disappears, the runner is torn down. Manual `/api/admin/scraper/start` is still useful for sockets outside the watched directory.
