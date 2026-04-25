package token_test

import (
	"testing"

	"github.com/ondrejsindelka/praetor-server/internal/token"
)

func TestGenerateUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		tok, err := token.Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if seen[tok.Plain] {
			t.Fatalf("duplicate token: %s", tok.Plain)
		}
		seen[tok.Plain] = true
		if tok.ID == "" {
			t.Fatal("token ID is empty")
		}
	}
}

func TestHashDeterministic(t *testing.T) {
	h1 := token.Hash("PRAETOR-ABCDE-FGHJK-MNPQR-STVWX-YZ012")
	h2 := token.Hash("PRAETOR-ABCDE-FGHJK-MNPQR-STVWX-YZ012")
	if string(h1) != string(h2) {
		t.Fatal("Hash is not deterministic")
	}
}

func TestParseAndValidate(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"PRAETOR-ABCDE-FGHJK-MNPQR-STVWX-YZ012", true},
		{"praetor-abcde-fghjk-mnpqr-stvwx-yz012", false}, // lowercase
		{"PRAETOR-ABCDE-FGHJK-MNPQR-STVWX", false},       // too short
		{"PRAETOR-ABCDE-FGHJK-MNPQR-STVWX-YZ01", false},  // last group too short
		{"PRAETOR-ABCDE-FGHJK-MNPQR-STVWX-YZ0I2", false}, // I is invalid in Crockford
		{"not-a-token", false},
	}
	for _, tc := range tests {
		err := token.ParseAndValidate(tc.input)
		if tc.valid && err != nil {
			t.Errorf("ParseAndValidate(%q) unexpected error: %v", tc.input, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("ParseAndValidate(%q) expected error, got nil", tc.input)
		}
	}
}
