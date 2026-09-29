# Milestone 12 — POV marker overlay (stretch)

> Long-shot. Browser source that overlays directly on top of a machine's actual game POV (e.g. as an OBS Browser Source layered above the kiosk capture), drawing world-anchored markers in real time: enemy silhouettes through walls, powerup tags, teammate gamertags floating above their heads. Requires perspective projection (3D world → 2D screen) instead of M11's top-down projection — same input data, harder math.

## 12a. Camera + projection model

Halo: CE per-player camera state (FOV, position, view direction) is already partially read by the scraper. Audit what's there; spec any missing offsets (camera matrix, near/far planes) for an M19 follow-up if needed. Build a per-tick "render frustum" model in the overlay client.

## 12b. Single-player POV alignment

New overlay at `/overlays/pov/<machine>/`. For full-screen single-player on the target machine: project enemy world positions through the player's camera matrix to screen coordinates. Render markers (silhouettes, name tags, distance indicators) as positioned absolute elements. Validate alignment by overlaying onto the actual kiosk video stream — markers should track tightly as the player turns.

## 12c. Split-screen handling

When the target machine is running 2/3/4-player split-screen, partition the screen into the appropriate viewports and project per-viewport per-player. The M5 reader already exposes local-player count + indices.

## 12d. Marker types

Initial set:

- Enemy silhouettes (filled shape outlining the enemy's bounding box, with team color).
- Teammate gamertag floats above-head.
- Powerup labels at spawn positions (with active/respawn timer if available).
- Optional: line-to-objective in CTF (toward the enemy flag if you're attacking, toward your base if you have it).

## 12e. Composite verification

OBS scene = kiosk video source layered with the POV overlay browser source above it. Test rig: known-good viewing angle on a known map → measure pixel offset between marker and actual entity at multiple angles. If offsets are consistent, calibration matrix is correct; if they drift, the camera offsets are off and feed back to 12a as offset bugs.

Smoke test: 1v1 Slayer on Wizard, single full-screen → enemy silhouette tracks the opponent through walls; teammate-tag-above-head test in a 2v2 game on Hang 'Em High. Split-screen verification: same setup with 2v2 on a single console.

**Stretch flag.** This milestone is explicitly stretch — if M11 reveals that the projection math is brittle, defer M12 to M21+ open bucket and ship M11 alone. Also explicitly out of scope for v1: through-wall occlusion (rendering markers dimmed when behind geometry), since that requires BSP knowledge from M11a's deferred case.

## Log

_Append-only. Never edit past entries; add a new dated line._

- 2026-06-18: Stretch-foundation increment — the **perspective-projection math** (12a/12b core), implemented during the autonomous overnight run. Only this part can be unit-tested; everything else depends on live data + the as-yet-unconfirmed camera offsets, so it's deferred (consistent with the stretch flag).
  - New `sveltekit/src/lib/utils/pov-projection.ts` (pure, unit-tested via `pov-projection.test.ts`): `worldToScreen(world, CameraState{pos, forward, up, fovY, aspect}, w, h)` — a right-handed pinhole projection returning pixel coords + depth + an `onScreen` frustum flag, culling points behind the camera. Internal vector helpers (dot/cross/sub/normalize). M11's top-down projection held up fine, so the stretch isn't being abandoned — but the perspective path is harder and rides on camera offsets that 12a still has to confirm.
  - **Deferred / blocked on live + reader work:** 12a's audit of whether the Halo: CE reader exposes a usable camera matrix (FOV, position, view dir, near/far) — **if the offsets aren't there, that's an M19 follow-up and M12 likely defers to M21+** per the stretch flag; 12b live alignment over the kiosk stream; 12c split-screen viewport partitioning; 12d marker rendering; 12e calibration. Through-wall occlusion stays out of scope for v1 (needs BSP).
  - **Decision:** shipped the projection kernel as a tested foundation rather than skip M12 entirely, but kept it minimal given the stretch status + unverified camera offsets. Recommend Stewart treat M12 as "foundation parked" until the 12a offset audit can run against live xemu.

- 2026-09-28: **Closed (xc-studio S0) — abandoned.** `pov-projection.ts` (+ test) was removed in `76807ad` (2026-08-10) together with the visualizer family and the M11 minimap; the 12a camera-offset audit never ran, so nothing beyond the projection kernel was ever built. Not planned for the flagship — a world-anchored marker layer, if it returns, is a backlog item for the private `xemu-cartographer/xc-studio` repo, not here.
