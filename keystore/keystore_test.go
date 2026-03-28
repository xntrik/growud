package keystore

import (
	"os"
	"testing"
)

func TestTokenRoundTrip(t *testing.T) {
	if os.Getenv("GROWUD_TEST_KEYRING") == "" {
		t.Skip("Skipping keyring test (set GROWUD_TEST_KEYRING=1 to enable)")
	}

	// Clean up any leftover test state
	_ = DeleteToken()

	// GetToken on empty keyring should return ("", nil)
	tok, err := GetToken()
	if err != nil {
		t.Fatalf("GetToken (empty): %v", err)
	}
	if tok != "" {
		t.Fatalf("GetToken (empty) = %q, want empty", tok)
	}

	// SetToken + GetToken round-trip
	if err := SetToken("test-token-abc"); err != nil {
		t.Fatalf("SetToken: %v", err)
	}

	tok, err = GetToken()
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if tok != "test-token-abc" {
		t.Fatalf("GetToken = %q, want %q", tok, "test-token-abc")
	}

	// Overwrite
	if err := SetToken("updated-token"); err != nil {
		t.Fatalf("SetToken (overwrite): %v", err)
	}
	tok, err = GetToken()
	if err != nil {
		t.Fatalf("GetToken (overwrite): %v", err)
	}
	if tok != "updated-token" {
		t.Fatalf("GetToken = %q, want %q", tok, "updated-token")
	}

	// DeleteToken
	if err := DeleteToken(); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	tok, err = GetToken()
	if err != nil {
		t.Fatalf("GetToken (after delete): %v", err)
	}
	if tok != "" {
		t.Fatalf("GetToken (after delete) = %q, want empty", tok)
	}

	// DeleteToken on already-empty keyring should not error
	if err := DeleteToken(); err != nil {
		t.Fatalf("DeleteToken (idempotent): %v", err)
	}
}
