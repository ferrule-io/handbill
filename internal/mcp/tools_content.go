package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mjquinlan2000/agent-dash/internal/rbac"
	"github.com/mjquinlan2000/agent-dash/internal/sites"
)

func init() { toolsets = append(toolsets, addContentTools) }

type fileInfoOut struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
}

type listFilesOut struct {
	Site      string        `json:"site"`
	VersionID *string       `json:"version_id"`
	Files     []fileInfoOut `json:"files"`
}

type readFileIn struct {
	Site string `json:"site" jsonschema:"The site id."`
	Path string `json:"path" jsonschema:"The file's path as list_files shows it, e.g. index.html."`
}

type readFileOut struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Encoding    string `json:"encoding"`
	Content     string `json:"content"`
}

type listVersionsIn struct {
	Site  string `json:"site" jsonschema:"The site id."`
	Limit int    `json:"limit,omitempty" jsonschema:"How many versions to return, newest first (default 50)."`
}

type versionEntry struct {
	ID          string  `json:"id"`
	CreatedAt   string  `json:"created_at"`
	PublishedBy *string `json:"published_by"`
	FileCount   int     `json:"file_count"`
	Size        int64   `json:"size"`
	Current     bool    `json:"current"`
}

type listVersionsOut struct {
	Site             string         `json:"site"`
	CurrentVersionID *string        `json:"current_version_id"`
	Total            int            `json:"total"`
	Versions         []versionEntry `json:"versions"`
}

type rollbackIn struct {
	Site      string `json:"site" jsonschema:"The site id."`
	VersionID string `json:"version_id" jsonschema:"The version to make current, from list_versions."`
}

type rollbackOut struct {
	Site             string `json:"site"`
	URL              string `json:"url"`
	CurrentVersionID string `json:"current_version_id"`
}

func currentVersionID(s sites.Site) *string {
	if !s.CurrentVersionID.Valid {
		return nil
	}
	id := s.CurrentVersionID.UUID.String()
	return &id
}

// textType reports whether a content type is returned as utf8 text.
func textType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch mt {
	case "application/json", "application/javascript", "application/xml", "image/svg+xml":
		return true
	}
	return strings.HasPrefix(mt, "text/") || strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml")
}

