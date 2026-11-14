package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/oauth"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/sharing"
	"github.com/ferrule-io/handbill/internal/sites"
	"github.com/ferrule-io/handbill/internal/sites/sitestest"
	"github.com/ferrule-io/handbill/internal/token"
)

type toolEnv struct {
	t    *testing.T
	d    *server.Deps
	env  *Env
	mail *fakeMail
}

func newToolEnv(t *testing.T) *toolEnv {
	d := sitestest.Deps(t)
	sh := sharing.NewService(d)
	fm := &fakeMail{}
	sh.SetMailer(fm)
	return &toolEnv{t: t, d: d, mail: fm, env: &Env{Deps: d, Sites: sites.NewService(d), Signer: token.NewSigner(d.Config.SecretKey), Sharing: sh}}
}

// session connects an in-memory client acting as user.
func (e *toolEnv) session(user uuid.UUID, email string) *mcpsdk.ClientSession {
	e.t.Helper()
	srv := NewServer(e.env)
	st, ct := mcpsdk.NewInMemoryTransports()
	p := oauth.Principal{UserID: user, Email: email, EmailVerified: true, ClientID: "https://client.example/meta", ClientName: "Test Agent"}
	ss, err := srv.Connect(oauth.WithPrincipal(e.t.Context(), p), st, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test"}, nil).Connect(e.t.Context(), ct, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call runs a tool, failing on protocol errors, and returns the result
// text and whether it is a tool error.
func call(t *testing.T, cs *mcpsdk.ClientSession, name string, args any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	return text, res.IsError
}

// ok runs a tool that must succeed and decodes its structured output.
func ok[T any](t *testing.T, cs *mcpsdk.ClientSession, name string, args any) T {
	t.Helper()
	text, isErr := call(t, cs, name, args)
	if isErr {
		t.Fatalf("%s(%v): %s", name, args, text)
	}
	var out T
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s: decode %q: %v", name, text, err)
	}
	return out
}

// fails runs a tool that must fail with a message containing want.
func fails(t *testing.T, cs *mcpsdk.ClientSession, name string, args any, want string) {
	t.Helper()
	text, isErr := call(t, cs, name, args)
	if !isErr || !strings.Contains(text, want) {
		t.Fatalf("%s(%v) = %q (error %v), want error containing %q", name, args, text, isErr, want)
	}
}

type roles struct {
	owner, manager, viewer, stranger *mcpsdk.ClientSession
	site                             string
}

func (e *toolEnv) roles() roles {
	e.t.Helper()
	owner := sitestest.User(e.t, e.d, "owner@example.com")
	manager := sitestest.User(e.t, e.d, "manager@example.com")
	viewer := sitestest.User(e.t, e.d, "viewer@example.com")
	stranger := sitestest.User(e.t, e.d, "stranger@example.com")
	site, err := e.env.Sites.Create(e.t.Context(), owner, "Team", "")
	if err != nil {
		e.t.Fatal(err)
	}
	sitestest.Member(e.t, e.d, site.ID, manager, "manager")
	sitestest.Member(e.t, e.d, site.ID, viewer, "viewer")
	return roles{
		owner: e.session(owner, "owner@example.com"), manager: e.session(manager, "manager@example.com"),
		viewer: e.session(viewer, "viewer@example.com"), stranger: e.session(stranger, "stranger@example.com"),
		site: site.ID,
	}
}

func TestListTools(t *testing.T) {
	e := newToolEnv(t)
	cs := e.session(sitestest.User(t, e.d, "a@example.com"), "a@example.com")
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		if a == nil || a.Title == "" || a.OpenWorldHint == nil || *a.OpenWorldHint {
			t.Errorf("%s: annotations %+v", tool.Name, a)
		}
		if !a.ReadOnlyHint && a.DestructiveHint == nil {
			t.Errorf("%s: no destructiveHint", tool.Name)
		}
		b, _ := json.Marshal(tool.InputSchema)
		var s map[string]any
		_ = json.Unmarshal(b, &s)
		if ap, ok := s["additionalProperties"]; !ok || ap == true {
			t.Errorf("%s: additionalProperties %v", tool.Name, ap)
		}
	}
	want := []string{"create_deployment", "create_site", "delete_site", "finalize_deployment", "get_site", "get_upload_url",
		"list_files", "list_members", "list_sites", "list_versions", "publish_site", "read_file", "revoke_access", "rollback_site",
		"set_member_role", "set_public_link", "share_site", "transfer_ownership", "update_site", "upload_files", "whoami"}
	slices.Sort(names)
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v", names)
	}
	// get_upload_url is not read-only: it mints a bearer-equivalent URL.
	readOnly := map[string]bool{"whoami": true, "list_sites": true, "get_site": true, "list_files": true, "read_file": true, "list_versions": true, "list_members": true}
	destructive := map[string]bool{"delete_site": true, "publish_site": true, "finalize_deployment": true,
		"revoke_access": true, "set_public_link": true, "transfer_ownership": true}
	idempotent := map[string]bool{"update_site": true, "upload_files": true, "rollback_site": true, "set_member_role": true}
	for _, tool := range res.Tools {
		a := tool.Annotations
		if a.ReadOnlyHint != readOnly[tool.Name] {
			t.Errorf("%s: readOnlyHint %v", tool.Name, a.ReadOnlyHint)
		}
		if !a.ReadOnlyHint && (*a.DestructiveHint != destructive[tool.Name] || a.IdempotentHint != idempotent[tool.Name]) {
			t.Errorf("%s: destructive %v idempotent %v", tool.Name, *a.DestructiveHint, a.IdempotentHint)
		}
	}
	// Unknown arguments are rejected.
	if _, isErr := call(t, cs, "list_sites", map[string]any{"bogus": 1}); !isErr {
		t.Error("unknown argument accepted")
	}
	if _, isErr := call(t, cs, "publish_site", map[string]any{"site": "abcdefghijkl", "mode": "replace", "files": []any{}, "notifyy": true}); !isErr {
		t.Error("unknown publish_site argument accepted")
	}
}

