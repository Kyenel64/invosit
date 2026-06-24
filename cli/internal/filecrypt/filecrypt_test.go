package filecrypt_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/filecrypt"
)

const tagSize = 16

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return buf
}

func mustEncrypt(t *testing.T, plaintext []byte) (dek, ciphertext []byte) {
	t.Helper()
	dek, ciphertext, err := filecrypt.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	return dek, ciphertext
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	cases := []struct {
		name      string
		plaintext []byte
	}{
		{name: "empty", plaintext: []byte{}},
		{name: "small text", plaintext: []byte("KEY=value\n")},
		{name: "binary with null bytes", plaintext: []byte{0x00, 0xff, 0x00, 0x10, 0x00}},
		{name: "one mib random", plaintext: randomBytes(t, 1<<20)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dek, ciphertext := mustEncrypt(t, tc.plaintext)

			out, err := filecrypt.Decrypt(dek, ciphertext)
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if !bytes.Equal(out, tc.plaintext) {
				t.Errorf("round-trip mismatch: got %d bytes, want %d", len(out), len(tc.plaintext))
			}
		})
	}
}

func TestEncryptOutputShape(t *testing.T) {
	plaintext := []byte("the quick brown fox")
	dek, ciphertext := mustEncrypt(t, plaintext)

	if len(dek) != filecrypt.DEKSize {
		t.Errorf("dek length = %d, want %d", len(dek), filecrypt.DEKSize)
	}
	want := len(plaintext) + filecrypt.NonceSize + tagSize
	if len(ciphertext) != want {
		t.Errorf("ciphertext length = %d, want %d (nonce %d + body %d + tag %d)",
			len(ciphertext), want, filecrypt.NonceSize, len(plaintext), tagSize)
	}
}

func TestEncryptUniquePerCall(t *testing.T) {
	plaintext := []byte("same plaintext both times")
	dek1, ciphertext1 := mustEncrypt(t, plaintext)
	dek2, ciphertext2 := mustEncrypt(t, plaintext)

	if bytes.Equal(dek1, dek2) {
		t.Error("DEK reused across calls")
	}
	if bytes.Equal(ciphertext1, ciphertext2) {
		t.Error("ciphertext identical across calls (nonce reuse)")
	}
	nonce1 := ciphertext1[:filecrypt.NonceSize]
	nonce2 := ciphertext2[:filecrypt.NonceSize]
	if bytes.Equal(nonce1, nonce2) {
		t.Error("nonce reused across calls")
	}
}

func TestDecryptTamperedFails(t *testing.T) {
	plaintext := []byte("authenticated payload")
	bodyStart := filecrypt.NonceSize

	cases := []struct {
		name  string
		index func(ciphertext []byte) int
	}{
		{name: "nonce", index: func(_ []byte) int { return 0 }},
		{name: "body", index: func(_ []byte) int { return bodyStart + len(plaintext)/2 }},
		{name: "tag", index: func(ciphertext []byte) int { return len(ciphertext) - 1 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dek, ciphertext := mustEncrypt(t, plaintext)
			tampered := bytes.Clone(ciphertext)
			i := tc.index(tampered)
			tampered[i] ^= 0xff

			_, err := filecrypt.Decrypt(dek, tampered)
			if !errors.Is(err, filecrypt.ErrDecrypt) {
				t.Errorf("want ErrDecrypt, got %v", err)
			}
		})
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	_, ciphertext := mustEncrypt(t, []byte("secret"))
	wrongDEK := randomBytes(t, filecrypt.DEKSize)

	_, err := filecrypt.Decrypt(wrongDEK, ciphertext)
	if !errors.Is(err, filecrypt.ErrDecrypt) {
		t.Errorf("want ErrDecrypt, got %v", err)
	}
}

func TestDecryptInvalidDEK(t *testing.T) {
	_, ciphertext := mustEncrypt(t, []byte("secret"))

	for _, size := range []int{0, 16, 31, 33} {
		_, err := filecrypt.Decrypt(make([]byte, size), ciphertext)
		if !errors.Is(err, filecrypt.ErrInvalidDEK) {
			t.Errorf("dek size %d: want ErrInvalidDEK, got %v", size, err)
		}
	}
}

func TestDecryptShortCiphertext(t *testing.T) {
	dek := randomBytes(t, filecrypt.DEKSize)

	for _, size := range []int{0, filecrypt.NonceSize - 1} {
		_, err := filecrypt.Decrypt(dek, make([]byte, size))
		if !errors.Is(err, filecrypt.ErrMalformedCiphertext) {
			t.Errorf("ciphertext size %d: want ErrMalformedCiphertext, got %v", size, err)
		}
	}
}
