// Package podman manages xemu + browser container pairs via the podman CLI.
package podman

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config holds settings for the podman Manager.
type Config struct {
	Enabled        bool
	SocketDir      string
	SharedDir      string
	InitDir        string
	ConfigsDir     string
	BrowserDir     string
	BrowserInitDir string
	PortBase       int
	PortStride     int
	HostIP         string // IP/hostname for remote access; defaults to "localhost"

	// NamePrefix namespaces every container this deployment creates (empty in
	// prod). Callers that generate names (the admin create route + the per-player
	// play flow) prepend it, so a beta sharing the host podman daemon can't share
	// a container name with prod. See LoadFromEnv / Manager.NamePrefix.
	NamePrefix string

	// DVDPath is an optional host path to a game ISO mounted read-only into each
	// xemu container (at containerDVDPath). Empty = no DVD. Acts as the GLOBAL
	// default disc when a Create doesn't specify a per-instance ISO. Needed for
	// root HDDs that boot a game from disc rather than from the HDD install.
	DVDPath string

	// ISODir is the shared host directory holding the game ISO library. A
	// per-instance ISO named (not absolute) in CreateOptions.GameISO resolves
	// against this dir. Default SharedDir/isos. ISOs are bind-mounted read-only
	// into their instance (at containerDVDPath); they are never copied onto the
	// per-instance overlay, which stays lean.
	ISODir string

	// PodmanCmd is the command (with optional leading args) used to invoke
	// podman. Default "podman" runs rootless under the server user.
	// Set to e.g. "sudo -n podman" to escalate when device passthrough is
	// required (KVM/DRI/NET_ADMIN); requires a passwordless sudoers rule.
	PodmanCmd string

	// RootHDD is the basename of the canonical, read-only root qcow2 (the
	// Halo-installed disk) inside SharedDir/hdds. Every per-instance overlay
	// uses it as a copy-on-write backing file. Default "_default.qcow2".
	RootHDD string

	// QemuImgCmd is the qemu-img binary used to create overlays on the host.
	// Default "qemu-img"; must be installed on the host running the server.
	QemuImgCmd string

	// ScreenLiveTimeout bounds the `podman inspect` liveness probe that the
	// screen reverse-proxy runs (Manager.ScreenLive) before dialing a container's
	// browser port. Keeps a hung podman from stalling a screen request. Zero
	// falls back to defaultScreenLiveTimeout (2s).
	ScreenLiveTimeout time.Duration

	// SetConsoleName, when true (default), writes the container name into the
	// instance's Xbox console name (E:\UDATA\NICKNAME.XBN) inside its overlay at
	// create time, so instances are distinguishable on system link / the
	// dashboard. Best-effort: skipped with a warning if the tooling
	// (qemu-storage-daemon + python3 + pyfatx) is unavailable.
	SetConsoleName bool

	// Tooling for the console-name write (all have sensible defaults).
	QemuStorageDaemonCmd string // default "qemu-storage-daemon"
	PythonCmd            string // default "python3"
	FatxToolPath         string // default <InitDir>/../tools/fatx_console_name.py

	// SetBrowserTrust, when true (default), pre-seeds the sidecar's Firefox profile's
	// NSS trust store with the instance CA at create time (host certutil), so the
	// sidecar loads xemu's HTTPS noVNC view without a "risky connection" warning on
	// first boot. Best-effort: skipped with a warning if certutil is unavailable
	// on the host, in which case the bind-mounted policies.json belt
	// (containers/browser/init/60-trust-xemu-cert.sh) is the fallback.
	SetBrowserTrust bool
	// CertutilCmd is the NSS certutil binary used to pre-seed the firefox trust
	// store. Default "certutil" (the nss / nss-tools package on the host).
	CertutilCmd string

	// Defaults for container environment variables.
	Encoder          string
	Framerate        int
	CRF              int
	Width            int
	Height           int
	PixelfluxWayland bool
	DRINode          string
	ShmSize          string
	BrowserShmSize   string
}

const (
	xemuImage    = "lscr.io/linuxserver/xemu:latest"
	browserImage = "docker.io/jlesage/firefox"

	// containerDVDPath is where a per-instance DVD/ISO is bind-mounted inside the
	// xemu container. The instance toml's dvd_path is patched to this path by the
	// init script (containers/xemu/init/02-patch-toml.sh) so xemu attaches the
	// disc at boot. Keep the two in sync.
	containerDVDPath = "/game.iso"

	// containerUID / containerGID are the uid/gid both halves of a pair run
	// as INSIDE their containers (xemu's PUID/PGID, the browser's
	// USER_ID/GROUP_ID). Root, always: xemu's pcap netplay needs the
	// NET_ADMIN/NET_RAW caps in its effective set, which Linux grants only
	// to uid 0. Podman itself is rootful (directly as root, or via
	// CONTAINERS_PODMAN_CMD=sudo -n podman), so this is independent of the
	// uid the league process runs as.
	containerUID = 0
	containerGID = 0
)

