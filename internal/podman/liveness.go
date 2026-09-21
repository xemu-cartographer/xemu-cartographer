package podman

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// defaultScreenLiveTimeout bounds the `podman inspect` liveness probe so a hung
// or slow podman can't stall a screen request. Overridable via
// Config.ScreenLiveTimeout.
const defaultScreenLiveTimeout = 2 * time.Second

// ScreenLive reports whether the browser half of the named pair
// (<name>-browser) is currently running.
//
// The screen reverse-proxy (routes/containers/proxy.go) uses this to fast-fail a
// *recorded-but-dead* container instead of dialing a port nothing is listening
// on and hanging the full dial-retry budget before surfacing as a 502. A
// container that is unknown, "created", "exited", or absent from podman reads
// as not-live; only "running" is live (a running-but-still-booting container
// reads as live and the proxy's dial retry covers its nginx warm-up). Bounded
// by an internal timeout (Config.ScreenLiveTimeout, default 2s) so it can never
// itself become the thing that hangs.
func (m *Manager) ScreenLive(name string) bool {
	m.mu.Lock()
	_, known := m.containers[name]
	m.mu.Unlock()
	if !known {
		return false
	}
	status, err := m.inspectStatus(name + "-browser")
	if err != nil {
		return false
	}
	return status == "running"
}

// inspectStatus shells `podman inspect --format {{.State.Status}}` against a
// specific container (a raw podman name, e.g. "<name>-browser"), bounded by the
// configured screen-live timeout, and returns the parsed status string.
func (m *Manager) inspectStatus(target string) (string, error) {
	timeout := m.cfg.ScreenLiveTimeout
	if timeout <= 0 {
		timeout = defaultScreenLiveTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := m.runCtx(ctx, "inspect", "--format", "{{.State.Status}}", target)
	if err != nil {
		return "", err
	}
	return parsePodmanStatus(out), nil
}

// runCtx is run (see podman.go) with a context deadline, so a hung podman
// invocation is bounded instead of blocking indefinitely.
func (m *Manager) runCtx(ctx context.Context, args ...string) ([]byte, error) {
	parts := strings.Fields(m.cfg.PodmanCmd)
	if len(parts) == 0 {
		parts = []string{"podman"}
	}
	full := append(parts[1:], args...)
	cmd := exec.CommandContext(ctx, parts[0], full...)
	return cmd.CombinedOutput()
}

// parsePodmanStatus normalizes the output of
// `podman inspect --format {{.State.Status}}` — which may be JSON-quoted and/or
// newline-terminated — into a bare status string ("running", "exited", …).
func parsePodmanStatus(raw []byte) string {
	s := string(raw)
	var unquoted string
	if json.Unmarshal(raw, &unquoted) == nil {
		s = unquoted
	}
	return strings.TrimSpace(s)
}
