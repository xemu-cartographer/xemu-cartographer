# Code-Structure Review — xemu-cartographer

> **Date:** 2026-07-12
> **Author:** Claude (overnight read-only review, prepared for Stewart)
> **Scope:** the codebase's *organization* — repo layout, Go packages, file sizes, the SvelteKit tree, cross-cutting conventions, and the halo-offset-mapper seam. Reviewed on branch `docs/architecture-review-2026-07-12` (identical to `main` except the pre-existing uncommitted `go.mod` edit).
> **Status:** Structural retrospective / advisory. **No files were moved, renamed, split, or deleted. No code changed. Nothing pushed.**
> **Companion to:** [`architecture-review-2026-07-12.md`](architecture-review-2026-07-12.md), which covers the major *design decisions*. This document is the *organization* pass and only overlaps that one where noted (config fail-open defaults, the split ADR record, the stale `podman.List` comment).

## How to read this

Six sections mirror the review request: repo layout, Go packages, file-level, SvelteKit, cross-cutting, and the offset-mapper boundary. Each item is **What / Why / Assessment / Better**, where *Better* is a concrete verb — **keep / split / merge / move / rename / delete** — with effort (S/M/L) and risk (low/med/high). Claims are **[doc]** (a comment/README states it, quoted) or **[inf]** (inferred from structure). Line counts and dead-vs-live calls were verified against the tree (dead-code claims by grepping actual `import` statements, not prose mentions). A ranked "worth restructuring" table and a targeted target-layout sketch close the document.

The headline, up front: **this is a well-structured, convention-driven codebase carrying visible archaeological layers.** Import direction is clean, naming is unusually consistent, and the domain boundaries are mostly right. The wear is concentrated in four places — a half-finished frontend v1→v2 type migration, three or four monolithic files that never got sub-divided, a scatter of dead frontend files left by that migration, and cross-cutting hygiene (config spread across four idioms, `go test` absent from CI, a drift-prone offset seam). None of it calls for a big reorg; almost every fix is a same-package move, a delete, or a config/CI one-liner.

## Executive summary — top structural findings

1. **The Go import architecture is genuinely clean — protect it.** No cycles (proven: `go list ./...` succeeds, which Go forbids under a cycle); the scraper core is a leaf that `manager` depends on (not the reverse); `internal/guards` imports only interface packages and never a concrete system. The one concrete exception (`guards → roles`) is documented and reasoned. This is the structural backbone and it has aged well.

2. **`scraper/manager` is really ~4 units wearing one package name.** Of its 33 files, **eight contain zero functions** — they're a pure v2 wire-DTO schema — sitting next to the runner goroutine, the aggregator, and the `v2_adapters.go` translator. Extracting the DTO layer into its own `wire/` package de-monoliths `manager` *and* fixes a real `TickPayload` name collision for free.

3. **Three or four god-files never got sub-divided:** `haloce/reader.go` (1504, bi-modal game-data vs per-tick), `scraper/types.go` (897, a type-only file whose `Tick*` family is ~615 lines), `podman/podman.go` (727, create+start+stop+cleanup+exec in one file), and `manager/runner.go` (610). The codebase already demonstrates the fix idiom next door (`reader_*.go` ×14, the podman siblings, `offsets` vs `offsets_reference`).

4. **The frontend carries a half-finished v1→v2 type migration (strangler-fig, frozen ~65% done).** The debug page's game/objects/scenario/tick/xbox tabs are on v2; overview/events/probe are still on v1 `scraper.ts`, bridged by two adapters. `scraper.ts` itself says *"New code should consume EnvelopeV2 from scraper-v2."* It reads as tech debt with a clear direction — finish it or formally freeze it, not leave it ambiguous.

5. **~13 genuinely dead frontend files** (proven zero-import), almost all stranded by that migration — dead debug tabs (`logs`, `raw`, `controls`), superseded shared components, an orphaned `H2AppearanceEditor`, and a `pov-projection` util-with-test-but-no-consumer. A single cleanup PR. (The files Stewart suspected — `PlayKiosk`, `play-match`, `Scene3D` — are all **live**.)

6. **`go test` is not in CI.** 58 test files exist and `task test` runs them, but the CI backend job runs only `go vet` + build. Adding one CI step makes all existing tests load-bearing. The most valuable *missing* tests are the pure UTF-16LE codecs in `scraper/xbox` (11 live read-path call sites, zero tests).

7. **Config is spread across four env-reading idioms and `.env.example` has drifted** (missing `WS_ALLOWED_ORIGINS`, `LAN_SAVES_TOKEN`, six `CONTAINERS_*`), including two security-relevant fail-open vars from the companion review's finding #4.

8. **The offset seam is documented but drift-prone by construction.** `offsets.go` is hand-reconciled from `halo-offset-mapper`'s export; the source JSON lives only in the sibling repo; the value-lock test freezes *this repo's* copy but can't detect *upstream* drift. Committing the source JSON + a match-test closes the gap.

The rest are healthy and recommended **keep**: the save-package cluster (correctly layered), the `guards/interfaces` DI, the four live render surfaces sharing one feed/view core, the `api-base` seam, and the naming conventions (a real asset).