// CreateOptions configures a new instance beyond its name.
type CreateOptions struct {
	// GameISO optionally attaches a game XISO as this instance's DVD. It may be
	// an absolute host path or a name resolved against Config.ISODir (the shared
	// ISO library). Empty falls back to Config.DVDPath (the global default disc).
	//
	// The ISO is bind-mounted read-only at containerDVDPath; the game is never
	// copied onto the per-instance overlay, which stays lean. A game disc present
	// at cold boot is auto-launched by the master image (Cerbios/Xbox boot path),
	// so an instance created with a GameISO boots STRAIGHT into that game — no
	// HDD-installed game, no dashboard step, no per-container config. Verified
	// live (see ADR-0004): with no ISO the instance sits on the UnleashX
	// dashboard; with an ISO it boots that disc's game.
	GameISO string

	// DisplayName is the canonical PRETTY name (printable ASCII, ≤15) that the
	// container name was slugified from. When set, it — not the mangled podman
	// name — is written as the Xbox console nickname and persisted as the
	// instance's canonical identity. Empty falls back to the container name (the
	// pre-decoupling behavior).
	DisplayName string
}

// Manager creates and controls podman containers.
type Manager struct {
	cfg        Config
	store      Store
	containers map[string]*ContainerInfo
	mu         sync.Mutex
	hooksMu    sync.RWMutex
	hooks      Hooks // lifecycle callbacks (hooks.go); fired outside mu
}

// NewManager loads persisted state via store and returns a ready Manager.
func NewManager(cfg Config, store Store) (*Manager, error) {
	containers, err := store.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("podman: load state: %w", err)
	}
	if containers == nil {
		containers = make(map[string]*ContainerInfo)
	}
	return &Manager{cfg: cfg, store: store, containers: containers}, nil
}

// Create provisions a new xemu + browser container pair without starting them,
// using the global default disc (Config.DVDPath, if any). See CreateWithOptions
// to attach a per-instance game ISO.
func (m *Manager) Create(name string) (*ContainerInfo, error) {
	return m.CreateWithOptions(name, CreateOptions{})
}

