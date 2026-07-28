package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	t.Parallel()

	password := "correct horse battery staple"
	hash, err := hashPassword(
		password,
		PasswordParams{
			Memory:      8 * 1024,
			Iterations:  1,
			Parallelism: 1,
			SaltLength:  16,
			KeyLength:   32,
		},
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 32)),
	)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := VerifyPassword(password, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("VerifyPassword() = false, want true")
	}
	valid, err = VerifyPassword("wrong password value", hash)
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("VerifyPassword() = true for wrong password")
	}
	if strings.Contains(hash, password) {
		t.Fatal("password hash contains plaintext password")
	}
}

func TestPasswordValidation(t *testing.T) {
	t.Parallel()

	if err := ValidatePassword("too-short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := ValidatePassword("long-enough-password"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
}

func TestSessionTokenHash(t *testing.T) {
	t.Parallel()

	raw, err := newToken(bytes.NewReader(bytes.Repeat([]byte{0x11}, 64)), 32)
	if err != nil {
		t.Fatal(err)
	}
	if raw == HashToken(raw) {
		t.Fatal("raw token equals stored hash")
	}
	if got, want := len(HashToken(raw)), 64; got != want {
		t.Fatalf("hash length = %d, want %d", got, want)
	}
}

func TestLoginLimiterBackoffAndReset(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	limiter := NewLoginLimiter(func() time.Time { return now })
	key := loginLimitKey("127.0.0.1", "Admin")

	if got := limiter.Failure(key); got != 250*time.Millisecond {
		t.Fatalf("first delay = %s, want 250ms", got)
	}
	if got := limiter.RetryAfter(key); got != 250*time.Millisecond {
		t.Fatalf("retry delay = %s, want 250ms", got)
	}
	now = now.Add(250 * time.Millisecond)
	if got := limiter.Failure(key); got != 500*time.Millisecond {
		t.Fatalf("second delay = %s, want 500ms", got)
	}
	limiter.Success(key)
	if got := limiter.RetryAfter(key); got != 0 {
		t.Fatalf("retry after success = %s, want 0", got)
	}
}

func TestSanitizeUserAgent(t *testing.T) {
	t.Parallel()

	value := sanitizeUserAgent(strings.Repeat("界", 100) + "\r\n")
	if !strings.HasPrefix(value, "界") {
		t.Fatalf("unexpected sanitized user agent %q", value)
	}
	if len(value) > 256 {
		t.Fatalf("sanitized user agent length = %d, want <= 256", len(value))
	}
	if strings.ContainsAny(value, "\r\n") {
		t.Fatal("sanitized user agent contains control characters")
	}
}
