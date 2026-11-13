package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"regexp"
)

var (
	// A S256 challenge is base64url(SHA-256) without padding: 43 characters.
	challengeRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	// RFC 7636 §4.1 code_verifier.
	verifierRE = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)
)

func validChallenge(c string) bool { return challengeRE.MatchString(c) }

func validVerifier(v string) bool { return verifierRE.MatchString(v) }

// verifyPKCE reports whether verifier hashes to the S256 challenge.
func verifyPKCE(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}