// createWithOptions provisions a new xemu + browser container pair without
// starting them, optionally attaching a per-instance game ISO as the DVD (see
// CreateOptions.GameISO). CreateWithOptions (hooks.go) wraps it with the
// Created lifecycle hook, which runs after mu is released.
func (m *Manager) createWithOptions(name string, opts CreateOptions) (*ContainerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.containers[name]; exists {
		return nil, fmt.Errorf("container %q already exists", name)
	}

	// Resolve the per-instance disc BEFORE provisioning anything, so a bad ISO
	// path aborts cleanly (no orphan overlay/config/containers).
	dvdPath, err := m.resolveGameISO(opts.GameISO)
	if err != nil {
		return nil, err
	}

	idx := nextIndex(m.containers)
	ports := AllocatePorts(m.cfg.PortBase, m.cfg.PortStride, idx)
	info := &ContainerInfo{
		Name:        name,
		DisplayName: opts.DisplayName,
		Index:       idx,
		Ports:       ports,
		GameISO:     dvdPath,
		Created:     time.Now(),
	}

	// Provision the instance's copy-on-write HDD overlay (thin layer over the
	// shared read-only root) before anything else, so a missing/locked root
	// aborts cleanly without leaving orphan config dirs or containers behind.
	if err := m.provisionOverlay(name); err != nil {
		return nil, fmt.Errorf("provision hdd overlay: %w", err)
	}

	// Stamp the PRETTY display name (if given, else the container name) into the
	// instance's Xbox console name inside its overlay (before first boot), so
	// instances are distinguishable on system link / the dashboard. The display
	// name is the canonical identity; the container name is a slug derived from
	// it. Best-effort: a missing FATX toolchain just means the instance keeps the
	// Xbox's random default name.
	if m.consoleNamingEnabled() {
		if err := m.writeConsoleName(m.overlayPath(name), consoleNameFor(name, opts.DisplayName)); err != nil {
			log.Printf("podman: warning: set console name for %q failed (instance will use the Xbox random default): %v", name, err)
		}
	}

	// Ensure per-container config directories exist — and the shared QMP
	// socket dir, which is a bind-mount source: podman refuses to start
	// with "statfs …/qmp: no such file or directory" on a fresh tier.
	configDir := filepath.Join(m.cfg.ConfigsDir, name)
	browserCfgDir := filepath.Join(m.cfg.BrowserDir, "config-"+name)
	for _, d := range []string{configDir, browserCfgDir, m.cfg.SocketDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", d, err)
		}
	}

	// Pre-generate a CA + server-cert pair for xemu's HTTPS listener.
	// nginx serves the server leaf (with SAN for localhost), and the
	// firefox container imports the CA as a trusted root via
	// containers/browser/init/60-trust-xemu-cert.sh. See cert.go for why
	// two certs instead of one self-signed leaf-as-its-own-root.
	sslDir := filepath.Join(configDir, "ssl")
	if err := generateXemuCerts(sslDir, name); err != nil {
		return nil, fmt.Errorf("generate ssl certs: %w", err)
	}

	// Pre-seed the sidecar's Firefox profile's NSS trust store with the instance CA
	// on the host (certutil), so the sidecar loads xemu's HTTPS noVNC view without a
	// "risky connection" warning on first boot — the durable, verifiable trust
	// path. Best-effort: a missing host certutil just falls back to the
	// in-container policies.json belt (60-trust-xemu-cert.sh). See browser_cert.go.
	if m.browserTrustEnabled() {
		if err := m.provisionBrowserTrust(browserCfgDir, filepath.Join(sslDir, "ca.pem")); err != nil {
			log.Printf("podman: warning: pre-seed firefox trust for %q failed (the sidecar may show a TLS warning until the in-container policy applies): %v", name, err)
		}
	}

	// Write the xemu launch (carrying the -qmp the scraper needs) into the WM
	// autostart. The image runs openbox (X11) or labwc (Wayland) depending on
	// PIXELFLUX_WAYLAND, reading DIFFERENT autostart files — so we write BOTH.
	// See writeXemuAutostart + WaitForQMP (the loud assert).
	if err := writeXemuAutostart(configDir, name); err != nil {
		return nil, err
	}

	// --- xemu container ---
	if err := m.createXemu(name, ports, configDir, dvdPath); err != nil {
		return nil, fmt.Errorf("create xemu container: %w", err)
	}

	// --- browser container ---
	if err := m.createBrowser(name, ports, browserCfgDir); err != nil {
		// Best-effort cleanup of the xemu container (-v: drop any anonymous volume).
		_, _ = m.run("rm", "-f", "-v", name)
		return nil, fmt.Errorf("create browser container: %w", err)
	}

	m.containers[name] = info
	if err := m.store.Upsert(info); err != nil {
		log.Printf("podman: warning: failed to save state: %v", err)
	}
	return info, nil
}

// xemuAutostartScript returns the WM autostart that launches xemu with the QMP
// socket the scraper reads. wayland=true wraps it in foot (labwc/Wayland);
// wayland=false runs the app directly (openbox/X11).
func xemuAutostartScript(name string, wayland bool) string {
	launch := fmt.Sprintf("/opt/xemu/AppRun -qmp unix:/qmp/%s.sock,server,nowait -full-screen", name)
	if wayland {
		launch = "foot -e " + launch
	}
	return "#!/bin/bash\n" + launch + "\n"
}

