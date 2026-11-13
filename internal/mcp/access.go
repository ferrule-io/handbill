package mcp

import (
	"context"
	"errors"

	"github.com/ferrule-io/handbill/internal/oauth"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/sites"
)

// principal returns the caller; RequireBearer guarantees one on /mcp.
func principal(ctx context.Context) (oauth.Principal, error) {
	p, ok := oauth.PrincipalFrom(ctx)
	if !ok {
		return oauth.Principal{}, errors.New("mcp: no principal in context")
	}
	return p, nil
}

// siteAccess is a site the caller may act on, with their role.
type siteAccess struct {
	p    oauth.Principal
	site sites.Site
	role rbac.Role
}

// site checks that the caller may take action on a site for tool. Missing
// and unshared sites give the same answer, as in /app-api.
func (env *Env) site(ctx context.Context, tool, siteID string, action rbac.Action) (siteAccess, error) {
	p, err := principal(ctx)
	if err != nil {
		return siteAccess{}, err
	}
	role, err := env.Sites.Role(ctx, siteID, p.UserID)
	if err != nil {
		return siteAccess{}, err
	}
	if role == "" {
		return siteAccess{}, siteNotFound(siteID)
	}
	if !rbac.Can(role, action) {
		need := "owner"
		if rbac.Can(rbac.Manager, action) {
			need = "manager"
		}
		return siteAccess{}, toolErrorf("your role on site %s is %s; %s needs %s", siteID, role, tool, need)
	}
	s, err := env.Sites.Get(ctx, siteID)
	if errors.Is(err, sites.ErrNotFound) {
		return siteAccess{}, siteNotFound(siteID)
	}
	if err != nil {
		return siteAccess{}, err
	}
	return siteAccess{p: p, site: s, role: role}, nil
}

func siteNotFound(siteID string) error {
	return toolErrorf("site %q was not found or is not shared with you", siteID)
}

func deploymentNotFound(id, siteID string) error {
	return toolErrorf("deployment %s was not found on site %s", id, siteID)
}

func deploymentClosed(id string) error {
	return toolErrorf("deployment %s is already finalized or abandoned: start a new one with create_deployment", id)
}

func versionNotFound(id, siteID string) error {
	return toolErrorf("version %s is not a version of site %s; see list_versions", id, siteID)
}
