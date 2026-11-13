package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func init() { toolsets = append(toolsets, addAccountTools) }

type whoamiIn struct{}

type whoamiOut struct {
	UserID        string `json:"user_id"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	ClientID      string `json:"client_id"`
	ClientName    string `json:"client_name"`
}

func addAccountTools(s *mcpsdk.Server, env *Env) {
	addTool(s, env, &mcpsdk.Tool{
		Name:        "whoami",
		Description: "Show which handbill account this connection acts as (email and user id) and which app is connected. Call it when you need to know whose sites you are working with.",
		Annotations: readOnly("Who am I"),
	}, nil, func(ctx context.Context, _ whoamiIn) (whoamiOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return whoamiOut{}, err
		}
		return whoamiOut{UserID: p.UserID.String(), Email: p.Email, EmailVerified: p.EmailVerified, ClientID: p.ClientID, ClientName: p.ClientName}, nil
	})
}