func addContentTools(s *mcpsdk.Server, env *Env) {
	addTool(s, env, &mcpsdk.Tool{
		Name:        "list_files",
		Description: "List the files of the site's current version with path, size in bytes, content type, and SHA-256. Use it before a merge publish to see what exists, or to check what viewers are served. Empty when nothing is published.",
		Annotations: readOnly("List files"),
	}, sitePatch, func(ctx context.Context, in siteIn) (listFilesOut, error) {
		a, err := env.site(ctx, "list_files", in.Site, rbac.Publish)
		if err != nil {
			return listFilesOut{}, err
		}
		files, err := env.Sites.ListFiles(ctx, a.site.ID)
		if errors.Is(err, sites.ErrNotFound) {
			return listFilesOut{}, siteNotFound(in.Site)
		}
		if err != nil {
			return listFilesOut{}, err
		}
		out := listFilesOut{Site: a.site.ID, VersionID: currentVersionID(a.site), Files: make([]fileInfoOut, len(files))}
		for i, f := range files {
			out.Files[i] = fileInfoOut{Path: f.Path, Size: f.Size, ContentType: f.ContentType, SHA256: f.SHA256}
		}
		return out, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "read_file",
		Description: "Read one file of the site's current version, by the path list_files shows (e.g. 'index.html'). Text files come back with encoding 'utf8', anything else as 'base64'. Files over 1 MB can't be read with this tool.",
		Annotations: readOnly("Read file"),
	}, patches(sitePatch, func(s *jsonschema.Schema) {
		p := prop(s, "path")
		p.MinLength, p.MaxLength = new(1), new(sites.MaxPathLen)
	}), func(ctx context.Context, in readFileIn) (readFileOut, error) {
		a, err := env.site(ctx, "read_file", in.Site, rbac.Publish)
		if err != nil {
			return readFileOut{}, err
		}
		obj, fi, err := env.Sites.OpenFile(ctx, a.site.ID, in.Path)
		if errors.Is(err, sites.ErrNotFound) {
			return readFileOut{}, toolErrorf("file %q was not found in the current version of site %s; see list_files", in.Path, a.site.ID)
		}
		if err != nil {
			return readFileOut{}, err
		}
		defer obj.Body.Close()
		if fi.Size > maxReadBytes {
			return readFileOut{}, toolErrorf("%s is %d bytes; read_file returns files up to 1 MiB", fi.Path, fi.Size)
		}
		body, err := io.ReadAll(io.LimitReader(obj.Body, maxReadBytes+1))
		if err != nil {
			return readFileOut{}, err
		}
		out := readFileOut{Path: fi.Path, ContentType: fi.ContentType, Size: fi.Size, SHA256: fi.SHA256}
		if textType(fi.ContentType) && utf8.Valid(body) {
			out.Encoding, out.Content = "utf8", string(body)
		} else {
			out.Encoding, out.Content = "base64", base64.StdEncoding.EncodeToString(body)
		}
		return out, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "list_versions",
		Description: "List the site's published versions, newest first: version id, publish time, publisher's email, file count, total size, and which one is current. Every publish is kept indefinitely; pass a version id to rollback_site to restore it.",
		Annotations: readOnly("List versions"),
	}, patches(sitePatch, func(s *jsonschema.Schema) {
		l := prop(s, "limit")
		l.Minimum, l.Maximum = new(1.0), new(float64(maxVersionLimit))
		l.Default = []byte("50")
	}), func(ctx context.Context, in listVersionsIn) (listVersionsOut, error) {
		a, err := env.site(ctx, "list_versions", in.Site, rbac.Publish)
		if err != nil {
			return listVersionsOut{}, err
		}
		vs, err := env.Sites.ListVersions(ctx, a.site.ID)
		if err != nil {
			return listVersionsOut{}, err
		}
		limit := in.Limit
		if limit <= 0 {
			limit = defaultVersionLimit
		}
		out := listVersionsOut{Site: a.site.ID, CurrentVersionID: currentVersionID(a.site), Total: len(vs), Versions: []versionEntry{}}
		for _, v := range vs[:min(limit, len(vs))] {
			out.Versions = append(out.Versions, versionEntry{
				ID: v.ID.String(), CreatedAt: ts(v.CreatedAt), PublishedBy: publisher(v), FileCount: v.FileCount, Size: v.Size,
				Current: a.site.CurrentVersionID.Valid && a.site.CurrentVersionID.UUID == v.ID,
			})
		}
		return out, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "rollback_site",
		Description: "Make an earlier version from list_versions the site's current version again. The URL serves it immediately. Nothing is deleted, so you can roll forward later by passing a newer version id.",
		Annotations: mutating("Roll back site", false, true),
	}, patches(sitePatch, uuidPatch("version_id")), func(ctx context.Context, in rollbackIn) (rollbackOut, error) {
		a, err := env.site(ctx, "rollback_site", in.Site, rbac.Publish)
		if err != nil {
			return rollbackOut{}, err
		}
		id, err := uuid.Parse(in.VersionID)
		if err != nil {
			return rollbackOut{}, versionNotFound(in.VersionID, in.Site)
		}
		if err := env.Sites.Rollback(ctx, a.site.ID, id); errors.Is(err, sites.ErrNotFound) {
			return rollbackOut{}, versionNotFound(in.VersionID, in.Site)
		} else if err != nil {
			return rollbackOut{}, err
		}
		return rollbackOut{Site: a.site.ID, URL: env.siteURL(a.site.ID), CurrentVersionID: id.String()}, nil
	})
}
