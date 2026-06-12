package vm

import (
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// VNC password persistence (roadmap 5.1.8).
//
// The operator-supplied password is stored in two derived forms:
//   - a bcrypt hash for verification without decryption, and
//   - an AES-GCM blob (key = SHA-256 of daemon.console.password_key) that
//     the manager decrypts when re-rendering domain XML, so the libvirt
//     `passwd=` attribute survives CPU/RAM redefines without the daemon
//     ever holding the plaintext at rest.
//
// libvirt/QEMU truncate VNC passwords to 8 significant characters; we cap
// the accepted length well above that (64) only to bound bcrypt input.

var errVNCPasswordKeyMissing = errors.New("daemon.console.password_key is not configured")

// hashVNCPassword returns the bcrypt hash of the password.
func hashVNCPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing vnc password: %w", err)
	}
	return string(h), nil
}

// verifyVNCPassword reports whether the password matches the bcrypt hash.
func verifyVNCPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func vncPasswordAEAD(passwordKey string) (cipher.AEAD, error) {
	if strings.TrimSpace(passwordKey) == "" {
		return nil, errVNCPasswordKeyMissing
	}
	key := sha256.Sum256([]byte(passwordKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// encryptVNCPassword seals the password with AES-GCM under a key derived
// from the daemon's console password key. Output is base64(nonce||cipher).
func encryptVNCPassword(passwordKey, password string) (string, error) {
	aead, err := vncPasswordAEAD(passwordKey)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := crand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(password), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptVNCPassword reverses encryptVNCPassword. A wrong key or a
// tampered blob fails GCM authentication and returns an error.
func decryptVNCPassword(passwordKey, blob string) (string, error) {
	aead, err := vncPasswordAEAD(passwordKey)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return "", fmt.Errorf("decoding vnc password blob: %w", err)
	}
	if len(raw) < aead.NonceSize() {
		return "", errors.New("vnc password blob too short")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypting vnc password: %w", err)
	}
	return string(plain), nil
}
