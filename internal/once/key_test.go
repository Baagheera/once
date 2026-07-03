package once

import "testing"

func TestValidateKeyRejectsNonASCII(t *testing.T) {
	for _, key := range []string{"fattura:è", "ключ", "demo:１２３"} {
		t.Run(key, func(t *testing.T) {
			if err := ValidateKey(key); err == nil {
				t.Fatal("expected non-ASCII key to be rejected")
			}
		})
	}
}

func TestValidateKeyAllowsDocumentedCharacters(t *testing.T) {
	key := "email:user42:welcome.v1_test@example=ok-123"
	if err := ValidateKey(key); err != nil {
		t.Fatalf("ValidateKey(%q): %v", key, err)
	}
}
