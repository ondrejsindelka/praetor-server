package notifier_test

import (
	"testing"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/notifier"
)

func TestSignVerifyRoundtrip(t *testing.T) {
	secret := []byte("supersecret")
	body := []byte(`{"event":"rule.fired"}`)

	sig := notifier.Sign(secret, body)
	if sig == "" {
		t.Fatal("Sign returned empty string")
	}
	if !notifier.Verify(secret, body, sig) {
		t.Error("Verify returned false for valid signature")
	}
}

func TestVerifyTamperedBody(t *testing.T) {
	secret := []byte("supersecret")
	body := []byte(`{"event":"rule.fired"}`)
	sig := notifier.Sign(secret, body)

	tampered := []byte(`{"event":"rule.fired","extra":"injected"}`)
	if notifier.Verify(secret, tampered, sig) {
		t.Error("Verify should return false for tampered body")
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	secret := []byte("supersecret")
	body := []byte(`{"event":"rule.fired"}`)
	sig := notifier.Sign(secret, body)

	wrongSecret := []byte("wrongsecret")
	if notifier.Verify(wrongSecret, body, sig) {
		t.Error("Verify should return false for wrong secret")
	}
}

func TestSignFormat(t *testing.T) {
	secret := []byte("key")
	body := []byte("body")
	sig := notifier.Sign(secret, body)

	if len(sig) < 7 || sig[:7] != "sha256=" {
		t.Errorf("Sign should return 'sha256=<hex>', got %q", sig)
	}
	// hex of sha256 is 64 chars; total length should be 7 + 64 = 71
	if len(sig) != 71 {
		t.Errorf("Sign length = %d, want 71", len(sig))
	}
}
