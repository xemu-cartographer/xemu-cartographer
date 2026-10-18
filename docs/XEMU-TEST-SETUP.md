# XEMU-TEST-SETUP — getting a live xemu test instance running

> **Single entry point** for spinning up a real xemu instance (Halo CE or H2) to
> exercise cartographer's scraper + spectator visualizer against **live game
> data** — for a quick 1-instance smoke, a 4-player splitscreen match on one
> instance, or the full 4× fleet.
>
> This doc is **operational glue**. The canonical machinery + RE detail live in
> the sibling **`halo-offset-mapper`** repo; this doc tells you which pieces to
> use and in what order, and how to hand the result to cartographer. Don't
> duplicate those — read them:
>
> | source (in `../halo-offset-mapper`) | what it is |
> |---|---|
> | `scripts/fleet/README.md` | the generators (`make_tomls.py`, `make_eeproms.py`) + runtime (`padfleet.py`, `launch.sh`, `shot.sh`, `sdl_enum.py`) |
> | `docs/FLEET-4X-SETUP-2026-06-22.md` | full 4× concurrent-fleet design + per-instance matrix + validation (the "parallel xemu" reference) |
> | `docs/LIVE-MAPPING-RUNBOOK.md` | reading guest memory over QMP `memsave`, the verified CE offsets, the diff loop |
> | `docs/LAN-SYSTEM-LINK-2026-06-21.md` | wiring N instances into one system-link match (only needed for multi-console, NOT 4-player splitscreen) |
>
> **4-player splitscreen = ONE xemu with 4 pads.** You do **not** need the fleet
> or system-link for it — one instance, four virtual controllers. The fleet (4
> instances) is for multi-console / cross-instance work.

---

## 0. Environment that bit us before (read first)

These are non-obvious and cost real time when missed:

- **Display:** launch with `DISPLAY=:0 SDL_VIDEODRIVER=x11` and
  `XAUTHORITY=/run/user/1000/xauth_wnKXpc` so xemu renders into the KDE Xwayland
  session and is screenshot-able. (Wayland portal can't reliably grab xemu's GL
  surface — see offset-mapper `docs/RUNTIME-PASS-*`.)
- **QMP:** per-instance **unix** socket on the command line, not in the TOML:
  `-qmp unix:/tmp/xemu-qmp-<N>.sock,server,nowait`. Cartographer + the runtime
  scripts both key off this socket path.
- **This xemu build strips `screendump`/`pmemsave`** — use QMP **`memsave`** for
  targeted reads, and grab screenshots from the **X window** (`shot.sh`, which is
  `xdotool search` + ImageMagick `import`), not via QMP.
- **ptrace:** `kernel.yama.ptrace_scope` is **1** on this box. Cartographer reads
  `/proc/<xemu-pid>/mem` (see §3), which needs either `ptrace_scope=0` (a `sudo`
  one-liner) or `setcap cap_sys_ptrace=eip` on the server binary, or xemu calling
  `PR_SET_PTRACER`. The offset-mapper scripts avoid this entirely by reading over
  QMP `memsave` instead.
- **Don't hand-roll configs.** Use `make_tomls.py` / `make_eeproms.py` (distinct
  HDD + eeprom MAC + pad GUIDs per instance). A shared eeprom = MAC collision on
  any shared net segment.
- **Per-instance file layout** lives in `/home/stew/.local/share/xemu/xemu/`:
  `xemuN.toml`, `eepromN.bin`, `hddN.qcow2`, shared `mcpx_1.0.bin` (MCPX) +
  `Cerbios.bin` (flashrom). Stock `hdd.qcow2` / `eeprom.bin` stay **pristine**.

Prereqs: `xemu` on PATH, `python-evdev` (`python3 -c "import evdev"`), `xdotool`
+ ImageMagick (`import`), and the game ISO at
`../halo-offset-mapper/xbe-dropzone/Halo CE.iso` (or `Halo 2.iso`).

---

## 1. Quick single-instance smoke (no players, just "is it alive")

```sh
cd ~/repos/Stewball32/halo-offset-mapper

# one-time: generate eeproms + tomls into ~/.local/share/xemu/xemu/
python3 scripts/fleet/make_eeproms.py      # eeprom1..4.bin (distinct MACs)
python3 scripts/fleet/make_tomls.py        # xemu1..4.toml  (H2 ISO by default — see §2 to switch to CE)

# launch instance 1, detached, on :0
setsid scripts/fleet/launch.sh 1 >/tmp/xemu1.log 2>&1 &

# screenshot it (Xwayland window grab)
scripts/fleet/shot.sh 1 /tmp/xemu1.png
```

`launch.sh N` runs `xemu -config_path xemuN.toml -qmp unix:/tmp/xemu-qmp-N.sock,server,nowait`.
The instance boots the HDD's dashboard and auto-launches the DVD title.

---

## 2. Switch an instance to Halo CE

`make_tomls.py` defaults `dvd_path` to `Halo 2.iso`. For CE, point it at the CE
ISO and use a CE-capable HDD. Either edit `make_tomls.py`
(`H2_ISO = .../Halo CE.iso`, regenerate) **or** hand-tweak one `xemuN.toml`:

