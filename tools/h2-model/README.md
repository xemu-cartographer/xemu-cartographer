# H2 Mark VI Spartan — native Linux mesh export + pose/render pipeline

Exports the Halo 2 **multiplayer Master Chief** (Mark VI MP body, `render_model`
tag `mode #83`) and its `masterchief_cc` armor change-colour map **entirely on
Linux** — no Windows tools, no Wine, no .NET, no xemu — then poses + renders it
for the appearance-studio "pose" feature.

This unblocks what `../h2-emblems/SPARTAN-MODEL.md` documented as blocked: the H2
`mode` geometry is compressed + node-skinned and Sigmmma `reclaimer` ships no H2
model defs. So we decode it ourselves, reusing the emblem pipeline's proven Xbox
cache parsing + top-2-bits "raw pointer" map selector.

## Why not Reclaimer under Wine (the "fast route")

Tested and rejected on this box:
- No .NET/Mono installed; `winetricks` offers only .NET Framework ≤4.8, no .NET 7
  Desktop Runtime. Reclaimer is `net7.0-windows` + **WPF** → needs a manual
  Windows Desktop Runtime under Wine (WPF rendering is unreliable) + GUI
  automation, and it isn't Linux-native anyway.
- `Reclaimer.Blam`/`Reclaimer.Core` both target `net7.0-windows` with `UseWPF` +
  `System.Drawing` + Xbox `xcompress*.dll`, so they don't build natively on Linux
  either, and `Reclaimer.Blam` isn't on NuGet.

Instead we use Reclaimer's open-source H2 reader as the **format spec** (mirrored
in `../reclaimer-ref/`) and reimplement the decode in Python. Field-for-field
authority: `render_model.cs`, `Halo2Common.cs`, `CacheFile*.cs`, `bitmap.cs`.

## Pipeline (all Linux)

1. **`h2_render_model.py`** — parses the Xbox `shared.map` header + tag index +
   string table, finds `mode #83`, decodes every LOD0 section: compressed
   positions (`UInt16N4` × bounding-box bounds), UVs (`UInt16N2`), normals
   (`HenDN3` 11/11/10), triangle-strip indices, and node-skin blend
   indices/weights. Emits `masterchief.obj`, `masterchief.npz` (arrays + 33-bone
   skeleton), and `masterchief.model.json`.
2. **`h2_bitmap.py`** — native H2 bitmap decoder (DXT1/3/5, A8R8G8B8, 16-bpp,
   P8). Extracts `masterchief` (diffuse, 512² DXT3) and `masterchief_cc`
   (256² DXT3). The cc map encodes **R = primary** armor coverage, **G =
   secondary** — written out as opaque masks `masterchief_cc_{p,s}.png`.
3. **`blender_build_render.py`** — Blender headless: builds the mesh + UVs +
   normals, a 33-bone armature with skin weights, exports **`masterchief.glb`**
   (mesh + skeleton + skin + embedded texture), then renders each pose
   orthographic + transparent into three passes:
   `<pose>.png` (diffuse), `<pose>_p.png` (primary coverage), `<pose>_s.png`
   (secondary coverage).
4. **`spartan_composite.py`** — the render-on-change compositor: fill primary
   coverage with armor colour 1 and secondary with colour 2 (multiplied over the
   diffuse), drop the emblem decal on the chest; verified rendering headless via
   chromium (`make_proof_html.py` → `out/proof.png`). Its TypeScript port
   (`sveltekit/src/lib/utils/spartan-art.ts`, `emblem-art.ts`'s
   `<mask><image>`+`<rect>` technique) was **deleted in `76807ad` (2026-08-10)
   and was never imported by the app**; the published renders under
   `sveltekit/static/spartan/` were removed in the xc-studio S0 prep
   (2026-09-28) for the same reason. Nothing in the frontend consumes this
   pipeline's output today — `MCC-POSES.md`'s publish paths describe the
   pre-S0 layout.

## Run

```bash
MAPS=~/scratch/h2extract/out          # Xbox shared.map + mainmenu.map
python3 h2_render_model.py --maps "$MAPS" --out out --debug
python3 h2_bitmap.py       --maps "$MAPS" --out out/tex
python3 - <<'PY'   # bake opaque cc masks
from PIL import Image
cc=Image.open("out/tex/masterchief_cc.png").convert("RGBA")
R,G=cc.getchannel("R"),cc.getchannel("G")
Image.merge("RGB",(R,R,R)).save("out/tex/masterchief_cc_p.png")
Image.merge("RGB",(G,G,G)).save("out/tex/masterchief_cc_s.png")
PY
blender -b -P blender_build_render.py -- --npz out/masterchief.npz \
        --tex out/tex --out out/render --glb out/masterchief.glb --res 768
python3 spartan_composite.py --render out/render \
        --emblems ../../sveltekit/static/emblems --out out/composite --pose salute
```

## Output (verified)

- `masterchief.glb` — 1 mesh, 3086 tris, **34-joint skin** (JOINTS_0/WEIGHTS_0),
  embedded diffuse. Round-trips in Blender.
- `masterchief.obj` — universal triangulated mesh (pos/uv/normal).
- `masterchief_cc.png` (+ `_p`/`_s` masks), `masterchief.png` diffuse.
- `out/render/<pose>{,_p,_s}.png` — the hand-posed renders from
  `blender_build_render.py` (idle / t_pose / salute / crouch). The published set
  was **24 poses**, not four: the 21 MCC-retargeted stances from
  `blender_pose_render.py` (see `MCC-POSES.md`) plus the hand-posed extras.

## IP

Real Halo 2 asset; for Stewart's personal / LAN use with his own copy of the
game. Do not redistribute the extracted assets. `../reclaimer-ref/` holds copies
of Reclaimer source (**GPL-3.0**, © Gravemind2401 — this file said MIT until
2026-09-28, which was wrong) used only as a format reference: the decoders here
are a Python reimplementation and no Reclaimer code ships with the app, but the
licence question is the owner's call, not settled by this note.

## Notes / next

- `masterchief_bump` is P8_bump (Halo-2 bump palette) — decode stub present; not
  needed for the tint pipeline (add for nicer shading later).
- Arm-pose joint angles in `blender_build_render.py` are hand-set; the rig is
  correct, so tune freely. The `jmad #126` animation graph could drive real
  in-game pose clips instead of hand angles.
- Elite (`mode #376` + its `_cc`) decodes with the same code (`--tag ...elite`).