## 1. Repo layout

**What.** Top level is conventional and legible: `cmd/server/` (the single binary), `internal/` (all Go, ~40 packages), `sveltekit/` (the SPA), `containers/` (xemu + browser container init scripts + tools), `docs/`, `scripts/` (Python asset extractors — `game-icons`, `game-geometry`, `halo-assets`), `tools/` (`h2-emblems` extractor), plus the build/config files (`Taskfile.yml`, `Containerfile`, `compose.yml`, `.air.toml`, a single root `.env`). `atlas/` (predecessor-project snapshots) is gitignored/local-only and quarantined by convention as "unverified reference." Two root-level markdown docs beyond the standard set: `CLAUDE.md`, `CHANGELOG.md` (expected) and `BSP-EDITOR.md` (a dev-tool doc).

**Why.** [doc] `CLAUDE.md` and `docs/README.md` describe the intended layout, and the single shared root `.env` is deliberate — `sveltekit/vite.config.ts` sets `envDir: '..'` so the SPA and the Go backend read one file. `atlas/` is documented as porting reference to "treat as unverified." **[doc]**

**Assessment.** Coherent and standard for a single-binary Go + SPA project; nothing is badly misplaced. Four small notes:

- **No `migrations/` directory exists** — worth stating because it's easy to assume one does. Schema is applied in Go on boot (see the companion review's finding #2); there is no PocketBase `pb_migrations/` or `migrations/` tree. This is a *decision*, not an omission, but a newcomer will look for it.
- **`BSP-EDITOR.md` is misplaced at the repo root.** It documents the `/bsp-editor` dev tool and belongs under `docs/` (the only root `.md` files that earn their place are `README`/`CHANGELOG`/`CLAUDE`/`LICENSE`). **[inf]**
- **`scripts/` and `tools/` overlap in purpose** — both hold Python asset/emblem extractors that reuse the sibling `halo-offset-mapper` decoders. The split (`scripts/` = map/icon/geometry, `tools/` = h2-emblems) is defensible but arbitrary; a reader won't predict which is which. **[inf]**
- **`docs/` has accreted files the convention doc doesn't list** (see §5) — not misplaced, just undocumented.

**Better — keep the shape; move two files (S, low).** No reorg. **Move** `BSP-EDITOR.md` → `docs/` (or `docs/tools/`). Optionally **merge** `tools/h2-emblems` under `scripts/` (or rename `scripts/` → `tools/`) so there's one home for the Python extractor family — cosmetic, effort S. Leave `atlas/`, `containers/`, and the single-`.env` model exactly as they are.

## 2. Go package structure

**What.** ~40 `internal/*` packages, mostly small and single-purpose, plus a few large ones: `scraper/manager` (33 files, ~5.6k lines), `scraper/haloce` (22 files, ~5.1k), `pocketbase/schema` (28 files, ~2.8k), `pocketbase/hooks` (21 files, ~1.8k), `podman` (12 files, ~1.7k), `scraper` core (7 files, ~1.7k). Cross-system communication is mediated by `internal/guards` + `internal/guards/interfaces/` (per-role interface files, one per file).

**Why.** [doc] The interface-mediated, no-cross-imports design is the stated architecture (`CLAUDE.md` "Cross-System Architecture"; `guards/interfaces` README: *"one interface per file for merge-safe parallel development"*). The per-game offset isolation (offsets live only in each game package) is a stated rule in `scraper/scraper.go`. **[doc]**

**Assessment.**

