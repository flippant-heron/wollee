package auth

import (
	"strings"
	"testing"
	"time"
)

// TestHashPassword verifies that password hashing produces valid Argon2id hashes
func TestHashPassword(t *testing.T) {
	h := &Hasher{}

	hash, err := h.HashPassword("test-password")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// Hash should not be empty
	if hash == "" {
		t.Error("hash is empty")
	}

	// Hash should follow the format: $argon2id$v=19$m=65536,t=3,p=4$<hex_salt>$<hex_hash>
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash does not start with $argon2id$: %s", hash)
	}

	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		t.Errorf("hash should have 6 parts separated by $, got %d: %s", len(parts), hash)
	}

	// Each run should produce a different hash due to random salt
	hash2, err := h.HashPassword("test-password")
	if err != nil {
		t.Fatalf("second HashPassword failed: %v", err)
	}

	if hash == hash2 {
		t.Error("two hashes of the same password should differ (different salt)")
	}
}

// TestVerifyPassword verifies that correct passwords pass verification
func TestVerifyPassword(t *testing.T) {
	h := &Hasher{}
	password := "test-password-123"

	hash, err := h.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	// Correct password should verify
	err = h.VerifyPassword(hash, password)
	if err != nil {
		t.Errorf("VerifyPassword failed for correct password: %v", err)
	}

	// Wrong password should fail
	err = h.VerifyPassword(hash, "wrong-password")
	if err == nil {
		t.Error("VerifyPassword succeeded with wrong password")
	}

	// Empty password should fail
	err = h.VerifyPassword(hash, "")
	if err == nil {
		t.Error("VerifyPassword succeeded with empty password")
	}
}

// TestVerifyPasswordInvalidFormat checks that invalid hash formats are rejected
func TestVerifyPasswordInvalidFormat(t *testing.T) {
	h := &Hasher{}

	tests := []string{
		"not-a-hash",
		"",
		"$argon2i$v=19$m=65536,t=3,p=4$salt$hash", // wrong version (argon2i)
		"$argon2id$m=65536,t=3,p=4$salt$hash",     // missing v parameter
	}

	for _, invalidHash := range tests {
		err := h.VerifyPassword(invalidHash, "password")
		if err == nil {
			t.Errorf("VerifyPassword should fail for invalid hash: %s", invalidHash)
		}
	}
}

// TestTokenManager verifies JWT token generation and verification
func TestTokenManager(t *testing.T) {
	secret := "test-secret-key-12345"
	tm := NewTokenManager(secret)

	token, err := tm.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	if token == "" {
		t.Error("generated token is empty")
	}

	// Token should be a valid JWT (three parts separated by dots)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Errorf("JWT should have 3 parts, got %d", len(parts))
	}

	// Token should verify successfully
	err = tm.VerifyToken(token)
	if err != nil {
		t.Errorf("VerifyToken failed for valid token: %v", err)
	}
}

// TestTokenManagerDifferentSecret verifies that tokens are bound to their secret
func TestTokenManagerDifferentSecret(t *testing.T) {
	secret1 := "secret-key-1"
	secret2 := "secret-key-2"

	tm1 := NewTokenManager(secret1)
	token, err := tm1.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	// Token should verify with the same secret
	err = tm1.VerifyToken(token)
	if err != nil {
		t.Errorf("VerifyToken failed with same secret: %v", err)
	}

	// Token should NOT verify with a different secret
	tm2 := NewTokenManager(secret2)
	err = tm2.VerifyToken(token)
	if err == nil {
		t.Error("VerifyToken succeeded with different secret (should fail)")
	}
}

// TestTokenManagerInvalidToken checks that invalid tokens are rejected
func TestTokenManagerInvalidToken(t *testing.T) {
	tm := NewTokenManager("test-secret")

	tests := []string{
		"not.a.token",
		"invalid",
		"",
		"header.payload.invalidsignature",
	}

	for _, invalidToken := range tests {
		err := tm.VerifyToken(invalidToken)
		if err == nil {
			t.Errorf("VerifyToken should fail for invalid token: %s", invalidToken)
		}
	}
}

// TestTokenExpiration verifies that the token expiration is set correctly
func TestTokenExpiration(t *testing.T) {
	if TokenExpiration != 24*time.Hour {
		t.Errorf("TokenExpiration should be 24 hours, got %v", TokenExpiration)
	}
}
