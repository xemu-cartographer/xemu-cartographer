# WebSocket API — moved

The WebSocket wire contract (frames, envelope, rooms, classes, payload shapes, tiers,
`finished_game`, compatibility policy) now lives with its implementation in the
**xc-scraper** repository: [`xc-scraper/docs/wire.md`](https://github.com/xemu-cartographer/xc-scraper/blob/main/docs/wire.md)
(sibling checkout `../xc-scraper`; module `github.com/xemu-cartographer/xc-scraper`).

The Go types are `github.com/xemu-cartographer/xc-scraper/wire` (aliased here by `internal/websocket` and
`internal/websocket/rooms`; the runner that emits them is `xc-scraper/runner`, framed into rooms by
`internal/leaguescraper`); the TypeScript mirror is vendored into `sveltekit/src/lib/types/scraper-v2.ts`
and the golden fixtures into `sveltekit/src/lib/types/wire-fixtures/` by `task sync-wire`
(`task sync-wire:check` / the CI "Wire sync check" step fail on drift). Host policy (auth doors, `?console=`, room gating) is its §1.

## League-side behaviour that is not part of the contract (step 8)

The dialect above is frozen; what changed in step 8 is **how the league serves it**.
The first holds in both in-process and wire mode; the second is **wire mode only**
(`XC_SCRAPER_URL` set — the league consumes the
[xc-scraper daemon](../../xc-scraper/docs/wire.md) and rebroadcasts its frames
byte-for-byte into the same `host:*` rooms; in-process there is no upstream and no
`1012` close ever happens):

- **`request_events` / `request_probe` are asynchronous.** The Hub dispatches them off
  its `Run` goroutine (`internal/websocket/handlers/request_events.go`, `async`), so a
  slow reply — in wire mode an HTTP hop to the daemon's
  `GET /api/instances/{name}/events|probe` — never stalls other clients' frames. The
  reply frame shape is unchanged; only its ordering relative to concurrent broadcasts
  is not guaranteed (it never was for a different room; now it also is not for the same
  connection).
- **Close `1012` "upstream resync" — no error frame (wire mode only).** When the
  league's upstream stream (re)connects to the daemon, an instance (re)appears, or an
  instance's `seq` regresses **and** its `started_at` moved (an epoch change: the daemon
  restarted or re-attached that xemu — a lower `seq` with an unchanged `started_at` is
  just a stale frame, dropped from the mirror and relayed without eviction, per wire.md
  "About seq"), every client holding a `host:*` room for the affected instance(s) — the
  instance room and its `:class` children, never a sibling sharing the name prefix — is
  closed with status `1012`
  (`ServiceRestart`) and reason `upstream resync` (`Hub.EvictRoomPrefix`, called from
  `internal/xcclient`). There is **no** `error` frame first: wire.md has no resync code
  and the frozen dialect gains none. Clients must treat it as any other close —
  reconnect, re-`join_room`, and let join replay rebuild state (the frontend already
  reconnects on every close it did not see `session_revoked` before). Third-party
  consumers that only saw `session_revoked` / `room_left` before should expect this
  close too. The `1012` close is **league-only behaviour, not part of the wire
  contract** — the daemon never emits it and the frozen dialect has no resync code;
  like the daemon-side extensions in `xc-scraper/docs/wire.md` Appendix A, it sits
  outside the contract, on the league side.

Daemon-only extensions (the `xc:` room family, the read/control HTTP API, the
finished-game webhook) are documented in `../xc-scraper/docs/wire.md` appendix A and
`../xc-scraper/docs/control.md`. Browsers never see an `xc:*` room: the one frame the
league consumes from that family (`host_runner` on `xc:hostrunner`, joined only with
`HOSTRUNNER_ENABLED`) is re-framed onto the league's `admin` room in the shape the
studio already used with the in-process host runner (`internal/xcclient/rebroadcast.go`).
