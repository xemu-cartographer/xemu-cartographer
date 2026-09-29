# Stream assets — retired (2026-09-28)

This page documented the M25 / Stream Studio surface: the `lib/config` asset registry behind the old `/studio/` gallery, the `/overlays/[instance]/*` render pages, `POST /api/overlay-tokens` and `components/broadcast/`. All of it was removed in `eda3056` (2026-07-18), `58459f2` (2026-08-08) and `76807ad` (2026-08-10); nothing it described is in the tree.

Do **not** carry over its OBS advice to tick "Refresh browser when scene becomes active" — on the current surface that reloads the postgame source and blanks MATCH TIME.

The current OBS surface (`/overlay/`, `/scorebug/`, `/leaderboard/`, `/postgame/`, addressed by `?console=` and authenticated by a `?spectator=` key) is documented in the private `xemu-cartographer/xc-studio` repo (`docs/OBS.md`, forthcoming); until that lands, the catalog and suggested source sizes in [`sveltekit/src/routes/studio/+page.svelte`](../sveltekit/src/routes/studio/+page.svelte) are the reference.