func TestWhoamiAndSites(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	who := ok[map[string]any](t, r.owner, "whoami", map[string]any{})
	if who["email"] != "owner@example.com" || who["client_name"] != "Test Agent" || who["email_verified"] != true {
		t.Fatalf("whoami: %v", who)
	}

	created := ok[siteOut](t, r.manager, "create_site", map[string]any{"title": "Mine", "description": "d"})
	if created.Role != "owner" || created.URL != e.d.Config.SiteOrigin(created.ID)+"/" {
		t.Fatalf("create_site: %+v", created)
	}
	fails(t, r.manager, "create_site", map[string]any{"title": "   "}, "title is required")

	list := ok[listSitesOut](t, r.manager, "list_sites", map[string]any{})
	if len(list.Sites) != 2 {
		t.Fatalf("manager list_sites: %+v", list)
	}
	if list := ok[listSitesOut](t, r.viewer, "list_sites", map[string]any{}); len(list.Sites) != 0 {
		t.Fatalf("viewer list_sites: %+v", list)
	}

	got := ok[getSiteOut](t, r.manager, "get_site", map[string]any{"site": r.site})
	if got.Role != "manager" || got.MemberCount != 3 || got.PublicLinkEnabled || got.CurrentVersion != nil {
		t.Fatalf("get_site: %+v", got)
	}
	fails(t, r.viewer, "get_site", map[string]any{"site": r.site}, "your role on site "+r.site+" is viewer; get_site needs manager")
	fails(t, r.stranger, "get_site", map[string]any{"site": r.site}, `site "`+r.site+`" was not found or is not shared with you`)
	fails(t, r.owner, "get_site", map[string]any{"site": "aaaaaaaaaaaa"}, "was not found or is not shared with you")
	if _, isErr := call(t, r.owner, "get_site", map[string]any{"site": "BAD"}); !isErr {
		t.Fatal("bad site id accepted")
	}

	fails(t, r.manager, "update_site", map[string]any{"site": r.site}, "pass title, description, or both")
	up := ok[updateSiteOut](t, r.manager, "update_site", map[string]any{"site": r.site, "title": "Renamed"})
	if up.Title != "Renamed" {
		t.Fatalf("update_site: %+v", up)
	}
	fails(t, r.viewer, "update_site", map[string]any{"site": r.site, "title": "x"}, "update_site needs manager")

	fails(t, r.manager, "delete_site", map[string]any{"site": r.site}, "your role on site "+r.site+" is manager; delete_site needs owner")
	del := ok[deleteSiteOut](t, r.owner, "delete_site", map[string]any{"site": r.site})
	if !del.Deleted {
		t.Fatalf("delete_site: %+v", del)
	}
	fails(t, r.owner, "get_site", map[string]any{"site": r.site}, "was not found")
}

