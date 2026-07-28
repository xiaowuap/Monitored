package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const envelopeVersion = "v1"

// Sealer encrypts connection and notification credentials with AES-256-GCM.
// The caller supplies a purpose string as associated data so ciphertext cannot
// be moved between records or secret types without detection.
type Sealer struct {
	aead cipher.AEAD
}

func NewSealer(masterKey string) (*Sealer, error) {
	if len(strings.TrimSpace(masterKey)) < 24 {
		return nil, errors.New("master key must contain at least 24 characters")
	}
	key := sha256.Sum256([]byte(masterKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("initialize AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize GCM: %w", err)
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plaintext []byte, purpose string) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(purpose) == "" {
		return nil, errors.New("secret purpose is required")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("create nonce: %w", err)
	}
	ciphertext := s.aead.Seal(nil, nonce, plaintext, []byte(purpose))
	envelope := append(nonce, ciphertext...)
	encoded := base64.RawURLEncoding.EncodeToString(envelope)
	return []byte(envelopeVersion + "." + encoded), nil
}

func (s *Sealer) Open(envelope []byte, purpose string) ([]byte, error) {
	if len(envelope) == 0 {
		return nil, nil
	}
	parts := strings.SplitN(string(envelope), ".", 2)
	if len(parts) != 2 || parts[0] != envelopeVersion {
		return nil, errors.New("unsupported secret envelope")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid secret envelope encoding")
	}
	if len(raw) < s.aead.NonceSize()+s.aead.Overhead() {
		return nil, errors.New("truncated secret envelope")
	}
	plaintext, err := s.aead.Open(
		nil,
		raw[:s.aead.NonceSize()],
		raw[s.aead.NonceSize():],
		[]byte(purpose),
	)
	if err != nil {
		return nil, errors.New("secret authentication failed")
	}
	return plaintext, nil
}
