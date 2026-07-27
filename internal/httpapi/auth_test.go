package httpapi

import (
	"testing"
	"time"

	"github.com/monitored-io/monitored/internal/config"
)

func TestAuthenticatorSignVerifyAndRejectTampering(t *testing.T) {
	auth := newAuthenticator(config.Server{
		MasterKey: "test-key-with-enough-entropy",
		PublicURL: "https://monitored.example",
	})
	want := identity{Email: "operator@example.com", Role: "admin", Exp: time.Now().Add(time.Minute).Unix()}
	token, err := auth.sign(want)
	if err != nil {
		t.Fatalf("sign() error = %v", err)
	}
	got, err := auth.verify(token)
	if err != nil {
		t.Fatalf("verify() error = %v", err)
	}
	if got.Email != want.Email || got.Role != want.Role {
		t.Fatalf("verify() = %#v, want %#v", got, want)
	}
	if _, err := auth.verify(token + "tampered"); err == nil {
		t.Fatal("verify() accepted a tampered token")
	}
}

func TestAuthenticatorRejectsExpiredToken(t *testing.T) {
	auth := newAuthenticator(config.Server{MasterKey: "test-key"})
	token, err := auth.sign(identity{
		Email: "operator@example.com",
		Role:  "admin",
		Exp:   time.Now().Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("sign() error = %v", err)
	}
	if _, err := auth.verify(token); err == nil {
		t.Fatal("verify() accepted an expired token")
	}
}
