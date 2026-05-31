package vm

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// generatedAdminPasswordLength is the length of an auto-generated Windows
// Administrator password. Windows accepts 14-127 characters when complexity is
// enabled; 20 gives ~118 bits of entropy from the alphabet below and is well
// above the floor.
const generatedAdminPasswordLength = 20

// adminPasswordAlphabets define the four Windows complexity classes
// (uppercase / lowercase / digit / symbol). The symbol set excludes characters
// that get mangled in shell pipelines (`$`, “ ` “, `\`, `"`, `'`, space).
var (
	adminPasswordUpper   = []byte("ABCDEFGHJKLMNPQRSTUVWXYZ")
	adminPasswordLower   = []byte("abcdefghijkmnopqrstuvwxyz")
	adminPasswordDigits  = []byte("23456789")
	adminPasswordSymbols = []byte("!@#%^&*()-_=+[]{};:,.<>?")
)

// adminPasswordAllAlphabets is the union of every complexity class, used for
// the "filler" characters once each class is satisfied.
var adminPasswordAllAlphabets = func() []byte {
	out := make([]byte, 0, len(adminPasswordUpper)+len(adminPasswordLower)+len(adminPasswordDigits)+len(adminPasswordSymbols))
	out = append(out, adminPasswordUpper...)
	out = append(out, adminPasswordLower...)
	out = append(out, adminPasswordDigits...)
	out = append(out, adminPasswordSymbols...)
	return out
}()

// generateAdminPassword returns a cryptographically random Windows
// Administrator password that satisfies the default Windows complexity policy
// (at least one uppercase, lowercase, digit, and symbol). The result is
// generatedAdminPasswordLength bytes long.
func generateAdminPassword() (string, error) {
	return generateAdminPasswordN(generatedAdminPasswordLength)
}

// generateAdminPasswordN is the testable form of generateAdminPassword that
// lets the caller pick the password length. The length must be >= 4 so we can
// guarantee one character from each complexity class.
func generateAdminPasswordN(length int) (string, error) {
	if length < 4 {
		return "", fmt.Errorf("admin password length must be >= 4, got %d", length)
	}
	out := make([]byte, length)
	// Seed one character per complexity class so the result always satisfies
	// the Windows password policy regardless of how the shuffler lands.
	classes := [][]byte{adminPasswordUpper, adminPasswordLower, adminPasswordDigits, adminPasswordSymbols}
	for i, class := range classes {
		ch, err := randomByteFrom(class)
		if err != nil {
			return "", err
		}
		out[i] = ch
	}
	for i := len(classes); i < length; i++ {
		ch, err := randomByteFrom(adminPasswordAllAlphabets)
		if err != nil {
			return "", err
		}
		out[i] = ch
	}
	// Fisher-Yates shuffle so the four seeded class characters aren't always
	// at the start of the password.
	for i := length - 1; i > 0; i-- {
		j, err := randomIndex(i + 1)
		if err != nil {
			return "", err
		}
		out[i], out[j] = out[j], out[i]
	}
	return string(out), nil
}

func randomByteFrom(alphabet []byte) (byte, error) {
	idx, err := randomIndex(len(alphabet))
	if err != nil {
		return 0, err
	}
	return alphabet[idx], nil
}

func randomIndex(n int) (int, error) {
	if n <= 0 {
		return 0, fmt.Errorf("random index requires n > 0, got %d", n)
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, fmt.Errorf("crypto/rand failure: %w", err)
	}
	return int(v.Int64()), nil
}
