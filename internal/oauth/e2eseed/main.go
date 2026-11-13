// Command e2eseed registers a CIMD client in the database of a running
// handbill, for the Playwright consent spec (web/e2e/oauth.spec.ts). It
// is a test tool, never part of the server.
//
// The client's document is stored as freshly fetched with a one-day cache,
// so /oauth/authorize never fetches https://client.e2e.test/cimd.json. It
// reads the server's DATABASE_URL (or PG*) and prints
//
//	{"clientId": "https://client.e2e.test/cimd.json", "redirectUri": "http://127.0.0.1/callback"}
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/db"
)

const (
	clientID    = "https://client.e2e.test/cimd.json"
	clientName  = "E2E Agent"
	redirectURI = "http://127.0.0.1/callback"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2eseed:", err)
		os.Exit(1)
	}
}

// getenv supplies placeholders for the variables the seeder does not use.
func getenv(k string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return map[string]string{
		"APP_ORIGIN":   "http://dash.localhost",
		"SITES_DOMAIN": "sites.localhost",
		"SECRET_KEY":   strings.Repeat("e2eseed-unused-", 3),
	}[k]
}

func run() error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	doc, _ := json.Marshal(map[string]any{
		"client_id": clientID, "client_name": clientName, "redirect_uris": []string{redirectURI},
		"grant_types": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_method": "none",
	})
	expires := time.Now().Add(24 * time.Hour)
	if _, err := db.New(pool).UpsertCIMDClient(ctx, db.UpsertCIMDClientParams{
		ID: uuid.Must(uuid.NewV7()), ClientID: clientID, Metadata: doc, ClientName: clientName,
		RedirectUris: []string{redirectURI}, TokenEndpointAuthMethod: "none", CacheExpiresAt: &expires,
	}); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"clientId": clientID, "redirectUri": redirectURI})
}
