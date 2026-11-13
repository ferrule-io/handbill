package mcp

import (
	"context"
	"errors"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/sites"
)

func init() { toolsets = append(toolsets, addSiteTools) }

type siteIn struct {
	Site string `json:"site" jsonschema:"The site id, from list_sites or create_site."`
}

type siteSummary struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	Description      string  `json:"description"`
	URL              string  `json:"url"`
	Role             string  `json:"role"`
	Published        bool    `json:"published"`
	CurrentVersionID *string `json:"current_version_id"`
	UpdatedAt        string  `json:"updated_at"`
}

type listSitesOut struct {
	Sites []siteSummary `json:"sites"`
}

type createSiteIn struct {
	Title       string `json:"title" jsonschema:"The site's title, shown to viewers and in the dashboard."`
	Description string `json:"description,omitempty" jsonschema:"Optional: what the site is for."`
}

type siteOut struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Role        string `json:"role"`
}

type currentVersionOut struct {
	ID          string  `json:"id"`
	CreatedAt   string  `json:"created_at"`
	PublishedBy *string `json:"published_by"`
	FileCount   int     `json:"file_count"`
	Size        int64   `json:"size"`
}

type getSiteOut struct {
	ID                string             `json:"id"`
	Title             string             `json:"title"`
	Description       string             `json:"description"`
	URL               string             `json:"url"`
	Role              string             `json:"role"`
	CurrentVersion    *currentVersionOut `json:"current_version"`
	PublicLinkEnabled bool               `json:"public_link_enabled"`
	MemberCount       int                `json:"member_count"`
	CreatedAt         string             `json:"created_at"`
	UpdatedAt         string             `json:"updated_at"`
}

type updateSiteIn struct {
	Site        string  `json:"site" jsonschema:"The site id."`
	Title       *string `json:"title,omitempty" jsonschema:"Optional: the new title."`
	Description *string `json:"description,omitempty" jsonschema:"Optional: the new description."`
}

type updateSiteOut struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Role        string `json:"role"`
	UpdatedAt   string `json:"updated_at"`
}

type deleteSiteOut struct {
	Deleted bool   `json:"deleted"`
	Site    string `json:"site"`
}

func (env *Env) siteURL(id string) string { return env.Deps.Config.SiteOrigin(id) + "/" }

func publisher(v sites.Version) *string {
	if v.PublisherEmail == "" {
		return nil
	}
	return &v.PublisherEmail
}

