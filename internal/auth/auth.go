package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/argon2"
)

const (
	// TokenExpiration is the duration a JWT token is valid
	TokenExpiration = 24 * time.Hour
)

var ErrInvalidToken = errors.New("invalid token")

// Hasher encapsulates password hashing and verification using Argon2id
type Hasher struct{}

// HashPassword creates an Argon2id hash of the password
// Uses OWASP-recommended parameters: memory=64MB, iterations=3, parallelism=4
func (h *Hasher) HashPassword(password string) (string, error) {
	const (
		memory      = 64 * 1024 // 64 MB
		iterations  = 3
		parallelism = 4
		saltLength  = 16
		keyLength   = 32
	)

	// Generate random salt using crypto/rand
	salt := make([]byte, saltLength)
	_, err := rand.Read(salt)
	if err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	// Create hash using Argon2id
	hash := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLength)

	// Encode as: $argon2id$v=19$m=65536,t=3,p=4$<hex_salt>$<hex_hash>
	hashStr := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		19, memory, iterations, parallelism, hex.EncodeToString(salt), hex.EncodeToString(hash))

	return hashStr, nil
}

// VerifyPassword checks if the provided password matches the Argon2id hash
func (h *Hasher) VerifyPassword(hashStr, password string) error {
	const (
		keyLength = 32
	)

	// Parse the hash format: $argon2id$v=19$m=65536,t=3,p=4$<hex_salt>$<hex_hash>
	// Split by $ to get individual parts
	parts := strings.Split(hashStr, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return errors.New("invalid hash format")
	}

	// Parse parameters from parts[2]: v=19
	var version int
	_, err := fmt.Sscanf(parts[2], "v=%d", &version)
	if err != nil {
		return fmt.Errorf("invalid version: %w", err)
	}

	// Parse parameters from parts[3]: m=65536,t=3,p=4
	var memory, iterations, parallelism int
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism)
	if err != nil {
		return fmt.Errorf("invalid parameters: %w", err)
	}

	// Decode salt and hash from hex
	salt, err := hex.DecodeString(parts[4])
	if err != nil {
		return fmt.Errorf("decode salt: %w", err)
	}

	storedHash, err := hex.DecodeString(parts[5])
	if err != nil {
		return fmt.Errorf("decode hash: %w", err)
	}

	// Verify password with same parameters
	computedHash := argon2.IDKey([]byte(password), salt, uint32(iterations), uint32(memory), uint8(parallelism), keyLength)

	// Compare hashes using constant-time comparison to prevent timing attacks
	if len(computedHash) != len(storedHash) {
		return errors.New("password does not match")
	}

	for i := range storedHash {
		if storedHash[i] != computedHash[i] {
			return errors.New("password does not match")
		}
	}

	return nil
}

// TokenManager manages JWT token generation and verification
type TokenManager struct {
	secret string
}

// NewTokenManager creates a new token manager with the given secret
func NewTokenManager(secret string) *TokenManager {
	return &TokenManager{secret: secret}
}

// Claims represents the JWT token claims
type Claims struct {
	jwt.RegisteredClaims
}

// GenerateToken creates a new JWT token that expires in TokenExpiration
func (tm *TokenManager) GenerateToken() (string, error) {
	claims := &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(TokenExpiration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(tm.secret))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return tokenString, nil
}

// VerifyToken validates a JWT token and returns true if valid
func (tm *TokenManager) VerifyToken(tokenString string) error {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return []byte(tm.secret), nil
	})

	if err != nil {
		return err
	}

	if !token.Valid {
		return ErrInvalidToken
	}

	return nil
}
