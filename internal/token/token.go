// Package token generates and validates Praetor enrollment tokens.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// alphabet is Crockford base32 (no I, L, O, U).
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Token holds the plain token (shown once), its SHA-256 hash (stored in DB), and its DB primary key.
type Token struct {
	ID    string // ULID, used as enrollment_tokens.id
	Plain string // PRAETOR-XXXXX-XXXXX-XXXXX-XXXXX-XXXXX, shown once to operator
	Hash  []byte // SHA-256(Plain), stored in DB
}

// Generate creates a new random enrollment token.
func Generate() (*Token, error) {
	b := make([]byte, 25)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("token: read random: %w", err)
	}

	encoded := base32Encode(b) // 40 chars of Crockford base32
	// Take first 25 chars and split into 5 groups of 5
	chars := encoded[:25]
	plain := fmt.Sprintf("PRAETOR-%s-%s-%s-%s-%s",
		chars[0:5], chars[5:10], chars[10:15], chars[15:20], chars[20:25])

	id := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()

	return &Token{
		ID:    id,
		Plain: plain,
		Hash:  Hash(plain),
	}, nil
}

// Hash returns SHA-256 of the plain token string.
func Hash(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

// HashHex returns the SHA-256 of plain as a hex string (for DB storage).
func HashHex(plain string) string {
	return fmt.Sprintf("%x", Hash(plain))
}

// ParseAndValidate checks that s matches the PRAETOR-XXXXX-XXXXX-XXXXX-XXXXX-XXXXX format.
func ParseAndValidate(s string) error {
	if !strings.HasPrefix(s, "PRAETOR-") {
		return fmt.Errorf("token: must start with PRAETOR-")
	}
	parts := strings.Split(s, "-")
	// ["PRAETOR", "XXXXX", "XXXXX", "XXXXX", "XXXXX", "XXXXX"]
	if len(parts) != 6 {
		return fmt.Errorf("token: expected PRAETOR-XXXXX-XXXXX-XXXXX-XXXXX-XXXXX, got %d parts", len(parts))
	}
	for i, p := range parts[1:] {
		if len(p) != 5 {
			return fmt.Errorf("token: part %d has length %d, want 5", i+1, len(p))
		}
		for _, c := range p {
			if !strings.ContainsRune(alphabet, c) {
				return fmt.Errorf("token: invalid character %q in part %d", c, i+1)
			}
		}
	}
	return nil
}

// base32Encode encodes b using Crockford base32 alphabet.
func base32Encode(b []byte) string {
	var result []byte
	for i := 0; i+4 < len(b); i += 5 {
		v := uint64(b[i])<<32 | uint64(b[i+1])<<24 | uint64(b[i+2])<<16 | uint64(b[i+3])<<8 | uint64(b[i+4])
		for j := 7; j >= 0; j-- {
			result = append(result, alphabet[(v>>(uint(j)*5))&0x1F])
		}
	}
	return string(result)
}
