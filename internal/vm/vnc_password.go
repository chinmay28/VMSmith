package vm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/vmsmith/vmsmith/pkg/types"
	"golang.org/x/crypto/bcrypt"
)

func (m *LibvirtManager) deriveVNCPassword(plain string) (hash string, encrypted string, err error) {
	key := strings.TrimSpace(m.cfg.Daemon.Console.PasswordKey)
	if key == "" {
		return "", "", types.NewAPIError("vnc_password_key_missing", "daemon.console.password_key must be configured before VNC passwords can be used")
	}
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", "", fmt.Errorf("hash vnc password: %w", err)
	}
	encrypted, err = encryptVNCPassword(key, plain)
	if err != nil {
		return "", "", err
	}
	return string(hashBytes), encrypted, nil
}

func verifyVNCPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

func encryptVNCPassword(masterKey, plain string) (string, error) {
	block, err := aes.NewCipher(deriveVNCPasswordKey(masterKey))
	if err != nil {
		return "", fmt.Errorf("init vnc password cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init vnc password gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate vnc password nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), nil)
	blob := append(nonce, sealed...)
	return base64.StdEncoding.EncodeToString(blob), nil
}

func decryptVNCPassword(masterKey, encoded string) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode vnc password blob: %w", err)
	}
	block, err := aes.NewCipher(deriveVNCPasswordKey(masterKey))
	if err != nil {
		return "", fmt.Errorf("init vnc password cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("init vnc password gcm: %w", err)
	}
	if len(blob) < gcm.NonceSize() {
		return "", fmt.Errorf("decode vnc password blob: ciphertext too short")
	}
	nonce := blob[:gcm.NonceSize()]
	ciphertext := blob[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt vnc password blob: %w", err)
	}
	return string(plain), nil
}

func deriveVNCPasswordKey(masterKey string) []byte {
	sum := sha256.Sum256([]byte(masterKey))
	return sum[:]
}
