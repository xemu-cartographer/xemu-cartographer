# Milestones

One markdown per milestone. Copy [`_template.md`](_template.md) when starting a new one.

| ID   | Title                                                                                       | Status      |
| ---- | ------------------------------------------------------------------------------------------- | ----------- |
| M00  | [Template cleanup](M00-template-cleanup.md)                                                 | Done        |
| M01  | [xemu memory bridge](M01-xemu-memory-bridge.md)                                             | Done        |
| M02  | [Halo: CE scraper](M02-haloce-scraper.md)                                                   | Done        |
| M03  | [Container lifecycle (Podman)](M03-container-lifecycle.md)                                  | Done        |
| M04  | [SvelteKit overlay + container management UI](M04-frontend-overlay-ui.md)                   | Done        |
| M05  | [Scraper & WebSocket phase model + cache refactor](M05-phase-model-refactor.md)             | Done        |
| M06  | [Frontend polish (theme + auth-refresh fix + debug revamp)](M06-frontend-polish.md)         | Done        |
| M07  | [Identity schemas: gamertags + teams](M07-identity-schemas.md)                              | Done        |
| M08  | [Roles + permissions](M08-roles-permissions.md)                                             | Done        |
| M09  | [Match-aware kiosk view](M09-match-aware-kiosk.md)                                          | In progress |
| M10  | [Overlay revamp + new browser sources](M10-overlay-revamp.md)                               | Done        |
| M11  | [Game minimaps](M11-game-minimaps.md)                                                       | Abandoned   |
| M12  | [POV marker overlay (stretch)](M12-pov-marker-overlay.md)                                   | Abandoned   |
| M13  | [PocketBase persistence foundation: games + series](M13-pb-persistence-games-series.md)     | In progress |
| M14  | [Series management: setup, pick/ban, in-progress UI](M14-series-management.md)              | In progress |
| M15  | [Per-user / per-team stats](M15-stats.md)                                                   | In progress |
| M16  | [Tournament system](M16-tournament-system.md)                                               | In progress |
| M17  | [Discord integration: stats lookup + per-guild channel posting](M17-discord-integration.md) | Planned     |
| M18  | [Rating system + multiple leaderboards](M18-rating-leaderboards.md)                         | In progress |
| M19  | [Robustness + offset validation](M19-robustness-offsets.md)                                 | Planned     |
| M20  | [Halo 2 scraper (with known caveats)](M20-halo2-scraper.md)                                 | In progress |
| M21+ | [Open](M21-plus-open.md)                                                                    | Planned     |
| M22  | [Moderation + audit log](M22-moderation-audit.md)                                           | Done        |
| M23  | [Team membership workflows](M23-team-membership-workflows.md)                               | Done        |
| M24  | [Remote game setup (controller-free lobby write)](M24-remote-game-setup.md)                 | Planned     |
| M25  | [OBS spectator overlays (token-auth render pages)](M25-obs-overlays.md)                      | Done        |
| M26  | [HDD copy-on-write overlay provisioning](M26-hdd-overlay-provisioning.md)                    | Done        |
| M27  | [3D visualizer (live markers + real BSP geometry)](M27-3d-visualizer.md)                     | In progress |
| M28  | [Broadcast graphics (themed scoreboard + player cards)](M28-broadcast-graphics.md)           | Done        |
| M29  | [Organizer route redesign (six-page library suite)](M29-organizer-redesign.md)              | In progress |
| M30  | [Player settings redesign (unified tabbed page)](M30-settings-redesign.md)                  | In progress |
| M31  | [xc-scraper pre-freeze hardening (extraction prep)](M31-xc-scraper-prefreeze-hardening.md)  | In progress |
| M32  | [xc-scraper daemon + wire-only cut-over (step 8)](M32-xc-scraper-daemon.md)                 | In progress |

> 2026-09-28 (xc-studio S0): M10, M25 and M28 closed, M11 and M12 abandoned — their surfaces were removed in `eda3056` (2026-07-18), `58459f2` (2026-08-08) and `76807ad` (2026-08-10), and the OBS overlay surface is being extracted to the private `xemu-cartographer/xc-studio` repo. Each file's Log carries the dated closing entry; M25 / M28 shipped mock-verified and were superseded before their live passes.

## Status values

`Planned` | `In progress` | `Done` | `Abandoned`

## Conventions

- Filename: `M??-kebab-name.md` (zero-padded to 2 digits).
- Never skip a number. If a milestone is dropped, mark it `Abandoned` and leave the file.
- The `Log` section inside each milestone is **append-only** — never edit past entries.

See [`../README.md`](../README.md) for the full convention.
