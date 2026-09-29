package proto

import (
	"crypto/ecdh"
	"crypto/mlkem"
	"encoding/base64"
	"regexp"
	"slices"
	"strings"
)

// VLESS Encryption (Xray's "vlessenc", mihomo ≥ 1.19.13): a post-quantum hybrid key
// exchange (ML-KEM-768 + X25519) inside VLESS itself, under REALITY. Recorded traffic
// stays sealed even for a future quantum computer. The listener holds the decryption
// string with private keys; clients get the encryption string with the public halves.
const VLESSEncMethod = "mlkem768x25519plus"

var (
	vlessEncModes = []string{"native", "xorpub", "random"}
	ticketRe      = regexp.MustCompile(`^\d+(-\d+)?s$`)
)

type vlessDecryption struct {
	mode    string
	keys    [][]byte // X25519 private keys (32 bytes) or ML-KEM-768 seeds (64 bytes)
	padding []string // short tokens, as mihomo reads them
}

// parseDecryption reads "mlkem768x25519plus.<mode>.<ticket>s.<keys and padding>"; tokens
// shorter than 20 characters are padding settings, like in mihomo's parser.
func parseDecryption(s string) (vlessDecryption, error) {
	parts := strings.Split(s, ".")
	if len(parts) < 4 || parts[0] != VLESSEncMethod || !slices.Contains(vlessEncModes, parts[1]) || !ticketRe.MatchString(parts[2]) {
		return vlessDecryption{}, fail("vless_decryption", "decryption")
	}
	d := vlessDecryption{mode: parts[1]}
	for _, p := range parts[3:] {
		if len(p) < 20 {
			d.padding = append(d.padding, p)
			continue
		}
		b, err := base64.RawURLEncoding.DecodeString(p)
		if err != nil || len(b) != 32 && len(b) != mlkem.SeedSize {
			return vlessDecryption{}, fail("vless_decryption", "decryption")
		}
		d.keys = append(d.keys, b)
	}
	if len(d.keys) == 0 {
		return vlessDecryption{}, fail("vless_decryption", "decryption")
	}
	return d, nil
}

// ClientEncryption derives the clients' encryption string from a listener's decryption:
// the same mode and padding, the public half of every key, and a full handshake on each
// connection (1rtt): 0-RTT data could be replayed.
func ClientEncryption(decryption string) (string, error) {
	d, err := parseDecryption(decryption)
	if err != nil {
		return "", err
	}
	parts := append([]string{VLESSEncMethod, d.mode, "1rtt"}, d.padding...)
	for _, k := range d.keys {
		var pub []byte
		if len(k) == 32 {
			priv, err := ecdh.X25519().NewPrivateKey(k)
			if err != nil {
				return "", fail("vless_decryption", "decryption")
			}
			pub = priv.PublicKey().Bytes()
		} else {
			dk, err := mlkem.NewDecapsulationKey768(k)
			if err != nil {
				return "", fail("vless_decryption", "decryption")
			}
			pub = dk.EncapsulationKey().Bytes()
		}
		parts = append(parts, base64.RawURLEncoding.EncodeToString(pub))
	}
	return strings.Join(parts, "."), nil
}

// hasEncryption: the template turns VLESS Encryption on ("" and "none" mean off).
func (t Template) hasEncryption() bool {
	d := t.str("decryption")
	return d != "" && d != "none"
}
