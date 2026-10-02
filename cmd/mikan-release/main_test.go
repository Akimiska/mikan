package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mikan/internal/release"
)

func TestManifestCarriesOnlyAnExplicitInstallerRequirement(t *testing.T) {
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELEASE_SIGNING_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	dir := t.TempDir()
	log := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(log, []byte("## 0.5.0.1\n### en\n- patch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "release")
	args := []string{"-version", "0.5.0.1", "-image", "ghcr.io/miroshka000/mikan", "-digest", "sha256:" + strings.Repeat("a", 64), "-changelog", log, "-out", out}
	if err := makeManifest(args); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err := os.ReadFile(filepath.Join(out, "manifest.json.sig"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := release.Parse(data, string(sig), pub)
	if err != nil {
		t.Fatal(err)
	}
	// Without the flag a release asks for no installer: a failed self-update must not
	// block a patch the old installer can apply.
	if m.Version != "0.5.0.1" || m.MinInstaller != "" {
		t.Fatalf("unexpected installer requirement: %+v", m)
	}
	if err := makeManifest(append(args, "-min-installer", "0.5.0.0")); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	sig, err = os.ReadFile(filepath.Join(out, "manifest.json.sig"))
	if err != nil {
		t.Fatal(err)
	}
	if m, err = release.Parse(data, string(sig), pub); err != nil || m.MinInstaller != "0.5.0.0" {
		t.Fatalf("explicit installer requirement lost: %+v %v", m, err)
	}
	if err := makeManifest(append(args, "-min-installer", "0.5.0.1.2")); err == nil {
		t.Fatal("invalid installer version accepted")
	}
	if err := makeManifest(append(args, "-min-installer", "0.5.0.2")); err == nil {
		t.Fatal("installer newer than published binaries accepted")
	}
}

// A bridge manifest names installers published under another tag; by default they come
// from the release's own tag.
func TestManifestInstallerURLFollowsTheTag(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELEASE_SIGNING_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	dir := t.TempDir()
	log := filepath.Join(dir, "CHANGELOG.md")
	bin := filepath.Join(dir, "mikan-x86_64")
	if err := os.WriteFile(log, []byte("## 0.4.4\n### en\n- old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("installer"), 0600); err != nil {
		t.Fatal(err)
	}
	url := func(extra ...string) string {
		t.Helper()
		out := filepath.Join(t.TempDir(), "release")
		args := append([]string{"-version", "0.4.4", "-image", "ghcr.io/miroshka000/mikan", "-digest", "sha256:" + strings.Repeat("a", 64),
			"-changelog", log, "-out", out, "-asset", "x86_64=" + bin}, extra...)
		if err := makeManifest(args); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(out, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m release.Manifest
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m.Installer["x86_64"].URL
	}
	if got := url(); got != "https://github.com/Miroshka000/mikan/releases/download/v0.4.4/mikan-x86_64" {
		t.Fatal(got)
	}
	if got := url("-tag", "v-bridge-0.5.0.0"); got != "https://github.com/Miroshka000/mikan/releases/download/v-bridge-0.5.0.0/mikan-x86_64" {
		t.Fatal(got)
	}
}
