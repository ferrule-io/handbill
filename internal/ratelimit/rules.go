package ratelimit

// Key selects whose bucket a request draws from.
type Key int

const (
	// ByIP keys on the client address (ClientIP; IPv6 by /64).
	ByIP Key = iota
	// ByUser keys on the signed-in user (identity), else the client
	// address.
	ByUser
	// ByBearer keys on the request's bearer token, so each OAuth grant of a
	// principal is limited on its own, else the client address. A rule
	// keyed ByBearer should also set IP, since tokens cost nothing to make
	// up.
	ByBearer
)

// Rule limits the requests served by the mux patterns it lists. A 429 is
// JSON for /app-api/ ({"error": {"code", "message"}}) and the OAuth POST
// endpoints ({"error", "error_description"}), plain text otherwise.
type Rule struct {
	// Name labels the rule in metrics and logs.
	Name string
	// Sites applies the rule to every request on a site host; Patterns
	// is then empty.
	Sites bool
	// Patterns are app mux patterns, exactly as registered.
	Patterns []string
	Key      Key
	Limit    Limit
	// IP, when set, also limits each client address, whatever Key is.
	IP *Limit
}

// Rules is the route table. Limits are per replica and generous enough
// that people never notice them; they cap abuse, not normal use. A
// pattern appears in at most one rule.
var Rules = []Rule{
	// Accounts: creating accounts, signing in, and recovery codes (whose
	// check costs up to ten argon2id hashes).
	{Name: "signup", Patterns: []string{
		"POST /app-api/auth/passkey/signup/begin",
		"POST /app-api/auth/passkey/signup/finish",
	}, Key: ByIP, Limit: Limit{PerMinute: 20, Burst: 30}},
	{Name: "signin", Patterns: []string{
		"POST /app-api/auth/passkey/login/begin",
		"POST /app-api/auth/passkey/login/finish",
	}, Key: ByIP, Limit: Limit{PerMinute: 60, Burst: 60}},
	{Name: "recovery", Patterns: []string{
		"POST /app-api/auth/recovery/signin",
	}, Key: ByIP, Limit: Limit{PerMinute: 5, Burst: 10}},
	{Name: "google", Patterns: []string{
		"POST /app-api/auth/google/start",
		"GET /auth/google/callback",
	}, Key: ByIP, Limit: Limit{PerMinute: 30, Burst: 30}},
	{Name: "email_verification", Patterns: []string{
		"POST /app-api/auth/email/send-verification",
	}, Key: ByUser, Limit: Limit{PerMinute: 0.1, Burst: 5}},
	{Name: "email_verify", Patterns: []string{
		"POST /app-api/auth/email/verify",
	}, Key: ByIP, Limit: Limit{PerMinute: 30, Burst: 30}},

	// OAuth. Registration stores a client per call, so it is strict.
	{Name: "oauth_register", Patterns: []string{
		"POST /oauth/register",
	}, Key: ByIP, Limit: Limit{PerMinute: 0.5, Burst: 10}},
	{Name: "oauth_authorize", Patterns: []string{
		"GET /oauth/authorize",
	}, Key: ByIP, Limit: Limit{PerMinute: 60, Burst: 30}},
	{Name: "oauth_token", Patterns: []string{
		"POST /oauth/token",
	}, Key: ByIP, Limit: Limit{PerMinute: 60, Burst: 60}},
	{Name: "oauth_revoke", Patterns: []string{
		"POST /oauth/revoke",
	}, Key: ByIP, Limit: Limit{PerMinute: 60, Burst: 30}},

	// Sharing: invites and transfers send mail; lookups take a token.
	{Name: "invite_send", Patterns: []string{
		"POST /app-api/sites/{id}/members",
		"POST /app-api/sites/{id}/transfer",
	}, Key: ByUser, Limit: Limit{PerMinute: 2, Burst: 30}},
	{Name: "invite_lookup", Patterns: []string{
		"POST /app-api/invites/lookup",
	}, Key: ByIP, Limit: Limit{PerMinute: 30, Burst: 20}},
	{Name: "invite_accept", Patterns: []string{
		"POST /app-api/invites/accept",
	}, Key: ByUser, Limit: Limit{PerMinute: 30, Burst: 20}},

	// Publishing.
	{Name: "mcp", Patterns: []string{
		"POST /mcp",
		"GET /mcp",
		"DELETE /mcp",
	}, Key: ByBearer, Limit: Limit{PerMinute: 300, Burst: 120}, IP: &Limit{PerMinute: 1200, Burst: 300}},
	{Name: "archive_upload", Patterns: []string{
		"PUT /api/deployments/{id}/archive",
	}, Key: ByIP, Limit: Limit{PerMinute: 30, Burst: 10}},
	{Name: "file_upload", Patterns: []string{
		"PUT /api/deployments/{id}/file",
	}, Key: ByIP, Limit: Limit{PerMinute: 120, Burst: 60}},

	// Site hosts: a page load fetches many assets at once.
	{Name: "sites", Sites: true, Key: ByIP, Limit: Limit{PerMinute: 1200, Burst: 400}},
}
