// Package keys handles the public-key half of Invosit's envelope encryption:
// wrapping a per-file DEK for a recipient's public key and unwrapping it with
// the matching private key. The symmetric half — generating the DEK and
// encrypting file content with AES-256-GCM — lives in package filecrypt.
package keys

import (
	"crypto/rand"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const (
	PublicKeySize  = 32
	PrivateKeySize = 32

	wrappedOverhead = box.AnonymousOverhead
)

var (
	// ErrInvalidPublicKey is returned when a public key is not PublicKeySize bytes.
	ErrInvalidPublicKey = errors.New("public key must be 32 bytes")
	// ErrInvalidPrivateKey is returned when a private key is not PrivateKeySize bytes.
	ErrInvalidPrivateKey = errors.New("private key must be 32 bytes")
	// ErrMalformedWrappedDEK is returned when a wrapped DEK is too short to
	// contain the anonymous-box overhead.
	ErrMalformedWrappedDEK = errors.New("wrapped dek too short")
	// ErrUnwrap is returned when the sealed box fails to open (wrong key or
	// tampered ciphertext). Detail is withheld deliberately.
	ErrUnwrap = errors.New("unwrap failed")
)

type Keypair struct {
	Public  []byte
	Private []byte
}

// Generate creates a fresh x25519 keypair, drawing randomness from crypto/rand.
func Generate() (Keypair, error) {
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return Keypair{}, fmt.Errorf("generate keypair: %w", err)
	}
	return Keypair{Public: publicKey[:], Private: privateKey[:]}, nil
}

// Wrap seals dek to recipientPublicKey with an x25519 anonymous sealed box. The
// per-call ephemeral sender key makes the output non-deterministic; its length
// is len(dek) + 48.
func Wrap(dek, recipientPublicKey []byte) ([]byte, error) {
	if len(recipientPublicKey) != PublicKeySize {
		return nil, ErrInvalidPublicKey
	}
	var recipient [32]byte
	copy(recipient[:], recipientPublicKey)
	wrapped, err := box.SealAnonymous(nil, dek, &recipient, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("seal anonymous: %w", err)
	}
	return wrapped, nil
}

// PublicFromPrivate derives the x25519 public key matching privateKey.
func PublicFromPrivate(privateKey []byte) ([]byte, error) {
	if len(privateKey) != PrivateKeySize {
		return nil, ErrInvalidPrivateKey
	}
	publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("derive public key: %w", err)
	}
	return publicKey, nil
}

// Unwrap opens an anonymous sealed box with privateKey. OpenAnonymous also needs
// the recipient's public key, so it is derived from privateKey here, keeping the
// signature to the private half the caller holds. Returns ErrUnwrap if the box
// does not open.
func Unwrap(wrappedDEK, privateKey []byte) ([]byte, error) {
	if len(privateKey) != PrivateKeySize {
		return nil, ErrInvalidPrivateKey
	}
	if len(wrappedDEK) < wrappedOverhead {
		return nil, ErrMalformedWrappedDEK
	}
	derivedPublic, err := PublicFromPrivate(privateKey)
	if err != nil {
		return nil, err
	}
	var ownPublic, ownPrivate [32]byte
	copy(ownPublic[:], derivedPublic)
	copy(ownPrivate[:], privateKey)
	dek, ok := box.OpenAnonymous(nil, wrappedDEK, &ownPublic, &ownPrivate)
	if !ok {
		return nil, ErrUnwrap
	}
	return dek, nil
}
