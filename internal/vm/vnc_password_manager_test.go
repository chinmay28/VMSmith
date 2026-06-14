package vm

import (
	"errors"
	"testing"

	"github.com/vmsmith/vmsmith/internal/config"
	"github.com/vmsmith/vmsmith/pkg/types"
)

func TestDeriveVNCPassword(t *testing.T) {
	mgr := &LibvirtManager{cfg: &config.Config{}}

	t.Run("missing key returns typed api error", func(t *testing.T) {
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
		if !verifyVNCPassword(hash, "hunter2") {
			t.Fatal("verifyVNCPassword rejected derived hash")
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