```toml
[sys.files]
dvd_path = '/home/stew/repos/halo-offset-mapper/xbe-dropzone/Halo CE.iso'
hdd_path = '/home/stew/.local/share/xemu/xemu/hdd2.qcow2'   # a Halo-installed HDD
```

> The fleet HDDs (`hdd2..hdd5.qcow2`) are the Halo-2 mapping set. Confirm the HDD
> you pick boots to a dashboard that launches the CE DVD (or has CE installed) —
> if not, install CE to a fresh HDD overlay first. Title-ID `0x4D530004` is what
> cartographer's `scraper.Detect` matches for CE.

---

## 3. 4-player splitscreen on ONE instance

Splitscreen needs **4 controllers on ports 1–4 of one xemu**. The pad fleet
already creates 8 distinct-GUID virtual pads; bind four of them to one instance.

**3.1 Start the pad fleet** (once; creates the uinput pads + FIFOs):

```sh
setsid python3 scripts/fleet/padfleet.py >/tmp/padfleet.log 2>&1 &
cat /tmp/h2fleet/pads.json     # GUID + FIFO for each of the 8 pads
```

Each pad is driven by writing one command per line to its FIFO
`/tmp/h2fleet/i<inst>-p<port>.fifo`. Vocabulary (from `padfleet.py`):
`a b x y lb rb back start guide ls rs` (button tap, `a 3` = 3×),
`up down left right` (d-pad), `rt/lt <0-255>` (triggers),
`move/bwd/sl/sr <s>` (left stick held), `looku/lookd/lookl/lookr <s>` (right
stick), `aimstep <dx> <dy>`, `hold <btn> <s>`, `neutral`, `quit`.

**3.2 A 4-pad single-instance TOML.** Bind ports 1–4 to four distinct pad GUIDs.
The pad GUIDs are deterministic (`predict_guid(version)` in `padfleet.py`,
versions `0x0201..0x0208`). Use pads `i1-p1, i2-p1, i3-p1, i4-p1`
(versions `0x0201, 0x0203, 0x0205, 0x0207`) so each player's FIFO is on a
different "instance" slot but all four feed **this** xemu:

```toml
# ~/.local/share/xemu/xemu/xemu-ss.toml  — ONE instance, 4 splitscreen pads.
[general]
show_welcome = false
games_dir = '/home/stew/repos/halo-offset-mapper/xbe-dropzone'

[input]
auto_bind = false
background_input_capture = true
gamepad_mappings = [
  { gamepad_id = '0300 81b8 5e040000 8e020000 0102 0000' },   # version 0x0201  -> FIFO i1-p1
  { gamepad_id = '0300 81b8 5e040000 8e020000 0302 0000' },   # version 0x0203  -> FIFO i2-p1
  { gamepad_id = '0300 81b8 5e040000 8e020000 0502 0000' },   # version 0x0205  -> FIFO i3-p1
  { gamepad_id = '0300 81b8 5e040000 8e020000 0702 0000' },   # version 0x0207  -> FIFO i4-p1
]

[input.bindings]
port1_driver = 'usb-xbox-gamepad'
port1 = '0300 81b8 5e040000 8e020000 0102 0000'
port2_driver = 'usb-xbox-gamepad'
port2 = '0300 81b8 5e040000 8e020000 0302 0000'
port3_driver = 'usb-xbox-gamepad'
port3 = '0300 81b8 5e040000 8e020000 0502 0000'
port4_driver = 'usb-xbox-gamepad'
port4 = '0300 81b8 5e040000 8e020000 0702 0000'

[sys]
mem_limit = '128'

[sys.files]
bootrom_path = '/home/stew/.local/share/xemu/xemu/mcpx_1.0.bin'
flashrom_path = '/home/stew/.local/share/xemu/xemu/Cerbios.bin'
eeprom_path = '/home/stew/.local/share/xemu/xemu/eeprom1.bin'
hdd_path = '/home/stew/.local/share/xemu/xemu/hdd2.qcow2'
dvd_path = '/home/stew/repos/halo-offset-mapper/xbe-dropzone/Halo CE.iso'
```

> Verify the GUID strings against `pads.json` after `padfleet.py` runs — the GUID
> bytes (`predict_guid`) are stable for a given version, but copy them from the
> manifest to be safe. Remove the spaces in `gamepad_id` if your xemu build wants
> them unspaced (match what `make_tomls.py` emits — it writes them unspaced).

Launch it (adapt `launch.sh`, or directly):

```sh
DISPLAY=:0 SDL_VIDEODRIVER=x11 XAUTHORITY=/run/user/1000/xauth_wnKXpc \
  xemu -config_path ~/.local/share/xemu/xemu/xemu-ss.toml \
       -qmp unix:/tmp/xemu-qmp-1.sock,server,nowait -name ce-splitscreen &
```

**3.3 Drive the match.** Boot to the Halo CE main menu, then build the
splitscreen lobby by writing to the pad FIFOs, **screenshotting between steps**
(`scripts/fleet/shot.sh 1`) to confirm menu state before each input — xemu boots
slowly and the menu is stateful:

