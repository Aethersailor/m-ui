package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Version             = 19
	MinimumPasswordCharacters = 12
	MaximumPasswordBytes      = 1024
)

type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var DefaultPasswordParams = PasswordParams{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

func ValidateUsername(username string) error {
	if len(username) < 3 || len(username) > 64 {
		return errors.New("username must contain 3 to 64 characters")
	}
	for index, char := range username {
		isLetter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		isDigit := char >= '0' && char <= '9'
		isPunctuation := char == '_' || char == '-' || char == '.'
		if !isLetter && !isDigit && !isPunctuation {
			return errors.New(
				"username may contain only ASCII letters, digits, dot, dash, and underscore",
			)
		}
		if index == 0 && !isLetter && !isDigit {
			return errors.New("username must start with a letter or digit")
		}
	}
	return nil
}

func ValidatePassword(password string) error {
	if !utf8.ValidString(password) {
		return errors.New("password must be valid UTF-8")
	}
	if utf8.RuneCountInString(password) < MinimumPasswordCharacters {
		return errors.New("password must contain at least 12 characters")
	}
	if len(password) > MaximumPasswordBytes {
		return errors.New("password must not exceed 1024 bytes")
	}
	if strings.ContainsRune(password, '\x00') {
		return errors.New("password must not contain NUL")
	}
	return nil
}

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	return hashPassword(password, DefaultPasswordParams, rand.Reader)
}

func hashPassword(
	password string,
	params PasswordParams,
	random io.Reader,
) (string, error) {
	if err := validatePasswordParams(params); err != nil {
		return "", err
	}
	salt := make([]byte, params.SaltLength)
	if _, err := io.ReadFull(random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.Memory,
		params.Parallelism,
		params.KeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		params.Memory,
		params.Iterations,
		params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.Memory,
		params.Parallelism,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parsePasswordHash(
	encoded string,
) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, errors.New("invalid password hash format")
	}
	if !strings.HasPrefix(parts[2], "v=") {
		return PasswordParams{}, nil, nil, errors.New("invalid Argon2 version")
	}
	version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || version != argon2Version {
		return PasswordParams{}, nil, nil, errors.New("unsupported Argon2 version")
	}

	var params PasswordParams
	fields := strings.Split(parts[3], ",")
	if len(fields) != 3 {
		return PasswordParams{}, nil, nil, errors.New("invalid Argon2 parameters")
	}
	memory, err := parseUintField(fields[0], "m=", 32)
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}
	iterations, err := parseUintField(fields[1], "t=", 32)
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}
	parallelism, err := parseUintField(fields[2], "p=", 8)
	if err != nil {
		return PasswordParams{}, nil, nil, err
	}
	params.Memory = uint32(memory)
	params.Iterations = uint32(iterations)
	params.Parallelism = uint8(parallelism)
	if params.Memory < 8*1024 || params.Memory > 1024*1024 ||
		params.Iterations == 0 || params.Iterations > 10 ||
		params.Parallelism == 0 || params.Parallelism > 16 {
		return PasswordParams{}, nil, nil, errors.New("unsafe Argon2 parameters")
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 16 || len(salt) > 64 {
		return PasswordParams{}, nil, nil, errors.New("invalid password salt")
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return PasswordParams{}, nil, nil, errors.New("invalid password hash")
	}
	params.SaltLength = uint32(len(salt))
	params.KeyLength = uint32(len(expected))
	return params, salt, expected, nil
}

func validatePasswordParams(params PasswordParams) error {
	if params.Memory < 8*1024 || params.Memory > 1024*1024 ||
		params.Iterations == 0 || params.Iterations > 10 ||
		params.Parallelism == 0 || params.Parallelism > 16 ||
		params.SaltLength < 16 || params.SaltLength > 64 ||
		params.KeyLength < 16 || params.KeyLength > 64 {
		return errors.New("unsafe Argon2 parameters")
	}
	return nil
}

func parseUintField(value, prefix string, bits int) (uint64, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, errors.New("invalid Argon2 parameter")
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, bits)
	if err != nil {
		return 0, errors.New("invalid Argon2 parameter")
	}
	return parsed, nil
}
