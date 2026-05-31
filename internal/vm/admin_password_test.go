package vm

import (
	"strings"
	"testing"
)

func TestGenerateAdminPassword_Length(t *testing.T) {
	pw, err := generateAdminPassword()
	if err != nil {
		t.Fatalf("generateAdminPassword: %v", err)
	}
	if len(pw) != generatedAdminPasswordLength {
		t.Fatalf("expected length %d, got %d (%q)", generatedAdminPasswordLength, len(pw), pw)
	}
}

func TestGenerateAdminPassword_SatisfiesComplexity(t *testing.T) {
	// Run 50 iterations so we catch shuffle outcomes that bury one class.
	for i := 0; i < 50; i++ {
		pw, err := generateAdminPassword()
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !strings.ContainsAny(pw, string(adminPasswordUpper)) {
			t.Errorf("iteration %d: %q has no uppercase", i, pw)
		}
		if !strings.ContainsAny(pw, string(adminPasswordLower)) {
			t.Errorf("iteration %d: %q has no lowercase", i, pw)
		}
		if !strings.ContainsAny(pw, string(adminPasswordDigits)) {
			t.Errorf("iteration %d: %q has no digit", i, pw)
		}
		if !strings.ContainsAny(pw, string(adminPasswordSymbols)) {
			t.Errorf("iteration %d: %q has no symbol", i, pw)
		}
	}
}

func TestGenerateAdminPassword_Uniqueness(t *testing.T) {
	// crypto/rand can technically collide, but with 20 chars from the union
	// alphabet that's ~118 bits of entropy — collisions in 100 iterations
	// would indicate a broken generator, not bad luck.
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		pw, err := generateAdminPassword()
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if _, dup := seen[pw]; dup {
			t.Fatalf("duplicate password generated on iteration %d: %q", i, pw)
		}
		seen[pw] = struct{}{}
	}
}

func TestGenerateAdminPasswordN_RejectsTooShort(t *testing.T) {
	if _, err := generateAdminPasswordN(3); err == nil {
		t.Fatal("expected error for length 3, got nil")
	}
}

func TestGenerateAdminPasswordN_MinLengthOK(t *testing.T) {
	// Exactly 4 chars: one from each class, no filler. Should always satisfy
	// the complexity policy.
	pw, err := generateAdminPasswordN(4)
	if err != nil {
		t.Fatalf("generateAdminPasswordN(4): %v", err)
	}
	if len(pw) != 4 {
		t.Fatalf("expected length 4, got %d", len(pw))
	}
	for _, class := range [][]byte{adminPasswordUpper, adminPasswordLower, adminPasswordDigits, adminPasswordSymbols} {
		if !strings.ContainsAny(pw, string(class)) {
			t.Fatalf("password %q missing a complexity class", pw)
		}
	}
}
