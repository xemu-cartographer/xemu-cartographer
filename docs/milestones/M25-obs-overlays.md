# M25 — OBS spectator overlays (token-auth render pages)

> **Status:** Closed 2026-09-28 — superseded; the render pages were deleted in `eda3056` (2026-07-18) before any live pass. See the Log.
> **Started:** 2026-06-20
> **Completed:** — (closed 2026-09-28 without the live pass; see Log)
> **Depends on:** [M10 — Overlay revamp + new browser sources](M10-overlay-revamp.md) (overlay-token auth + dummy filter), [M05 phase model](M05-phase-model-refactor.md) (v2 WS feed), [M09](M09-match-aware-kiosk.md) (membership/roster)

## Goal

Ship the first **render surfaces** for the OBS overlay path that M10 left deferred ("10b scoreboard surfaces… need live data + OBS"). M10 already shipped the hard part — the scoped, revocable, read-only **overlay token** + two-door WS handshake — and the mint UI emits a URL pointing at `/overlays/<instance>/?token=…`, a page that didn't exist yet. M25 builds that page (plus a second surface), so a streamer can paste a minted URL straight into OBS as a transparent **Browser Source** and see a live scoreboard. A **mock mode** lets the overlays be styled and demoed with no live game.

## Scope

**In:**

- Two transparent-background overlay render pages consuming the live spectator WS feed (the v2 `scraperWSV2` store), authenticated via the M10 overlay token (read-only):
  1. **Scoreboard / roster** (primary) at `/overlays/[instance]/` — the URL the mint page already produces. Players grouped by team, names, alive/dead, health + shield bars, K/D/A + team scores (best-effort).
  2. **Match-status strip** at `/overlays/[instance]/status/` — compact top bar: phase (LIVE/SETUP/IDLE), team scores, gametype/variant, map, score limit.
- A **mock/sample-data mode** (`?mock=1`) so the overlays render an animated 4v4 Team Slayer with no live game and no token.
- Mint-page (`/overlays/manage/`) enhancement: emit **both** overlay URLs bound to the one scoped token + a mock-preview link.

**Out (deferred):**

- Minimap / POV overlays (M11/M12 — need traced map assets + projection rendering).
- Event/kill-feed popup overlay (M10c) — buildable from the live `event` room (read-only-joinable), but a follow-up.
- POV-bound routing to a specific guest machine (M10a/10e) — these pages bind to a **container instance** (matching the shipped token scope), not a per-machine POV seat.
- Server-side dummy-roster filtering on the broadcast feed (see Caveats).

## Route shape — divergence from the M10a sketch

M10a sketched `/overlays/<surface>/<machine>/`. The **shipped** M10 mint handler ([internal/pocketbase/routes/overlays/handlers.go](../../internal/pocketbase/routes/overlays/handlers.go)) already emits `/overlays/<instance>/?token=<jwt>` and the overlay token is scoped to one `host:<instance>` room. M25 follows the **shipped token contract** rather than the older sketch: surfaces are sub-routes of the instance (`/overlays/<instance>/` and `/overlays/<instance>/status/`) so the **same scoped token works for every surface** (the token binds an instance, not a view). POV-by-machine routing remains an M10a/10e follow-up.

## Architecture

- **Pure view-model builders** — [sveltekit/src/lib/utils/overlay-view.ts](../../sveltekit/src/lib/utils/overlay-view.ts): `buildScoreboard(game, tick)` + `statusStrip(game, scenario)`. IO-free, fully unit-tested ([overlay-view.test.ts](../../sveltekit/src/lib/utils/overlay-view.test.ts)). They join the roster (identity + cumulative stats) with the latest tick (alive/health/shields), group by team, and flag best-effort scores via `hasScores` so the UI degrades instead of asserting numbers we can't trust.
- **Feed lifecycle** — [sveltekit/src/lib/stores/overlay-feed.svelte.ts](../../sveltekit/src/lib/stores/overlay-feed.svelte.ts): `createOverlayFeed()` wraps the shared `scraperWSV2` singleton for the live path and swaps in animated sample data for `?mock=1`, behind one uniform set of getters. Live path subscribes only to the per-class rooms the overlay token may join (`game`/`tick`/`scenario`) — never `host:summary`, never `request_*` (the Hub rejects both for an overlay connection).
- **Mock data** — [sveltekit/src/lib/utils/overlay-mock.ts](../../sveltekit/src/lib/utils/overlay-mock.ts): a believable 4v4 Team Slayer on Bloodgulch; the feed ticks a frame counter ~5 fps so bars wobble and scores creep (the preview looks alive).
- **Render pages** — self-contained, transparent canvas. `<svelte:head>` neutralises the theme's body `background-image` **and** `body::before/::after` decorations (the `xbox` theme's hex mesh would otherwise composite into the capture), sets `overflow:hidden`, kills scrollbars. The route prefix `/overlays/` is already in `HIDDEN_LAYOUT_PATHS`, so no app nav/header chrome renders. Anonymous load (no auth redirect) — the token in the URL is the credential, validated by the WS handshake.

## Live-verified vs best-effort

The overlays lean on the **runtime-verified** parts of the feed and degrade the rest:

- **Verified / load-bearing:** player names, teams, alive status, health/shields, map name, gametype/variant, phase.
- **Best-effort (not yet runtime-verified — pending a real match):** per-player **kills/deaths/assists/score** and **team scores**. Builders surface them but set `hasScores=false` when everything is zero, and the UI mutes K/D/A to `—` and hides team scores in that case. When the numbers do arrive they render; if they turn out wrong on the live pass, only these fields are affected.

## How to test tonight (OBS)

**A. Preview with mock data (no backend state needed, fastest):**

