// Command e2eseed seeds accounts, signed-in sessions, and published sites
// straight into the database and storage of a running agent-dash, for the
// Playwright specs (web/e2e). It is a test tool, never part of the server:
// no endpoint exposes it.
//
// It reads the server's own configuration variables for the database and
// storage (DATABASE_URL or PG*, STORAGE_TYPE, STORAGE_LOCAL_DIR, S3_*,
// GCS_*), so it must run with the same values as the server under test.
// APP_ORIGIN, SITES_DOMAIN, and SECRET_KEY are optional here.
//
// Input (stdin):
//
//	{"users": [{"email": "a@example.com"}],
//	 "sites": [{"owner": "a@example.com", "title": "T", "description": "D",
//	            "members": [{"email": "b@example.com", "role": "viewer"}],
//	            "versions": [{"files": [{"path": "index.html", "text": "…"},
//	                                    {"path": "logo.png", "base64": "…"}]}]}]}
//
// Output (stdout):
//
//	{"users": {"a@example.com": {"id": "…", "sessionToken": "…"}},
//	 "sites": [{"id": "…", "versions": ["<oldest version id>", …]}]}
//
// Each user gets a verified primary email and a 24-hour session; a browser
// is signed in by setting the cookie __Host-session to sessionToken on the
// app host. Versions publish in order (replace mode), so the last is current.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/server"
	"github.com/mjquinlan2000/agent-dash/internal/sites"
	"github.com/mjquinlan2000/agent-dash/internal/storage"
	"github.com/mjquinlan2000/agent-dash/internal/token"
)

type spec struct {
	Users []struct {
		Email string `json:"email"`
	} `json:"users"`
	Sites []struct {
		Owner       string `json:"owner"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Members     []struct {
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"members"`
		Versions []struct {
			Files []struct {
				Path   string  `json:"path"`
				Text   *string `json:"text"`
				Base64 *string `json:"base64"`
			} `json:"files"`
		} `json:"versions"`
	} `json:"sites"`
}

type userOut struct {
	ID           uuid.UUID `json:"id"`
	SessionToken string    `json:"sessionToken"`
}

type siteOut struct {
	ID       string      `json:"id"`
	Versions []uuid.UUID `json:"versions"`
}

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
	var in spec
	dec := json.NewDecoder(os.Stdin)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("read spec: %w", err)
	}
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()
	store, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	svc := sites.NewService(&server.Deps{Config: cfg, Logger: slog.Default(), DB: pool, Storage: store})
	q := db.New(pool)

	users := map[string]userOut{}
	for _, u := range in.Users {
		email := strings.ToLower(u.Email)
		row, err := q.CreateUser(ctx, uuid.Must(uuid.NewV7()))
		if err != nil {
			return err
		}
		e, err := q.CreateEmail(ctx, db.CreateEmailParams{ID: uuid.Must(uuid.NewV7()), UserID: row.ID, Email: email, IsPrimary: true})
		if err != nil {
			return fmt.Errorf("email %s: %w", email, err)
		}
		if _, err := q.MarkEmailVerified(ctx, e.ID); err != nil {
			return err
		}
		plaintext, hash := token.New()
		if _, err := pool.Exec(ctx, `INSERT INTO sessions (id, token_hash, user_id, user_agent, expires_at) VALUES ($1, $2, $3, 'e2eseed', $4)`,
			uuid.Must(uuid.NewV7()), hash, row.ID, time.Now().Add(24*time.Hour)); err != nil {
			return fmt.Errorf("session: %w", err)
		}
		users[email] = userOut{ID: row.ID, SessionToken: plaintext}
	}
	user := func(email string) (uuid.UUID, error) {
		u, ok := users[strings.ToLower(email)]
		if !ok {
			return uuid.Nil, fmt.Errorf("unknown user %q (list it under users)", email)
		}
		return u.ID, nil
	}

	out := struct {
		Users map[string]userOut `json:"users"`
		Sites []siteOut          `json:"sites"`
	}{Users: users, Sites: []siteOut{}}
	for _, s := range in.Sites {
		owner, err := user(s.Owner)
		if err != nil {
			return err
		}
		site, err := svc.Create(ctx, owner, s.Title, s.Description)
		if err != nil {
			return err
		}
		for _, m := range s.Members {
			id, err := user(m.Email)
			if err != nil {
				return err
			}
			if err := q.CreateMembership(ctx, db.CreateMembershipParams{SiteID: site.ID, UserID: id, Role: m.Role}); err != nil {
				return fmt.Errorf("member %s: %w", m.Email, err)
			}
		}
		so := siteOut{ID: site.ID, Versions: []uuid.UUID{}}
		for _, v := range s.Versions {
			var files []sites.File
			for _, f := range v.Files {
				var body []byte
				switch {
				case f.Text != nil:
					body = []byte(*f.Text)
				case f.Base64 != nil:
					if body, err = base64.StdEncoding.DecodeString(*f.Base64); err != nil {
						return fmt.Errorf("file %s: %w", f.Path, err)
					}
				}
				files = append(files, sites.File{Path: f.Path, Body: body})
			}
			ver, err := svc.Publish(ctx, site.ID, owner, sites.Replace, files, nil)
			if err != nil {
				return err
			}
			so.Versions = append(so.Versions, ver.ID)
		}
		out.Sites = append(out.Sites, so)
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}
