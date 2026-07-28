package security

import (
	"bytes"
	"testing"
)

func TestSealerRoundTripAndPurposeBinding(t *testing.T) {
	sealer, err := NewSealer("this-is-a-long-random-test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal([]byte(`{"token":"secret"}`), "connection:123")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	opened, err := sealer.Open(sealed, "connection:123")
	if err != nil {
		t.Fatal(err)
	}
	if string(opened) != `{"token":"secret"}` {
		t.Fatalf("unexpected plaintext %q", opened)
	}
	if _, err := sealer.Open(sealed, "connection:456"); err == nil {
		t.Fatal("purpose substitution must fail")
	}
}

func TestSealerRejectsTampering(t *testing.T) {
	sealer, _ := NewSealer("this-is-a-long-random-test-master-key")
	sealed, _ := sealer.Seal([]byte("secret"), "notification:1")
	sealed[len(sealed)-1] ^= 1
	if _, err := sealer.Open(sealed, "notification:1"); err == nil {
		t.Fatal("tampered ciphertext must fail")
	}
}