// writeXemuAutostart writes the xemu launch (carrying -qmp) into BOTH the openbox
// (X11) and labwc (Wayland) autostart locations, so whichever WM the current
// lscr.io/linuxserver/xemu image runs, -qmp fires and the scraper gets a QMP
// socket. The image only seeds these from /defaults when absent, so pre-writing
// wins. Robust to a future image flipping WMs; if a THIRD mechanism ever appears,
// WaitForQMP fails loudly at Start.
func writeXemuAutostart(configDir, name string) error {
	for _, t := range []struct {
		subdir  string
		wayland bool
	}{
		{"openbox", false},
		{"labwc", true},
	} {
		dir := filepath.Join(configDir, ".config", t.subdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
		p := filepath.Join(dir, "autostart")
		if err := os.WriteFile(p, []byte(xemuAutostartScript(name, t.wayland)), 0o755); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
	}
	return nil
}

// qmpStartTimeout bounds how long Start waits for xemu's QMP socket to appear.
// Covers container start + WM launch + xemu opening the monitor (well before
// the guest boots).
const qmpStartTimeout = 75 * time.Second

// WaitForQMP polls for the instance's bind-mounted QMP socket to appear. It's
// the loud assert that xemu actually launched with -qmp — if the WM/autostart
// mechanism drifts and drops the flag, this fails instead of leaving the scraper
// silently unable to read the container's memory.
func (m *Manager) WaitForQMP(name string, timeout time.Duration) error {
	sock := abs(filepath.Join(m.cfg.SocketDir, name+".sock"))
	deadline := time.Now().Add(timeout)
	for {
		if fi, err := os.Stat(sock); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("QMP socket %s did not appear within %s — xemu likely launched without -qmp (WM/autostart drift?)", sock, timeout)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (m *Manager) createXemu(name string, ports Ports, configDir, dvdPath string) error {
	encoder := m.cfg.Encoder
	if encoder == "" {
		encoder = "x264enc"
	}
	framerate := m.cfg.Framerate
	if framerate == 0 {
		framerate = 60
	}
	crf := m.cfg.CRF
	if crf == 0 {
		crf = 20
	}
	width := m.cfg.Width
	if width == 0 {
		width = 960
	}
	height := m.cfg.Height
	if height == 0 {
		height = 720
	}
	driNode := m.cfg.DRINode
	if driNode == "" {
		driNode = "/dev/dri/renderD128"
	}
	shmSize := m.cfg.ShmSize
	if shmSize == "" {
		shmSize = "1g"
	}

	args := []string{
		"create",
		"--name", name,
		"--hostname", name,
		"--network", "host",
		"--device", "/dev/kvm",
		"--device", "/dev/dri",
		"--cap-add", "NET_ADMIN",
		"--cap-add", "NET_RAW",
		"--security-opt", "seccomp=unconfined",
		"--shm-size", shmSize,
		"--restart", "unless-stopped",
		// Environment
		"-e", fmt.Sprintf("CUSTOM_PORT=%d", ports.XemuHTTP),
		"-e", fmt.Sprintf("CUSTOM_HTTPS_PORT=%d", ports.XemuHTTPS),
		"-e", fmt.Sprintf("CUSTOM_WS_PORT=%d", ports.XemuWS),
		"-e", fmt.Sprintf("PIXELFLUX_WAYLAND=%v", m.cfg.PixelfluxWayland),
		"-e", fmt.Sprintf("DRINODE=%s", driNode),
		"-e", fmt.Sprintf("DRI_NODE=%s", driNode),
		"-e", fmt.Sprintf("SELKIES_ENCODER=%s", encoder),
		"-e", "SELKIES_H264_STREAMING_MODE=true",
		"-e", fmt.Sprintf("SELKIES_FRAMERATE=%d", framerate),
		"-e", fmt.Sprintf("SELKIES_H264_CRF=%d", crf),
		"-e", "SELKIES_H264_PAINTOVER_BURST_FRAMES=1",
		"-e", fmt.Sprintf("SELKIES_MANUAL_WIDTH=%d", width),
		"-e", fmt.Sprintf("SELKIES_MANUAL_HEIGHT=%d", height),
		"-e", fmt.Sprintf("MAX_RESOLUTION=%dx%d", width*2, height*2),
		// xemu must run as UID 0 inside the container so it can use the
		// NET_ADMIN + NET_RAW capabilities granted via --cap-add for pcap
		// netplay (binds raw sockets on the host's enp191s0). Linux only
		// projects container caps into the effective set for root by default
		// — a non-root PUID would see "Operation not permitted" on pcap.
		// This is a constant, NOT the league's own uid: the league may run
		// unprivileged and drive rootful podman through `sudo -n podman`
		// (CONTAINERS_PODMAN_CMD), and the in-container uid must still be
		// 0 (the pre tier hit exactly that with PUID=1000, 2026-09-16).
		// Files written under bind mounts end up root-owned on the host;
		// `Manager.DeleteFiles` and `Manager.CleanupOrphans` use sudo to
		// remove them when the operator wants to wipe a container's state.
		"-e", fmt.Sprintf("PUID=%d", containerUID),
		"-e", fmt.Sprintf("PGID=%d", containerGID),
		"-e", "TZ=America/Los_Angeles",
		// Volumes
		"-v", fmt.Sprintf("%s:/config", abs(configDir)),
		"-v", fmt.Sprintf("%s:/custom-cont-init.d:ro", abs(m.cfg.InitDir)),
		"-v", fmt.Sprintf("%s:/qmp", abs(m.cfg.SocketDir)),
		"-v", fmt.Sprintf("%s:/shared", abs(m.cfg.SharedDir)),
	}
	// Per-instance read-only DVD/ISO. When set, the game boots off this disc
	// (a game disc present at cold boot auto-launches — see ADR-0004) with no
	// HDD-installed game needed. dvdPath is already resolved to an absolute host
	// path by resolveGameISO. The instance toml's dvd_path (patched by
	// 02-patch-toml.sh) points xemu at containerDVDPath inside the container.
	if dvdPath != "" {
		args = append(args, "-v", fmt.Sprintf("%s:%s:ro", dvdPath, containerDVDPath))
	}
	args = append(args, xemuImage)

	out, err := m.run(args...)
	if err != nil {
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

func (m *Manager) createBrowser(name string, ports Ports, browserCfgDir string) error {
	browserName := name + "-browser"
	shmSize := m.cfg.BrowserShmSize
	if shmSize == "" {
		shmSize = "2gb"
	}
	width := m.cfg.Width
	if width == 0 {
		width = 960
	}
	height := m.cfg.Height
	if height == 0 {
		height = 720
	}
	// Both containers run on `--network host`. xemu requires it for pcap
	// netplay (binds to wlan0); the browser piggybacks so the sidecar Firefox
	// can reach xemu via `localhost` without crossing podman's bridge →
	// host firewall (which silently drops SYNs to host ports even with no
	// firewalld running, courtesy of netavark's default rules).
	//
	// X auth caveat: jlesage's Xvnc starts without `-auth`, and the image
	// ships no xauth binary or Xauthority setup. Clients (xcompmgr,
	// hsetroot, Firefox) then die with "Authorization required, but no
	// authorization protocol specified". Passing `-ac` to Xvnc disables
	// access control entirely — safe here because the X server only
	// listens on a unix socket inside the container's namespace, even
	// with `--network host`. Without `-ac`, the container restart-loops.
	//
	// Trade-off: the sidecar's WEB_LISTENING_PORT and VNC_LISTENING_PORT
	// listen on 0.0.0.0 on the host. Single-public-port goal is preserved
	// at the host firewall layer for prod deploys; the JWT-gated reverse-
	// proxy + WS relay (proxy.go, vnc.go) remain the only intended public
	// path through PocketBase :8090.
	xemuTargetHost := "localhost"

	args := []string{
		"create",
		"--name", browserName,
		"--hostname", browserName,
		"--network", "host",
		"--add-host", fmt.Sprintf("%s:127.0.0.1", browserName),
		"--shm-size", shmSize,
		"--restart", "unless-stopped",
		// Environment
		// USER_ID/GROUP_ID match xemu's PUID/PGID — both halves run as root
		// inside their containers regardless of the league's own uid.
		"-e", fmt.Sprintf("USER_ID=%d", containerUID),
		"-e", fmt.Sprintf("GROUP_ID=%d", containerGID),
		"-e", fmt.Sprintf("WEB_LISTENING_PORT=%d", ports.BrowserWeb),
		"-e", fmt.Sprintf("VNC_LISTENING_PORT=%d", ports.BrowserVNC),
		"-e", "XVNC_SERVER_CUSTOM_PARAMS=-ac",
		// HTTPS is required by linuxserver/xemu — selkies's video/audio
		// pipeline uses WebCodecs, which browsers gate behind HTTPS even
		// on localhost. Per the image's official docs:
		// https://docs.linuxserver.io/images/docker-xemu/
		// "HTTPS is required for full functionality. Modern browser
		// features such as WebCodecs, used for video and audio, will not
		// function over an insecure HTTP connection." Trust on the cert
		// is solved separately (see /etc/cont-init.d/60-trust-xemu-cert.sh
		// in containers/browser/init/), not by switching to HTTP.
		"-e", fmt.Sprintf("FF_OPEN_URL=https://%s:%d", xemuTargetHost, ports.XemuHTTPS),
		"-e", "FF_KIOSK=1",
		// -no-remote prevents Firefox 145+ from doing the cross-instance
		// "remote control" handoff (where it dispatches the URL to a perceived
		// existing instance and exits 0). When the persisted /config/profile
		// has a stale .parentlock from a prior unclean shutdown, that handoff
		// fires immediately on startup and KEEP_APP_RUNNING=1 below restarts
		// it, producing an endless supervisor respawn loop and a black-screen
		// screen view.
		"-e", "FF_CUSTOM_ARGS=-no-remote",
		"-e", "FF_PREF_AUTOPLAY=media.autoplay.default=0",
		// Suppress Firefox first-run / session-restore / "set as default" prompts
		// that otherwise overlay the sidecar on every restart.
		"-e", "FF_PREF_DISABLE_RESUME_FROM_CRASH=browser.sessionstore.resume_from_crash=false",
		"-e", "FF_PREF_DISABLE_MAX_RESUMED_CRASHES=browser.sessionstore.max_resumed_crashes=0",
		"-e", "FF_PREF_DISABLE_SESSION_RESTORE=browser.sessionstore.resume_session_once=false",
		"-e", `FF_PREF_DISABLE_WHATSNEW=browser.startup.homepage_override.mstone="ignore"`,
		"-e", "FF_PREF_DISABLE_WELCOME=browser.aboutwelcome.enabled=false",
		"-e", "FF_PREF_DISABLE_DEFAULT_BROWSER=browser.shell.checkDefaultBrowser=false",
		// Suppress the "Firefox automatically sends some data to Mozilla"
		// banner that appears on first run.
		"-e", "FF_PREF_DISABLE_DATAREPORTING_NOTICE=datareporting.policy.dataSubmissionPolicyBypassNotification=true",
		"-e", "FF_PREF_DATAREPORTING_ACCEPTED=datareporting.policy.dataSubmissionPolicyAcceptedVersion=2",
		"-e", "FF_PREF_DISABLE_TELEMETRY_FIRSTRUN=toolkit.telemetry.reportingpolicy.firstRun=false",
		"-e", fmt.Sprintf("DISPLAY_WIDTH=%d", width),
		"-e", fmt.Sprintf("DISPLAY_HEIGHT=%d", height),
		"-e", "KEEP_APP_RUNNING=1",
		// Volumes
		"-v", fmt.Sprintf("%s:/config:rw", abs(browserCfgDir)),
		// Read-only access to xemu's HTTPS cert. The cont-init script
		// 60-trust-xemu-cert.sh imports this into Firefox's NSS DB
		// (cert9.db) as a trusted root, so the sidecar loads
		// https://localhost:<XemuHTTPS> without a cert warning.
		"-v", fmt.Sprintf("%s:/xemu-cert:ro", abs(filepath.Join(m.cfg.ConfigsDir, name, "ssl"))),
	}

	// Mount browser init scripts as individual files in /etc/cont-init.d/
	// (jlesage's s6-overlay only scans this directory, not subdirectories).
	if m.cfg.BrowserInitDir != "" {
		entries, err := os.ReadDir(abs(m.cfg.BrowserInitDir))
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					args = append(args, "-v", fmt.Sprintf("%s/%s:/etc/cont-init.d/%s:ro",
						abs(m.cfg.BrowserInitDir), e.Name(), e.Name()))
				}
			}
		}
	}

	args = append(args, browserImage)

	out, err := m.run(args...)
	if err != nil {
		return fmt.Errorf("%s: %s", err, out)
	}
	return nil
}

// Start starts both the xemu and browser containers, then asserts xemu came up
// with QMP (WaitForQMP) so a dropped -qmp fails loudly instead of leaving the
// scraper unable to read the instance's memory.
func (m *Manager) Start(name string) error {
	m.mu.Lock()
	if _, ok := m.containers[name]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("container %q not found", name)
	}
	if out, err := m.run("start", name); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start %s: %s: %s", name, err, out)
	}
	if out, err := m.run("start", name+"-browser"); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("start %s-browser: %s: %s", name, err, out)
	}
	m.mu.Unlock()

	// Assert QMP outside the lock (it polls for up to qmpStartTimeout).
	return m.WaitForQMP(name, qmpStartTimeout)
}