func TestPublish(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	png := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	files := []map[string]any{
		{"path": "index.html", "content": "<h1>Hi</h1>"},
		{"path": "img/logo.png", "content": base64.StdEncoding.EncodeToString(png), "encoding": "base64"},
	}
	pub := ok[publishOut](t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": files})
	if pub.Version.FileCount != 2 || pub.URL != e.d.Config.SiteOrigin(r.site)+"/" {
		t.Fatalf("publish: %+v", pub)
	}
	fails(t, r.viewer, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": files}, "publish_site needs manager")
	fails(t, r.stranger, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": files}, "not shared with you")

	merged := ok[publishOut](t, r.manager, "publish_site", map[string]any{
		"site": r.site, "mode": "merge", "files": []map[string]any{{"path": "about.html", "content": "a"}}, "delete_paths": []string{"index.html"},
	})
	if merged.Version.FileCount != 2 {
		t.Fatalf("merge: %+v", merged)
	}
	fails(t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": files, "delete_paths": []string{"a"}}, "delete paths apply only to merge")
	fails(t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": []map[string]any{{"path": "x.bin", "content": "!!!", "encoding": "base64"}}}, "files[0].content is not valid base64")
	fails(t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "replace", "files": []map[string]any{{"path": "../x", "content": "x"}}}, "")
	if _, isErr := call(t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "upsert", "files": files}); !isErr {
		t.Fatal("bad mode accepted")
	}

	got := ok[readFileOut](t, r.manager, "read_file", map[string]any{"site": r.site, "path": "img/logo.png"})
	if b, _ := base64.StdEncoding.DecodeString(got.Content); got.Encoding != "base64" || !bytes.Equal(b, png) {
		t.Fatalf("binary round trip: %+v", got)
	}
	text := ok[readFileOut](t, r.manager, "read_file", map[string]any{"site": r.site, "path": "about.html"})
	if text.Encoding != "utf8" || text.Content != "a" {
		t.Fatalf("text read: %+v", text)
	}
	ok[publishOut](t, r.manager, "publish_site", map[string]any{"site": r.site, "mode": "merge", "files": []map[string]any{{"path": "big.txt", "content": strings.Repeat("x", 1<<20+1)}}})
	fails(t, r.manager, "read_file", map[string]any{"site": r.site, "path": "big.txt"}, "big.txt is 1048577 bytes; read_file returns files up to 1 MiB")
	fails(t, r.manager, "read_file", map[string]any{"site": r.site, "path": "nope.html"}, "was not found")
	fails(t, r.viewer, "read_file", map[string]any{"site": r.site, "path": "about.html"}, "read_file needs manager")

	lf := ok[listFilesOut](t, r.manager, "list_files", map[string]any{"site": r.site})
	if len(lf.Files) != 3 || lf.VersionID == nil {
		t.Fatalf("list_files: %+v", lf)
	}

	lv := ok[listVersionsOut](t, r.manager, "list_versions", map[string]any{"site": r.site, "limit": 2})
	if lv.Total != 3 || len(lv.Versions) != 2 || !lv.Versions[0].Current || lv.Versions[0].PublishedBy == nil || *lv.Versions[0].PublishedBy != "manager@example.com" {
		t.Fatalf("list_versions: %+v", lv)
	}
	if _, isErr := call(t, r.manager, "list_versions", map[string]any{"site": r.site, "limit": 501}); !isErr {
		t.Fatal("limit 501 accepted")
	}

	first := ok[listVersionsOut](t, r.manager, "list_versions", map[string]any{"site": r.site}).Versions[2].ID
	rb := ok[rollbackOut](t, r.manager, "rollback_site", map[string]any{"site": r.site, "version_id": first})
	if rb.CurrentVersionID != first {
		t.Fatalf("rollback: %+v", rb)
	}
	fails(t, r.manager, "rollback_site", map[string]any{"site": r.site, "version_id": uuid.NewString()}, "is not a version of site "+r.site+"; see list_versions")
	other := ok[siteOut](t, r.owner, "create_site", map[string]any{"title": "Other"})
	otherV := ok[publishOut](t, r.owner, "publish_site", map[string]any{"site": other.ID, "mode": "replace", "files": files}).Version.ID
	fails(t, r.owner, "rollback_site", map[string]any{"site": r.site, "version_id": otherV}, "is not a version of site")
}

func TestStagedDeployment(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	dep := ok[createDeploymentOut](t, r.manager, "create_deployment", map[string]any{"site": r.site, "mode": "replace"})
	for _, f := range []string{"index.html", "page.html"} {
		up := ok[uploadFilesOut](t, r.manager, "upload_files", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID, "files": []map[string]any{{"path": f, "content": f}}})
		if up.FilesAdded != 1 {
			t.Fatalf("upload_files: %+v", up)
		}
	}
	fails(t, r.manager, "upload_files", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID}, "pass files, delete_paths, or both")

	other := ok[siteOut](t, r.manager, "create_site", map[string]any{"title": "Other"})
	fails(t, r.manager, "upload_files", map[string]any{"site": other.ID, "deployment_id": dep.DeploymentID, "files": []map[string]any{{"path": "a", "content": "a"}}},
		"deployment "+dep.DeploymentID+" was not found on site "+other.ID)

	u := ok[uploadURLOut](t, r.manager, "get_upload_url", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID})
	parsed, err := url.Parse(u.UploadURL)
	if err != nil || u.Method != "PUT" || u.MaxBytes != config.DefaultLimits().MaxArchiveSize {
		t.Fatalf("get_upload_url: %+v", u)
	}
	g, err := e.env.Signer.ParseUploadGrant(parsed.Query().Get("grant"), time.Now())
	if err != nil || g.DeploymentID.String() != dep.DeploymentID || g.SiteID != r.site || parsed.Path != "/api/deployments/"+dep.DeploymentID+"/archive" {
		t.Fatalf("grant: %+v %v %s", g, err, parsed.Path)
	}

	fin := ok[publishOut](t, r.manager, "finalize_deployment", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID})
	if fin.Version.FileCount != 2 {
		t.Fatalf("finalize: %+v", fin)
	}
	fails(t, r.manager, "finalize_deployment", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID}, "is already finalized or abandoned: start a new one with create_deployment")
	fails(t, r.manager, "get_upload_url", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID}, "already finalized")
	fails(t, r.manager, "upload_files", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID, "files": []map[string]any{{"path": "a", "content": "a"}}}, "already finalized")
	fails(t, r.viewer, "create_deployment", map[string]any{"site": r.site, "mode": "merge"}, "create_deployment needs manager")
	fails(t, r.manager, "finalize_deployment", map[string]any{"site": r.site, "deployment_id": uuid.NewString()}, "was not found on site")
}

