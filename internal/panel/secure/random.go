package secure

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"math/big"
)

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Token returns n characters drawn uniformly from [0-9A-Za-z] (log2(62) ≈ 5.95 bits each).
func Token(n int) string {
	return pick(base62, n)
}

// Login returns a random admin login: a letter, then 11 of [a-z0-9]. Not a secret, but
// a guessable "admin" would hand half of every brute-force attempt to the attacker.
func Login() string {
	return pick(base62[36:], 1) + pick(base62[:10]+base62[36:], 11)
}

func pick(alphabet string, n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}

func SHA256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
