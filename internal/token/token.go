// Package token issues and checks the server's secrets
// (docs/design.md#tokens-and-secrets):
//
//   - lookup tokens (sessions, site tickets, OAuth codes and tokens, invites,
//     transfers, public links, email verification), stored as SHA-256;
//   - recovery codes, stored as argon2id beside a keyed lookup hash;
//   - HMAC-signed values (site cookies, upload grants), not stored.
package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Purposes of keys derived with DeriveKey outside the Signer.
const (
	PurposeRecoveryCode = "recovery-code" // RecoveryCodeLookup
	PurposePasskeyDecoy = "passkey-decoy" // decoy credential IDs (internal/auth)
)

// DeriveKey returns the HMAC-SHA256 key for purpose derived from secret
// (config.Config.SecretKey), so each purpose has its own key. Rotating
// SECRET_KEY changes every derived key.
func DeriveKey(secret []byte, purpose string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte("handbill/token/v1/" + purpose))
	return m.Sum(nil)
}

// New returns a fresh lookup token: 32 random bytes, base64url without
// padding, and its Hash. Store only the hash.
func New() (plaintext string, hash []byte) {
	var b [32]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error.
	plaintext = base64.RawURLEncoding.EncodeToString(b[:])
	return plaintext, Hash(plaintext)
}

// Hash returns the SHA-256 of a presented lookup token, for lookup by hash.
func Hash(plaintext string) []byte {
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

// Recovery codes are 80 random bits as 16 characters of RFC 4648 base32.
var recoveryEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

const recoveryLen = 16

// argon2id parameters: the OWASP minimum (19 MiB, 2 passes, 1 lane). The
// codes carry 80 random bits, so this mainly slows offline guessing of a
// leaked database; a sign-in verifies one code, found by its lookup hash.
const (
	argonMemory  = 19 * 1024 // KiB
	argonTime    = 2
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// NewRecoveryCode returns a fresh recovery code formatted for display as
// XXXX-XXXX-XXXX-XXXX.
func NewRecoveryCode() string {
	var b [10]byte
	_, _ = rand.Read(b[:])
	s := recoveryEncoding.EncodeToString(b[:])
	return s[0:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:16]
}

// normalizeRecoveryCode uppercases a typed code and drops dashes and
// whitespace; ok is false unless 16 base32 characters remain.
func normalizeRecoveryCode(code string) (string, bool) {
	var b strings.Builder
	b.Grow(recoveryLen)
	for _, r := range code {
		switch {
		case r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r':
			continue
		case r >= 'a' && r <= 'z':
			r -= 'a' - 'A'
		}
		if !(r >= 'A' && r <= 'Z' || r >= '2' && r <= '7') {
			return "", false
		}
		b.WriteRune(r)
	}
	if b.Len() != recoveryLen {
		return "", false
	}
	return b.String(), true
}

// HashRecoveryCode returns the PHC-encoded argon2id hash of a recovery code
// ($argon2id$v=19$m=…,t=…,p=…$salt$hash). Case, dashes, and whitespace do
// not matter.
func HashRecoveryCode(code string) (string, error) {
	norm, ok := normalizeRecoveryCode(code)
	if !ok {
		return "", fmt.Errorf("token: malformed recovery code")
	}
	salt := make([]byte, argonSaltLen)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(norm), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// RecoveryCodeLookup returns the lookup hash of a recovery code: the
// HMAC-SHA256 of its normalized form under key (DeriveKey with
// PurposeRecoveryCode). Stored unique beside the argon2id hash, it lets a
// sign-in find the one code to verify. ok is false for a malformed code.
func RecoveryCodeLookup(key []byte, code string) (hash []byte, ok bool) {
	norm, ok := normalizeRecoveryCode(code)
	if !ok {
		return nil, false
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(norm))
	return m.Sum(nil), true
}

// VerifyRecoveryCode reports whether code matches a HashRecoveryCode result.
// Case, dashes, and whitespace in code do not matter.
func VerifyRecoveryCode(encoded, code string) bool {
	norm, ok := normalizeRecoveryCode(code)
	if !ok {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil ||
		memory == 0 || passes == 0 || threads == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(norm), salt, passes, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
