# Status

> **Last updated:** 2026-09-28 (xc-studio S0: the M10 / M25 / Stream Studio / M28 / M11 / M12 "Now" rows closed against the commits that removed their surfaces, one `xc-studio` row added). Prior: 2026-08-31 (M31 pre-freeze hardening added to Now; the 2026-08-29/30 four-branch release below is SHIPPED to `main` + prod); 2026-07-02 live CE+H2 offset verification on the 3way-systemlink rig — see **Live offset verification** below.

The single-pane view of where this project is right now. Update whenever "Now" changes.

## Shipped 2026-08-29/30 — the four-branch release (PR #33, merge `a5a7de9`)

The OBS overlay re-skin, the organizer six-page redesign (M29), the settings
five-tab consolidation (M30), and the Halo 2 offsets unblock (M20) all landed on
`main` via PR #33 and are **live in prod** (`/srv/http/xemu-cartographer/prod`,
commit `cce741a`, cutover 2026-08-29 20:58 PDT — 7 migrations applied). The
`update/*` feature branches they were authored on are merged and deleted; only
`main` and `beta` remain. Per-milestone detail in the sections below.

**Still open across that release:** Gametypes/Rulesets field truing against
in-game screenshots + a live pass with a real ingested disc (M29); the Teams tab
design that un-parks the membership surface, and the H2 profile control-byte
mapping that enables the settings Controls panel (M30); H2 assists/score/
kill-streak/team-scores and the Slim client/server offset sets (M20).

## M30 player settings redesign

The `/gamertag/` page and the old `/settings/` layout consolidated into one
five-tab Settings page (General · Halo: CE (WIP) · Halo 2 (WIP) · Stream ·
Accounts). New stream identity: `users.motto` + `users.nameplate` (curated
picker over the M29 nameplates pool, selectable-guard hook), served to overlays
through `/api/public/profiles` → `player.motto`/`player.plateBg` on the plate.
Default-gamertag changes now sync `users.gamertag` and regenerate both signed
profiles. **Parked by decision**: the old Teams/membership settings surface
(components stay in-tree, awaiting the Teams tab design). See
[milestones/M30-settings-redesign.md](milestones/M30-settings-redesign.md).

## M29 organizer route redesign

The `/organizer/` tabs became six rail pages (designer handoff): **Offsets**
(runtime offset-set imports, delete-with-migration, scraper dynamic source),
**Discs** (role model play/server/shelved + allow-on-xbox, replaces the
available bool), **Maps** (canonical build catalog keyed game+filename+hash,
variant curation, power items), **Gametypes** (creator absorbed; library vs
in-game name; server-schema fields), **Rulesets** (gametypes + pool + size +
series), **Nameplates** (600×100 banner library with the exact-geometry plate
crop; text-outline deviation back-ported to the overlay NamePlate).
Smoke-verified on a fresh dev DB; remaining: Gametypes/Rulesets field truing
against in-game screenshots + a live pass with a real ingested disc. See
[milestones/M29-organizer-redesign.md](milestones/M29-organizer-redesign.md).

## Live offset verification (2026-07-02) — CE + H2, unblocks M19/M20

Ran a live xemu offset-verification session on the `scripts/3way-systemlink/` rig
(branch `feat/halo2-scraper`) + the `../halo-offset-mapper` runtime tooling. Findings +
provenance committed & pushed in **halo-offset-mapper** (`docs/ce-offset-mapping-2026-07-02.md`,
`docs/h2-offset-confirm-2026-07-02.md`).

- **Rig proven from cold:** `udp_reflector.py` + `launch-instance.sh {1,2}` → 2 CE
  instances → **system link confirmed end-to-end** (host game discovered across the
  reflector, join, START) → live 2-player Blood Gulch Slayer FFA match. `launch-h2.sh`
  → H2 boots + drives to a live splitscreen match.
- **CE:** foundation/player/biped/weapon/object + system-link network layer offsets all
  re-verified live (zero corrections). NEW: `OffBipedCrouchScale 0x464` static-derived →
  **runtime-verified** (crouch induced); `AddrScoreSlayer 0x276710` **FFA per-player
  indexing confirmed** (prior open item); attributed cross-player kill reconfirmed on
  Blood Gulch (client stayed connected — no desync on open FFA).
