package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/google/uuid"
)

func GenerateUUID() (string, error) {
	generated, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	return generated.String(), nil
}

func GenerateShortID() (string, error) {
	return generateShortID(rand.Reader)
}

func generateShortID(random io.Reader) (string, error) {
	value := make([]byte, 8)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", fmt.Errorf("generate short ID: %w", err)
	}
	return hex.EncodeToString(value), nil
}
