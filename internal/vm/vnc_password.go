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

var errVNCPasswordKeyMissing = errors.New("daemon.console.password_key is not configured")

func hashVNCPassword(password string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing vnc password: %w", err)
	}
	return string(h), nil
}

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