- **H2:** phase-1 GameReader offsets **runtime-verified live** in an H2 match (Turf) —
  players/objects arrays, biped health/shield/max(45/70)/pos/frags(2), Battle Rifle
  mag 36/reserve 72, title id `0x4D530064`. This is the H2 scraper read-path piece that
  was "unverified without xemu".
- **Reusable tooling added** (halo-offset-mapper): `scripts/runtime/ce_lobby.sh`
  (system-link lobby driver — gated building blocks; menu nav must be screenshot-verified,
  not reliably hands-free) + a `tag-handle` schema-kind fix (unblocked `offsetmap validate`).
- **Remaining:** multi-instance **H2 system link** (reflector recipe applies; needs a
  2-console H2 match) + H2 combat-stat induction; CE objective-tick values (CTF capture /
  Oddball possession — physics enter-edge wall); vehicle-seat / overshield / FFA-leader /
  friendly-fire / time-limit. Robust hands-free menu nav would need screenshot/OCR gating.

## Repo housekeeping (2026-07-01)

Patchwork/backup pass over the 6-worktree working set. **Nothing was merged into `main`**; all changes are committed on their own branches and pushed.

- **Backup (was the top risk):** `main` (74 commits ahead of origin, unpushed) + all 17 local feature/chore branches are now pushed to `origin` and tracking. Previously only an old `main` existed on the remote.
- **In-progress work committed:**
  - `chore/xemu-test-harness` — harness now uses xemu 0.8.136's native `xemu` display backend instead of stock-QEMU `sdl` (which that build doesn't compile), fixing the visible-window launch.
  - `feat/spartan-skinfix-ce-markv` + `feat/spartan-real-poses` — added `tools/h2-model/.gitignore` (mirrors `tools/ce-model/`) so `__pycache__/` + `out/mcc/composite_cmp/` render artifacts stop showing as untracked.
- **Build/test:** green on `feat/broadcast-graphics` (= `main` + M28) — Go `build`/`vet`/all tests; frontend `lint`/`check` (0 err)/`test` (282)/`build`.
- **Stashes triaged — all preserved, none applied:**
  - `stash@{0}` (orphaned, ex-Stream Studio hub branch — the hub itself went in `eda3056`) — **obsolete**: the visualizer `?map=` demo-mode WIP was finished + merged as `223c718` and has since evolved (`mockStagedModel`). Safe to drop; left in place.
  - `stash@{1}` (`feat/json-seeder`) — its `02-patch-toml.sh` netif fix is **already on `main`** (functionally identical); the only real delta is a `seed.example.json` credential change → **Stewart's call**.
  - `stash@{2}` (`chore/align-dev-seed-creds`) — `data.go` seed-credential approach → **Stewart's call** (competes with json-seeder; deliberately left, per the deferred-seeder decision).
- **TODO/FIXME:** only 3 in source (Halo:CE offset provenance + an M7 live-verify note) — none quick/safe (all need live xemu). Left as-is.

**⚠️ Directory move:** this repo drives 6 git worktrees under `/home/stew/repos/` (`xemu-cartographer`, `xemu-cart-markv`, `xemu-cartographer-emblem-fix`, `-harness`, `-overlay`, `-spartan-poses`). After moving the parent dir, run `git worktree repair` from the main checkout (passing the moved worktree paths) to fix the absolute gitdir links — otherwise the linked worktrees detach.

## Goals

