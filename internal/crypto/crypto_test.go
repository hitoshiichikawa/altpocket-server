package crypto

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"testing"
)

// helper: a deterministic 32-byte key that is easy to recognize in
// failures. The actual byte values are arbitrary; we only require they
// differ between makeKey and makeOtherKey.
func makeKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i + 1)
	}
	return key
}

func makeOtherKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(0xff - i)
	}
	return key
}

// TestEncryptDecryptRoundTrip covers Req 7.1: a value encrypted with a
// 32-byte key and decrypted with the same key must equal the original.
func TestEncryptDecryptRoundTrip(t *testing.T) {
	// Arrange
	key := makeKey(t)
	plaintext := []byte("1//abc.refresh-token.example")

	// Act
	blob, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}
	got, err := Decrypt(key, blob)
	if err != nil {
		t.Fatalf("Decrypt returned error: %v", err)
	}

	// Assert
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: got %q want %q", got, plaintext)
	}
	// Sanity: the blob must contain the nonce + ciphertext+tag overhead.
	if len(blob) < NonceSize+len(plaintext) {
		t.Fatalf("blob length too small: got %d want >= %d", len(blob), NonceSize+len(plaintext))
	}
}

// TestDecryptWithWrongKey covers Req 7.2: decryption with a different
// 32-byte key must fail with ErrDecryptionFailed.
func TestDecryptWithWrongKey(t *testing.T) {
	// Arrange
	key := makeKey(t)
	other := makeOtherKey(t)
	plaintext := []byte("refresh-token-payload")
	blob, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt returned error: %v", err)
	}

	// Act
	_, err = Decrypt(other, blob)

	// Assert
	if !errors.Is(err, ErrDecryptionFailed) {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}
}

// TestDecryptDetectsTampering covers Req 7.3: any single-byte mutation
// (in either nonce or ciphertext+tag region) must surface as
// ErrDecryptionFailed.
func TestDecryptDetectsTampering(t *testing.T) {
	key := makeKey(t)
	plaintext := []byte("refresh-token-payload")

	cases := []struct {
		name     string
		mutateAt int // byte index to flip
	}{
		{name: "nonce_first_byte", mutateAt: 0},
		{name: "nonce_last_byte", mutateAt: NonceSize - 1},
		{name: "ciphertext_first_byte", mutateAt: NonceSize},
		{name: "tag_last_byte_offset", mutateAt: -1}, // resolved to len-1 below
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			blob, err := Encrypt(key, plaintext)
			if err != nil {
				t.Fatalf("Encrypt returned error: %v", err)
			}
			tampered := append([]byte(nil), blob...)
			idx := tc.mutateAt
			if idx < 0 {
				idx = len(tampered) + idx
			}
			tampered[idx] ^= 0x01

			// Act
			_, err = Decrypt(key, tampered)

			// Assert
			if !errors.Is(err, ErrDecryptionFailed) {
				t.Fatalf("expected ErrDecryptionFailed for %s, got %v", tc.name, err)
			}
		})
	}
}

// TestEncryptProducesUniqueNonce covers Req 7.4: encrypting the same
// plaintext with the same key must produce distinct ciphertexts each
// time because the nonce is random per call.
func TestEncryptProducesUniqueNonce(t *testing.T) {
	// Arrange
	key := makeKey(t)
	plaintext := []byte("refresh-token-payload")
	const iterations = 100

	// Act
	seen := make(map[string]struct{}, iterations)
	for i := 0; i < iterations; i++ {
		blob, err := Encrypt(key, plaintext)
		if err != nil {
			t.Fatalf("Encrypt iter %d: %v", i, err)
		}
		seen[string(blob)] = struct{}{}
	}

	// Assert
	if len(seen) != iterations {
		t.Fatalf("expected %d unique ciphertexts, got %d (nonce reuse?)", iterations, len(seen))
	}
}

// TestEncryptEmptyInput covers Req 7.5: empty plaintext must be
// rejected (the design chose to refuse rather than silently encrypt).
func TestEncryptEmptyInput(t *testing.T) {
	// Arrange
	key := makeKey(t)

	// Act
	_, err := Encrypt(key, []byte{})

	// Assert
	if !errors.Is(err, ErrEmptyPlaintext) {
		t.Fatalf("expected ErrEmptyPlaintext, got %v", err)
	}

	// Also exercise nil input.
	if _, err := Encrypt(key, nil); !errors.Is(err, ErrEmptyPlaintext) {
		t.Fatalf("expected ErrEmptyPlaintext for nil plaintext, got %v", err)
	}
}

