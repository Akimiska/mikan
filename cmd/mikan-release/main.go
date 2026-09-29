// mikan-release makes the signed manifest of a release (used by .github/workflows/release.yml)
// and the release signing key.
//
//	mikan-release keygen -out release-signing.pem
//	RELEASE_SIGNING_KEY="$(cat key.pem)" mikan-release manifest -version 0.3.9 \
//	    -image ghcr.io/miroshka000/mikan -digest sha256:… \
//	    -asset x86_64=dist/mikan-x86_64 -asset aarch64=dist/mikan-aarch64 -out dist
//	mikan-release verify dist/manifest.json
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mikan/internal/release"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mikan-release:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mikan-release keygen|manifest|verify …")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:])
	case "manifest":
		return makeManifest(args[1:])
	case "verify":
		return verify(args[1:])
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "file for the private key (PKCS#8 PEM)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("-out is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		return err
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pub))
	return nil
}

type assets []string

func (a *assets) String() string     { return strings.Join(*a, ",") }
func (a *assets) Set(v string) error { *a = append(*a, v); return nil }

func makeManifest(args []string) error {
	fs := flag.NewFlagSet("manifest", flag.ContinueOnError)
	version := fs.String("version", "", "release version, e.g. 0.3.9")
	image := fs.String("image", "", "image repository, e.g. ghcr.io/miroshka000/mikan")
	digest := fs.String("digest", "", "sha256 digest of the pushed multi-arch image")
	changelog := fs.String("changelog", "CHANGELOG.md", "where the release notes are")
	out := fs.String("out", "dist", "output directory")
	var files assets
	fs.Var(&files, "asset", "arch=path of an installer binary (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key, err := privateKey(os.Getenv("RELEASE_SIGNING_KEY"))
	if err != nil {
		return err
	}
	log, err := os.ReadFile(*changelog)
	if err != nil {
		return err
	}
	m := release.Manifest{Version: *version, Published: time.Now().UTC().Truncate(time.Second), Image: *image, Digest: *digest,
		Installer: map[string]release.Asset{}, Notes: release.Notes(log, *version)}
	for _, a := range files {
		arch, path, ok := strings.Cut(a, "=")
		if !ok {
			return fmt.Errorf("-asset %q: want arch=path", a)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		m.Installer[arch] = release.Asset{
			URL:    fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", release.Repo, *version, filepath.Base(path)),
			SHA256: hex.EncodeToString(sum[:]),
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	sig := release.Sign(data, key)
	// The manifest must pass the checks the panel and the installer make.
	if _, err := release.Parse(data, sig, key.Public().(ed25519.PublicKey)); err != nil {
		return err
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	notes := m.Notes["en"]
	if ru := m.Notes["ru"]; ru != "" {
		notes += "\n\n<details><summary>Русский</summary>\n\n" + ru + "\n\n</details>"
	}
	for name, content := range map[string][]byte{"manifest.json": data, "manifest.json.sig": []byte(sig + "\n"), "notes.md": []byte(notes + "\n")} {
		if err := os.WriteFile(filepath.Join(*out, name), content, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("manifest %s: %s\n", m.Version, m.Ref())
	return nil
}

func verify(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: mikan-release verify manifest.json")
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(args[0] + ".sig")
	if err != nil {
		return err
	}
	pub, err := release.Key(release.PublicKey)
	if err != nil {
		return err
	}
	m, err := release.Parse(data, string(sig), pub)
	if err != nil {
		return err
	}
	fmt.Printf("ok: %s %s\n", m.Version, m.Ref())
	return nil
}

func privateKey(pemText string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("RELEASE_SIGNING_KEY: no PEM key")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("RELEASE_SIGNING_KEY: %w", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("RELEASE_SIGNING_KEY: not an Ed25519 key")
	}
	return priv, nil
}
