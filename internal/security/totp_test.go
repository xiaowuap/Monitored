package security

import (
	"testing"
	"time"
)

func TestTOTPMatchesRFC6238SHA1VectorAtSixDigits(t *testing.T) {
	// RFC 6238 SHA-1 test secret "12345678901234567890"; the published
	// eight-digit value at Unix 59 is 94287082, whose six-digit form is 287082.
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if !ValidateTOTP(secret, "287082", time.Unix(59, 0)) {
		t.Fatal("valid RFC 6238 code was rejected")
	}
	if ValidateTOTP(secret, "287083", time.Unix(59, 0)) {
		t.Fatal("invalid TOTP code was accepted")
	}
}

func TestTOTPSecretAndURI(t *testing.T) {
	secret, err := NewTOTPSecret()
	if err != nil || len(secret) < 20 {
		t.Fatalf("invalid secret %q: %v", secret, err)
	}
	uri := TOTPURI("Monitored", "admin@example.com", secret)
	if uri == "" || uri[:15] != "otpauth://totp/" {
		t.Fatalf("invalid URI %q", uri)
	}
}
