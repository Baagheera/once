package once

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

func NewAttemptToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}

func HashAttemptToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func ValidateAttemptToken(token string) error {
	if token == "" {
		return fmt.Errorf("missing attempt token")
	}
	if len(token) > 128 {
		return fmt.Errorf("attempt token is too long")
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		return fmt.Errorf("invalid attempt token")
	}
	return nil
}
