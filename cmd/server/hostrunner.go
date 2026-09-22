package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/xemu-cartographer/xc-scraper/hostrunner"
	"github.com/xemu-cartographer/xemu-cartographer/internal/guards"
	"github.com/xemu-cartographer/xemu-cartographer/internal/podman"
	ws "github.com/xemu-cartographer/xemu-cartographer/internal/websocket"
)

// newHostRunnerSink builds the EventSink that fans the host runner's observable
// stream (player intents, emitted keys, native box/team counts) to the admin WS
// room. It reads svc.WS lazily at Emit time, so it's safe to construct the
// registry before the hub is populated (the shared *Services pointer pattern).
// Each event is wrapped in a "host_runner"-typed Message so the admin screen can
// filter it from other admin-room traffic.
func newHostRunnerSink(svc *guards.Services) hostrunner.SinkFunc {
	return func(ev hostrunner.RunnerEvent) {
		if svc == nil || svc.WS == nil {
			return
		}
		payload, err := json.Marshal(ev)
		if err != nil {
			log.Printf("hostrunner: marshal event: %v", err)
			return
		}
		msg, err := json.Marshal(ws.Message{Type: "host_runner", Room: "admin", Payload: payload})
		if err != nil {
			log.Printf("hostrunner: marshal message: %v", err)
			return
		}
		svc.WS.SendToRoomRaw("admin", msg)
	}
}

// hostRunnerURLResolver maps an instance name to its container's websockify URL
// (the vncinput input target) via the podman manager. The manager pointer is
// resolved at call time through get so it can be wired before podman is
// constructed; returns ("", false) when containers are disabled or the port
// isn't known — the runner then attaches with no input channel (ticks + emits
// state, presses nothing).
func hostRunnerURLResolver(get func() *podman.Manager) func(string) (string, bool) {
	return func(name string) (string, bool) {
		mgr := get()
		if mgr == nil {
			return "", false
		}
		info, ok := mgr.Get(name)
		if !ok || info.Ports.BrowserWeb == 0 {
			return "", false
		}
		return fmt.Sprintf("ws://127.0.0.1:%d/websockify", info.Ports.BrowserWeb), true
	}
}

// envBool reads a boolean env var (strconv.ParseBool semantics), falling back to
// def when unset or unparseable.
func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