// Stop stops both the browser and xemu containers.
func (m *Manager) Stop(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.containers[name]; !ok {
		return fmt.Errorf("container %q not found", name)
	}
	// Stop browser first (depends on xemu).
	if out, err := m.run("stop", name+"-browser"); err != nil {
		log.Printf("podman: stop %s-browser: %s: %s", name, err, out)
	}
	if out, err := m.run("stop", name); err != nil {
		return fmt.Errorf("stop %s: %s: %s", name, err, out)
	}
	return nil
}

// remove stops and removes both containers, then removes them from state.
// Remove (hooks.go) wraps it with the Removed lifecycle hook.
func (m *Manager) remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.containers[name]; !ok {
		return fmt.Errorf("container %q not found", name)
	}

	// Stop + remove both containers. `rm -f -v` removes the container, its
	// read-write (writable) layer, AND any anonymous volume the image declared
	// (defensive — the jlesage/firefox + linuxserver/xemu images currently
	// declare none, and all their per-instance state is bind-mounted into the
	// host dirs cleaned below, but -v guards against a future VOLUME). Named
	// volumes are never created (we only use host-path bind mounts), so none
	// leak. Firefox is its own container (<name>-browser); both halves go here.
	_, _ = m.run("stop", name+"-browser")
	if out, err := m.run("rm", "-f", "-v", name+"-browser"); err != nil {
		log.Printf("podman: rm %s-browser: %s: %s", name, err, out)
	}
	_, _ = m.run("stop", name)
	if out, err := m.run("rm", "-f", "-v", name); err != nil {
		log.Printf("podman: rm %s: %s: %s", name, err, out)
	}

	// Teardown is symmetric with Create: remove every per-instance (non-shared)
	// file Create generated — the CoW overlay (with this instance's console
	// name + deltas), the xemu config dir (per-instance eeprom, toml, ssl,
	// shaders, X/labwc state) and the browser profile. The shared base is left
	// untouched: the root qcow2, the bootrom/MCPX + flashrom/BIOS under
	// shared/bios, and the default toml. See containerOwnedPaths.
	if err := m.removeContainerFiles(name); err != nil {
		log.Printf("podman: warning: remove %s per-instance files: %v", name, err)
	}

	delete(m.containers, name)
	if err := m.store.Delete(name); err != nil {
		log.Printf("podman: warning: failed to delete state: %v", err)
	}
	return nil
}

