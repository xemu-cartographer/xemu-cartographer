# Runbook

Step-by-step procedures for operating a deployed instance. Each section is a self-contained recipe — copy/pasteable commands, expected output, and recovery steps if something goes wrong.

> Keep procedures here when you find yourself looking up the same steps twice. If a section grows beyond ~one screen, split it into `runbooks/<name>.md`.

## Deploy a new release

The release workflow is a single sequence — `git tag` is the trigger, everything else propagates from it. See [ADR-0001](decisions/0001-version-from-git-tag.md) for the mechanism.

1. **Move `CHANGELOG.md` `[Unreleased]` entries** into a new section `[X.Y.Z] - YYYY-MM-DD`. Pick `X.Y.Z` per SemVer:
   - `feat` → minor bump
   - `fix` → patch bump
   - `feat!` / `BREAKING CHANGE:` footer → major bump
2. **Commit:** `git commit -m "chore(release): vX.Y.Z"`.
3. **Tag:** `git tag vX.Y.Z && git push origin main vX.Y.Z`.
4. **Build:**
   - Binaries: `task build` → `./bin/server` self-reports the new version and `./bin/xc-scraper --version` prints the same `git describe` string (the daemon is built from the sibling `../xc-scraper` checkout by `build:scraper`; the frontend step needs `PUBLIC_PB_PORT` in the environment).
   - Container: `task container:build` → image tagged `yourproject:vX.Y.Z` and `yourproject:latest` (league only — the daemon runs natively, [DEPLOYMENTS.md](DEPLOYMENTS.md)).
5. **Verify locally:**

   ```sh
   ./bin/server serve &
   curl http://localhost:8090/api/version
   # {"version":"vX.Y.Z","commit":"abc1234","date":"..."}
   ```

6. **Deploy** per your environment (push the image to a registry, restart the service, etc.). A tier on wire mode also gets the new `bin/xc-scraper` + `sudo systemctl restart xc-scraper-<tier>` — on pre `srv-pre.sh` does both (the restart via `sudo -n`, or it prints the command when there is no sudoers rule); on prod `srv-prod-stage.sh <ref>` stages and `sudo srv-prod-cutover.sh` swaps + restarts both units (see [DEPLOYMENTS.md](DEPLOYMENTS.md) "Deploying pre" / "Deploying prod"; unit template and install steps: `../xc-scraper/deploy/README.md`); order relative to the league restart does not matter — the daemon spools finished games and the league reconnects.

> If you tag without changing source, `task build` may skip the rebuild because Task's source-cache doesn't track the git tag. Workaround: `task clean && task build`, or touch any `.go` file.

## Back up `pb_data`

_TODO — fill in. Should cover: where the volume lives, how to snapshot it, where backups are stored, retention._

## Restore `pb_data` from backup

_TODO — fill in. Should cover: stopping the service, replacing the volume, verifying ownership (UID 1000), restarting, verifying admin login._

## Rotate secrets

_TODO — fill in. Should cover: which env vars exist, where they're set in production, how to roll them without downtime (or with planned downtime)._

