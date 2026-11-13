// Package siteid generates and validates site ids: 12 characters of
// lowercase base32 (a-z, 2-7), about 60 bits of randomness. A site id is a
// single DNS label under SITES_DOMAIN and is not a secret.
package siteid

import (
	"crypto/rand"
	"encoding/base32"
)

// Len is the length of a site id.
const Len = 12

var encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// New returns a random site id.
func New() string {
	// 8 random bytes encode to 13 base32 characters; the first 12 carry 60 bits.
	var b [8]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error.
	return encoding.EncodeToString(b[:])[:Len]
}

// Valid reports whether s has the form of a site id.
func Valid(s string) bool {
	if len(s) != Len {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}
