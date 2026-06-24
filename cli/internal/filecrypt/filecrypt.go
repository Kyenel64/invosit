package filecrypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

const DEKSize = 32

const NonceSize = 12

var (
	// ErrInvalidDEK is returned when a DEK is not exactly DEKSize bytes.
	ErrInvalidDEK = errors.New("dek must be 32 bytes")
	// ErrMalformedCiphertext is returned when ciphertext is too short to
	// contain a nonce and tag.
	ErrMalformedCiphertext = errors.New("ciphertext too short")
	// ErrDecrypt is returned when GCM authentication fails (wrong key or
	// tampered ciphertext). Detail is withheld deliberately.
	ErrDecrypt = errors.New("decrypt failed")
)

// Encrypt generates a fresh 256-bit DEK and seals plaintext with AES-256-GCM.
// The returned ciphertext is framed as nonce(12) || ciphertext || tag(16).
func Encrypt(plaintext []byte) (dek, ciphertext []byte, err error) {
	dek = make([]byte, DEKSize)
	if _, err = rand.Read(dek); err != nil {
		return nil, nil, fmt.Errorf("generate dek: %w", err)
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}
	// Seal appends to nonce, yielding nonce || ciphertext || tag in one slice.
	ciphertext = gcm.Seal(nonce, nonce, plaintext, nil)
	return dek, ciphertext, nil
}

// Decrypt opens an AES-256-GCM frame (nonce(12) || ciphertext || tag(16)) with
// dek. It returns ErrDecrypt if authentication fails.
func Decrypt(dek, ciphertext []byte) ([]byte, error) {
	if len(dek) != DEKSize {
		return nil, ErrInvalidDEK
	}
	gcm, err := newGCM(dek)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, ErrMalformedCiphertext
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

func newGCM(dek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new gcm: %w", err)
	}
	return gcm, nil
}
