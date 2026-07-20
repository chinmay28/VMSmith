package vm

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

// TestDeriveVNCPassword covers the manager-level derivation entry point
// (roadmap 5.1.8): the typed vnc_password_key_missing error on a daemon
// with no daemon.console.password_key, and the happy path producing a
// verifiable bcrypt hash plus a decryptable AES-GCM blob without leaking
// the plaintext. The helper-level primitives are covered separately in
// vnc_password_test.go; this test locks the manager wiring and the typed
// error contract (errors.As) the API layer depends on for its 422 mapping.
func TestDeriveVNCPassword(t *testing.T) {
	// Each subtest builds its own manager so the key-missing and happy
	// paths cannot couple through shared mutable config.
	t.Run("missing key returns typed api error", func(t *testing.T) {
		mgr := &LibvirtManager{cfg: &config.Config{}}
		_, _, err := mgr.deriveVNCPassword("secret")
		if err == nil {
			t.Fatal("expected error")
		}
		var apiErr *types.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %T %v, want *types.APIError", err, err)
		}
		if apiErr.Code != "vnc_password_key_missing" {
			t.Fatalf("code = %q, want vnc_password_key_missing", apiErr.Code)
		}
	})

	t.Run("hashes and encrypts without leaking plaintext", func(t *testing.T) {
		mgr := &LibvirtManager{cfg: &config.Config{}}
		mgr.cfg.Daemon.Console.PasswordKey = "unit-test-master-key"
		hash, enc, err := mgr.deriveVNCPassword("hunter2")
		if err != nil {
			t.Fatalf("deriveVNCPassword: %v", err)
		}
		if hash == "" || enc == "" {
			t.Fatalf("deriveVNCPassword returned empty artifacts: hash=%q enc=%q", hash, enc)
		}
		if hash == "hunter2" || enc == "hunter2" {
			t.Fatal("plaintext leaked into derived artifacts")
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte("hunter2")) != nil {
			t.Fatal("derived hash does not verify against the source password")
		}
		plain, err := decryptVNCPassword(mgr.cfg.Daemon.Console.PasswordKey, enc)
		if err != nil {
			t.Fatalf("decryptVNCPassword: %v", err)
		}
		if plain != "hunter2" {
			t.Fatalf("decrypted plaintext = %q, want hunter2", plain)
		}
	})
}
