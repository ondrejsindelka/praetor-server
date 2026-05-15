package notifier

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// Sign returns "sha256=<hex>" HMAC of body using secret.
// Header name: X-Praetor-Signature
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks an incoming signature (constant-time compare).
func Verify(secret, body []byte, signature string) bool {
	expected := Sign(secret, body)
	return hmac.Equal([]byte(expected), []byte(signature))
}
