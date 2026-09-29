# Deployments

How cartographer is deployed, under the host's **/srv Hosting Standard**
(`/srv/shared/HOSTING-PLAN.md`; canonical registries in `/srv/registry/` —
[`SITES.md`](/srv/registry/SITES.md) + [`PORTS.md`](/srv/registry/PORTS.md)).
Three tiers. Deployed tiers hold **artifacts + state only** (binary, `pb_data/`,
`.env`) — never a git checkout; builds happen in the repo, installs land under
`/srv`.

> **Cartographer runs NATIVELY, not in Docker.** It provisions xemu containers
> and needs host podman, `/dev/kvm`, `/dev/dri` and raw sockets, so each tier is
> a plain binary with its own run dir, `.env`, port and `pb_data`.
>
> Since step 8 a tier is **two** native processes: the league (`bin/server`) and
> the **xc-scraper daemon** (`bin/xc-scraper`, DESIGN-STEP8 §11 / D-14). The
> daemon is not containerised either — it needs the QMP socket directory,
> `open("/proc/<pid>/mem")` of every xemu process and same-host PIDs; in a
> container that would mean `pid=host` + privileged + host networking, which buys
> nothing over a plain binary. The `Containerfile` image is therefore
> **league-only**; a container deployment still needs the daemon on the host.

## Tiers

| Tier | Purpose | Runs from | How | Port | Hostname | Bot |
| --- | --- | --- | --- | --- | --- | --- |
| **dev** | live-reload coding | this working tree (Air + Vite HMR) | `./run-dev.sh` | `19099` vite / `19090` backend | `dev.norcal.pro` (only while up) | **off** (forced) |
| **pre** (test) | the gate before prod — merged branches soak here | `/srv/http/xemu-cartographer/pre/` | `/srv/registry/srv-pre.sh xemu-cartographer [ref]` → systemd **user** unit `site-xemu-cartographer-pre` | `18099` | `beta.norcal.pro` | cart-beta **test** app (per tier `.env`) |
| **prod** | the live site | `/srv/http/xemu-cartographer/prod/` | systemd unit `site-xemu-cartographer-prod` (migrated 2026-08-27; still root + `0.0.0.0` — de-root/loopback is a pending follow-up) | `8099` | `lan.norcal.pro` | cart **prod** app |

