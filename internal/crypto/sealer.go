package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const envelopeVersion = "v1"

type Sealer struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewSealer(key MasterKey) (*Sealer, error) {
	return newSealer(key, rand.Reader)
}

func newSealer(key MasterKey, random io.Reader) (*Sealer, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create AES-GCM: %w", err)
	}
	return &Sealer{aead: aead, random: random}, nil
}

func (s *Sealer) Encrypt(plaintext []byte, purpose string) (string, error) {
	if len(plaintext) == 0 {
		return "", errors.New("plaintext is empty")
	}
	if purpose == "" {
		return "", errors.New("encryption purpose is empty")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return "", fmt.Errorf("generate encryption nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, plaintext, []byte(purpose))
	return envelopeVersion + "." +
		base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Sealer) Decrypt(envelope string, purpose string) ([]byte, error) {
	if purpose == "" {
		return nil, errors.New("encryption purpose is empty")
	}
	version, payload, ok := strings.Cut(envelope, ".")
	if !ok || version != envelopeVersion {
		return nil, errors.New("unsupported encrypted envelope")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, errors.New("encrypted envelope is malformed")
	}
	nonceSize := s.aead.NonceSize()
	if len(decoded) <= nonceSize {
		return nil, errors.New("encrypted envelope is truncated")
	}
	nonce := decoded[:nonceSize]
	ciphertext := decoded[nonceSize:]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext, []byte(purpose))
	if err != nil {
		return nil, errors.New("encrypted envelope authentication failed")
	}
	return plaintext, nil
}
