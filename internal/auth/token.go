package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
)

const (
	sessionTokenBytes = 32
	csrfTokenBytes    = 32
	opaqueIDBytes     = 16
)

func HashToken(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func newToken(random io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func newOpaqueID(random io.Reader) (string, error) {
	value := make([]byte, opaqueIDBytes)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate random ID: %w", err)
	}
	return hex.EncodeToString(value), nil
}
