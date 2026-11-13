package token

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		p, h := New()
		b, err := base64.RawURLEncoding.Strict().DecodeString(p)
		if err != nil || len(b) != 32 {
			t.Fatalf("New() plaintext %q: want 32 bytes base64url, got %d bytes, err %v", p, len(b), err)
		}
		sum := sha256.Sum256([]byte(p))
		if !bytes.Equal(h, sum[:]) || !bytes.Equal(Hash(p), h) {
			t.Fatalf("hash mismatch for %q", p)
		}
		if seen[p] {
			t.Fatalf("duplicate token %q", p)
		}
		seen[p] = true
	}
	if bytes.Equal(Hash("a"), Hash("b")) {
		t.Fatal("Hash collides")
	}
}

var recoveryFormat = regexp.MustCompile(`^[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}-[A-Z2-7]{4}$`)

func TestRecoveryCode(t *testing.T) {
	code := NewRecoveryCode()
	if !recoveryFormat.MatchString(code) {
		t.Fatalf("NewRecoveryCode() = %q, want XXXX-XXXX-XXXX-XXXX in base32", code)
	}
	if other := NewRecoveryCode(); other == code {
		t.Fatalf("two codes equal: %q", code)
	}
	enc, err := HashRecoveryCode(code)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash %q is not the expected PHC argon2id form", enc)
	}
	if enc2, _ := HashRecoveryCode(code); enc2 == enc {
		t.Fatal("hashes are not salted")
	}

	plain := strings.ReplaceAll(code, "-", "")
	for _, typed := range []string{
		code,
		strings.ToLower(code),
		plain,
		strings.ToLower(plain),
		" " + plain[:8] + " " + plain[8:] + "\n",
		plain[:2] + "-" + plain[2:5] + "--" + plain[5:],
	} {
		if !VerifyRecoveryCode(enc, typed) {
			t.Errorf("VerifyRecoveryCode(%q) = false, want true", typed)
		}
	}

	wrong := []byte(plain)
	if wrong[0] == 'A' {
		wrong[0] = 'B'
	} else {
		wrong[0] = 'A'
	}
	for _, typed := range []string{
		string(wrong),
		plain[:15],
		plain + "A",
		plain[:15] + "0", // not in the base32 alphabet
		"",
		NewRecoveryCode(),
	} {
		if VerifyRecoveryCode(enc, typed) {
			t.Errorf("VerifyRecoveryCode(%q) = true, want false", typed)
		}
	}

	// A hash made from any typed form verifies against any other form.
	enc3, err := HashRecoveryCode(strings.ToLower(plain))
	if err != nil || !VerifyRecoveryCode(enc3, code) {
		t.Fatalf("hash of normalized form does not verify: %v", err)
	}

	for _, bad := range []string{"", "short", plain + "!"} {
		if _, err := HashRecoveryCode(bad); err == nil {
			t.Errorf("HashRecoveryCode(%q) succeeded", bad)
		}
	}
	for _, bad := range []string{
		"",
		"$argon2i$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
		strings.Replace(enc, "v=19", "v=16", 1),
		strings.Replace(enc, "m=19456", "m=0", 1),
		enc[:strings.LastIndex(enc, "$")],
		enc + "$",
	} {
		if VerifyRecoveryCode(bad, code) {
			t.Errorf("VerifyRecoveryCode accepted encoded %q", bad)
		}
	}
}

var (
	secret = []byte("0123456789abcdef0123456789abcdef")
	now    = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
)