*Import direction — clean, a genuine strength.* Verified: no cycles (`go list ./...` succeeds); `scraper` core does **not** import `scraper/manager` (the two apparent hits are doc comments); only `cmd/server/main.go` and `routes/scraper/` import the concrete manager, everything else goes through `scraperiface.Service`; `internal/guards` imports no concrete system, only interface packages + `internal/roles`. The `guards → roles` concrete dependency is a **documented, reasoned exception** (`roles.go`: adding an interface would couple the scraper/Discord systems to a PB-only concern for no benefit). The `guards/interfaces` per-role pattern is interface-segregation done idiomatically — Go's structural typing means concrete types satisfy the interfaces with zero import, which is *why* there are no cycles. It's on the granular end (≈8 files for one system's contract) but each interface maps to a documented caller. **Keep — do not touch.**

*`scraper/manager` is a god-package by accretion, not one cohesive unit.* Its 33 files group into ~four concerns: the Manager facade + per-instance runner engine (`manager.go`, `runner.go`, `loop.go`, `phase.go`, …), the cross-instance aggregator (`aggregator.go` + `summary.go`), a **pure v2 wire-DTO schema** (`xbox.go`, `scenario.go`, `game.go`, `tick.go`, `debug.go`, `objects.go`, `previous_game.go`, `summary.go` — **eight files with zero functions**, verified), and the `v2_adapters.go` (587) translator between the reader structs and those DTOs. Cohesion *within each file* is high (median ~110 lines, consistent one-class-per-file); it's cohesion *across the package* that's diluted, because a JSON wire schema is a different kind of thing from a goroutine state machine.

*`scraper/types.go` (897) is a benign shared god data-file* — 47 struct definitions and one function; the `Tick*` family alone is ~615 of the 897 lines. Large, but type-only and the lingua franca every reader/adapter speaks, so the cost is navigation, not coupling.

*`podman/podman.go` (727) is a real god-file* — the `Manager` does lifecycle (`Create/Start/Stop/Remove`), container construction (`createXemu`/`createBrowser`), file cleanup (`DeleteFiles`/`CleanupOrphans`), queries (`List/Status/Logs/Get`), and exec plumbing (`run`/`runSudo`) in one file. Notably the cross-cutting concerns were *already* peeled into siblings (`overlay.go`, `cert.go`, `console_name.go`, `ports.go`, …), so 727 is the core that never got split.

*The save cluster is coherent and correctly layered* — `halosave` (format builder, leaf) → `saveartifact` (packages a build into a deployable tar, the "shared seam" between the record hooks and the download route) → `routes/lansaves` (HTTP surface), with `diskspace` an orthogonal capacity-check leaf. Dependencies all point downward; no overlap or duplication. `saveartifact`'s own doc frames it as the anti-duplication seam. **Keep.**

*Dead Go code: essentially none.* No leftover v1 emission functions (the "v1" mentions are historical comments; the v2 adapters are the live path); the `*.go.example` files (one per package) are intentional scaffolding templates; `offsets_reference.go`'s `*Broken` constants are intentional, isolated, self-documented reference data.

**Better.**

- **Extract the v2 wire-DTO layer** out of `manager` into `internal/scraper/wire/` (or `manager/wire/`): move the eight zero-function DTO files + `v2_adapters.go`. This shrinks `manager` to its runtime engine (~2k lines) and makes the wire contract independently reviewable. It also **fixes a real name collision**: `scraper.TickPayload` (fat reader shape) and `manager.TickPayload` (slim wire shape) currently share a name and are trivially confusable; a `wire.Tick` qualifier disambiguates for free. **Effort M, risk low** (the DTOs have no behavior; the adapters are pure functions). Rename `v2_adapters.go` → `wire_adapters.go` (or split per class) while you're there — the `v2_` prefix outlived the migration.
- **Consider peeling the aggregator** (`aggregator.go` + `summary.go`) into `internal/scraper/hostsummary/`. **Effort S, risk low.** Lower priority.
- **Keep** the save cluster, the `guards/interfaces` DI, and the import architecture untouched.

## 3. File-level

**What.** The largest Go source files are `haloce/reader.go` (1504), `haloce/offsets.go` (1175), `scraper/types.go` (897), `podman/podman.go` (727), `manager/runner.go` (610), `manager/loop.go` (589), `manager/v2_adapters.go` (587), `haloce/reader_probe.go` (532). The largest frontend files are `routes/debug/theme/+page.svelte` (1216), `routes/bsp-editor/+page.svelte` (1213), `lib/utils/bsp-edit.ts` (1007), `routes/admin/pod/+page.svelte` (1069), `routes/settings/+page.svelte` (1010), `lib/types/scraper-v2.ts` (808), `components/bsp-editor/EditorScene.svelte` (782), `routes/admin/rosters/+page.svelte` (777), `components/visualizer/Scene3D.svelte` (752), `lib/types/scraper.ts` (703).

**Why.** [inf] Most are files that grew with their feature and never got sub-divided; `offsets.go` is intentionally large (it's data). The codebase clearly *knows how* to split — `haloce` already has 14 `reader_*.go` files each scoped to one read-domain, `offsets.go` vs `offsets_reference.go`, and the podman siblings — so the god-files are the exceptions that outran the idiom, not a lack of convention.

**Assessment & Better (Go).** All splits below are same-package moves → **risk low**.

- **`reader.go` (1504) — the biggest file in the repo, and bi-modal.** It mixes game-data composition (`ReadGameData`/`composeGameData` + roster/score/machine helpers, ~lines 149–705) with per-tick reading (`ReadTick`/`readTickPlayer`/`readDynPlayerFull` + power-item machinery, ~706–1488). **Split** at ~706 into `reader_gamedata.go`, `reader_tick.go`, `reader_poweritems.go`, leaving `reader.go` as the `Reader` struct + `ReadGameState` + bases. **Effort M.** Highest-priority file split — it's the largest and clearly bi-modal, and the package already uses exactly this idiom.
- **`types.go` (897) — split** the `Tick*` family (~615 lines) into `tick_types.go`, leaving the `Game*`/`Static*` structs in `types.go`. **Effort S.** Trivial type-only move.
- **`podman.go` (727) — split** into `podman_create.go` (`Create`+`createXemu`+`createBrowser`, ~290 lines), `podman_cleanup.go` (`DeleteFiles`/`CleanupOrphans`/`containerOwnedPaths`, ~150), `podman_exec.go` (`run`/`runSudo`, ~50), leaving lifecycle + queries. **Effort S each.** Matches the existing overlay/cert extraction pattern.
- **`runner.go` (610) — split** the `instanceCache`/`previousGame` state structs into `instance_cache.go` and the marshal/broadcast cluster into `emit.go`. **Effort S.** And **peel** `runSystemSnapshot` (the Xbox-machine snapshot pass, orthogonal to the phase loop) out of `loop.go` into `system_snapshot.go`. **Effort S.**
- **`offsets.go` (1175) — keep.** It's data; splitting it gains nothing and the `offsets`/`offsets_reference` split already isolates the intentionally-dead constants.

**Assessment & Better (frontend files).**

- **`routes/admin/pod/+page.svelte` (1069) — the worst monolith.** ~30 inlined functions mixing data-fetch/mutation, a polling controller, bulk-action state, table sorting, and row-render helpers. **Extract** the pure sort/priority/row-shape logic to `lib/utils/pod-view.ts` (testable) first — **Effort S** — then the bulk-action state to a small store — **Effort M, risk med** (live admin surface with polling; the pure helpers extract safely first).
- **`routes/settings/+page.svelte` (1010) — split** into three section components (`SettingsGeneral`, `SettingsGamertagsTeams`, `SettingsConnectedAccounts`) under a new `components/settings/`; the sections are already visually and functionally separate. **Effort M, risk low.**
- **`routes/bsp-editor/+page.svelte` (1213) — extract** the non-canvas panels (layer list, export, culling controls) into `components/bsp-editor/*` children; `EditorScene.svelte` already isolates the canvas. **Effort M, risk low.** (See §4 on why the tool is first-class.)
- **`admin/rosters/+page.svelte` (777) — keep / low priority.** It's already the *good outcome* of a prior split (carved out of a former `/admin/identity/` monolith); its size is inherent to a moderation surface.
- **`debug/theme/+page.svelte` (1216), `EditorScene.svelte` (782), `Scene3D.svelte` (752) — keep.** The theme pages are dev-only Skeleton galleries; the two 3D files are inherently large renderers whose *logic* already lives in `viz3d.ts`/`bsp-edit.ts`/`visualizer-view.ts`.

**Dead files (proven zero real imports) — delete (S, low).** Almost all stranded by the debug v1→v2 rewrite: `debug/logs/{LogsTab.svelte, logs-vm.ts}`, `debug/raw/{RawTab.svelte, raw-vm.ts}`, `debug/controls/ControlsTab.svelte`, `debug/shared/{ColGroupedTable.svelte, col-grouped-table.ts, KvCard.svelte, PlayerListItem.svelte, PlayerStatsCard.svelte}`, `gamertag/H2AppearanceEditor.svelte` (superseded by `AppearanceStudio.svelte`, which calls itself a *"drop-in for H2AppearanceEditor"*), and `lib/utils/pov-projection.ts` + its test (a pure module with a test but no consumer). ~11 files + 2 tests, one cohesive cleanup PR. **Correction to the brief:** `PlayKiosk.svelte`, `play-match.ts`, and `Scene3D.svelte` are **live** (used by `routes/play` and `routes/visualizer3d`), not dead.

## 4. SvelteKit side

**What.** `routes/` is feature-organized with idiomatic groups: a `(user)` auth group, an `admin/` group behind an `isAdmin` layout guard, an `organizer/` group behind a `requireOrganizer` guard, plus public feature routes. `lib/` splits into `components/` (11 domain subdirs), `utils/` (pure logic + view-builders, mostly `.test.ts`-covered), `stores/` (Svelte-5 rune stores), `types/`, `config/` (the nav + stream-asset registries). The routes-thin / lib-thick split is mostly respected; the exceptions are the mega-pages in §3.

**Why.** [doc] `config/navigation.ts` is the documented nav source-of-truth; `config/` also held the overlay asset registry (deleted in `eda3056`, 2026-07-18, with every route it named); the SPA-fallback + guard-layout patterns are documented in `CLAUDE.md`.

**Assessment.**

*The live render surfaces share one core — healthy, not N copies.* `routes/visualizer/[instance]` (2D), `routes/visualizer3d/[instance]` (3D), `routes/overlays/[instance]/*` (killfeed/status/timer), and `routes/overlays/players` all subscribe through **one** feed (`stores/overlay-feed.svelte.ts`, built on `scraper-ws-v2`) and share the view-builders (`overlay-view.ts` for scoreboards/killfeed, `visualizer-view.ts` for the map model, `game-geometry.ts` for mesh loading). The 2D and 3D visualizers are deliberate near-siblings — same feed, same model, differing only in the renderer component. The `?mock=1` seam is injected once at the store boundary so every surface gets mock data for free. This is the healthiest part of the frontend. `overlays/players` is **not** dead (it's a registered target of that asset registry). **Keep.** *[2026-09-28: every route and file named in this paragraph was deleted in `eda3056` / `76807ad`; kept as the review's record.]*

*The v1→v2 type migration is a strangler-fig frozen ~65% done — the biggest frontend debt.* The debug page's `game/objects/scenario/tick/xbox` tabs consume `scraper-v2.ts`; the `overview/events/probe` tabs still consume v1 `scraper.ts`, bridged by `debug/shared/v2-to-v1-game.ts` + `previous-game-adapter.ts`. The old v1 WS store is already deleted (only `scraper-ws-v2` remains) — you don't delete the v1 transport if v1 is meant to be permanent — and `scraper.ts`'s own header says *"New code should consume EnvelopeV2 from scraper-v2."* This reads as in-flight tech debt with a clear direction, not a stable split. One nuance: the pod-management types (`ScraperInfo`/`Phase`/`ScraperInspect`) used by the `admin/pod` pages are a *separate concern* (runner status, not per-tab game payloads) and may legitimately keep living in a renamed module even post-cutover.

*Component subdir cohesion is good;* `debug/`'s tab-folder convention (component + `*-vm.ts` + presentational children + a `shared/`) is repeated consistently. One nit: `debug/shared/` mixes genuinely-shared tiles with the two migration adapters — those are bridge code, not shared UI.

*BSP-editor is a first-class offline authoring tool, not an orphan.* It's unlinked from nav, but its **output is wired into the live render path**: `game-geometry.ts::loadBspMesh` serves the editor-baked `spectator_file` mesh (falling back to raw) to *both* visualizers, and `bsp-edit.ts` stamps exports `generated_by: 'bsp-editor'`. It's a producer feeding a manifest the visualizers consume — deliberately unlinked the way a build tool isn't in the app menu. **Keep.**

*The `api-base` seam is clean* — `utils/api-base.ts` exports only `apiBaseURL()`/`wsBaseURL()`; the higher-level API clients and transports all build on it, nothing hardcodes hosts around it.

**Better.**

- **Finish or formally freeze the v1→v2 migration (M–L, med).** The tabs are isolated (each = vm + presentational children, mounted independently), so convert one PR at a time with the adapters as a safety net: **convert** `events` + `probe` (S each), then `overview` + **delete** both adapters (M), then **rename** `types/scraper.ts` → `scraper-runner.ts` (or `pod-status.ts`) for the surviving Info/Phase/Inspect types so the "v1" label stops implying debt (S). If you do *not* intend to finish, add an "intentionally v1" banner to the three tabs so the seam reads as a decision, not rot.
- **Group the render-core utils** (`overlay-view`, `visualizer-view`, `game-geometry`, `floorplan`, `floor-bands`, `rooms`, `mock-map`, `viz3d`) into a `lib/viz/` (or `lib/render/`) subdir so the architecture is legible; they're currently loose in a flat `utils/` bag alongside unrelated helpers. **Effort S, risk low** (import-path churn, all test-covered).
- **Move** the two debug adapters out of `debug/shared/` into `debug/migration/` (or `lib/utils/`) so `shared/` stays "reusable UI." **Effort S.**
- **Add** `/bsp-editor` (and optionally the `debug/theme` galleries) to a nav "tools" group so their keep-status is explicit rather than URL-only. **Effort S.**

## 5. Cross-cutting

### Config / env handling

**What.** Env access uses **four idioms with no central surface**: the typed `envStr`/`envInt`/`envBool` trio in `internal/podman/config.go` (the only structured loader, ~30 `CONTAINERS_*` vars); hand-parsed inline reads in `cmd/server/main.go` (`OVERLAY_TOKEN_SECRET`, `OVERLAY_TOKEN_TTL_HOURS`); bare `os.Getenv` in leaf packages (`websocket/handler.go` `WS_ALLOWED_ORIGINS`, `disgo/bot.go`, all 12 OAuth providers, `seed/data.go`); and a *fourth* local `envDefault` helper in `routes/lansaves/`. The frontend reads one `PUBLIC_PB_PORT` from the shared root `.env`.

**Assessment.** Many surfaces, and — more importantly — **`.env.example` has drifted and is not authoritative.** Runtime vars present in code but missing from `.env.example`: `WS_ALLOWED_ORIGINS` (fail-open when unset), `LAN_SAVES_TOKEN` / `LAN_SAVES_STAGING_DIR` / `LAN_SAVES_FATX_CLUSTER` (LAN endpoints fail *open* when the token is unset), and six `CONTAINERS_*` vars (`ROOT_HDD`, `QEMU_IMG_CMD`, `SET_CONSOLE_NAME`, `QEMU_STORAGE_DAEMON_CMD`, `PYTHON_CMD`, `FATX_TOOL`). Two of these are the security fail-open defaults the companion review flagged (finding #4) — compounded here because an operator reading `.env.example` never learns the vars exist. Also a dead `.gitignore` line whitelisting `!sveltekit/.env.example`, which doesn't exist.

**Better.** **Sync** `.env.example` (add the ~9 missing vars, esp. `WS_ALLOWED_ORIGINS` + `LAN_SAVES_TOKEN`; drop the dead `.gitignore` line) — **S, low**, purely additive. Optionally **centralize** into one `internal/config` package read once at boot (absorbing the podman loader + the main/WS/LAN/OAuth reads) — **M, low** churn but kills three idioms. Add a tiny reflection test asserting every config key appears in `.env.example` so drift becomes a test failure — **S** — matching the repo's existing "value-lock test" habit.

### Error patterns

**What.** Three route idioms coexist, split ~50/50 by subsystem: PocketBase's `apis.NewXxxError` (used by `teams`/`rosters`/`adminusers`/`team_membership_requests`, ~13 files) vs raw `e.JSON(status, map[string]string{"error":…})` (used by `containers`/`lansaves`/`overlays`/`scraper`/`xemu`, ~13 files), plus sentinel errors in the guard layer. Lower layers are each internally consistent: hooks use `routine.FireAndForget` (documented best-effort/async), the scraper uses `log.Printf`, podman/xemu wrap with `fmt.Errorf(…%w)`.

**Assessment.** Ad hoc at the route boundary — two API error *shapes* reach clients depending on which subsystem a route lives in, so a frontend error handler can't assume one schema. Not harmful (both carry a message) but the kind of divergence that compounds. The one swallow is `podman.Status` returning `("unknown", nil)` on inspect failure — defensible, but it pairs badly with the stale `podman.List` "enriched with live status" comment (companion finding #1).

**Better.** **Standardize** route errors on `apis.NewXxxError` (structured, integrates with PB's error middleware) and convert the ~13 raw-`e.JSON` files — **M, low** (bodies stay comparable); optionally exempt the `xemu/` dev-diagnostic routes with a documented carve-out. **Fix** the stale `podman.List` comment (companion finding #1). The lower-layer conventions are fine — just record them in a house-style note.

### Test layout + coverage gaps

**What.** 58 co-located `_test.go` files, with strong coverage in `scraper/manager`, `halosave`, `games`, `podman`, `overlaytoken`, `bracket`, `stats`, `notifications`, `audit`, `disgo`, `series`, `rating`, plus a pure "value-lock" guard `haloce/offsets_verified_test.go`. **CI runs `go vet` + build but not `go test`** — though `task test` runs `go test ./cmd/... ./internal/...` locally.

**Assessment.** The layout is idiomatic and the tested set is impressive. The gap that matters is **pure, load-bearing decoders going untested**: `scraper/xbox/encoding.go`'s UTF-16LE codecs (`DecodeUTF16LE` is called at ~11 live read-path sites and drives the memory scanner — zero tests, trivially table-testable — the single highest-value gap), plus `xemu`'s `HighGVA`/`parseHexSuffix` address math and the `xbox` byte-decoders (`clock`, `xbe_certificate`, etc.). The `roles`/schema/route gaps are lower-priority (they need a live PB harness, which the repo has). **The CI omission is the multiplier**: even the 58 existing tests only protect contributors who remember `task test`.

**Better.** **Add** `go test ./cmd/... ./internal/...` to the CI backend job (mirroring the Taskfile scoping that avoids the root-owned `containers/` mount) — **S, low, do this first**; it makes every existing test load-bearing. **Add** `xbox/encoding_test.go` for the three UTF-16LE codecs — **S**, highest value-per-line. Then `xemu` pure-helper tests and the `xbox` decoders — **S–M**.

### Naming conventions

**What / Assessment.** Naming is **remarkably consistent — a real asset**: `action_<verb>.go` (audit), `event_<verb>.go` (teamlog), `notification_<event>.go` (notifications), file-per-collection in `schema/`, `<collection>_<verb>.go` in `hooks/`, one-interface-per-file in `guards/interfaces/`, `require_<x>.go` guards, `all<thing>.go` registry aggregators, and `.go.example` scaffolding templates. The tree is navigable by filename alone. Minor inconsistencies: three different prefixes (`action_`/`event_`/`notification_`) for three "one-writer-per-file" packages (defensible — different domains); `disgo/commands/` uses bare names (`ping.go`) without a prefix; the `v2_adapters.go` name (§2). **Better — keep**; add a one-paragraph file-naming table to `docs/README.md` (which already has a doc-file naming table) so the conventions are *stated*, not just inferred. **S, low.**

### Docs / ADR placement

**What / Assessment.** `docs/README.md` authoritatively documents the intended layout (`STATUS.md`, `RUNBOOK.md`, `XEMU-TEST-SETUP.md`, `milestones/`, `decisions/`). Most of `docs/` conforms. Issues: **`BSP-EDITOR.md` sits at the repo root** (should be under `docs/`, §1); **`overnight-progress.md` is a stale dated session log** living permanently in `docs/`; several feature-reference docs (`websocket-api.md`, `splitscreen-viewports.md`, `STREAM-ASSETS.md`, the `lan-hub/` and `gamertag-system/` bundles) are useful but **the convention doc under-describes them**; and the **ADR record is split** — only 0001/0002 on `main`, with 0003/0004 stranded on feature branches and a numbering collision at `M08:41` (companion finding #3). **Better:** **move** `BSP-EDITOR.md` → `docs/` (S); **archive** `overnight-progress.md` (S); **land** ADR-0003/0004's files on `main` + resolve the collision (M, med — companion finding #3); **extend** the `docs/README.md` layout list to match reality (S).

## 6. Boundary with halo-offset-mapper + the rig

**What.** The seam is **documented and hand-maintained, not generated.** `scraper/scraper.go` states it plainly: *"Offsets flow from the halo-offset-mapper export (`export_cartographer.py`) → reconciled by hand into the game package's `offsets.go` (see that repo's `docs/CARTOGRAPHER-IMPORT.md`)."* No offset JSON is committed in this repo — the source-of-truth maps (`ce-h1og-default.offsets.json`, `h2-stock.offsets.json`) live in the sibling `halo-offset-mapper/offset-maps/`; this repo copies values into Go constants by hand, each tagged with a `// halocaster.py:NNN` origin. The contract is guarded by `offsets_verified_test.go` (a pure value-lock: hard-coded verified constants that fail a test if a future edit drifts them). The `scripts/` + `tools/` Python extractors **reuse** the sibling's `halomap.py`/`bitmaps.py` decoders (they don't reimplement decoding), requiring `../halo-offset-mapper` checked out beside the repo. Game-derived art is gitignored; only extractors + factual metadata inventories are committed.

**Why.** [doc] Hand-reconciliation is chosen because offsets need human runtime-verification — `offsets.go`'s header documents specific by-eye investigations (the `0x2E4004` vs `0x2E4068` disambiguation, the `0x1B4` camo-vs-drop_time overload). Decoder-reuse-not-vendor keeps a single source of truth for `.map`/bitmap parsing in the sibling. The art/metadata git-ignore split is a stated legal position.

**Assessment.** A **deliberately loose, human-in-the-loop coupling — well-documented but drift-prone by construction**, on two axes. First, the **offset source of truth is split across two repos with a manual copy step**: the JSON lives in `halo-offset-mapper`, the Go constants live here, the bridge is a person reading `CARTOGRAPHER-IMPORT.md`. The value-lock test is a good mitigation but it freezes *this repo's* copy — it **cannot detect upstream drift**: if the sibling re-exports a corrected offset, this repo's test keeps passing on the old value until a human notices. Second, the **scripts have an unversioned hard dependency on a sibling checkout** (`../halo-offset-mapper`) — no submodule, no pinned commit — so a decoder API change breaks the extractors with no version signal. Fine for a solo dev with both repos side-by-side; fragile for reproducibility. The art/metadata split, by contrast, is principled and has aged well.

**Better.**

- **Keep hand-reconciliation for offsets** — the runtime-verification investigations genuinely need human judgment; full codegen would lose that narrative.
- **Commit the source export JSON into this repo + match-test it (S, low — highest-leverage seam fix).** It's factual metadata (same stance that makes the asset catalog committable). Add a test that reads the JSON and asserts the Go constants match, so the value-lock guards against *both* local edits and *upstream* drift, and the numbers' source of truth is present in-repo.
- **Optionally codegen** the constant block from the committed JSON via `go generate`, keeping the hand-written investigation prose in a separate file (**M, med**) — removes the manual copy step while preserving the narrative.
- **Pin the sibling decoder dependency (S, low)** — record the expected `halo-offset-mapper` commit in each script's README or as a git submodule, so the Python reuse has a version contract.

## Ranked — worth restructuring (leverage = impact ÷ effort)

Ordered best-return-first. Effort **S** ≈ a sitting, **M** ≈ a day or two, **L** ≈ a project. Risk is the risk of *making the change*.

| # | Action | §  | Impact | Effort | Risk | Verb |
|---|--------|:--:|:------:|:------:|:----:|------|
| 1 | Add `go test ./cmd/... ./internal/...` to the CI backend job | 5 | High | S | low | keep+guard |
| 2 | Sync `.env.example` (add `WS_ALLOWED_ORIGINS`, `LAN_SAVES_TOKEN`, 6 `CONTAINERS_*`; drop dead `.gitignore` line) | 5 | Med–High | S | low | fix |
| 3 | Commit the offset source JSON into the repo + a match-test | 6 | Med–High | S | low | fix |
| 4 | Delete the ~13 proven-dead frontend files (one PR) | 3 | Med | S | low | delete |
| 5 | `xbox/encoding_test.go` for the UTF-16LE codecs (11 read-path sites) | 5 | Med | S | low | keep+guard |
| 6 | Split `haloce/reader.go` (1504) → `reader_gamedata`/`reader_tick`/`reader_poweritems` | 3 | Med | M | low | split |
| 7 | Split `types.go` → `tick_types.go`; `podman.go` → `_create`/`_cleanup`/`_exec`; `runner.go`/`loop.go` | 2,3 | Med | S | low | split |
| 8 | Extract `manager` v2 wire-DTO layer → `wire/` pkg (+ rename `v2_adapters`, fixes `TickPayload` collision) | 2 | Med | M | low | split+rename |
| 9 | Docs: move `BSP-EDITOR.md` → `docs/`; archive `overnight-progress.md`; land ADR-0003/0004 on `main` + fix the 0003 collision | 1,5 | Med | S–M | low–med | move |
| 10 | Finish (or formally freeze) the frontend v1→v2 type migration; then rename `scraper.ts` | 4 | Med | M–L | med | refactor |
| 11 | Group render-core utils into `lib/viz/`; move debug adapters out of `shared/`; add `/bsp-editor` to a nav tools group | 4 | Low | S | low | move |
| 12 | Extract `admin/pod` (→ `pod-view.ts`) + `settings` (→ 3 section components) monoliths | 3 | Low–Med | M | med | split |
| 13 | Standardize route error idiom on `apis.NewXxxError` (convert ~13 raw-`e.JSON` files) | 5 | Low–Med | M | low | refactor |

**Sequencing.** Items 1–5 are all **S/low** and independently landable — a natural first batch that buys the most safety and clarity per hour (CI safety net, closed config/security drift, a drift-proof offset seam, a dead-code sweep, and the highest-value test). Items 6–8 are the mechanical god-file/package splits (do `reader.go` first). Item 9 also resolves the companion review's finding #3. Item 10 is the one genuinely larger refactor and deserves a deliberate decision (finish vs. freeze). 11–13 are opportunistic polish.

## Target layout — targeted, not wholesale

**A full reorg is NOT warranted.** The package boundaries, import direction, and naming are fundamentally sound; the right move is a handful of *local* splits/moves/deletes, all shown below. Nothing crosses a system boundary; every change follows an idiom the codebase already uses elsewhere.

```
internal/scraper/
  scraper.go, state.go, viewport.go, refs.go
  types.go                    → split: types.go (Game*/Static*) + tick_types.go (Tick* family)
  event_payloads.go
  manager/                    (shrinks to the runtime engine)
    manager.go runner.go loop.go phase.go aggregator.go demand.go …
    runner.go                 → split: + instance_cache.go + emit.go
    loop.go                   → peel: + system_snapshot.go
  wire/                       ← NEW: the pure v2 DTO schema, lifted out of manager/
    xbox.go scenario.go game.go tick.go debug.go objects.go previous_game.go summary.go
    adapters.go               ← was manager/v2_adapters.go (renamed; the v2_ prefix is retired)
  haloce/
    reader.go                 → split: reader.go + reader_gamedata.go + reader_tick.go + reader_poweritems.go
    reader_*.go (×14)  offsets.go  offsets_reference.go  offsets_verified_test.go
    offsets_source.json       ← NEW: committed export from halo-offset-mapper; match-tested

internal/podman/
  podman.go                   → split: podman.go + podman_create.go + podman_cleanup.go + podman_exec.go
  overlay.go cert.go console_name.go ports.go config.go state.go store.go   (unchanged)

internal/config/              ← OPTIONAL NEW: one typed env surface (absorb podman.LoadFromEnv + main/WS/LAN reads)

sveltekit/src/lib/
  viz/                        ← NEW: overlay-view, visualizer-view, game-geometry, floorplan, floor-bands, rooms, mock-map, viz3d
  components/settings/        ← NEW: SettingsGeneral / SettingsGamertagsTeams / SettingsConnectedAccounts
  components/debug/
    migration/                ← MOVE here: v2-to-v1-game.ts, previous-game-adapter.ts (out of shared/)
    (DELETE) logs/ raw/ controls/ ; shared/{ColGroupedTable,KvCard,PlayerListItem,PlayerStatsCard} ; gamertag/H2AppearanceEditor
  utils/ (DELETE) pov-projection.ts + .test.ts
  types/scraper.ts            → rename → scraper-runner.ts once overview/events/probe leave v1

docs/
  BSP-EDITOR.md               ← MOVE from repo root
  decisions/0003-*, 0004-*    ← LAND from feature branches (+ fix M08:41 collision)
  archive/overnight-progress.md   ← MOVE (stale session log)
```

## Caveats & things not verified

- **Read-only; nothing moved, split, renamed, deleted, or built by me.** The splits/deletes above are *recommendations*. All "dead" calls were proven by grepping actual `import` statements (excluding prose mentions), but a file reachable only by an out-of-repo pipeline (e.g. a bsp-editor manifest, or a route hit only by URL) can look dead to a static import sweep — the judgment-call items (`debug/emblem`, `debug/theme`) are flagged as dev-tools, not asserted dead.
- **`go build`/`go vet`/`go list` were run during investigation** (by the read-only sub-agents) to confirm "no import cycles" and package compile-ability; these don't modify source, and I verified afterward that the working tree still shows only the pre-existing `go.mod` edit and `go.sum` is unchanged.
- **Line counts** are from `wc -l` on `main` @ this branch; a few frontend "dead file" git-dates are approximate.
- **The offset-seam upstream half lives in the sibling `halo-offset-mapper` repo,** which I did not read — statements about where the JSON source of truth lives are from this repo's comments/docs, not from inspecting the sibling.
- **Companion overlaps** (`WS_ALLOWED_ORIGINS`/`LAN_SAVES_TOKEN` fail-open, the split ADR record, the stale `podman.List` comment) are treated here as *organization* symptoms; the *decision*-level treatment is in `architecture-review-2026-07-12.md`.

*End of review. Read-only; no code, schema, or files moved/renamed/deleted; nothing pushed.*




