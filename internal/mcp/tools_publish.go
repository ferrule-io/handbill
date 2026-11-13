package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/token"
	"github.com/ferrule-io/handbill/internal/upload"
)

func init() { toolsets = append(toolsets, addPublishTools) }

const publishDescription = `Publish files to a site as a new version in one call; the site's URL serves them immediately. Use it for sites up to about 10 MB in total (the request limit is 16 MB after encoding). For larger sites, many binary assets, or files produced over several steps, use create_deployment with upload_files or get_upload_url, then finalize_deployment.

Paths are relative to the site root, slash-separated and case-sensitive: "index.html", "css/site.css", "img/logo.png" - no leading "/", no "..", nothing under "_auth/". "/" serves index.html, a directory serves its index.html, "/about" also finds "about.html", and a 404.html, if present, is shown for missing pages.

Use encoding "utf8" (the default) for text files - HTML, CSS, JavaScript, SVG, JSON, Markdown - and pass the text as-is; use "base64" for binary files such as images, fonts, PDFs, and audio. The content type comes from the extension; set content_type only to override it.

mode is required. "replace" publishes exactly the files you pass: anything not listed disappears from the site. "merge" starts from the current version, adds or overwrites the files you pass, and removes delete_paths - use it to change a few files of an existing site. delete_paths is only allowed with merge.

Publishing is atomic: viewers see the old version or the new one, never a mix. Every publish is kept; list_versions and rollback_site undo a bad publish. No server-side code runs, so build interactivity with client-side JavaScript and embed or bundle any data the pages need.`

const uploadURLDescription = "Get a short-lived URL for uploading a whole site as one archive into an open staged deployment - the best option for big sites, many files, or binary assets when you can run shell commands. Build the archive from the site's root directory, for example `tar -czf site.tar.gz -C dist .` or `(cd dist && zip -r ../site.zip .)`, then upload the raw file with an HTTP PUT: `curl --fail-with-body -T site.tar.gz '<upload_url>'`. Send no Authorization header: the URL itself is the credential, so do not share or log it. It expires after 15 minutes; get a new one if needed. .tar.gz and .zip are accepted (detected from the content). If every file is inside one top-level folder (as `tar -czf site.tar.gz dist/` produces), that folder is removed from the paths. Symlinks and special files are rejected. Limits: 512 MB uncompressed, 64 MB per file, 10,000 entries. You can upload more than once; later uploads overwrite earlier paths. Then call finalize_deployment."

type publishIn struct {
	Site        string      `json:"site" jsonschema:"The site id."`
	Mode        string      `json:"mode" jsonschema:"replace: publish exactly these files. merge: change these files on top of the current version."`
	Files       []fileInput `json:"files" jsonschema:"The files to publish."`
	DeletePaths []string    `json:"delete_paths,omitempty" jsonschema:"merge only: paths of the current version to remove."`
}

type versionOut struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	FileCount int    `json:"file_count"`
	Size      int64  `json:"size"`
}

type publishOut struct {
	Site    string     `json:"site"`
	URL     string     `json:"url"`
	Version versionOut `json:"version"`
}

type createDeploymentIn struct {
	Site string `json:"site" jsonschema:"The site id."`
	Mode string `json:"mode" jsonschema:"replace: publish exactly the deployment's files. merge: apply them on top of the current version at finalize."`
}

type createDeploymentOut struct {
	DeploymentID string `json:"deployment_id"`
	Site         string `json:"site"`
	Mode         string `json:"mode"`
}

type uploadFilesIn struct {
	Site         string      `json:"site" jsonschema:"The site id."`
	DeploymentID string      `json:"deployment_id" jsonschema:"The deployment id from create_deployment."`
	Files        []fileInput `json:"files,omitempty" jsonschema:"Files to add."`
	DeletePaths  []string    `json:"delete_paths,omitempty" jsonschema:"merge only: paths of the current version to remove at finalize."`
}

type uploadFilesOut struct {
	DeploymentID string `json:"deployment_id"`
	FilesAdded   int    `json:"files_added"`
	PathsDeleted int    `json:"paths_deleted"`
}