// DeleteFiles removes the bind-mount source directories for the given
// container (xemu configs/<name> and browser config-<name>) via sudo rm -rf.
// Independent of Remove: callers can wipe a stopped container's files to reset
// its profile without removing the container record, or clean up orphaned
// directories left behind by an earlier Remove.
//
// Refuses if either half of the pair is currently running, paused, or
// restarting — wiping the bind mount under a live container would corrupt the
// X session and the Firefox profile.
func (m *Manager) DeleteFiles(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, c := range []string{name, name + "-browser"} {
		out, err := m.run("inspect", "--format", "{{.State.Status}}", c)
		if err != nil {
			// Container doesn't exist in podman — fine, treat as stopped.
			continue
		}
		s := strings.TrimSpace(string(out))
		if s == "running" || s == "paused" || s == "restarting" {
			return fmt.Errorf("container %q is %s; stop it first", c, s)
		}
	}

	return m.removeContainerFiles(name)
}

// removeContainerFiles rm -rf's every per-instance bind-mount source for the
// container (xemu configs/<name>, browser config-<name>, the hdds/<name>.*
// overlay) via the same privilege prefix used for podman (rootful podman stamps
// them root-owned). Shared/base files — the root qcow2 (and any other
// "_"-prefixed baseline), shared/bios firmware, shared/toml — are never in
// containerOwnedPaths, so they are left untouched. Shared by Remove (full
// teardown) and DeleteFiles (wipe-without-removing-record).
func (m *Manager) removeContainerFiles(name string) error {
	paths := m.containerOwnedPaths(name)
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"rm", "-rf"}, paths...)
	if out, err := m.runSudo(args...); err != nil {
		return fmt.Errorf("rm -rf: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CleanupOrphans removes on-disk container artefacts (browser config dirs,
// xemu config dirs, per-instance HDD files) for which no live container record
// exists in m.containers. Baseline HDDs whose basename starts with "_" (e.g.
// "_default.qcow2") are treated as protected and skipped.
//
// Returns the list of paths that were removed (empty slice when nothing was
// orphaned) so the caller can surface a useful summary to the operator.
func (m *Manager) CleanupOrphans() ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	known := make(map[string]struct{}, len(m.containers))
	for name := range m.containers {
		known[name] = struct{}{}
	}

	var orphans []string

	// Browser config dirs: containers/browser/config-<name>/
	if entries, err := os.ReadDir(abs(m.cfg.BrowserDir)); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name, ok := strings.CutPrefix(e.Name(), "config-")
			if !ok {
				continue
			}
			if _, live := known[name]; live {
				continue
			}
			orphans = append(orphans, filepath.Join(abs(m.cfg.BrowserDir), e.Name()))
		}
	}

	// xemu config dirs: containers/xemu/configs/<name>/
	if entries, err := os.ReadDir(abs(m.cfg.ConfigsDir)); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, live := known[e.Name()]; live {
				continue
			}
			orphans = append(orphans, filepath.Join(abs(m.cfg.ConfigsDir), e.Name()))
		}
	}

	// HDD files: containers/xemu/shared/hdds/<name>.<ext>
	hddDir := filepath.Join(abs(m.cfg.SharedDir), "hdds")
	if entries, err := os.ReadDir(hddDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			base := e.Name()
			// Skip baselines: any file whose basename starts with "_", plus
			// an explicit guard for _default.qcow2 in case the convention
			// drifts.
			if strings.HasPrefix(base, "_") || base == "_default.qcow2" {
				continue
			}
			name := strings.TrimSuffix(base, filepath.Ext(base))
			if _, live := known[name]; live {
				continue
			}
			orphans = append(orphans, filepath.Join(hddDir, base))
		}
	}

	if len(orphans) == 0 {
		return nil, nil
	}

	args := append([]string{"rm", "-rf"}, orphans...)
	if out, err := m.runSudo(args...); err != nil {
		return orphans, fmt.Errorf("rm -rf: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return orphans, nil
}

// NamePrefix is the configured container-name namespace (empty in prod). Name-
// generating callers prepend it so this deployment's container names can't
// collide with another deployment's on a shared podman daemon.
func (m *Manager) NamePrefix() string { return m.cfg.NamePrefix }

// List returns all managed containers from the persisted store. Note: this is
// the recorded state only — it does NOT reflect live podman status. Callers
// that need liveness must consult Status / ScreenLive per container.
func (m *Manager) List() ([]ContainerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ContainerInfo, 0, len(m.containers))
	for _, info := range m.containers {
		out = append(out, *info)
	}
	return out, nil
}

