// Package crypto provides AES-GCM encryption/decryption for secrets at rest.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// Crypto provides AES-GCM encrypt/decrypt for secrets at rest.
type Crypto struct {
	aead cipher.AEAD
}

// NewCrypto creates a Crypto from a base64-encoded 32-byte master key.
func NewCrypto(masterKeyB64 string) (*Crypto, error) {
	key, err := base64.StdEncoding.DecodeString(masterKeyB64)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode master key: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("crypto: master key must be exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return &Crypto{aead: aead}, nil
}

// Encrypt encrypts plaintext. Returns nonce+ciphertext as []byte.
func (c *Crypto) Encrypt(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("crypto: generate nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts ciphertext produced by Encrypt.
func (c *Crypto) Decrypt(ciphertext []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(ciphertext) < ns {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := ciphertext[:ns], ciphertext[ns:]
	plaintext, err := c.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("crypto: decrypt failed")
	}
	return plaintext, nil
}

// GenerateMasterKey returns a random 32-byte key as base64.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("crypto: generate key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