type deploymentIn struct {
	Site         string `json:"site" jsonschema:"The site id."`
	DeploymentID string `json:"deployment_id" jsonschema:"The deployment id from create_deployment."`
}

type uploadURLOut struct {
	UploadURL string   `json:"upload_url"`
	Method    string   `json:"method"`
	ExpiresAt string   `json:"expires_at"`
	MaxBytes  int64    `json:"max_bytes"`
	Formats   []string `json:"formats"`
	Example   string   `json:"example"`
}

func toVersionOut(v sites.Version) versionOut {
	return versionOut{ID: v.ID.String(), CreatedAt: ts(v.CreatedAt), FileCount: v.FileCount, Size: v.Size}
}

// deployment returns a deployment of the site; deployments of other
// sites are not found.
func (env *Env) deployment(ctx context.Context, siteID, id string) (sites.Deployment, error) {
	depID, err := uuid.Parse(id)
	if err != nil {
		return sites.Deployment{}, deploymentNotFound(id, siteID)
	}
	dep, err := env.Sites.GetDeployment(ctx, depID)
	if errors.Is(err, sites.ErrNotFound) || (err == nil && dep.SiteID != siteID) {
		return sites.Deployment{}, deploymentNotFound(id, siteID)
	}
	return dep, err
}

// deploymentErr maps service errors for a deployment to tool messages.
func deploymentErr(err error, id, siteID string) error {
	switch {
	case errors.Is(err, sites.ErrDeploymentClosed):
		return deploymentClosed(id)
	case errors.Is(err, sites.ErrNotFound):
		return deploymentNotFound(id, siteID)
	}
	return err
}

