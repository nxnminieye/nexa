package coreapp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var ErrPasswordMismatch = errors.New("password mismatch")

type PasswordHasher interface {
	Hash([]byte) (string, error)
	Verify(string, []byte) error
}

type Argon2idOptions struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   uint32
	KeyBytes    uint32
}

type argon2idHasher struct {
	options Argon2idOptions
}

func NewArgon2idHasher(options Argon2idOptions) (PasswordHasher, error) {
	if err := validateArgon2idOptions(options); err != nil {
		return nil, err
	}
	return &argon2idHasher{options: options}, nil
}

func validateArgon2idOptions(options Argon2idOptions) error {
	if options.MemoryKiB < 8*1024 || options.MemoryKiB > 1024*1024 ||
		options.Iterations < 1 || options.Iterations > 10 ||
		options.Parallelism < 1 || options.Parallelism > 32 ||
		options.SaltBytes < 16 || options.SaltBytes > 64 ||
		options.KeyBytes < 16 || options.KeyBytes > 64 {
		return invalid("password.argon2id")
	}
	return nil
}

func (h *argon2idHasher) Hash(password []byte) (string, error) {
	if len(password) == 0 {
		return "", invalid("password.hash")
	}
	salt := make([]byte, h.options.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", coreError("password.hash", CodeStoreFailure, err)
	}
	key := argon2.IDKey(password, salt, h.options.Iterations, h.options.MemoryKiB, h.options.Parallelism, h.options.KeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", h.options.MemoryKiB, h.options.Iterations, h.options.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (h *argon2idHasher) Verify(encoded string, password []byte) error {
	options, salt, expected, err := parseArgon2id(encoded)
	if err != nil {
		return ErrPasswordMismatch
	}
	actual := argon2.IDKey(password, salt, options.Iterations, options.MemoryKiB, options.Parallelism, options.KeyBytes)
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrPasswordMismatch
	}
	return nil
}

func parseArgon2id(encoded string) (Argon2idOptions, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return Argon2idOptions{}, nil, nil, ErrPasswordMismatch
	}
	var options Argon2idOptions
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &options.MemoryKiB, &options.Iterations, &options.Parallelism); err != nil {
		return Argon2idOptions{}, nil, nil, ErrPasswordMismatch
	}
	if parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", options.MemoryKiB, options.Iterations, options.Parallelism) {
		return Argon2idOptions{}, nil, nil, ErrPasswordMismatch
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return Argon2idOptions{}, nil, nil, ErrPasswordMismatch
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return Argon2idOptions{}, nil, nil, ErrPasswordMismatch
	}
	options.SaltBytes = uint32(len(salt))
	options.KeyBytes = uint32(len(key))
	if err := validateArgon2idOptions(options); err != nil {
		return Argon2idOptions{}, nil, nil, ErrPasswordMismatch
	}
	return options, salt, key, nil
}
