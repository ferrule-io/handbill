package identity

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestRoundTrip(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("empty context has a user")
	}
	u := User{ID: uuid.New(), Email: "a@example.com", EmailVerified: true}
	got, ok := FromContext(WithUser(context.Background(), u))
	if !ok || got != u {
		t.Fatalf("FromContext = %+v, %v", got, ok)
	}
}