// Only a deployment's creator may add to, upload into, or finalize it;
// other managers and the owner are refused.
func TestDeploymentBoundToCreator(t *testing.T) {
	e := newToolEnv(t)
	r := e.roles()
	second := sitestest.User(t, e.d, "second@example.com")
	sitestest.Member(t, e.d, r.site, second, "manager")
	other := e.session(second, "second@example.com")

	dep := ok[createDeploymentOut](t, r.manager, "create_deployment", map[string]any{"site": r.site, "mode": "replace"})
	ok[uploadFilesOut](t, r.manager, "upload_files", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID, "files": []map[string]any{{"path": "index.html", "content": "mine"}}})
	notYours := "deployment " + dep.DeploymentID + " was created by another account"
	for _, cs := range []*mcpsdk.ClientSession{other, r.owner} {
		fails(t, cs, "upload_files", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID, "files": []map[string]any{{"path": "index.html", "content": "theirs"}}}, notYours)
		fails(t, cs, "get_upload_url", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID}, notYours)
		fails(t, cs, "finalize_deployment", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID}, notYours)
	}

	ok[publishOut](t, r.manager, "finalize_deployment", map[string]any{"site": r.site, "deployment_id": dep.DeploymentID})
	got := ok[readFileOut](t, r.owner, "read_file", map[string]any{"site": r.site, "path": "index.html"})
	lv := ok[listVersionsOut](t, r.owner, "list_versions", map[string]any{"site": r.site})
	if got.Content != "mine" || len(lv.Versions) != 1 || lv.Versions[0].PublishedBy == nil || *lv.Versions[0].PublishedBy != "manager@example.com" {
		t.Fatalf("published %q by %+v", got.Content, lv.Versions)
	}
}
