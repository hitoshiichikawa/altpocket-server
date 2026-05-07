// Package crypto provides AES-256-GCM encryption helpers used for
// protecting sensitive at-rest values such as Google Sheets refresh
// tokens. It is intentionally a small, dependency-free utility:
//
//   - It never logs keys, plaintexts, ciphertexts, or nonces.
//   - It never panics; all error conditions are reported via sentinel
//     errors so that callers can map them to domain-level failures.
//   - The persisted layout is fixed at [12-byte nonce || ciphertext+tag],
//     so the same key/value pair always round-trips through Encrypt/Decrypt.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// KeySize is the required key length in bytes for AES-256.
const KeySize = 32

// NonceSize is the GCM nonce length in bytes (the standard 12 bytes).
const NonceSize = 12

// Sentinel errors. Callers should compare with errors.Is.
//
// ErrDecryptionFailed is intentionally returned for both wrong-key and
// tampered-ciphertext cases so that the caller cannot distinguish the
// two failure modes from outside (preventing oracle-style attacks).
var (
	ErrInvalidKeyLength   = errors.New("crypto: invalid key length")
	ErrMalformedKey       = errors.New("crypto: malformed key (base64 decode failed)")
	ErrEmptyPlaintext     = errors.New("crypto: empty plaintext")
	ErrCiphertextTooShort = errors.New("crypto: ciphertext too short")
	ErrDecryptionFailed   = errors.New("crypto: decryption failed")
)

// DecodeKey decodes a base64-encoded key string into a KeySize-byte key.
// It returns ErrMalformedKey if base64 decoding fails (including the
// empty-string case where decoding succeeds but yields zero bytes), and
// ErrInvalidKeyLength if the decoded length is not exactly KeySize.
func DecodeKey(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, ErrMalformedKey
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrMalformedKey
	}
	if len(raw) != KeySize {
		return nil, ErrInvalidKeyLength
	}
	return raw, nil
}

// Encrypt encrypts plaintext with AES-256-GCM using key.
// It returns a byte slice in the layout [nonce(NonceSize) || ciphertext+tag].
// Each call generates a fresh random nonce via crypto/rand, so two calls
// with the same key/plaintext produce different blobs.
//
// Returns:
//   - ErrInvalidKeyLength if len(key) != KeySize.
//   - ErrEmptyPlaintext if len(plaintext) == 0.
//   - any error returned by crypto/rand or the AES/GCM constructors
//     (these are wrapped in their original form to preserve the cause).
func Encrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeyLength
	}
	if len(plaintext) == 0 {
		return nil, ErrEmptyPlaintext
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	// Allocate nonce with capacity for the full sealed blob so that
	// gcm.Seal can append into the same backing array.
	nonce := make([]byte, NonceSize, NonceSize+len(plaintext)+gcm.Overhead())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts a [nonce || ciphertext+tag] blob with AES-256-GCM
// using key.
//
// Returns:
//   - ErrInvalidKeyLength if len(key) != KeySize.
//   - ErrCiphertextTooShort if len(blob) <= NonceSize (no room for tag).
//   - ErrDecryptionFailed if GCM auth-tag verification fails (used for
//     wrong-key, tampered-nonce, and tampered-ciphertext cases; callers
//     must not distinguish them).
func Decrypt(key, blob []byte) ([]byte, error) {
	if len(key) != KeySize {
		return nil, ErrInvalidKeyLength
	}
	if len(blob) <= NonceSize {
		return nil, ErrCiphertextTooShort
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := blob[:NonceSize]
	ciphertext := blob[NonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		// Collapse all GCM auth failures into a single sentinel so
		// callers (and attackers) cannot distinguish wrong-key from
		// tampered-ciphertext. The underlying err is intentionally
		// dropped because it can leak implementation hints.
		return nil, ErrDecryptionFailed
	}
	return plaintext, nil
}
