package oauth

import "time"

// Lifetimes of OAuth artifacts (docs/design.md#tokens-and-secrets).
const (
	requestTTL      = 10 * time.Minute    // pending authorization request
	codeTTL         = 2 * time.Minute     // authorization code
	accessTokenTTL  = time.Hour           // access token
	refreshTokenTTL = 30 * 24 * time.Hour // refresh token, renewed by rotation

	// CIMD documents are cached per their Cache-Control, clamped to
	// [cimdMinTTL, cimdMaxTTL]; cimdDefaultTTL applies without max-age.
	cimdMinTTL     = 5 * time.Minute
	cimdMaxTTL     = 24 * time.Hour
	cimdDefaultTTL = time.Hour
)

// Size limits.
const (
	maxClientIDLen    = 2048
	maxRedirectURILen = 2048
	maxStateLen       = 1024
	maxFormBytes      = 16 << 10
	maxCIMDBytes      = 5 << 10
	maxRedirectURIs   = 20
	maxClientNameLen  = 100 // runes, after removing control characters
)

// offlineAccess is the only scope recorded; it has no effect.
const offlineAccess = "offline_access"
