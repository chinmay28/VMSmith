package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSign_MatchesReferenceHMAC(t *testing.T) {
	const secret = "topsecret"
	body := []byte(`{"id":"42","type":"vm.started"}`)

	got := Sign(secret, body)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	if got != want {
		t.Fatalf("Sign() = %q, want %q", got, want)
	}
}

func TestSign_StableForSameInput(t *testing.T) {
	a := Sign("k", []byte("hello"))
	b := Sign("k", []byte("hello"))
	if a != b {
		t.Fatalf("Sign() not stable: %q vs %q", a, b)
	}
}

func TestSign_DiffersOnDifferentSecret(t *testing.T) {
	a := Sign("k1", []byte("body"))
	b := Sign("k2", []byte("body"))
	if a == b {
		t.Fatalf("Sign() should differ across secrets, both = %q", a)
	}
}