func TestSiteCookie(t *testing.T) {
	s := NewSigner(secret)
	for _, c := range []SiteCookie{
		{SiteID: "abcdefgh2345", UserID: uuid.New(), Expires: now.Add(time.Hour)},
		{SiteID: "abcdefgh2345", LinkGen: 7, Expires: now.Add(time.Hour)},
	} {
		v := s.SiteCookie(c)
		got, err := s.ParseSiteCookie(v, now)
		if err != nil {
			t.Fatalf("ParseSiteCookie: %v", err)
		}
		if got != c {
			t.Fatalf("round trip: got %+v, want %+v", got, c)
		}
	}

	c := SiteCookie{SiteID: "abcdefgh2345", UserID: uuid.New(), Expires: now.Add(time.Minute)}
	v := s.SiteCookie(c)
	if _, err := s.ParseSiteCookie(v, c.Expires); !errors.Is(err, ErrExpired) {
		t.Errorf("at expiry: err = %v, want ErrExpired", err)
	}
	if _, err := s.ParseSiteCookie(v, c.Expires.Add(time.Second)); !errors.Is(err, ErrExpired) {
		t.Errorf("after expiry: err = %v, want ErrExpired", err)
	}
	// Sub-second expiry is truncated to the second.
	sub := SiteCookie{SiteID: "abcdefgh2345", Expires: now.Add(1500 * time.Millisecond)}
	got, err := s.ParseSiteCookie(s.SiteCookie(sub), now)
	if err != nil || !got.Expires.Equal(now.Add(time.Second)) {
		t.Errorf("sub-second expiry: got %v, %v", got.Expires, err)
	}

	assertInvalid(t, "site cookie", v, func(v string) error { _, err := s.ParseSiteCookie(v, now); return err })
	if _, err := NewSigner([]byte("another-secret-another-secret-xx")).ParseSiteCookie(v, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("other secret: err = %v, want ErrInvalid", err)
	}
}

func TestUploadGrant(t *testing.T) {
	s := NewSigner(secret)
	g := UploadGrant{DeploymentID: uuid.New(), SiteID: "abcdefgh2345", Expires: now.Add(10 * time.Minute)}
	v := s.UploadGrant(g)
	got, err := s.ParseUploadGrant(v, now)
	if err != nil || got != g {
		t.Fatalf("round trip: got %+v, %v; want %+v", got, err, g)
	}
	if _, err := s.ParseUploadGrant(v, g.Expires.Add(time.Second)); !errors.Is(err, ErrExpired) {
		t.Errorf("after expiry: err = %v, want ErrExpired", err)
	}
	assertInvalid(t, "upload grant", v, func(v string) error { _, err := s.ParseUploadGrant(v, now); return err })
}

func TestWrongPurpose(t *testing.T) {
	s := NewSigner(secret)
	cookie := s.SiteCookie(SiteCookie{SiteID: "abcdefgh2345", UserID: uuid.New(), Expires: now.Add(time.Hour)})
	grant := s.UploadGrant(UploadGrant{DeploymentID: uuid.New(), SiteID: "abcdefgh2345", Expires: now.Add(time.Hour)})
	if _, err := s.ParseUploadGrant(cookie, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("site cookie parsed as upload grant: err = %v", err)
	}
	if _, err := s.ParseSiteCookie(grant, now); !errors.Is(err, ErrInvalid) {
		t.Errorf("upload grant parsed as site cookie: err = %v", err)
	}
}

// assertInvalid checks that tampered variants of a valid signed value v are
// rejected with ErrInvalid.
func assertInvalid(t *testing.T, what, v string, parse func(string) error) {
	t.Helper()
	body, sig, _ := strings.Cut(v, ".")
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	other, _ := base64.RawURLEncoding.DecodeString(body)
	forged := base64.RawURLEncoding.EncodeToString(bytes.Replace(other, []byte(`"e":`), []byte(`"e":9`), 1))
	for name, bad := range map[string]string{
		"empty":          "",
		"no signature":   body,
		"empty sig":      body + ".",
		"body bit flip":  flip(body, len(body)/2) + "." + sig,
		"sig bit flip":   body + "." + flip(sig, 3),
		"truncated sig":  body + "." + sig[:len(sig)-2],
		"extended sig":   v + "A",
		"forged payload": forged + "." + sig,
		"extra part":     v + ".x",
	} {
		if err := parse(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %s: err = %v, want ErrInvalid", what, name, err)
		}
	}
}
