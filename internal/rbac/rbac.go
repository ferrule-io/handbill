// Package rbac is the per-site role model (docs/design.md#roles): which
// actions owners, managers, and viewers may take. Every surface (the UI's
// /app-api, MCP tools, the site file server) asks Can instead of comparing
// roles itself.
package rbac

// Role is a member's role on one site. The zero value means no role.
type Role string

const (
	Owner   Role = "owner"
	Manager Role = "manager"
	Viewer  Role = "viewer"
)

// Valid reports whether r is one of the three roles.
func (r Role) Valid() bool {
	switch r {
	case Owner, Manager, Viewer:
		return true
	}
	return false
}

// Action is something a member may do to a site.
type Action string

const (
	View             Action = "view"
	Publish          Action = "publish" // publish, update files, roll back, list versions and files
	EditMeta         Action = "edit_meta"
	ManageViewers    Action = "manage_viewers"
	ManageManagers   Action = "manage_managers"
	ManagePublicLink Action = "manage_public_link"
	Transfer         Action = "transfer"
	Delete           Action = "delete"
)

// table is the design's roles table: the actions each role may take.
var table = map[Role]map[Action]bool{
	Owner: {
		View: true, Publish: true, EditMeta: true, ManageViewers: true,
		ManageManagers: true, ManagePublicLink: true, Transfer: true, Delete: true,
	},
	Manager: {
		View: true, Publish: true, EditMeta: true, ManageViewers: true,
	},
	Viewer: {
		View: true,
	},
}

// Can reports whether role may take action a. An empty or unknown role may
// take no action.
func Can(role Role, a Action) bool {
	return table[role][a]
}
