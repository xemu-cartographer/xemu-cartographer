package podman

import (
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCertutilImportArgs(t *testing.T) {
	got := certutilImportArgs("sql:/p", "CA nick", "/ssl/ca.pem")
	want := []string{"-A", "-n", "CA nick", "-t", "C,,", "-i", "/ssl/ca.pem", "-d", "sql:/p"}
	if len(got) != len(want) {
		t.Fatalf("len %d != %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("arg %d = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

func TestBrowserProfileDir(t *testing.T) {
	m := &Manager{}
	if got := m.browserProfileDir("/x/browser/config-foo"); got != "/x/browser/config-foo/profile" {
		t.Fatalf("browserProfileDir = %q", got)
	}
}

// TestProvisionBrowserTrustImportsCA runs the real host certutil path end to
// end: generate an instance CA + leaf (the exact chain nginx serves — see
// cert.go), pre-seed the sidecar profile's NSS DB with the CA, then confirm NSS
// validates the leaf for TLS server auth. That last step IS Firefox's cert
// check: if it passes, a Firefox reading this profile accepts xemu's HTTPS cert
// with no "risky connection" interstitial.
//
// Skips when NSS certutil isn't installed. CI does not run `go test`, so this
// only runs locally (where the podman host would have certutil anyway).
func TestProvisionBrowserTrustImportsCA(t *testing.T) {
	certutil, err := exec.LookPath("certutil")
	if err != nil {
		t.Skip("certutil (nss/nss-tools) not installed; skipping firefox-trust integration test")
	}

	dir := t.TempDir()
	sslDir := filepath.Join(dir, "ssl")
	if err := generateXemuCerts(sslDir, "itest"); err != nil {
		t.Fatalf("generateXemuCerts: %v", err)
	}

	browserCfg := filepath.Join(dir, "browser")
	m := &Manager{cfg: Config{SetBrowserTrust: true, CertutilCmd: certutil}}
	caPath := filepath.Join(sslDir, "ca.pem")
	if err := m.provisionBrowserTrust(browserCfg, caPath); err != nil {
		t.Fatalf("provisionBrowserTrust: %v", err)
	}

	profile := m.browserProfileDir(browserCfg)
	if _, err := os.Stat(filepath.Join(profile, "cert9.db")); err != nil {
		t.Fatalf("cert9.db not created in profile: %v", err)
	}
	dbDir := "sql:" + profile

	// The CA is present as a trusted SSL root.
	if out, err := exec.Command(certutil, "-L", "-d", dbDir, "-n", browserCADisplayName).CombinedOutput(); err != nil {
		t.Fatalf("CA not found in NSS DB: %v: %s", err, out)
	}

	// Import the served leaf (first PEM block of the cert.pem bundle) as an
	// untrusted cert, then have NSS validate it for server auth against the
	// pre-seeded CA — exactly what Firefox does on connect.
	leafPath := filepath.Join(dir, "leaf.pem")
	if err := os.WriteFile(leafPath, firstPEMBlock(t, filepath.Join(sslDir, "cert.pem")), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(certutil, "-A", "-n", "itest-leaf", "-t", ",,", "-i", leafPath, "-d", dbDir).CombinedOutput(); err != nil {
		t.Fatalf("import leaf: %v: %s", err, out)
	}
	if out, err := exec.Command(certutil, "-V", "-u", "V", "-n", "itest-leaf", "-d", dbDir).CombinedOutput(); err != nil {
		t.Fatalf("NSS rejected xemu leaf for TLS server auth (the sidecar would show a warning): %v: %s", err, out)
	}
}

func firstPEMBlock(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(raw)
	if blk == nil {
		t.Fatalf("no PEM block in %s", path)
	}
	return pem.EncodeToMemory(blk)
}