func addSiteTools(s *mcpsdk.Server, env *Env) {
	addTool(s, env, &mcpsdk.Tool{
		Name:        "list_sites",
		Description: "List the sites you can publish to: sites you own or manage, most recently updated first. Each entry has the site id (pass it as `site` to the other tools), title, your role, the site's URL, and whether anything is published yet. Sites shared with you only as a viewer are not listed. To update recurring content such as a daily dashboard, reuse the existing site so its URL stays the same; use create_site only for something new.",
		Annotations: readOnly("List sites"),
	}, nil, func(ctx context.Context, _ struct{}) (listSitesOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return listSitesOut{}, err
		}
		ms, err := env.Sites.ListForUser(ctx, p.UserID)
		if err != nil {
			return listSitesOut{}, err
		}
		out := listSitesOut{Sites: []siteSummary{}}
		for _, m := range ms {
			if m.Role != rbac.Owner && m.Role != rbac.Manager {
				continue
			}
			sum := siteSummary{
				ID: m.Site.ID, Title: m.Site.Title, Description: m.Site.Description, URL: env.siteURL(m.Site.ID),
				Role: string(m.Role), Published: m.Site.CurrentVersionID.Valid, UpdatedAt: ts(m.Site.UpdatedAt),
			}
			if m.Site.CurrentVersionID.Valid {
				id := m.Site.CurrentVersionID.UUID.String()
				sum.CurrentVersionID = &id
			}
			out.Sites = append(out.Sites, sum)
		}
		return out, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "create_site",
		Description: "Create a new, empty site that you own and return its id and URL. The URL is permanent and private: viewers must sign in, and only people the owner shares the site with can open it. Nothing is served until you publish with publish_site or a staged deployment. Check list_sites first: updating an existing site keeps its URL, which is usually what the user wants.",
		Annotations: mutating("Create site", false, false),
	}, patches(titlePatch, descriptionPatch), func(ctx context.Context, in createSiteIn) (siteOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return siteOut{}, err
		}
		site, err := env.Sites.Create(ctx, p.UserID, in.Title, in.Description)
		if err != nil {
			return siteOut{}, err
		}
		return siteOut{ID: site.ID, Title: site.Title, Description: site.Description, URL: env.siteURL(site.ID), Role: string(rbac.Owner)}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "get_site",
		Description: `Get one site's details: title, description, URL, your role, the current version (id, publish time, file count, total size, publisher), whether an "anyone with the link" public link is enabled, and how many accounts have access.`,
		Annotations: readOnly("Get site"),
	}, sitePatch, func(ctx context.Context, in siteIn) (getSiteOut, error) {
		a, err := env.site(ctx, "get_site", in.Site, rbac.Publish)
		if err != nil {
			return getSiteOut{}, err
		}
		sum, err := db.New(env.Deps.DB).GetSiteSharingSummary(ctx, a.site.ID)
		if err != nil {
			return getSiteOut{}, err
		}
		out := getSiteOut{
			ID: a.site.ID, Title: a.site.Title, Description: a.site.Description, URL: env.siteURL(a.site.ID),
			Role: string(a.role), PublicLinkEnabled: sum.PublicLinkEnabled, MemberCount: int(sum.MemberCount),
			CreatedAt: ts(a.site.CreatedAt), UpdatedAt: ts(a.site.UpdatedAt),
		}
		if a.site.CurrentVersionID.Valid {
			v, err := env.Sites.GetVersion(ctx, a.site.ID, a.site.CurrentVersionID.UUID)
			if err != nil {
				return getSiteOut{}, err
			}
			out.CurrentVersion = &currentVersionOut{
				ID: v.ID.String(), CreatedAt: ts(v.CreatedAt), PublishedBy: publisher(v), FileCount: v.FileCount, Size: v.Size,
			}
		}
		return out, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "update_site",
		Description: "Change a site's title and/or description. Only the fields you pass change; published content and the URL are not affected.",
		Annotations: mutating("Update site", false, true),
	}, patches(sitePatch, titlePatch, descriptionPatch), func(ctx context.Context, in updateSiteIn) (updateSiteOut, error) {
		if in.Title == nil && in.Description == nil {
			return updateSiteOut{}, toolErrorf("pass title, description, or both")
		}
		a, err := env.site(ctx, "update_site", in.Site, rbac.EditMeta)
		if err != nil {
			return updateSiteOut{}, err
		}
		site, err := env.Sites.Update(ctx, a.site.ID, in.Title, in.Description)
		if err != nil {
			return updateSiteOut{}, err
		}
		return updateSiteOut{
			ID: site.ID, Title: site.Title, Description: site.Description, URL: env.siteURL(site.ID),
			Role: string(a.role), UpdatedAt: ts(site.UpdatedAt),
		}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "delete_site",
		Description: "Permanently delete a site: every version, every file, all sharing, and its URL, which stops working immediately. This cannot be undone: rollback_site cannot recover a deleted site. Only the owner can delete. Confirm with the user first. To take content down temporarily, publish a placeholder page instead.",
		Annotations: mutating("Delete site", true, false),
	}, sitePatch, func(ctx context.Context, in siteIn) (deleteSiteOut, error) {
		a, err := env.site(ctx, "delete_site", in.Site, rbac.Delete)
		if err != nil {
			return deleteSiteOut{}, err
		}
		if err := env.Sites.Delete(ctx, a.site.ID); err != nil {
			// A storage cleanup failure after the database delete still
			// deleted the site; the orphaned blobs are left for blob GC.
			if _, getErr := env.Sites.Get(ctx, a.site.ID); !errors.Is(getErr, sites.ErrNotFound) {
				return deleteSiteOut{}, err
			}
			env.Deps.Logger.Error("mcp delete_site: storage cleanup failed", "site", a.site.ID, "err", err)
		}
		return deleteSiteOut{Deleted: true, Site: a.site.ID}, nil
	})
}