// Status returns the podman status of the xemu container (e.g. "running",
// "exited", "created").
func (m *Manager) Status(name string) (string, error) {
	m.mu.Lock()
	if _, ok := m.containers[name]; !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("container %q not found", name)
	}
	m.mu.Unlock()

	out, err := m.run("inspect", "--format", "{{.State.Status}}", name)
	if err != nil {
		return "unknown", nil
	}
	return parsePodmanStatus(out), nil
}

// Logs shells `podman logs --tail N` against either the xemu (which="xemu") or
// browser (which="browser") container of the named pair. tail is clamped to
// [1, 1000]. The returned string is the combined stdout+stderr of podman.
func (m *Manager) Logs(name string, tail int, which string) (string, error) {
	m.mu.Lock()
	if _, ok := m.containers[name]; !ok {
		m.mu.Unlock()
		return "", fmt.Errorf("container %q not found", name)
	}
	m.mu.Unlock()

	if tail <= 0 {
		tail = 200
	}
	if tail > 1000 {
		tail = 1000
	}

	target := name
	if which == "browser" {
		target = name + "-browser"
	}

	out, err := m.run("logs", "--tail", strconv.Itoa(tail), target)
	if err != nil {
		return string(out), fmt.Errorf("podman logs %s: %w", target, err)
	}
	return string(out), nil
}

