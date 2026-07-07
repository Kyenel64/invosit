package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kyenel64/invosit/cli/internal/apiclient"
	"github.com/kyenel64/invosit/cli/internal/keys"
	"github.com/kyenel64/invosit/cli/internal/keystore"
)

func newKeyStore(t *testing.T) *keystore.FileStore {
	t.Helper()
	store, err := keystore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

// publicKeyServer records registered public keys and mimics the API's
// set-or-409 behavior.
func publicKeyServer(t *testing.T) (*httptest.Server, *[][]byte) {
	t.Helper()
	var registered [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			PublicKey []byte `json:"public_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(registered) > 0 && !bytes.Equal(registered[len(registered)-1], req.PublicKey) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		registered = append(registered, req.PublicKey)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, &registered
}

func TestEnsureKeypairRegistered_FirstLoginGeneratesAndRegisters(t *testing.T) {
	keyStore := newKeyStore(t)
	srv, registered := publicKeyServer(t)

	var stderr bytes.Buffer
	err := ensureKeypairRegistered(context.Background(), keyStore, apiclient.NewClient(srv.URL), "tok", "usr_abc", &stderr)
	if err != nil {
		t.Fatalf("ensureKeypairRegistered: %v", err)
	}

	privateKey, err := keyStore.Load("usr_abc")
	if err != nil {
		t.Fatalf("Load after first login: %v", err)
	}
	derived, err := keys.PublicFromPrivate(privateKey)
	if err != nil {
		t.Fatalf("PublicFromPrivate: %v", err)
	}
	if len(*registered) != 1 || !bytes.Equal((*registered)[0], derived) {
		t.Errorf("registered keys = %d, want exactly the derived public key", len(*registered))
	}
	if stderr.Len() != 0 {
		t.Errorf("unexpected warning: %s", stderr.String())
	}
}

func TestEnsureKeypairRegistered_SecondLoginReusesKey(t *testing.T) {
	keyStore := newKeyStore(t)
	srv, registered := publicKeyServer(t)
	client := apiclient.NewClient(srv.URL)

	var stderr bytes.Buffer
	if err := ensureKeypairRegistered(context.Background(), keyStore, client, "tok", "usr_abc", &stderr); err != nil {
		t.Fatalf("first login: %v", err)
	}
	first, err := keyStore.Load("usr_abc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := ensureKeypairRegistered(context.Background(), keyStore, client, "tok", "usr_abc", &stderr); err != nil {
		t.Fatalf("second login: %v", err)
	}
	second, err := keyStore.Load("usr_abc")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !bytes.Equal(first, second) {
		t.Error("second login regenerated the keypair")
	}
	if len(*registered) != 2 {
		t.Errorf("PUT count = %d, want 2 (registration repeats idempotently)", len(*registered))
	}
	if !bytes.Equal((*registered)[0], (*registered)[1]) {
		t.Error("second login registered a different public key")
	}
}

func TestEnsureKeypairRegistered_MismatchWarnsAndSucceeds(t *testing.T) {
	keyStore := newKeyStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	t.Cleanup(srv.Close)

	var stderr bytes.Buffer
	err := ensureKeypairRegistered(context.Background(), keyStore, apiclient.NewClient(srv.URL), "tok", "usr_abc", &stderr)
	if err != nil {
		t.Fatalf("mismatch should not fail login: %v", err)
	}
	if !strings.Contains(stderr.String(), "different public key") {
		t.Errorf("stderr = %q, want mismatch warning", stderr.String())
	}
	if _, err := keyStore.Load("usr_abc"); err != nil {
		t.Errorf("local keypair should be kept on mismatch: %v", err)
	}
}

func TestEnsureKeypairRegistered_RegistrationFailureFails(t *testing.T) {
	keyStore := newKeyStore(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	var stderr bytes.Buffer
	err := ensureKeypairRegistered(context.Background(), keyStore, apiclient.NewClient(srv.URL), "tok", "usr_abc", &stderr)
	if err == nil {
		t.Fatal("registration failure should fail login")
	}
	if !strings.Contains(err.Error(), "failed to register public key") {
		t.Errorf("err = %v, want failed-to-register wrap", err)
	}
}

// The server body carries the key base64-encoded — sanity-check the wire shape
// the API's []byte bind expects.
func TestEnsureKeypairRegistered_SendsBase64(t *testing.T) {
	keyStore := newKeyStore(t)
	var rawBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(r.Body)
		rawBody = buf.String()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	var stderr bytes.Buffer
	if err := ensureKeypairRegistered(context.Background(), keyStore, apiclient.NewClient(srv.URL), "tok", "usr_abc", &stderr); err != nil {
		t.Fatalf("ensureKeypairRegistered: %v", err)
	}

	var req struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.Unmarshal([]byte(rawBody), &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(req.PublicKey)
	if err != nil {
		t.Fatalf("public_key not base64: %v", err)
	}
	if len(decoded) != keys.PublicKeySize {
		t.Errorf("decoded key length = %d, want %d", len(decoded), keys.PublicKeySize)
	}
}
