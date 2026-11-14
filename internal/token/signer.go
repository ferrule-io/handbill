package token

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Errors returned when parsing signed values. A value that fails any check
// (format, signature, purpose) is ErrInvalid; a genuine but stale one is
// ErrExpired.
var (
	ErrInvalid = errors.New("token: invalid signed value")
	ErrExpired = errors.New("token: signed value expired")
)

// Purposes domain-separate signed values: each has its own key derived from
// SECRET_KEY, so a value signed for one purpose never verifies as another.
const (
	purposeSiteCookie  = "site-cookie"
	purposeUploadGrant = "upload-grant"
)

// Signer signs and verifies values with HMAC-SHA256 keyed by SECRET_KEY.
// Signed values have the form base64url(JSON payload) "." base64url(MAC).
// Expiry times are carried to the second.
type Signer struct {
	keys map[string][]byte
}

// NewSigner returns a Signer for secret (config.Config.SecretKey).
func NewSigner(secret []byte) *Signer {
	s := &Signer{keys: make(map[string][]byte, 2)}
	for _, p := range []string{purposeSiteCookie, purposeUploadGrant} {
		s.keys[p] = DeriveKey(secret, p)
	}
	return s
}

func (s *Signer) sign(purpose string, payload any) string {
	b, err := json.Marshal(payload)
	if err != nil {
		panic("token: marshal payload: " + err.Error()) // payload types are fixed structs
	}
	body := base64.RawURLEncoding.EncodeToString(b)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.mac(purpose, body))
}

func (s *Signer) mac(purpose, body string) []byte {
	m := hmac.New(sha256.New, s.keys[purpose])
	m.Write([]byte(body))
	return m.Sum(nil)
}

// verify checks v's signature for purpose and decodes its payload into dst.
func (s *Signer) verify(purpose, v string, dst any) error {
	body, sig, ok := strings.Cut(v, ".")
	if !ok {
		return ErrInvalid
	}
	got, err := base64.RawURLEncoding.Strict().DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.mac(purpose, body)) {
		return ErrInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrInvalid
	}
	return nil
}

func checkExpiry(unix int64, now time.Time) (time.Time, error) {
	exp := time.Unix(unix, 0).UTC()
	if !now.Before(exp) {
		return time.Time{}, ErrExpired
	}
	return exp, nil
}

// SiteCookie is the payload of the __Host-site cookie. Its validity also
// depends on current database state (sites.Access): a member cookie needs a
// role on the site, a public-link cookie the current link generation.
type SiteCookie struct {
	SiteID  string
	UserID  uuid.UUID // uuid.Nil for a public-link cookie
	LinkGen int64     // public-link generation; 0 for member cookies
	Expires time.Time
}

type siteCookiePayload struct {
	Site string    `json:"s"`
	User uuid.UUID `json:"u"`
	Gen  int64     `json:"g"`
	Exp  int64     `json:"e"`
}

// SiteCookie returns the signed cookie value for c.
func (s *Signer) SiteCookie(c SiteCookie) string {
	return s.sign(purposeSiteCookie, siteCookiePayload{Site: c.SiteID, User: c.UserID, Gen: c.LinkGen, Exp: c.Expires.Unix()})
}

// ParseSiteCookie verifies a cookie value signed by SiteCookie and not yet
// expired at now.
func (s *Signer) ParseSiteCookie(v string, now time.Time) (SiteCookie, error) {
	var p siteCookiePayload
	if err := s.verify(purposeSiteCookie, v, &p); err != nil {
		return SiteCookie{}, err
	}
	exp, err := checkExpiry(p.Exp, now)
	if err != nil {
		return SiteCookie{}, err
	}
	return SiteCookie{SiteID: p.Site, UserID: p.User, LinkGen: p.Gen, Expires: exp}, nil
}

// UploadGrant authorizes one account to upload into a staged deployment:
// one raw file at Path (/api/deployments/<id>/file), or an archive when
// Path is empty (/api/deployments/<id>/archive). Its validity also requires
// the deployment to still be open and UserID to be its creator and still
// able to publish to the site.
type UploadGrant struct {
	DeploymentID uuid.UUID
	SiteID       string
	UserID       uuid.UUID // the requester, who must be the deployment's creator
	Path         string    // set: one raw file at this path (/file); empty: an archive (/archive)
	Expires      time.Time
}

type uploadGrantPayload struct {
	Deployment uuid.UUID `json:"d"`
	Site       string    `json:"s"`
	User       uuid.UUID `json:"u"`
	Path       string    `json:"p,omitempty"`
	Exp        int64     `json:"e"`
}

// UploadGrant returns the signed value for g, to carry in an upload URL.
func (s *Signer) UploadGrant(g UploadGrant) string {
	return s.sign(purposeUploadGrant, uploadGrantPayload{Deployment: g.DeploymentID, Site: g.SiteID, User: g.UserID, Path: g.Path, Exp: g.Expires.Unix()})
}

// ParseUploadGrant verifies a value signed by UploadGrant and not yet
// expired at now.
func (s *Signer) ParseUploadGrant(v string, now time.Time) (UploadGrant, error) {
	var p uploadGrantPayload
	if err := s.verify(purposeUploadGrant, v, &p); err != nil {
		return UploadGrant{}, err
	}
	exp, err := checkExpiry(p.Exp, now)
	if err != nil {
		return UploadGrant{}, err
	}
	return UploadGrant{DeploymentID: p.Deployment, SiteID: p.Site, UserID: p.User, Path: p.Path, Expires: exp}, nil
}
