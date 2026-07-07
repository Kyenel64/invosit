package keys_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/keys"
)

const wrappedOverhead = 48

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return buf
}

func mustGenerate(t *testing.T) keys.Keypair {
	t.Helper()
	keypair, err := keys.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return keypair
}

func mustWrap(t *testing.T, dek, publicKey []byte) []byte {
	t.Helper()
	wrapped, err := keys.Wrap(dek, publicKey)
	if err != nil {
		t.Fatalf("Wrap: %v", err)
	}
	return wrapped
}

func TestGenerateProducesDistinctKeys(t *testing.T) {
	first := mustGenerate(t)
	second := mustGenerate(t)

	if len(first.Public) != keys.PublicKeySize || len(first.Private) != keys.PrivateKeySize {
		t.Fatalf("key sizes = pub %d priv %d, want %d/%d",
			len(first.Public), len(first.Private), keys.PublicKeySize, keys.PrivateKeySize)
	}
	if bytes.Equal(first.Public, second.Public) {
		t.Error("public key reused across calls")
	}
	if bytes.Equal(first.Private, second.Private) {
		t.Error("private key reused across calls")
	}
}

func TestWrapUnwrapRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		dek  []byte
	}{
		{name: "real dek", dek: randomBytes(t, 32)},
		{name: "empty", dek: []byte{}},
		{name: "single byte", dek: []byte{0x7f}},
		{name: "binary with null bytes", dek: []byte{0x00, 0xff, 0x00, 0x10, 0x00}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keypair := mustGenerate(t)
			wrapped := mustWrap(t, tc.dek, keypair.Public)

			out, err := keys.Unwrap(wrapped, keypair.Private)
			if err != nil {
				t.Fatalf("Unwrap: %v", err)
			}
			if !bytes.Equal(out, tc.dek) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d", len(out), len(tc.dek))
			}
		})
	}
}

func TestWrapNonDeterministic(t *testing.T) {
	keypair := mustGenerate(t)
	dek := randomBytes(t, 32)

	first := mustWrap(t, dek, keypair.Public)
	second := mustWrap(t, dek, keypair.Public)

	if bytes.Equal(first, second) {
		t.Error("wrap is deterministic (ephemeral sender key reused)")
	}
	for _, wrapped := range [][]byte{first, second} {
		out, err := keys.Unwrap(wrapped, keypair.Private)
		if err != nil {
			t.Fatalf("Unwrap: %v", err)
		}
		if !bytes.Equal(out, dek) {
			t.Error("non-deterministic wraps did not both unwrap to the dek")
		}
	}
}

func TestWrapOutputShape(t *testing.T) {
	keypair := mustGenerate(t)
	dek := randomBytes(t, 32)

	wrapped := mustWrap(t, dek, keypair.Public)

	want := len(dek) + wrappedOverhead
	if len(wrapped) != want {
		t.Errorf("wrapped length = %d, want %d (dek %d + overhead %d)",
			len(wrapped), want, len(dek), wrappedOverhead)
	}
}

func TestPublicFromPrivateMatchesGenerated(t *testing.T) {
	keypair := mustGenerate(t)

	derived, err := keys.PublicFromPrivate(keypair.Private)
	if err != nil {
		t.Fatalf("PublicFromPrivate: %v", err)
	}
	if !bytes.Equal(derived, keypair.Public) {
		t.Error("derived public key differs from the generated one")
	}

	dek := randomBytes(t, 32)
	wrapped := mustWrap(t, dek, derived)
	out, err := keys.Unwrap(wrapped, keypair.Private)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(out, dek) {
		t.Error("wrap to derived public key did not unwrap")
	}
}

func TestPublicFromPrivateInvalidLength(t *testing.T) {
	for _, size := range []int{0, 16, 31, 33} {
		_, err := keys.PublicFromPrivate(make([]byte, size))
		if !errors.Is(err, keys.ErrInvalidPrivateKey) {
			t.Errorf("private key size %d: want ErrInvalidPrivateKey, got %v", size, err)
		}
	}
}

func TestUnwrapWrongKeyFails(t *testing.T) {
	recipient := mustGenerate(t)
	stranger := mustGenerate(t)

	wrapped := mustWrap(t, randomBytes(t, 32), recipient.Public)

	_, err := keys.Unwrap(wrapped, stranger.Private)
	if !errors.Is(err, keys.ErrUnwrap) {
		t.Errorf("want ErrUnwrap, got %v", err)
	}
}

func TestUnwrapTamperedFails(t *testing.T) {
	keypair := mustGenerate(t)
	dek := randomBytes(t, 32)

	cases := []struct {
		name  string
		index func(wrapped []byte) int
	}{
		{name: "ephemeral key", index: func(_ []byte) int { return 0 }},
		{name: "body", index: func(wrapped []byte) int { return len(wrapped) / 2 }},
		{name: "tag", index: func(wrapped []byte) int { return len(wrapped) - 1 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := mustWrap(t, dek, keypair.Public)
			tampered := bytes.Clone(wrapped)
			i := tc.index(tampered)
			tampered[i] ^= 0xff

			_, err := keys.Unwrap(tampered, keypair.Private)
			if !errors.Is(err, keys.ErrUnwrap) {
				t.Errorf("want ErrUnwrap, got %v", err)
			}
		})
	}
}

func TestWrapInvalidPublicKey(t *testing.T) {
	dek := randomBytes(t, 32)

	for _, size := range []int{0, 16, 31, 33} {
		_, err := keys.Wrap(dek, make([]byte, size))
		if !errors.Is(err, keys.ErrInvalidPublicKey) {
			t.Errorf("public key size %d: want ErrInvalidPublicKey, got %v", size, err)
		}
	}
}

func TestUnwrapInvalidPrivateKey(t *testing.T) {
	keypair := mustGenerate(t)
	wrapped := mustWrap(t, randomBytes(t, 32), keypair.Public)

	for _, size := range []int{0, 16, 31, 33} {
		_, err := keys.Unwrap(wrapped, make([]byte, size))
		if !errors.Is(err, keys.ErrInvalidPrivateKey) {
			t.Errorf("private key size %d: want ErrInvalidPrivateKey, got %v", size, err)
		}
	}
}

func TestUnwrapShortWrappedDEK(t *testing.T) {
	keypair := mustGenerate(t)

	for _, size := range []int{0, wrappedOverhead - 1} {
		_, err := keys.Unwrap(make([]byte, size), keypair.Private)
		if !errors.Is(err, keys.ErrMalformedWrappedDEK) {
			t.Errorf("wrapped size %d: want ErrMalformedWrappedDEK, got %v", size, err)
		}
	}
}
