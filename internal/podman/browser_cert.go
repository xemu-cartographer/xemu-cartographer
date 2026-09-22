package podman

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// browserCADisplayName is the NSS nickname the instance CA is imported under. It
// matches the CN in cert.go's makeCA and the nickname the in-container
// policies.json belt references, so the host + fallback paths stay consistent.
const browserCADisplayName = "xemu-cartographer dev CA"

func (m *Manager) browserTrustEnabled() bool { return m.cfg.SetBrowserTrust }

func (m *Manager) certutilCmd() string {
	if m.cfg.CertutilCmd != "" {
		return m.cfg.CertutilCmd
	}
	return "certutil"
}

// browserProfileDir is the Firefox profile the jlesage sidecar uses. jlesage's
// firefox launches with -profile /config/profile (bind-mounted from
// browserCfgDir/profile) and reads its NSS trust store (cert9.db/key4.db) from
// there. Its cont-init only mkdir -p's this dir, never wipes it, so a
// pre-seeded DB survives to first launch.
func (m *Manager) browserProfileDir(browserCfgDir string) string {
	return filepath.Join(browserCfgDir, "profile")
}

// certutilImportArgs builds the `certutil -A` argv that imports a PEM CA as a
// trusted TLS root into the NSS SQL database at dbDir ("sql:/path"). The trust
// flags "C,," mean: trusted CA for SSL/TLS server auth, with no S/MIME or
// code-signing trust. Pulled out for unit testing.
func certutilImportArgs(dbDir, nickname, caPath string) []string {
	return []string{"-A", "-n", nickname, "-t", "C,,", "-i", caPath, "-d", dbDir}
}

// provisionBrowserTrust pre-seeds the sidecar's Firefox profile's NSS trust store
// with the instance CA (browserCfgDir/profile/cert9.db) at create time, so the
// sidecar loads https://localhost:<XemuHTTPS> — xemu's noVNC view, served by nginx
// with our SAN-pinned leaf (see cert.go) — without the "Warning: Potential
// Security Risk Ahead" interstitial on first boot.
//
// This runs on the HOST (which reliably has NSS `certutil`), writing directly
// into the bind-mounted profile dir BEFORE the container ever starts. It's the
// primary trust path because it removes every in-container uncertainty:
// jlesage/firefox ships no nss-tools, so the old cont-init `certutil` fallback
// depended on a runtime `apk add` (needs network + repos), and whether Alpine
// Firefox honours a dropped-in policies.json is build-dependent. NSS reading
// cert9.db from -profile, by contrast, is version-stable AND verifiable off-box
// (`certutil -V -u V`, see browser_cert_test.go). The bind-mounted policies.json
// (60-trust-xemu-cert.sh) remains as a durable, host-tool-free belt.
//
// Best-effort: returns an error (logged by the caller, Create still succeeds) if
// certutil is missing or the import fails; the sidecar then relies on the
// in-container policies.json belt.
func (m *Manager) provisionBrowserTrust(browserCfgDir, caPath string) error {
	if _, err := os.Stat(caPath); err != nil {
		return fmt.Errorf("ca cert not found at %s: %w", caPath, err)
	}
	profile := m.browserProfileDir(browserCfgDir)
	if err := os.MkdirAll(profile, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", profile, err)
	}
	dbDir := "sql:" + profile

	// Create an empty NSS SQL DB if the profile doesn't have one yet. On a fresh
	// Create the profile dir is empty; Firefox reuses this DB on first launch.
	if _, err := os.Stat(filepath.Join(profile, "cert9.db")); err != nil {
		if out, err := exec.Command(m.certutilCmd(), "-N", "-d", dbDir, "--empty-password").CombinedOutput(); err != nil {
			return fmt.Errorf("certutil -N: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}

	args := certutilImportArgs(dbDir, browserCADisplayName, caPath)
	if out, err := exec.Command(m.certutilCmd(), args...).CombinedOutput(); err != nil {
		return fmt.Errorf("certutil -A: %w: %s", err, strings.TrimSpace(string(out)))
	}
	log.Printf("podman: pre-seeded firefox trust with instance CA in %s", profile)
	return nil
}
