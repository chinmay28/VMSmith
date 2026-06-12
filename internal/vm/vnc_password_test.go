package vm

import (
	"strings"
	"testing"
)

func TestVNCPasswordEncryptDecryptRoundTrip(t *testing.T) {
	blob, err := encryptVNCPassword("master-key", "s3cret!")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(blob, "s3cret!") {
		t.Fatal("ciphertext contains plaintext")
	}

	plain, err := decryptVNCPassword("master-key", blob)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "s3cret!" {
		t.Errorf("round-trip = %q, want %q", plain, "s3cret!")
	}
}

func TestVNCPasswordDecryptWrongKeyFails(t *testing.T) {
	blob, err := encryptVNCPassword("key-a", "pw")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := decryptVNCPassword("key-b", blob); err == nil {
		t.Error("decrypt with wrong key succeeded; GCM auth should fail")
	}
}

func TestVNCPasswordEncryptUniqueNonce(t *testing.T) {
	a, _ := encryptVNCPassword("k", "pw")
	b, _ := encryptVNCPassword("k", "pw")
	if a == b {
		t.Error("two encryptions produced identical blobs; nonce reuse")
	}
}

func TestVNCPasswordMissingKey(t *testing.T) {
	if _, err := encryptVNCPassword("", "pw"); err == nil {
		t.Error("encrypt with empty key succeeded")
	}
	if _, err := encryptVNCPassword("   ", "pw"); err == nil {
		t.Error("encrypt with blank key succeeded")
	}
	if _, err := decryptVNCPassword("", "blob"); err == nil {
		t.Error("decrypt with empty key succeeded")
	}
}

func TestVNCPasswordDecryptGarbage(t *testing.T) {
	if _, err := decryptVNCPassword("k", "not-base64!!"); err == nil {
		t.Error("decrypt of invalid base64 succeeded")
	}
	if _, err := decryptVNCPassword("k", "QUJD"); err == nil { // "ABC": shorter than a nonce
		t.Error("decrypt of too-short blob succeeded")
	}
}

func TestVNCPasswordHashVerify(t *testing.T) {
	hash, err := hashVNCPassword("hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "hunter2" || !strings.HasPrefix(hash, "$2") {
		t.Errorf("hash does not look like bcrypt: %q", hash)
	}
	if !verifyVNCPassword(hash, "hunter2") {
		t.Error("verify rejected the correct password")
	}
	if verifyVNCPassword(hash, "hunter3") {
		t.Error("verify accepted the wrong password")
	}
}

// TestGenerateDomainXML_VNCPassword asserts the passwd attribute renders
// XML-escaped and is omitted entirely when unset.
func TestGenerateDomainXML_VNCPassword(t *testing.T) {
	params := DomainParams{Name: "v", CPUs: 1, RAMMB: 512, DiskPath: "/d.qcow2"}
	params.SetVNCPassword(`a'<&>"z`)

	xml, err := GenerateDomainXML(params)
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	want := "passwd='a&apos;&lt;&amp;&gt;&quot;z'"
	if !strings.Contains(xml, want) {
		t.Errorf("XML missing escaped passwd attr %q:\n%s", want, xml)
	}

	plain, err := GenerateDomainXML(DomainParams{Name: "v", CPUs: 1, RAMMB: 512, DiskPath: "/d.qcow2"})
	if err != nil {
		t.Fatalf("GenerateDomainXML: %v", err)
	}
	if strings.Contains(plain, "passwd=") {
		t.Error("passwd attribute rendered without a password")
	}
}