1. `task dev` (frontend + backend). Frontend on Vite (`:5173`), backend PocketBase on `:PUBLIC_PB_PORT` (default `:8090`).
2. Open the scoreboard mock: `http://localhost:5173/overlays/demo/?mock=1` and the status strip: `http://localhost:5173/overlays/demo/status/?mock=1`. (`demo` is a throwaway instance name — any name works in mock mode.) You should see a 4v4 Team Slayer scoreboard / a `RED 31 : 27 BLUE … BLOODGULCH` strip, animating. Add `&scale=1.5` to enlarge for a quick look.

**B. Live overlay with a real token:**

1. `task dev`, sign in as an admin or a user holding the `overlay_manager` role.
2. Go to **`/overlays/manage/`**. Enter the **container/instance name** (the scraper runner / container name — e.g. `pod-a`) and an optional label, click **Mint token**.
3. The success card shows two ready-to-paste absolute URLs bound to that one token — **Scoreboard / roster** and **Match-status strip** — each with a copy button. Copy them now (the token isn't shown again).

**C. Add to OBS:**

1. OBS → Sources → **+** → **Browser**.
2. Paste the copied overlay URL. Set **Width 1920, Height 1080** (overlays self-anchor: scoreboard top-left, status strip top-center). Leave "Shutdown source when not visible" off so it stays connected.
3. Tick **"Refresh browser when scene becomes active"** for clean reconnects. The page background is fully transparent — the overlay composites over your game capture.
4. Repeat for the status-strip URL as a second Browser Source. Position/scale each in OBS, or use `&scale=` on the URL.

## What Stewart must wire for the live test

- A **running scraper instance** for the minted instance name. Easiest path: `CONTAINERS_ENABLED=true` + a container whose QMP socket the discovery watcher picks up, OR a manual `POST /api/admin/scraper/start`. The instance name in the mint form must match the runner/container name (the `host:<instance>` the token scopes to).
- A **live Halo: CE match** in that instance to populate roster + tick. Until a match is Live, the scoreboard shows "Waiting for match…".
- This is also the pass that **verifies the best-effort fields** (K/D/A, team scores) against real memory.

## Caveats / follow-ups

- **Dummy-roster filter not applied to the broadcast.** `internal/scraper/roster.FilterRoster` (M10d) exists but isn't wired into the manager's broadcast `game` payload, so a neutral-host out-of-bounds dummy could appear in the live scoreboard. Mock data has none. Follow-up: apply the filter at the broadcast (or overlay) layer. (See [M10 §10d](M10-overlay-revamp.md).)
- The legacy `/overlays/players/` page still authenticates with a **user JWT** (not an overlay token) and only neutralises `background` (not the theme `::before` mesh). M25's pages supersede it for the OBS path; converging/retiring `players/` is a follow-up.
- Multi-team (>2) status strip shows only the first two teams in the head-to-head; the scoreboard shows all teams. FFA renders a ranked flat list on the scoreboard.

## Actions

- [x] Pure `buildScoreboard` / `statusStrip` view-model builders + unit tests
- [x] `overlay-mock.ts` animated sample data + `createOverlayFeed()` live/mock lifecycle (overlay-token, read-only rooms only)
- [x] `/overlays/[instance]/` scoreboard render page (transparent, OBS-sized, no scrollbars)
- [x] `/overlays/[instance]/status/` match-status strip render page
- [x] `/overlays/manage/` emits both overlay URLs + mock-preview link
- [x] `pnpm check` / `lint` / `test` / `build` green; headless render of both surfaces with mock data confirmed
- [ ] Live verification against a real Halo: CE match (roster/tick render, K/D/A + team scores correctness)
- [ ] Wire `roster.FilterRoster` into the broadcast/overlay path (dummy filter)

## Verification

- `pnpm check` (0 errors/warnings), `pnpm lint`, `pnpm test` (overlay-view builders covered), `pnpm build` (adapter-static; dynamic `[instance]` served via SPA fallback) — all green.
- Headless (Playwright) render at 1920×1080 of `/overlays/demo/?mock=1` and `/overlays/demo/status/?mock=1`: scoreboard + strip render correctly (dead/respawn dimming, camo/overshield pips, MOCK badge, LIVE pulse). Computed styles confirm `html`/`body` background `rgba(0,0,0,0)`, `background-image:none`, `body::before` suppressed, `overflow-x:hidden`, no scrollbars. The no-token live URL shows the "mint a token / append ?mock=1" hint.
- **Pending:** the live match pass (see "What Stewart must wire").

## Log

_Append-only. Never edit past entries; add a new dated line._

- 2026-06-20: created — scoreboard + status-strip render pages on the shipped M10 overlay-token contract, mock mode, mint-page URL emission; mock-verified end-to-end (check/lint/test/build + headless render). Live match pass + dummy-filter wiring outstanding.
- 2026-09-28: **Closed (xc-studio S0) — superseded.** The `/overlays/[instance]/` scoreboard + status strip and the `/overlays/manage/` mint page were deleted in `eda3056` (2026-07-18), four weeks after this milestone opened; the live pass never ran. The overlay-token contract they rendered on went in `76807ad` (2026-08-10). The current OBS surface is `/overlay/ /scorebug/ /leaderboard/ /postgame/` (`58459f2`, `?console=` + `?spectator=`), now being extracted to the private `xemu-cartographer/xc-studio` repo. **Retired from the OBS steps above:** do not tick "Refresh browser when scene becomes active" (it reloads the postgame source and blanks MATCH TIME), and sources are no longer 1920×1080 — each graphic is sized to its content (catalog in `sveltekit/src/routes/studio/+page.svelte`).