```sh
P1=/tmp/h2fleet/i1-p1.fifo; P2=/tmp/h2fleet/i2-p1.fifo
P3=/tmp/h2fleet/i3-p1.fifo; P4=/tmp/h2fleet/i4-p1.fifo
echo a   > $P1                 # P1 advances through the menus
echo start > $P2; echo start > $P3; echo start > $P4   # P2-4 press Start to join splitscreen
# … navigate: Multiplayer → Split Screen → pick gametype (Slayer) → pick map
#   (Chill Out / Prisoner) → Start. Verify each screen with shot.sh before acting.
```

> The exact button sequence is menu-state-dependent; drive it screenshot → input
> → screenshot. Once players spawn, the scraper (next section) confirms 4 placed
> players with positions.

---

## 4. Attach cartographer's scraper + open the visualizer

With the match live and the cartographer server running (`task dev` →
PocketBase on `PUBLIC_PB_PORT`, default 8090; this box runs it on **8093**):

**4.1 Allow the memory read** (one-time, ptrace_scope=1 → 0):

```sh
echo 0 | sudo tee /proc/sys/kernel/yama/ptrace_scope      # or setcap cap_sys_ptrace=eip on the server binary
```

**4.2 Start the scraper** against the instance's QMP socket (admin JWT required;
grab it from the PB admin UI). The server finds the xemu PID by scanning
`/proc/*/cmdline` for the socket path, connects QMP for GVA→HVA translation, and
opens `/proc/<pid>/mem`:

```sh
curl -X POST http://127.0.0.1:8093/api/admin/scraper/start \
  -H "Authorization: $JWT" \
  -d '{"name":"ce-ss","sock":"/tmp/xemu-qmp-1.sock"}'      # 201 on success; 502 if QMP init fails
```

`scraper.Detect` auto-picks the CE plugin from title `0x4D530004`. The runner now
streams `current_state`/`tick`/`event` envelopes to the `host:ce-ss` room.

**4.3 Mint an overlay token + open the spectator visualizer.** At
`http://127.0.0.1:8093/overlays/manage/` mint a token scoped `host:ce-ss`, then:

- 2D floorplan: `http://127.0.0.1:8093/visualizer/ce-ss/?token=<TOKEN>`
- 3D scene:     `http://127.0.0.1:8093/visualizer3d/ce-ss/?token=<TOKEN>`

(`?mock=1` previews with sample data; `?map=chillout|prisoner|bloodgulch` renders
the cached geometry with staged mock players — no live game needed.)

---

## 5. Teardown

```sh
curl -X POST http://127.0.0.1:8093/api/admin/scraper/stop/ce-ss -H "Authorization: $JWT"
pkill -f 'xemu-qmp-1.sock' ; pkill -f padfleet.py
echo 1 | sudo tee /proc/sys/kernel/yama/ptrace_scope      # restore if you changed it
rm -f /tmp/xemu-qmp-1.sock
```

---

## 6. Gotchas

- **No CE on the chosen HDD** → boots to dashboard with no title; pick a
  CE-installed HDD or install CE to a fresh overlay first.
- **Scraper `502 QMP init`** → xemu not at the socket path, or QMP not up yet
  (give it a few seconds after launch).
- **Reads return zeros / EPERM** → ptrace_scope not 0 (§4.1), or the server isn't
  same-uid as xemu.
- **Black screenshot** → grabbing the hidden "SDL Offscreen Window"; `shot.sh`
  already filters for the visible `xemu | v…` window by QMP-socket PID.
- **Pads do nothing** → `padfleet.py` not running, or the TOML GUIDs don't match
  `pads.json`, or `auto_bind` isn't `false` (so xemu grabbed a different pad).
- **Duplicate pad GUIDs** → a stale `padfleet.py` from a previous session is still
  running. `sdl_enum.py` should show each fleet GUID exactly ONCE; if a GUID
  repeats, xemu may bind the wrong (FIFO-less) duplicate and your inputs do
  nothing. Kill the extra `padfleet.py`, then relaunch xemu so it re-binds.
- **Map changed in-game but the visualizer still shows the old map** → the scraper
  resolves the scenario/map identity at **start** time, not per-tick. After an
  in-game map change (e.g. quit to lobby → pick a new map), **restart the runner**
  (`POST /scraper/stop/<name>` then `/scraper/start`) so it re-detects the new
  scenario; the roster/positions update live, but the map tag does not.
- **No sudo for `ptrace_scope`?** Relaunch xemu with the `PR_SET_PTRACER_ANY`
  LD_PRELOAD shim (a ~10-line C constructor calling
  `prctl(PR_SET_PTRACER, PR_SET_PTRACER_ANY)`, built with `gcc -shared -fPIC`):
  `LD_PRELOAD=/tmp/ptrace_shim.so xemu …`. xemu then lets any same-uid process
  (the cartographer server) read its `/proc/<pid>/mem` without changing the
  system-wide `ptrace_scope`.
