package crypto_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/ondrejsindelka/praetor-server/internal/watchdog/crypto"
)

// validKey generates a deterministic valid base64-encoded 32-byte key for tests.
func validKey() string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	c, err := crypto.NewCrypto(validKey())
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}

	plaintexts := [][]byte{
		{},
		{0x42},
		bytes.Repeat([]byte("A"), 64*1024),
		[]byte("hello, world — secret API key 🔑"),
	}

	for _, pt := range plaintexts {
		ct, err := c.Encrypt(pt)
		if err != nil {
			t.Fatalf("Encrypt(%d bytes): %v", len(pt), err)
		}
		got, err := c.Decrypt(ct)
		if err != nil {
			t.Fatalf("Decrypt(%d bytes): %v", len(pt), err)
		}
		if !bytes.Equal(got, pt) {
			t.Errorf("roundtrip mismatch for %d-byte plaintext", len(pt))
		}
	}
}

func TestEncryptProducesDistinctCiphertexts(t *testing.T) {
	c, err := crypto.NewCrypto(validKey())
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	pt := []byte("same plaintext")
	ct1, err := c.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt 1: %v", err)
	}
	ct2, err := c.Encrypt(pt)
	if err != nil {
		t.Fatalf("Encrypt 2: %v", err)
	}
	if bytes.Equal(ct1, ct2) {
		t.Error("two encryptions of the same plaintext produced identical ciphertexts (nonce reuse?)")
	}
}

func TestMalformedMasterKey_WrongLength(t *testing.T) {
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	_, err := crypto.NewCrypto(short)
	if err == nil {
		t.Fatal("expected error for wrong-length key, got nil")
	}
}

func TestMalformedMasterKey_BadBase64(t *testing.T) {
	_, err := crypto.NewCrypto("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for bad base64, got nil")
	}
}

func TestCiphertextTamper(t *testing.T) {
	c, err := crypto.NewCrypto(validKey())
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	ct, err := c.Encrypt([]byte("sensitive"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip a bit in the ciphertext body (after the nonce).
	ct[len(ct)-1] ^= 0xFF
	_, err = c.Decrypt(ct)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext, got nil")
	}
}

func TestDecrypt_TooShort(t *testing.T) {
	c, err := crypto.NewCrypto(validKey())
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}
	_, err = c.Decrypt([]byte("tiny"))
	if err == nil {
		t.Fatal("expected error for too-short ciphertext, got nil")
	}
}

func TestGenerateMasterKey(t *testing.T) {
	k1, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey: %v", err)
	}
	k2, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey 2: %v", err)
	}
	if k1 == k2 {
		t.Error("two generated master keys are identical")
	}
	// Must be usable directly with NewCrypto.
	if _, err := crypto.NewCrypto(k1); err != nil {
		t.Fatalf("NewCrypto with generated key: %v", err)
	}
}

// TestSanitizer_ErrorDoesNotLeakSensitiveValue verifies that when a plaintext
// string is passed as a masterKeyB64 value, the error message does not echo it back.
func TestSanitizer_ErrorDoesNotLeakSensitiveValue(t *testing.T) {
	// This is a plausible "oops" scenario: operator passes the actual API key
	// instead of the base64-encoded master key.
	plaintextAPIKey := "sk-my-super-secret-api-key-12345" //nolint:gosec // test fixture, not a real credential
	_, err := crypto.NewCrypto(plaintextAPIKey)
	if err == nil {
		// The value happened to decode to 32 bytes — vanishingly unlikely but handle it.
		t.Skip("plaintext happened to be valid base64 32-byte key; skipping leak test")
	}
	if strings.Contains(err.Error(), plaintextAPIKey) {
		t.Errorf("error message leaks the sensitive input value: %q", err.Error())
	}
}