xemu-cartographer is a real-time game-state scraper for Xbox titles running in [xemu](https://xemu.app/), rebuilt on top of a clean Go + PocketBase + Disgo + SvelteKit template.

Prior implementation is preserved at [atlas/xemu-cartographer-legacy/](../atlas/xemu-cartographer-legacy/). HaloCaster (the older Halo-specific Python/C# sibling) is at [atlas/HaloCaster/](../atlas/HaloCaster/) and holds the richest set of Halo: CE memory offsets. Everything under `atlas/` is **reference-only and must be re-verified before porting** — offsets, patterns, and APIs may have drifted or been wrong to begin with.

Milestones, not dates. Generally each blocks the next, though M03 was ported early (out of sequence) to provide a test substrate for M01+M02 — see [M03 status](milestones/M03-container-lifecycle.md).

## Now

- [ ] [M32 — xc-scraper daemon + wire-only cut-over (step 8)](milestones/M32-xc-scraper-daemon.md) — **in progress** (`feat/xc-restructure` + sibling `../xc-scraper` `main`, 2026-09-07). The scraper runs as its own process: `cmd/xc-scraper` (trimmed WS hub, attach loop, layered config + `control.json`, read/control API, spool-first finished-game webhook, TCP QMP, in-daemon host runner behind `--hostrunner`) and the league's R1 dual mode (`XC_SCRAPER_URL` set ⇒ `internal/xcclient` stream client + mirror, byte-identical rebroadcast, config pusher, `POST /api/xc/finished_game` ingest; unset ⇒ in-process as before). `task dev` runs the daemon on `:8992`, `task build` yields `bin/server` + `bin/xc-scraper`, unit template in `../xc-scraper/deploy/`, `-race` in CI. Decision: [ADR-0005](decisions/0005-wire-only-scraper-cutover.md). Remaining: owner gates (push `xc-scraper`, ports `8990-8992`, units, `srv-pre.sh` sibling checkout), pre soak on wire mode, then R2 (delete in-process).
- [ ] [M31 — xc-scraper pre-freeze hardening](milestones/M31-xc-scraper-prefreeze-hardening.md) — **in progress** (`feat/xc-prefreeze-hardening`, 2026-08-31). The 8-item pre-freeze fix list ahead of extracting the scraper to `xc-scraper`: complete oldest-first `previous_game` event log (+`events_truncated`), unified 11-class registry for hello/sinks, per-sender resync (anonymous cross-replay fixed), `game_uid` + `end_reason` on the finished-game artifact + bounded shutdown flush, `offsets.RegisterBaseline` + panic-free resolve, QMP dial/command deadlines, transactional idempotent `PersistFinishedGame` on a `games.game_uid` unique index. All landed with regression tests; remaining: the live end-to-end match pass + DESIGN item 9 (`--sock` retry / dup basenames). Design: the local `xemu-cartographer-split/scraper-repo-design/` workspace.
- [ ] [M09 — Match-aware kiosk view](milestones/M09-match-aware-kiosk.md) — **in progress** (`wip/milestone-9`). 9a/9b/9c implemented end-to-end (membership lookup + `/api/me/match` + `/play/` page + WS & kiosk/VNC proxy roster-narrowing); all CI checks green. Remaining: the live 4-container smoke test (podman-gated, can't run in CI).
- [x] [M10 — Overlay revamp + new browser sources](milestones/M10-overlay-revamp.md) — **closed 2026-09-28.** The 10d data foundation lives on as `xc-scraper/roster` + the `game_filtered` / `event_filtered` classes; the M10 render surfaces went with `/overlays/*` in `eda3056` (2026-07-18) and the overlay-token auth layer was removed wholesale in `76807ad` (2026-08-10), superseded by spectator keys (`/admin/tokens`). The "door class not validated server-side" limitation is fixed (`authz.Can` on the parsed room). See the milestone log.
- [x] [M25 — OBS spectator overlays](milestones/M25-obs-overlays.md) — **closed 2026-09-28 (superseded).** The `/overlays/[instance]/` scoreboard + status strip were deleted in `eda3056` (2026-07-18) without a live pass; the successor surface is `/overlay/ /scorebug/ /leaderboard/ /postgame/` (`58459f2`, 2026-08-08) — see the xc-studio row.
- [x] **Stream Studio — browser-source hub** — **closed 2026-09-28 (superseded).** The `/studio/` gallery, its asset registry, `AssetPreview` and the timer + kill-feed tiles were deleted in `eda3056` (2026-07-18); `/studio/` is now the OBS surface picker + spectator-key minter. [STREAM-ASSETS.md](STREAM-ASSETS.md) is a retirement note.
- [x] [M28 — Broadcast graphics](milestones/M28-broadcast-graphics.md) — **closed 2026-09-28 (superseded).** The themed scoreboard + player-card routes went with `/overlays/*` (`eda3056`); `components/broadcast/*`, `broadcast-profiles` and `overlay-params.ts` were deleted in `58459f2` (2026-08-08). `GET /api/public/profiles` (born here, `27fc09c`) is the one piece still in the tree (`internal/pocketbase/routes/public_profiles.go`).
- [x] [M11 — Game minimaps](milestones/M11-game-minimaps.md) — **closed 2026-09-28 (abandoned).** `minimap.ts` and the 2D minimap went with the visualizer family in `76807ad` (2026-08-10, "superseded by a nicer external version"). Not planned for the flagship.
- [x] [M12 — POV marker overlay (stretch)](milestones/M12-pov-marker-overlay.md) — **closed 2026-09-28 (abandoned).** `pov-projection.ts` was removed in `76807ad` (2026-08-10); the 12a camera-offset audit never ran.
- [ ] **xc-studio — the OBS overlay surface is being extracted** to the private sibling `xemu-cartographer/xc-studio` (design signed 2026-09-28; league-first). The flagship keeps `/overlay/ /scorebug/ /leaderboard/ /postgame/` until the studio's cut-over, then serves 302s to the studio origin. S0 (2026-09-28) was flagship prep only: catalog sizes, the overlay faces in the organizer/settings NamePlate previews, dead code/asset deletion (`static/spartan/`, `three`, the vendored pack's foreign-feed files), the resolver's upstream-down 503, and this docs retirement.
- [ ] [M13 — PocketBase persistence foundation](milestones/M13-pb-persistence-games-series.md) — **in progress** (`wip/milestone-13`; chain wired on `wip/game-end-chain`). Schema + writer + heuristic landed. **`game_events` fork resolved (option a)** + full game-end chain wired (events stamping + series advance + rating) with the Live→Ready trigger; integration-tested. Remaining: live verification, 13d durable queue.
- [ ] [M14 — Series management](milestones/M14-series-management.md) — **in progress** (`wip/milestone-14`). `internal/series.Progress` landed; **14d completion wiring landed** in the game-end chain. Setup / pick-ban / in-progress UIs deferred.
- [ ] [M15 — Per-user / per-team stats](milestones/M15-stats.md) — **in progress** (`wip/milestone-15`). Stats aggregation + query layer (`internal/stats`) landed (unit-tested); now reads real persisted games via the chain. Stats/match-history/dummy-override UIs deferred.
- [ ] [M16 — Tournament system](milestones/M16-tournament-system.md) — **in progress** (`wip/milestone-16`). Bracket generators (`internal/bracket`: single-elim + round-robin) landed (unit-tested). Schema/UI/wiring + double-elim/Swiss deferred.
- [ ] [M18 — Rating system + leaderboards](milestones/M18-rating-leaderboards.md) — **in progress** (`wip/milestone-18`; recompute wired on `wip/game-end-chain`). Elo + leaderboard ranking landed; **18b recompute wired** + `ratings` store added. Leaderboard pages, Discord cmds deferred.
- [ ] [M17 — Discord integration](milestones/M17-discord-integration.md) — **in progress, offline** (`wip/m17-discord`). `discord_guilds` config + `internal/discordcfg` filter + stats/recent/cartographer commands + embed builders + the `games`-insert post hook (FireAndForget, no-op offline) all built + unit/integration-tested. **Gateway intentionally not connected** — live verification (command sync, interactions, real posts, 17d tournament wiring) needs a supervised session.

## Next

Listed by intended execution order (not strict numerical order). M22 and M23 are claimed slots from the M21+ open bucket, drafted alongside M07's scope expansion on 2026-05-26. M07 closed 2026-05-26; M22 closed 2026-05-31; M23 closed 2026-06-02; M08 closed 2026-06-04; M26 (HDD copy-on-write overlays) closed 2026-06-20.

- M19 — Robustness + offset validation (needs live xemu memory)
- M20 — Halo 2 scraper (with known caveats)
- M21+ — Open bucket

## Maybe

- **Milestone-open marker tag.** Cut `vX.Y.0-alpha.0` as the first commit of every new milestone so `git describe` (and therefore `/api/version`) self-identifies which milestone the running binary belongs to: `v0.7.0-alpha.0-5-gXXXX` reads as "5 commits into M7." Escalate to `-alpha.1` / `-beta.1` / `-rc.1` only for shareable mid-milestone checkpoints; cut `vX.Y.0` at milestone close. Promote to an ADR (likely 0002) once the workflow's been used on a milestone or two and pulls its weight.

## Out of scope

- Desktop GUI (WinForms, DearPyGui) — web is the UI.
- `cmd/{memscan,prove,localproof}` offset-discovery tools — re-derive on demand.
- Halo-specific logic leaking into `xc-scraper/xemu` or the top-level `xc-scraper/scraper` — domain code stays in the game plugins (`xc-scraper/haloce`, `xc-scraper/halo2`). None of it lives in this repo any more: the readers, runner and wire contract are the sibling module `github.com/xemu-cartographer/xc-scraper`.