For the LAN token specifically, see [Retire `LAN_SAVES_TOKEN`](#retire-lan_saves_token-and-close-the-console-door) below — it is now an `api_tokens` machine key, not an env var, and rotating one means minting a new key and revoking the old.

## Read the authz boot report

Every boot prints eight `authz:` lines (DESIGN-STEP6 §8.2) right after the seeder and before any route binds. They are the fastest health check for the authorization layer — read them top to bottom after a deploy:

```
authz: roles ok (admin, organizer, member, overlay_manager, anonymous)
authz: admins=1
authz: anonymous scopes=[overlay.read_state:*, room.join:host:*:event_filtered, room.join:host:*:game_filtered, room.join:host:*:scenario, room.join:host:*:tick]
authz: console door OPEN (?console= accepted; PD-1 window)
authz: LAN_SAVES_TOKEN unset
authz: api_tokens machine=2 spectator=3 device=0 (revoked=1, expired=0)
authz: /api/lan/* fail-closed: 2 live machine keys
authz: WS_ALLOWED_ORIGINS unset — fail-open (PD-15), set it before exposing /api/ws
```

| Line | Healthy | Act on |
| --- | --- | --- |
| 1 `roles ok (…)` | The five built-in roles exist. | `authz: roles MISSING: [...] — run migrations` — the `1788300001_roles_scopes` migration did not apply; check `_migrations`. Every scoped decision denies until the rows exist. |
| 2 `admins=<n>` | `n ≥ 1`. | `authz: WARNING no admin users — grant one from /_/ (superuser) or the seed` — log in to `/_/` as the superuser (or set `SEED_SUPERUSER_EMAIL`/`_PASSWORD`) and grant `admin` from `/admin/roles/`. Superusers always pass, so you are never locked out. |
| 3 `anonymous scopes=[…]` | Lists the console-door classes while overlays still use `?console=`; `authz: console door CLOSED (anonymous role has no scopes)` once you have flipped PD-1. | Anything else listed here (e.g. `room.join:host:*:game`) widens what an unauthenticated overlay sees — trim it. |
| 4 `console door OPEN …` / `console door CLOSED (?console= connects but can join nothing)` | Whichever you intend. | Printed once at startup; the state follows line 3. Edits to the `anonymous` row apply live (roles cache 60 s) but the line itself is only re-printed on the next boot. |
| 5 `LAN_SAVES_TOKEN unset` | Unset in production. | `LAN_SAVES_TOKEN imported as kid=legacy-env … WARNING: rotate …` — the env token is still the LAN credential; follow the cut-over below. |
| 6 `api_tokens machine=<n> spectator=<n> device=<n> (revoked=<n>, expired=<n>)` | Matches what you minted. | A surprise count means someone else minted keys — audit `/admin/tokens/`. |
| 7 `/api/lan/* fail-closed: <n> live machine keys` | `n ≥ 1` (or line 5 says imported). | `authz: WARNING /api/lan/* has no valid key — all LAN clients will get 401` — every LAN station is locked out; mint a machine key before the next LAN night. |
| 8 `WS_ALLOWED_ORIGINS unset — fail-open (PD-15) …` | Absent (the variable is set). | Set `WS_ALLOWED_ORIGINS` to your public origin(s) before exposing `/api/ws`; the handshake accepts every origin until you do. |

### The `leaguescraper:` line (step 8 R1)

Right after the `authz:` block (and F1's ninth `authz: XC_SCRAPER_WEBHOOK_TOKEN …` line) the boot prints one `leaguescraper:` line that says which scraper feed this process runs (DESIGN-STEP8 §12):

```
leaguescraper: mode=in-process (XC_SCRAPER_URL unset)
leaguescraper: mode=wire url=http://127.0.0.1:8990 token=set control=set
```

| Line | Healthy | Act on |
| --- | --- | --- |
| `mode=in-process (XC_SCRAPER_URL unset)` | The embedded runner + discovery watcher, as before R1. | If you meant to run the daemon, `XC_SCRAPER_URL` is unset or blank in this unit's environment. |
| `mode=wire url=… token=set control=set` | The league consumes the xc-scraper daemon at `url`; no runner, no league-side discovery. `token=unset` only when the daemon really runs without `--token`. | `control=unset` means the control plane is dead: the daemon always requires a control token (there is no open mode for `/api/ctl/*`), so every admin start/stop, config push and probe relay will answer `401` — set the same `XC_SCRAPER_CONTROL_TOKEN` on both sides and restart. The daemon may still be down at this point — the client reconnects forever and the mirror is empty until it connects; nothing else to do. A boot **error** `leaguescraper: XC_SCRAPER_URL must be http(s)://host[:port]` means the value carries a `ws://` scheme or a path; `XC_SCRAPER_STALE_AFTER must be a positive duration` means a bad Go duration. |

## Switch the scraper feed to the xc-scraper daemon (R1) — and roll it back

R1 is a per-process flag (DESIGN-STEP8 D-4): the same binary runs either the embedded runner or the daemon consumer, decided by `XC_SCRAPER_URL` at boot. The daemon and the league must never both attach to the same QMP directory — each attacher persists every finished game, so two attachers write every game twice.

**Cut over**

1. Start the daemon unit (`xc-scraper-<tier>`, from the template in `../xc-scraper/deploy/xc-scraper.service`: `xc-scraper --watch-dir <the league's CONTAINERS_SOCKET_DIR> --listen 127.0.0.1:<8990 prod | 8991 pre> --state-dir ./xc-scraper-state --game-webhook http://127.0.0.1:<league port>/api/xc/finished_game`, tokens from the tier's `.env` as `XC_SCRAPER_TOKEN` / `XC_SCRAPER_CONTROL_TOKEN` / `XC_SCRAPER_WEBHOOK_TOKEN`, plus `XC_SCRAPER_HOSTRUNNER=true` and `XC_SCRAPER_HOST_DRIVE_MARKER=play-` when the league runs with `HOSTRUNNER_ENABLED`) and confirm its `xc-scraper: listen=… token=set control=set … webhook=set … hostrunner=on|off` boot line + `GET /api/health` (`{"ok":true,…}`, no token needed).
2. Set `XC_SCRAPER_URL=http://<daemon-host>[:port]`, `XC_SCRAPER_TOKEN`, `XC_SCRAPER_CONTROL_TOKEN`, `XC_SCRAPER_WEBHOOK_TOKEN` (same values as the daemon's flags) in the league unit's environment; leave `CONTAINERS_*` as they are (the pod lifecycle stays league-side).
3. Restart the league and read `leaguescraper: mode=wire …`. Once the stream connects the daemon's instances appear on `/admin/pod/`; the next finished game arrives through `POST /api/xc/finished_game` (a `games` row, no runner log lines on the league side). `GET /api/admin/scraper/upstream` (admin JWT) reports the stream's health at any time — `{"mode":"wire","connected":…,"since":…,"reconnects":…,"last_frame_at":…,"seq_gaps":…,"shed":…,"stale":…,"attempts":…,"last_error":…,"auth_rejected":…}` (`{"mode":"in-process"}` when `XC_SCRAPER_URL` is unset; `attempts` = failed dials since the stream was last up, `last_error` the last redacted failure, `auth_rejected` = the daemon admitted the socket but refused `XC_SCRAPER_TOKEN` — `connected` stays true while nothing flows, the league log says `xcclient: upstream … rejected the feed token` once, and `/admin/pod/` shows the banner); it is the one scraper route that keeps answering while the daemon is away (`GET /api/admin/scraper` and the rest answer `503 {"error":"scraper upstream unavailable"}` then). The admin list is the daemon's `GET /api/instances` view (1 s cache): an attach the daemon is still retrying — a typo'd socket, a box whose QMP socket is not up yet — shows as phase `attaching` with its last error, and `POST /api/admin/scraper/{name}/stop` cancels + holds it (`detached`) instead of leaving the name blocked until a daemon restart.

**Roll back** (order matters):

1. **Stop the daemon unit first** (`systemctl stop xc-scraper-<tier>`) so it can no longer attach or post games. Do NOT merely restart it without `--game-webhook` / with the webhook unset: the daemon spools every finished game it could not deliver under `<state-dir>/spool/` and replays the spool as soon as a webhook is configured again, so games from the rollback window would be persisted a second time on the next cut-over. If the daemon did run webhook-less during a rollback, delete the spool files for that window before cutting over again.
2. Unset `XC_SCRAPER_URL` in the league unit's environment (the other `XC_SCRAPER_*` values may stay; `XC_SCRAPER_WEBHOOK_TOKEN` keeps the ingest key alive, which is harmless).
3. Restart the league and read `leaguescraper: mode=in-process (XC_SCRAPER_URL unset)`; the discovery watcher re-attaches every socket in `CONTAINERS_SOCKET_DIR` within one poll.

Doing 2–3 before 1 leaves the daemon and the embedded runner attached to the same sockets: both persist every finished game (duplicate `games` rows) until the daemon is stopped.

## Retire `LAN_SAVES_TOKEN` and close the console door

The authz batch removed LAN "open mode" and made every LAN / overlay credential an `api_tokens` row. Both `roles.scopes` and `api_tokens` are **additive** — reverting the binary leaves them inert, so there is no rollback step beyond redeploying the previous release. Do the steps in this order; each one is safe to pause on.

1. **Mint one `machine` key per LAN station, scoped `lan.*`, while `LAN_SAVES_TOKEN` is still set.** From Studio, `/admin/tokens/` → Mint → kind `machine`, label it after the station, scopes `lan.*`. Or from a shell with an admin JWT:

   ```sh
   curl -X POST https://<host>/api/admin/tokens \
     -H "Authorization: Bearer $ADMIN_JWT" -H 'Content-Type: application/json' \
     -d '{"kind":"machine","label":"lan-station-1","scopes":["lan.*"]}'
   # → 201 {"kid":"mk_…","token":"mk_….<secret>", …}   ← the token is shown ONCE
   ```

   Put the returned token on the station (it goes where the shared token went: `X-LAN-Token`, `?token=`, or `Authorization: Bearer`). The old env token keeps working meanwhile — it is imported at boot as kid `legacy-env` and shows read-only in the list.

2. **Verify each station syncs with its own key** (`GET /api/lan/sync/manifest` with the new token → 200). Boot line 7 should now count your keys: `authz: /api/lan/* fail-closed: <n> live machine keys`.

3. **Unset `LAN_SAVES_TOKEN` and restart.** Line 5 flips to `authz: LAN_SAVES_TOKEN unset` and the `legacy-env` row disappears from `/admin/tokens/`. (Trying to revoke it from the UI answers `409 unset LAN_SAVES_TOKEN instead` — the env var is the only switch.)

4. **Mint spectator keys for every OBS browser source.** `/admin/tokens/` → kind `spectator`, pick the instance and the classes the source needs. Every OBS surface joins `game_filtered` + `tick`; `/scorebug/`, `/leaderboard/` and `/postgame/` add `scenario`, `/overlay/` (POV) adds `event_filtered` instead — so the practical grant is all four classes plus `overlay.read_state:<inst>`, which is exactly the spectator preset both `/admin/tokens/` and `/studio/` mint (`spectatorScopesFor`). Users holding `overlay_manager` can do this without the admin role (`overlay.mint`). Add `&spectator=<key>` to each OBS browser-source URL — the overlay pages keep `?console=<name>` as the "which console" selector and pick the key up off the page URL themselves, putting it on the socket as `?spectator=` (raw WS clients pass `?spectator=<key>` directly).

5. **Leave the `anonymous` role's scopes seeded until every overlay URL carries `?spectator=`.** Boot line 4 reads `console door OPEN` throughout; that is expected during the migration.

6. **Flip PD-1: empty the `anonymous` row's `scopes`** in the PocketBase dashboard (`/_/` as the superuser → `roles` → `anonymous` → `scopes: []`; the `roles` collection has no API mutate rule, so this is the one place it can be edited). Takes effect on live sockets within about two minutes (the adapter's roles cache is 60 s and every socket re-resolves its principal every 60 s), no restart. The next boot reads `authz: console door CLOSED (?console= connects but can join nothing)`. Any overlay still on `?console=` goes blank — that is how you find the stragglers.

7. **Rotating a key later** is mint-new → move the client → `DELETE /api/admin/tokens/{kid}` (optional `{"reason":"…"}` body) on the old one. Revocation reaches connected sockets within 60 s (`session_revoked`, close 4401).

## Recover from "PocketBase admin locked out"

_TODO — fill in. PocketBase has a CLI for resetting admin password; document the exact command and any caveats around running it against a live container._

## Reset the dev environment

```sh
task clean              # wipes bin/, tmp/, pb_public/
cd sveltekit && pnpm install
task dev                # fresh seed each run (Air uses -tags dev)
```

Dev DB is ephemeral — `tmp/pb_data/` is wiped by Air on exit. See CLAUDE.md for the full story.

## The dev loop (step 8): three processes

`task dev` (or `task dev:lan`) runs three things in parallel, each with hot reload:

| process | task | listens | how to check |
| --- | --- | --- | --- |
| xc-scraper daemon | `dev:scraper` — `air -c .air.toml` in `../xc-scraper` | `127.0.0.1:8992` | `curl -s 127.0.0.1:8992/api/health` → `{"ok":true,"version":"dev",…}`; boot line `xc-scraper: listen=127.0.0.1:8992 …` |
| Go backend | `dev:backend` — Air, `-tags dev` | `PUBLIC_PB_PORT` | `curl -s 127.0.0.1:$PUBLIC_PB_PORT/api/health`; boot line `leaguescraper: mode=wire\|in-process …` |
| SvelteKit | `dev:frontend` — Vite | `5173` (Vite default; `./run-dev.sh` uses `19099`) | the browser |

- The daemon's env comes from the Taskfile (`XC_SCRAPER_LISTEN`, `XC_SCRAPER_WATCH_DIR=<repo>/containers/xemu/qmp`, `XC_SCRAPER_GAME_WEBHOOK=http://127.0.0.1:$PUBLIC_PB_PORT/api/xc/finished_game`, `XC_SCRAPER_STATE_DIR=../xc-scraper/tmp/xc-scraper-state`) plus whatever `XC_SCRAPER_*` twins are in `.env` (tokens, `XC_SCRAPER_HOSTRUNNER=true`, …). The state dir lives under xc-scraper's `tmp/` on purpose: `task clean` here does not wipe it, so a pushed control document and spooled games survive a league restart the way they do on a tier.
- The league only **consumes** the dev daemon when `.env` has `XC_SCRAPER_URL=http://127.0.0.1:8992`. Without it the backend boots in-process and, with `CONTAINERS_ENABLED=true`, both would attach `containers/xemu/qmp/` — set the URL, or run `task dev:backend` + `task dev:frontend` without the daemon.
- `task dev` still needs `sudo` when containers are on (D-13: the PUID story), so the tools must be on root's `PATH` (`/usr/local/bin`, README Prerequisites). After a `sudo task dev` the `.task/` cache dir is root-owned; a later unprivileged `task build` fails with `open .task/checksum/…: permission denied` — run it with `TASK_TEMP_DIR=/tmp/xc-task` (or `sudo chown -R $USER .task`).
- Air kills the daemon with SIGINT on every rebuild (`send_interrupt`, 6 s grace); the attach loop re-attaches the sockets on the next boot and the league reconnects within its backoff.

Without Task/Air (or when `sudo` is not available): build both binaries (`task build` or `go build`) and run them by hand — `bin/xc-scraper --allow-empty --listen 127.0.0.1:8992 --state-dir /tmp/xc-dev/state` and `XC_SCRAPER_URL=http://127.0.0.1:8992 bin/server serve --http=127.0.0.1:8149 --dir /tmp/xc-dev/pb_data` (`--allow-empty` lets the daemon idle with no `--watch-dir`). On the local go1.27 toolchain the **league** binary must be *built* with `GOEXPERIMENT=nodwarf5,nojsonv2` exported — a default-experiment build dies at boot with `fatal error: stack overflow` in `migrations.init` (jsonv2 vs PocketBase's `Collection.UnmarshalJSON`); the daemon has no PocketBase and does not care. Verified 2026-09-07: daemon `--allow-empty` on `:8992` + league on `:8149` with `XC_SCRAPER_URL` prints `leaguescraper: mode=wire url=http://127.0.0.1:8992 …`, `xcclient: connected …`, `xcclient: hello: 0 instance(s), 1 room(s) joined`, then `configpush: … 403 control_disabled` until the daemon gets a `--control-token` (expected).