- Ports are claimed in the **canonical host registry**
  `/srv/registry/PORTS.md` (cartographer's spread — `8099`, `18099`,
  `19090/19099`, rig reservations `3300-3399` tcp + `9970-9989` udp — is
  grandfathered; don't renumber). Audit with `sudo /srv/registry/check-ports.sh`.
- Tiers bind **loopback only** (prod's `0.0.0.0` is the known exception being
  fixed); cloudflared is the public front door, the LAN Caddy serves
  `lan.local`.
- Data is separate per tier: each run dir has its own `pb_data/`. Dev's is
  ephemeral (`tmp/pb_data`, wiped on exit).
- **Discord: one gateway per bot token** — each tier that runs the bot needs its
  own Discord application; dev never runs it.
- Naming hazard: `lab.norcal.pro` is **norcal-halo-site**, not this project — the
  dev tier is `dev.norcal.pro` (the cloudflared ingress and
  `sveltekit/vite.config.dev.ts:28-30` agree); prod `lan.norcal.pro` sits in the
  same ingress list — double-check which you're editing.
- *Planned (xc-studio):* the OBS overlay surface moves to its own **static**
  tiers, `studio.norcal.pro` (prod) and `studio-beta.norcal.pro` (pre) — no
  binary, no `pb_data`, no port in this table; documented in the private
  `xemu-cartographer/xc-studio` repo, not in this file.

### The xc-scraper daemon per tier

| Tier | Daemon port | Unit | Webhook target (league) | State dir |
| --- | --- | --- | --- | --- |
| **dev** | `8992` | none — `task dev` runs it under Air (`dev:scraper`) | `http://127.0.0.1:$PUBLIC_PB_PORT/api/xc/finished_game` | `../xc-scraper/tmp/xc-scraper-state` |
| **pre** | `8991` | `xc-scraper-pre` (system unit, root) | `http://127.0.0.1:18099/api/xc/finished_game` | `<tier>/xc-scraper-state/` |
| **prod** | `8990` | `xc-scraper-prod` (system unit, root) | `http://127.0.0.1:8099/api/xc/finished_game` | `<tier>/xc-scraper-state/` |

- The unit template and install steps live in the sibling repo:
  [`../xc-scraper/deploy/xc-scraper.service`](../../xc-scraper/deploy/xc-scraper.service)
  + [`../xc-scraper/deploy/README.md`](../../xc-scraper/deploy/README.md)
  (`sed` the `<tier>` / `899X` / `<web port>` placeholders). It runs as root because
  the podman stack runs xemu as root (README "Memory access").
- `task build` produces **both** `bin/server` and `bin/xc-scraper`. Since
  2026-09-16 the install script (`srv-pre.sh`) builds the sibling too and installs
  `bin/xc-scraper` into the tier next to `bin/server` (see "Deploying" below);
  `bin/xc-scraper --version` prints the sibling's own `git describe` string, which
  `BUILD-INFO` records on its `sibling` line next to the league's.
- The daemon's ports `8990-8992` are a reversible default (D-14), claimed in
  `/srv/registry/PORTS.md` since 2026-09-17. Pre runs on `8991` (`xc-scraper-pre`,
  installed 2026-09-16); prod on `8990` (`xc-scraper-prod`, a root system unit
  installed by the owner at the first prod cut-over, 2026-09-18 — see "Deploying
  prod").
- The daemon and the league share the tier's `.env` (`EnvironmentFile`): the daemon
  reads `XC_SCRAPER_TOKEN` / `XC_SCRAPER_CONTROL_TOKEN` / `XC_SCRAPER_WEBHOOK_TOKEN`
  (+ any `XC_SCRAPER_<FLAG>` twin), the league reads the same three plus
  `XC_SCRAPER_URL=http://127.0.0.1:<daemon port>` — the switch that puts it in
  wire mode (R1). No systemd ordering between the two units: the league reconnects
  forever and the daemon spools finished games until the webhook answers 2xx.
- **Cut-over / rollback order** (the one hard rule — two attachers on one QMP
  directory persist every game twice): cut over by booting the league with
  `XC_SCRAPER_URL` set, then starting the daemon; roll back by stopping the daemon
  **first**, then restarting the league without `XC_SCRAPER_URL`. Step list in
  [RUNBOOK.md](RUNBOOK.md) ("Switch the scraper feed to the xc-scraper daemon").

## Promotion path

```
dev (working tree)  ──►  pre (/srv, :18099)  ──►  prod (/srv, :8099)
   run-dev.sh            srv-pre.sh               site-xemu-cartographer-prod
```

Nothing should reach prod without soaking on pre. For anything touching the
schema, pre applies the migration first — see [MIGRATIONS.md](MIGRATIONS.md).

## Deploying pre

```sh
/srv/registry/srv-pre.sh xemu-cartographer beta     # build the beta branch tip
/srv/registry/srv-pre.sh xemu-cartographer <ref>    # any committish
/srv/registry/srv-pre.sh xemu-cartographer status|logs|restart|stop|info
```

What it does (see the script header for the full contract): builds the ref in a
**temporary detached worktree** (only committed code ships — the repo checkout
is never touched). Since the step 7 restructure `go.mod` resolves
`github.com/xemu-cartographer/xc-scraper` via `replace => ../xc-scraper`, so the
script (sibling-aware since 2026-09-16) adds a **second detached worktree** of
the local `~/repos/xc-scraper` checkout next to the flagship one — `main` by
default, `SIBLING_REF=<committish>` overrides — so the `replace` resolves without
any network access or credential (the sibling is the **private** org repository
`github.com/xemu-cartographer/xc-scraper`; only CI needs the
`XC_SCRAPER_CHECKOUT_TOKEN` secret, the host builds from the local clones —
`git -C ~/repos/xc-scraper pull` first if pre should get a newer sibling).
`task container:build` does the same for images by passing the sibling as a
named build context (`podman build --build-context xc-scraper=../xc-scraper`,
see `Containerfile`; `.containerignore` keeps `sveltekit/node_modules`, `pb_data`,
`.env*` etc. out of the context — without it the frontend stage's `pnpm build`
aborts on the host's `node_modules`). The script then builds `bin/server` (league
worktree) and `bin/xc-scraper` (sibling worktree, `-X main.version=<sibling git
describe>`, verified against `--version`), installs both plus `pb_public/` +
`tools/game-maps/` into the tier, regenerates `run.sh`, writes `BUILD-INFO` with
provenance verification (league and `sibling` lines), then (re)starts the
`site-xemu-cartographer-pre` **user** unit (linger is on — survives reboots, no
sudo) and polls `/api/health`. It never touches `.env` or `pb_data/`; a missing
`.env` is seeded once from the repo's `.env.pre` / `*.example` and must be
reviewed. After the health check it restarts the **system** unit
`xc-scraper-pre` with one non-interactive `sudo -n systemctl restart
xc-scraper-pre` — the only sudo in the script; without a matching sudoers rule
(`norcal ALL=(root) NOPASSWD: /usr/bin/systemctl restart xc-scraper-pre`) it
prints the command for the operator to run by hand instead. The order relative to
the league restart does not matter: the daemon spools finished games across its
own restart and the league reconnects.

**Pending migrations apply on boot**, so a healthy check also proves the
migrations applied.

## Deploying prod

Prod is a root **system** unit (`site-xemu-cartographer-prod`: `User=root`,
`EnvironmentFile=<tier>/.env`, `ExecStart=<tier>/server serve --http=0.0.0.0:8099`
— the league binary sits at the tier root, `pb_data/` next to it, no `--dir`),
so a deploy is two steps with a root gate between them:

```sh
/srv/registry/srv-prod-stage.sh [ref]        # 1. build <ref> (default main) → stage as *.new, nothing restarts
/srv/registry/srv-prod-stage.sh info         #    installed vs staged
sudo /srv/registry/srv-prod-cutover.sh       # 2. stop → snapshot pb_data → swap → start → health + boot lines
sudo /srv/registry/srv-prod-cutover.sh rollback
```

`srv-prod-stage.sh` is `srv-pre.sh`'s build stage pointed at prod (same detached
worktrees of the local `~/repos/xemu-cartographer` + `~/repos/xc-scraper`
checkouts, same ldflags stamps and provenance checks, `SIBLING_REF=` override).
It warns when pre is not on the ref being staged — prod gets what soaked on pre.
It writes only inert files next to the live ones: `server.new`,
`bin/xc-scraper.new`, `pb_public.new/`, `tools.new/`, `BUILD-INFO.new` (the
daemon lives under `bin/` as in pre and the unit template; the league stays at
the tier root because the prod unit says so). No sudo.

`srv-prod-cutover.sh` (root) stops `xc-scraper-prod` if installed, stops the
league, snapshots `pb_data/` → `pb_data.bak-<stamp>` (reflink, `/srv` is btrfs),
renames each live artifact to `*.old-<stamp>` and the staged one into place,
writes `BUILD-INFO` (with a `rollback` line naming the stamp), starts the league
and polls `/api/health` (migrations apply before OnServe, so healthy ⇒ migrated),
prints the `authz:` / `leaguescraper:` boot lines, then starts the daemon unit
again. `rollback` reverses the renames (code only — see "Rollback" below) and
leaves the daemon **stopped**, because a league that came back without
`XC_SCRAPER_URL` would otherwise share the QMP directory with it.

**First prod cut-over to the new system: 2026-09-18 08:30 PT, `0aa2f31` → `34ca2a3`
(sibling `e03f82f`), stamp `20260918-0830`.** The owner added the league block to
prod `.env` (`XC_SCRAPER_URL=http://127.0.0.1:8990` + the three tokens,
`WS_ALLOWED_ORIGINS`, `XC_SCRAPER_HOSTRUNNER` / `XC_SCRAPER_HOST_DRIVE_MARKER`),
installed `xc-scraper-prod` from the template (`../xc-scraper/deploy/README.md`,
`<tier>=/srv/http/xemu-cartographer/prod`, `8990`, `<web port>=8099`, enabled,
**not** started), then ran the cut-over once — the script starts the league in
wire mode first and the daemon after it, which is the R1 order. Result: the four
migrations pending on prod (`1788211877_games_ingest_dedupe`,
`1788300001_roles_scopes`, `1788300002_api_tokens`,
`1788300003_user_roles_granted_by_optional`) applied on boot, `authz: roles ok`,
`admins=6`, `leaguescraper: mode=wire url=http://127.0.0.1:8990 token=set
control=set`, league healthy in 4 s, daemon healthy 6 s later; a box that was
running through the swap (`c-e-deez-nuts`, Halo CE) was re-attached by the daemon
at boot (title recognised, lobby enumerated) and the league joined its rooms over
the wire (`xcclient: connected … hello: 1 instance(s), 7 room(s) joined`). The
three `xcclient: … connection refused` retries between league start and daemon
start are expected. Rehearsed beforehand: the same four migrations were applied
by the `34ca2a3` binary to a copy of prod's `pb_data` on 2026-09-17 — clean boot.
Rollback point: `server.old-20260918-0830` etc. + `pb_data.bak-20260918-0830` (58M).

## Rollback

```sh
/srv/registry/srv-pre.sh xemu-cartographer <last-good-ref>
sudo /srv/registry/srv-prod-cutover.sh rollback      # prod: back to *.old-<stamp>
```

⚠️ Code rolls back; **migrations do not**. An applied migration stays applied —
that's why they're proven on pre first. To undo a schema change, write a new
forward migration. (Prod's cut-over leaves `pb_data.bak-<stamp>`; restoring it
by hand is the full revert and loses everything written since.)

## Backups

`/srv/backups/` + a nightly snapshot timer is a planned phase of the hosting
standard (not live yet). Until then, copy a tier's `pb_data` yourself before
risky migrations: `cp -r <tier>/pb_data <tier>/pb_data.bak-$(date +%Y%m%d)` —
and prune old copies periodically; they are full copies.

## History

The pre-/srv deployment generations are retired: `deploy-beta.sh` (in-repo
full-cycle wrapper), then `~/xcarto-beta` + `pull-beta.sh` (home-dir run dir,
manual start), then briefly a hand-rolled `/srv/.../pre` on an unregistered
port. All superseded by `srv-pre.sh` + systemd units per the hosting standard.
