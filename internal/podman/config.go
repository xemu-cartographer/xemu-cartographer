package podman

import (
	"os"
	"strconv"
	"time"
)

// LoadFromEnv builds a Config from CONTAINERS_* environment variables, falling
// back to legacy TOML defaults for any value not set.
func LoadFromEnv() Config {
	return Config{
		Enabled:        envBool("CONTAINERS_ENABLED", false),
		SocketDir:      envStr("CONTAINERS_SOCKET_DIR", "./containers/xemu/qmp"),
		SharedDir:      envStr("CONTAINERS_SHARED_DIR", "./containers/xemu/shared"),
		InitDir:        envStr("CONTAINERS_INIT_DIR", "./containers/xemu/init"),
		ConfigsDir:     envStr("CONTAINERS_CONFIGS_DIR", "./containers/xemu/configs"),
		BrowserDir:     envStr("CONTAINERS_BROWSER_DIR", "./containers/browser"),
		BrowserInitDir: envStr("CONTAINERS_BROWSER_INIT_DIR", "./containers/browser/init"),
		PortBase:       envInt("CONTAINERS_PORT_BASE", 3100),
		PortStride:     envInt("CONTAINERS_PORT_STRIDE", 10),
		HostIP:         envStr("CONTAINERS_HOST_IP", "localhost"),
		// NamePrefix namespaces every container this deployment creates. Empty in
		// prod. A beta/preview sharing the host's rootful podman daemon sets e.g.
		// "beta-" so its container names can never collide with prod's — even the
		// auto-generated per-player "play-<uid>" boxes become "beta-play-<uid>".
		// The prefix is baked into the logical name at creation and flows through
		// the store / podman name / socket / dirs unchanged (no double-prefixing).
		NamePrefix: envStr("CONTAINERS_NAME_PREFIX", ""),
		DVDPath:    envStr("CONTAINERS_DVD_PATH", ""),
		// Shared game ISO library. A per-instance ISO named in
		// CreateOptions.GameISO resolves against this dir; ISOs are bind-mounted
		// read-only into their instance, never copied onto the overlay.
		ISODir:    envStr("CONTAINERS_ISO_DIR", "./containers/xemu/shared/isos"),
		PodmanCmd: envStr("CONTAINERS_PODMAN_CMD", "podman"),
		// Canonical read-only root qcow2 (the Halo-installed disk) that every
		// per-instance overlay backs onto. Lives in SharedDir/hdds/. Keep this
		// in sync with DEFAULT_HDD_NAME in containers/xemu/init/.env.
		RootHDD:    envStr("CONTAINERS_ROOT_HDD", "_default.qcow2"),
		QemuImgCmd: envStr("CONTAINERS_QEMU_IMG_CMD", "qemu-img"),
		// Liveness-probe timeout for the screen proxy's pre-dial `podman inspect`.
		// The pre-rename CONTAINERS_KIOSK_* name is still honoured as a fallback so
		// a deployed .env keeps working; drop the alias when the sidecar goes.
		ScreenLiveTimeout: time.Duration(envInt("CONTAINERS_SCREEN_LIVE_TIMEOUT_MS",
			envInt("CONTAINERS_KIOSK_LIVE_TIMEOUT_MS", 2000))) * time.Millisecond,
		// Write the container name into the instance's Xbox console name
		// (E:\UDATA\NICKNAME.XBN) inside its overlay at create time.
		SetConsoleName:       envBool("CONTAINERS_SET_CONSOLE_NAME", true),
		QemuStorageDaemonCmd: envStr("CONTAINERS_QEMU_STORAGE_DAEMON_CMD", "qemu-storage-daemon"),
		PythonCmd:            envStr("CONTAINERS_PYTHON_CMD", "python3"),
		FatxToolPath:         envStr("CONTAINERS_FATX_TOOL", ""),
		// Pre-seed the sidecar's Firefox profile's NSS trust store with the instance
		// CA at create time so the sidecar loads xemu's HTTPS view without a warning.
		SetBrowserTrust:  envBool("CONTAINERS_SET_BROWSER_TRUST", true),
		CertutilCmd:      envStr("CONTAINERS_CERTUTIL_CMD", "certutil"),
		Encoder:          envStr("CONTAINERS_ENCODER", "x264enc"),
		Framerate:        envInt("CONTAINERS_FRAMERATE", 60),
		CRF:              envInt("CONTAINERS_CRF", 20),
		Width:            envInt("CONTAINERS_WIDTH", 960),
		Height:           envInt("CONTAINERS_HEIGHT", 720),
		PixelfluxWayland: envBool("CONTAINERS_PIXELFLUX_WAYLAND", true),
		DRINode:          envStr("CONTAINERS_DRINODE", "/dev/dri/renderD128"),
		ShmSize:          envStr("CONTAINERS_SHM_SIZE", "1g"),
		BrowserShmSize:   envStr("CONTAINERS_BROWSER_SHM_SIZE", "2gb"),
	}
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
