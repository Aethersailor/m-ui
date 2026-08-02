package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	muicrypto "github.com/Aethersailor/m-ui/internal/crypto"
	"github.com/Aethersailor/m-ui/internal/store"
)

const BootstrapTokenPurpose = "auth:first_admin_bootstrap"

type BootstrapRepository interface {
	BootstrapState(context.Context) (store.BootstrapState, error)
	EnsureBootstrap(context.Context, store.BootstrapSeed, time.Time) error
	RotateBootstrap(context.Context, store.BootstrapSeed, time.Time) error
	CompleteBootstrap(
		context.Context,
		string,
		store.BootstrapCompletion,
		time.Time,
	) error
}

type SetupStatus struct {
	Required bool
}

// EnsureBootstrap creates the durable one-time capability when a new database
// is first opened. The plaintext exists only in the caller's memory and is
// never returned or logged by this function.
func EnsureBootstrap(
	ctx context.Context,
	repository BootstrapRepository,
	sealer *muicrypto.Sealer,
	random io.Reader,
	clock func() time.Time,
) error {
	if repository == nil || sealer == nil {
		return errors.New("bootstrap repository and sealer are required")
	}
	if random == nil {
		random = rand.Reader
	}
	if clock == nil {
		clock = time.Now
	}
	state, err := repository.BootstrapState(ctx)
	if err == nil {
		if state.Required {
			return nil
		}
		// An existing administrator is made permanently complete by the store
		// on the next initialization. Nothing needs to be generated here.
		return repository.EnsureBootstrap(ctx, store.BootstrapSeed{}, clock().UTC())
	}
	if !errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("read administrator bootstrap state: %w", err)
	}
	raw, err := newToken(random, bootstrapTokenBytes)
	if err != nil {
		return fmt.Errorf("generate administrator bootstrap token: %w", err)
	}
	ciphertext, err := sealer.Encrypt([]byte(raw), BootstrapTokenPurpose)
	if err != nil {
		return fmt.Errorf("seal administrator bootstrap token: %w", err)
	}
	return repository.EnsureBootstrap(ctx, store.BootstrapSeed{
		TokenHash:       HashToken(raw),
		TokenCiphertext: ciphertext,
		CreatedAt:       clock().UTC(),
	}, clock().UTC())
}

func ReadBootstrapToken(
	state store.BootstrapState,
	sealer *muicrypto.Sealer,
) (string, error) {
	if sealer == nil || state.TokenCiphertext == "" || !state.Required {
		return "", ErrBootstrapCompleted
	}
	plaintext, err := sealer.Decrypt(
		state.TokenCiphertext,
		BootstrapTokenPurpose,
	)
	if err != nil {
		return "", fmt.Errorf("decrypt administrator bootstrap token: %w", err)
	}
	raw := string(plaintext)
	if err := validateBootstrapToken(raw); err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare(
		[]byte(HashToken(raw)),
		[]byte(state.TokenHash),
	) != 1 {
		return "", ErrInvalidBootstrapToken
	}
	return raw, nil
}

func RotateBootstrapToken(
	ctx context.Context,
	repository BootstrapRepository,
	sealer *muicrypto.Sealer,
	random io.Reader,
	clock func() time.Time,
) (string, error) {
	if repository == nil || sealer == nil {
		return "", errors.New("bootstrap repository and sealer are required")
	}
	if random == nil {
		random = rand.Reader
	}
	if clock == nil {
		clock = time.Now
	}
	raw, err := newToken(random, bootstrapTokenBytes)
	if err != nil {
		return "", fmt.Errorf("generate administrator bootstrap token: %w", err)
	}
	ciphertext, err := sealer.Encrypt([]byte(raw), BootstrapTokenPurpose)
	if err != nil {
		return "", fmt.Errorf("seal administrator bootstrap token: %w", err)
	}
	if err := repository.RotateBootstrap(ctx, store.BootstrapSeed{
		TokenHash:       HashToken(raw),
		TokenCiphertext: ciphertext,
		CreatedAt:       clock().UTC(),
	}, clock().UTC()); err != nil {
		return "", err
	}
	return raw, nil
}

func validateBootstrapToken(raw string) error {
	if len(raw) < 40 || len(raw) > 64 {
		return ErrInvalidBootstrapToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != bootstrapTokenBytes {
		return ErrInvalidBootstrapToken
	}
	return nil
}

const bootstrapTokenBytes = 32