func addPublishTools(s *mcpsdk.Server, env *Env) {
	addTool(s, env, &mcpsdk.Tool{
		Name:        "publish_site",
		Description: publishDescription,
		Annotations: mutating("Publish site", true, false),
	}, patches(sitePatch, modePatch, filesPatch, deletePathsPatch), func(ctx context.Context, in publishIn) (publishOut, error) {
		a, err := env.site(ctx, "publish_site", in.Site, rbac.Publish)
		if err != nil {
			return publishOut{}, err
		}
		files, err := decodeFiles(in.Files)
		if err != nil {
			return publishOut{}, err
		}
		v, err := env.Sites.Publish(ctx, a.site.ID, a.p.UserID, sites.Mode(in.Mode), files, in.DeletePaths)
		if errors.Is(err, sites.ErrNotFound) {
			return publishOut{}, siteNotFound(in.Site)
		}
		if err != nil {
			return publishOut{}, err
		}
		return publishOut{Site: a.site.ID, URL: env.siteURL(a.site.ID), Version: toVersionOut(v)}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "create_deployment",
		Description: "Start a staged publish for large sites or files produced over several steps; returns a deployment_id. Add files with upload_files (inline, up to 16 MB per call, repeatable) and/or upload a whole .tar.gz or .zip through get_upload_url, then call finalize_deployment to publish everything atomically as one new version. The site does not change until finalize. mode 'replace' publishes exactly the deployment's files; 'merge' applies them on top of the version that is current when you finalize, and removes any delete_paths. A deployment you never finalize has no effect.",
		Annotations: mutating("Create deployment", false, false),
	}, patches(sitePatch, modePatch), func(ctx context.Context, in createDeploymentIn) (createDeploymentOut, error) {
		a, err := env.site(ctx, "create_deployment", in.Site, rbac.Publish)
		if err != nil {
			return createDeploymentOut{}, err
		}
		id, err := env.Sites.CreateDeployment(ctx, a.site.ID, a.p.UserID, sites.Mode(in.Mode))
		if errors.Is(err, sites.ErrNotFound) {
			return createDeploymentOut{}, siteNotFound(in.Site)
		}
		if err != nil {
			return createDeploymentOut{}, err
		}
		return createDeploymentOut{DeploymentID: id.String(), Site: a.site.ID, Mode: in.Mode}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "upload_files",
		Description: "Add files to an open staged deployment from create_deployment. Files use the same format as publish_site: relative path, content, encoding 'utf8' for text or 'base64' for binary, optional content_type. Uploading a path again replaces the earlier upload. In merge deployments, delete_paths marks files of the current version to remove at finalize. Keep each call under 16 MB and call repeatedly for more. Nothing is visible until finalize_deployment.",
		Annotations: mutating("Upload files", false, true),
	}, patches(sitePatch, uuidPatch("deployment_id"), filesPatch, deletePathsPatch), func(ctx context.Context, in uploadFilesIn) (uploadFilesOut, error) {
		if len(in.Files) == 0 && len(in.DeletePaths) == 0 {
			return uploadFilesOut{}, toolErrorf("pass files, delete_paths, or both")
		}
		a, err := env.site(ctx, "upload_files", in.Site, rbac.Publish)
		if err != nil {
			return uploadFilesOut{}, err
		}
		dep, err := env.deployment(ctx, a.site.ID, in.DeploymentID)
		if err != nil {
			return uploadFilesOut{}, err
		}
		files, err := decodeFiles(in.Files)
		if err != nil {
			return uploadFilesOut{}, err
		}
		if len(files) > 0 {
			if err := env.Sites.AddFiles(ctx, dep.ID, files); err != nil {
				return uploadFilesOut{}, deploymentErr(err, in.DeploymentID, in.Site)
			}
		}
		if len(in.DeletePaths) > 0 {
			if err := env.Sites.DeletePaths(ctx, dep.ID, in.DeletePaths); err != nil {
				return uploadFilesOut{}, deploymentErr(err, in.DeploymentID, in.Site)
			}
		}
		return uploadFilesOut{DeploymentID: dep.ID.String(), FilesAdded: len(files), PathsDeleted: len(in.DeletePaths)}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "get_upload_url",
		Description: uploadURLDescription,
		Annotations: readOnly("Get upload URL"),
	}, patches(sitePatch, uuidPatch("deployment_id")), func(ctx context.Context, in deploymentIn) (uploadURLOut, error) {
		a, err := env.site(ctx, "get_upload_url", in.Site, rbac.Publish)
		if err != nil {
			return uploadURLOut{}, err
		}
		dep, err := env.deployment(ctx, a.site.ID, in.DeploymentID)
		if err != nil {
			return uploadURLOut{}, err
		}
		if dep.State != "open" {
			return uploadURLOut{}, deploymentClosed(in.DeploymentID)
		}
		g := token.UploadGrant{DeploymentID: dep.ID, SiteID: dep.SiteID, Expires: time.Now().Add(upload.GrantTTL)}
		return uploadURLOut{
			UploadURL: upload.URL(env.Deps.Config, env.Signer, g),
			Method:    "PUT",
			ExpiresAt: ts(g.Expires),
			MaxBytes:  sites.MaxArchiveSize,
			Formats:   []string{"tar.gz", "zip"},
			Example:   "curl --fail-with-body -T site.tar.gz '<upload_url>'",
		}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "finalize_deployment",
		Description: "Publish an open staged deployment as the site's new current version, atomically. In merge mode its files are applied on top of whatever version is current right now. If the deployment has no files, or a merge would leave the site empty, nothing changes and the deployment stays open so you can add files and retry. A deployment can be finalized once. Returns the new version and the site URL.",
		Annotations: mutating("Finalize deployment", true, false),
	}, patches(sitePatch, uuidPatch("deployment_id")), func(ctx context.Context, in deploymentIn) (publishOut, error) {
		a, err := env.site(ctx, "finalize_deployment", in.Site, rbac.Publish)
		if err != nil {
			return publishOut{}, err
		}
		dep, err := env.deployment(ctx, a.site.ID, in.DeploymentID)
		if err != nil {
			return publishOut{}, err
		}
		v, err := env.Sites.Finalize(ctx, dep.ID)
		if err != nil {
			return publishOut{}, deploymentErr(err, in.DeploymentID, in.Site)
		}
		return publishOut{Site: a.site.ID, URL: env.siteURL(a.site.ID), Version: toVersionOut(v)}, nil
	})
}
