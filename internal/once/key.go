package once

import (
	"fmt"
	"unicode"
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
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '.', '_', ':', '@', '=', '-':
			continue
		default:
			return fmt.Errorf("key contains invalid character %q", r)
		}
	}
	return nil
}
