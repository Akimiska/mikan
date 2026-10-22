package release

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
)

// Verify checks a signature of pub over exact bytes. ErrSignature when it does not hold.
func Verify(data []byte, sig string, pub ed25519.PublicKey) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, data, raw) {
		return ErrSignature
	}
	return nil
}

// VerifyAny checks a signature over exact bytes by any of keys: a manifest and the release
// index, by the release keys. ErrSignature when none holds.
func VerifyAny(data []byte, sig string, keys ...ed25519.PublicKey) error {
	for _, k := range keys {
		if Verify(data, sig, k) == nil {
			return nil
		}
	}
	return ErrSignature
}

// The marketplace's documents are signed in a context: the signature covers the context and
// then the bytes, so a signature made for one kind of document never passes for another, nor
// for a release, whoever holds which key. It is published next to the file as SigSuffix.
const (
	// CatalogContext is the official catalog's (CatalogKey).
	CatalogContext = "mikan catalog v1\n"
	// CommunityContext is the community registry's index (CommunityKey).
	CommunityContext = "mikan community v1\n"
	// SigSuffix names the signature in a context next to its file; a release's own (".sig") is
	// over the bytes alone.
	SigSuffix = ".sig2"
)

// SignIn signs data in a context (VerifyIn).
func SignIn(context string, data []byte, key ed25519.PrivateKey) string {
	return Sign(append([]byte(context), data...), key)
}

// VerifyIn checks a signature made in context over data by pub. ErrSignature when it does not
// hold.
func VerifyIn(context string, data []byte, sig string, pub ed25519.PublicKey) error {
	return Verify(append([]byte(context), data...), sig, pub)
}

// ValidDigest says whether s is an image digest as a manifest or the catalog names one.
func ValidDigest(s string) bool { return digestRe.MatchString(s) }

// Getter reads url whole, at most limit bytes.
type Getter func(ctx context.Context, url string, limit int64) ([]byte, error)

// FetchSigned reads a signed file (up to limit) and its signature (url+suffix: ".sig" for a
// release's, SigSuffix for one in a context) and hands both to parse. They are two requests: a
// release published between them leaves the new file with the old signature, so a pair that
// does not match is asked for once more.
func FetchSigned(ctx context.Context, get Getter, url, suffix string, limit int64, parse func(data []byte, sig string) error) error {
	once := func() error {
		data, err := get(ctx, url, limit)
		if err != nil {
			return err
		}
		sig, err := get(ctx, url+suffix, 4096)
		if err != nil {
			return err
		}
		return parse(data, string(sig))
	}
	err := once()
	if errors.Is(err, ErrSignature) {
		err = once()
	}
	return err
}