// Get returns the ContainerInfo for name, or false if unknown.
func (m *Manager) Get(name string) (ContainerInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.containers[name]
	if !ok {
		return ContainerInfo{}, false
	}
	return *info, true
}

// run executes the configured podman command with the given arguments.
// PodmanCmd may contain leading args (e.g. "sudo -n podman") which are split
// on whitespace and prepended to args.
func (m *Manager) run(args ...string) ([]byte, error) {
	parts := strings.Fields(m.cfg.PodmanCmd)
	if len(parts) == 0 {
		parts = []string{"podman"}
	}
	full := append(parts[1:], args...)
	cmd := exec.Command(parts[0], full...)
	return cmd.CombinedOutput()
}

// runSudo executes a non-podman command with the same privilege prefix used
// to invoke podman. The trailing "podman" element of PodmanCmd is dropped so
// the remaining leading args (e.g. ["sudo", "-n"]) become the sudo prefix.
// When PodmanCmd is bare "podman" (rootless setup) the command runs with no
// prefix — appropriate, since rootless podman doesn't produce root-owned files.
//
// Used by DeleteFiles and CleanupOrphans to remove bind-mount sources that
// rootful podman has stamped as root-owned.
func (m *Manager) runSudo(args ...string) ([]byte, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("runSudo: no command")
	}
	full := append(sudoPrefix(m.cfg.PodmanCmd), args...)
	cmd := exec.Command(full[0], full[1:]...)
	return cmd.CombinedOutput()
}

// sudoPrefix extracts the privilege prefix from PodmanCmd: everything BEFORE the
// "podman" element. It drops "podman" AND any podman-specific flags that follow
// it (e.g. --runtime=crun), so a non-podman command (rm) isn't handed to podman.
// A bare "podman" (rootless) yields no prefix — the command runs directly, which
// is correct since rootless podman doesn't produce root-owned files.
//
// Fixes a bug where the previous "drop only a trailing podman" logic left
// `--runtime=crun` in place for the default PodmanCmd "sudo -n podman
// --runtime=crun", causing runSudo to invoke `sudo podman --runtime=crun rm -rf`
// (podman rejects `rm -rf`) and orphan every removed instance's bind-mount files.
func sudoPrefix(podmanCmd string) []string {
	parts := strings.Fields(podmanCmd)
	for i, p := range parts {
		if filepath.Base(p) == "podman" {
			return parts[:i:i]
		}
	}
	return parts
}

// containerOwnedPaths returns every host-side path that podman + container
// init scripts may write into for the given container. Used by DeleteFiles.
// The HDD path glob picks up whatever extension 03-setup-hdd.sh derived from
// DEFAULT_HDD_NAME.
func (m *Manager) containerOwnedPaths(name string) []string {
	paths := []string{
		filepath.Join(m.cfg.ConfigsDir, name),
		filepath.Join(m.cfg.BrowserDir, "config-"+name),
	}
	if matches, err := filepath.Glob(filepath.Join(m.cfg.SharedDir, "hdds", name+".*")); err == nil {
		paths = append(paths, matches...)
	}
	return paths
}

// abs resolves path to absolute, ignoring errors (returns original on failure).
func abs(path string) string {
	a, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return a
}