// TestDecodeKeyRejectsMalformedInput covers Req 7.6: empty input,
// invalid base64, and decoded keys of wrong length must each surface
// the documented sentinel error.
func TestDecodeKeyRejectsMalformedInput(t *testing.T) {
	// Build helpers for valid base64 of various lengths.
	encode := func(n int) string {
		b := make([]byte, n)
		if _, err := io.ReadFull(cryptorand.Reader, b); err != nil {
			t.Fatalf("rand.Read: %v", err)
		}
		return base64.StdEncoding.EncodeToString(b)
	}

	cases := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "empty_string", input: "", wantErr: ErrMalformedKey},
		{name: "not_base64", input: "!!not-base64!!", wantErr: ErrMalformedKey},
		{name: "decoded_16_bytes", input: encode(16), wantErr: ErrInvalidKeyLength},
		{name: "decoded_64_bytes", input: encode(64), wantErr: ErrInvalidKeyLength},
		{name: "decoded_31_bytes", input: encode(31), wantErr: ErrInvalidKeyLength},
		{name: "decoded_33_bytes", input: encode(33), wantErr: ErrInvalidKeyLength},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeKey(tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("DecodeKey(%q): expected %v, got %v", tc.name, tc.wantErr, err)
			}
		})
	}
}

// TestDecodeKeyAcceptsValidKey is the positive counterpart to
// TestDecodeKeyRejectsMalformedInput so that we don't only assert
// failure paths.
func TestDecodeKeyAcceptsValidKey(t *testing.T) {
	raw := make([]byte, KeySize)
	if _, err := io.ReadFull(cryptorand.Reader, raw); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	enc := base64.StdEncoding.EncodeToString(raw)

	got, err := DecodeKey(enc)
	if err != nil {
		t.Fatalf("DecodeKey returned error: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("DecodeKey returned %x, want %x", got, raw)
	}
}

// TestDecryptRejectsShortCiphertext covers Req 7.6: a blob shorter
// than the nonce length must surface as ErrCiphertextTooShort, never as
// a panic or ErrDecryptionFailed.
func TestDecryptRejectsShortCiphertext(t *testing.T) {
	key := makeKey(t)
	cases := []struct {
		name string
		blob []byte
	}{
		{name: "nil", blob: nil},
		{name: "empty", blob: []byte{}},
		{name: "shorter_than_nonce", blob: make([]byte, NonceSize-1)},
		{name: "exactly_nonce_size", blob: make([]byte, NonceSize)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decrypt(key, tc.blob)
			if !errors.Is(err, ErrCiphertextTooShort) {
				t.Fatalf("expected ErrCiphertextTooShort, got %v", err)
			}
		})
	}
}

// TestEncryptRejectsWrongKeyLength complements DecodeKey checks at the
// Encrypt boundary so that a caller skipping DecodeKey cannot silently
// produce a non-AES-256 ciphertext.
func TestEncryptRejectsWrongKeyLength(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
	}{
		{name: "nil_key", key: nil},
		{name: "16_bytes", key: make([]byte, 16)},
		{name: "31_bytes", key: make([]byte, 31)},
		{name: "33_bytes", key: make([]byte, 33)},
		{name: "64_bytes", key: make([]byte, 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Encrypt(tc.key, []byte("payload"))
			if !errors.Is(err, ErrInvalidKeyLength) {
				t.Fatalf("expected ErrInvalidKeyLength, got %v", err)
			}
		})
	}
}

// TestDecryptRejectsWrongKeyLength is the symmetric check on Decrypt.
func TestDecryptRejectsWrongKeyLength(t *testing.T) {
	// Build a valid blob with a real key first, then attempt to decrypt
	// with a too-short key.
	key := makeKey(t)
	blob, err := Encrypt(key, []byte("payload"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	if _, err := Decrypt(make([]byte, 16), blob); !errors.Is(err, ErrInvalidKeyLength) {
		t.Fatalf("expected ErrInvalidKeyLength for 16-byte key, got %v", err)
	}
}
