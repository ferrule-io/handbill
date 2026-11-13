// Package identity carries the signed-in user through request contexts. The
// accounts workstream's session middleware (registered with
// server.Routes.UseApp) sets it; every other package only reads it, so
// features that need "who is this?" never import the auth implementation.
package identity

import (
	"context"

	"github.com/google/uuid"
)

// User is the signed-in account.
type User struct {
	ID            uuid.UUID
	Email         string
	EmailVerified bool
}

type key struct{}

// WithUser returns ctx carrying u.
func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, key{}, u)
}

// FromContext returns the signed-in user, if any.
func FromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(key{}).(User)
	return u, ok
}
