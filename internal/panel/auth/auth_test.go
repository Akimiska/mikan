package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatalf("unexpected hash format %q", hash)
	}
	if ok, err := VerifyPassword("s3cret-password", hash); err != nil || !ok {
		t.Fatalf("correct password rejected: %v", err)
	}
	if ok, _ := VerifyPassword("s3cret-passwore", hash); ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := VerifyPassword("x", "$argon2id$garbage"); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestLimiterBlocksAndEscalates(t *testing.T) {
	l := NewLimiter(3, time.Minute, 10*time.Second, time.Hour)
	now := time.Unix(1_000_000, 0)
	for i := 0; i < 2; i++ {
		if l.Fail("k", now) {
			t.Fatal("blocked too early")
		}
	}
	if !l.Fail("k", now) {
		t.Fatal("not blocked after 3 failures")
	}
	if ok, wait := l.Allowed("k", now); ok || wait != 10*time.Second {
		t.Fatalf("allowed=%v wait=%v", ok, wait)
	}
	now = now.Add(11 * time.Second)
	if ok, _ := l.Allowed("k", now); !ok {
		t.Fatal("still blocked after block time")
	}
	for i := 0; i < 3; i++ {
		l.Fail("k", now)
	}
	if _, wait := l.Allowed("k", now); wait != 20*time.Second {
		t.Fatalf("second block = %v, want doubled 20s", wait)
	}
	if ok, _ := l.Allowed("other", now); !ok {
		t.Fatal("unrelated key blocked")
	}
}

func TestLimiterWindowForgetsOldFailures(t *testing.T) {
	l := NewLimiter(3, time.Minute, time.Minute, time.Hour)
	now := time.Unix(1_000_000, 0)
	l.Fail("k", now)
	l.Fail("k", now)
	if l.Fail("k", now.Add(2*time.Minute)) {
		t.Fatal("failures outside the window must not count")
	}
}

func TestRecoveryCodesAreSingleUse(t *testing.T) {
	plain, stored := NewRecoveryCodes(3)
	rest, ok := UseRecoveryCode(stored, strings.ToUpper(plain[1]))
	if !ok {
		t.Fatal("valid code rejected (case/dash normalisation)")
	}
	if _, ok := UseRecoveryCode(rest, plain[1]); ok {
		t.Fatal("code accepted twice")
	}
	if _, ok := UseRecoveryCode(rest, plain[0]); !ok {
		t.Fatal("other codes must stay valid")
	}
}
