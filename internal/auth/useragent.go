package auth

import (
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// describeUserAgent turns a User-Agent header into "Browser on OS" for
// passkey names and the session list. It is a label, not a fingerprint.
func describeUserAgent(ua string) string {
	browser := ""
	for _, b := range []struct{ token, name string }{
		{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"FxiOS/", "Firefox"},
		{"CriOS/", "Chrome"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"},
	} {
		if strings.Contains(ua, b.token) {
			browser = b.name
			break
		}
	}
	os := ""
	for _, o := range []struct{ token, name string }{
		{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"}, {"CrOS", "ChromeOS"},
		{"Mac OS X", "macOS"}, {"Macintosh", "macOS"}, {"Windows", "Windows"}, {"Linux", "Linux"},
	} {
		if strings.Contains(ua, o.token) {
			os = o.name
			break
		}
	}
	switch {
	case browser != "" && os != "":
		return browser + " on " + os
	case browser != "":
		return browser
	case os != "":
		return os
	}
	return "Unknown device"
}

// passkeyName is the default name of a new passkey.
func passkeyName(ua string, c *webauthn.Credential) string {
	if c.Authenticator.Attachment == protocol.CrossPlatform && !c.Flags.BackupEligible {
		return "Security key"
	}
	d := describeUserAgent(ua)
	if d == "Unknown device" {
		return "Passkey"
	}
	return "Passkey · " + d
}
