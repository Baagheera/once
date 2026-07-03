package once

import (
	"fmt"
)

const MaxKeyLen = 256

func ValidateKey(key string) error {
	if key == "" {
		return fmt.Errorf("empty key")
	}
	if len(key) > MaxKeyLen {
		return fmt.Errorf("key is too long")
	}
	for _, r := range key {
		if !validKeyRune(r) {
			return fmt.Errorf("key contains invalid character %q", r)
		}
	}
	return nil
}

func validKeyRune(r rune) bool {
	if r >= 'a' && r <= 'z' {
		return true
	}
	if r >= 'A' && r <= 'Z' {
		return true
	}
	if r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case '.', '_', ':', '@', '=', '-':
		return true
	default:
		return false
	}
}
