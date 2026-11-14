package mcp

import (
	"context"
	"errors"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ferrule-io/handbill/internal/rbac"
	"github.com/ferrule-io/handbill/internal/sharing"
	"github.com/ferrule-io/handbill/internal/sites"
)

// Sharing tools (docs/design.md#sharing): roles are checked by
// sharing.Service; errors here translate its sentinels into messages the
// model can act on.

func init() { toolsets = append(toolsets, addSharingTools) }

type memberOut struct {
	UserID        string  `json:"user_id"`
	Email         *string `json:"email"`
	EmailVerified bool    `json:"email_verified"`
	Role          string  `json:"role"`
	You           bool    `json:"you"`
	Since         string  `json:"since"`
}

type inviteOut struct {
	InviteID  string  `json:"invite_id"`
	Email     string  `json:"email"`
	Role      string  `json:"role"`
	InvitedBy *string `json:"invited_by"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt string  `json:"expires_at"`
}

type listMembersOut struct {
	Site           string      `json:"site"`
	Members        []memberOut `json:"members"`
	PendingInvites []inviteOut `json:"pending_invites"`
}

type shareIn struct {
	Site   string `json:"site" jsonschema:"The site id."`
	Email  string `json:"email" jsonschema:"The invitee's email address."`
	Role   string `json:"role" jsonschema:"viewer: can open the site. manager: can also publish and invite viewers (owner only)."`
	Notify bool   `json:"notify,omitempty" jsonschema:"Also email the link to the invitee when the server has SMTP configured and your email address is verified. Default false."`
}

type shareOut struct {
	Site       string `json:"site"`
	InviteID   string `json:"invite_id"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	InviteLink string `json:"invite_link"`
	Granted    bool   `json:"granted"`
	Emailed    bool   `json:"emailed"`
	// EmailNotSentReason is set when notify was requested but not sent.
	EmailNotSentReason sharing.NotSentReason `json:"email_not_sent_reason,omitempty"`
	ExpiresAt          string                `json:"expires_at"`
	Note               string                `json:"note"`
}

type setRoleIn struct {
	Site   string `json:"site" jsonschema:"The site id."`
	UserID string `json:"user_id" jsonschema:"The member's user_id from list_members."`
	Role   string `json:"role" jsonschema:"The new role: viewer or manager."`
}

type setRoleOut struct {
	Site   string `json:"site"`
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type revokeIn struct {
	Site string `json:"site" jsonschema:"The site id."`
	ID   string `json:"id" jsonschema:"A member's user_id or a pending invite's invite_id, from list_members."`
}

type revokeOut struct {
	Site    string `json:"site"`
	Revoked string `json:"revoked"`
}

type publicLinkIn struct {
	Site string `json:"site" jsonschema:"The site id."`
	Op   string `json:"op" jsonschema:"enable: create the link. rotate: replace it (the old link and everyone using it stop working). disable: turn it off."`
}

type publicLinkOut struct {
	Site    string  `json:"site"`
	Enabled bool    `json:"enabled"`
	Link    *string `json:"link"`
	Note    string  `json:"note"`
}

type transferIn struct {
	Site   string `json:"site" jsonschema:"The site id."`
	Email  string `json:"email" jsonschema:"The new owner's email address."`
	Notify bool   `json:"notify,omitempty" jsonschema:"Also email the link to them when the server has SMTP configured and your email address is verified. Default false."`
}

type transferOut struct {
	Site               string                `json:"site"`
	Email              string                `json:"email"`
	AcceptanceLink     string                `json:"acceptance_link"`
	Emailed            bool                  `json:"emailed"`
	EmailNotSentReason sharing.NotSentReason `json:"email_not_sent_reason,omitempty"`
	ExpiresAt          string                `json:"expires_at"`
	Note               string                `json:"note"`
}

// notSentNote tells the model why a requested email was not sent and that
// the link (named by its output field) is its to pass on.
func notSentNote(r sharing.NotSentReason, field string) string {
	why := "Emailing it failed"
	switch r {
	case sharing.NotSentMailDisabled:
		why = "This server cannot send email"
	case sharing.NotSentSenderUnverified:
		why = "Not emailed because your email address is not verified (verify it in Handbill's settings)"
	}
	return why + ": give " + field + " to them yourself; it works once."
}

func enumPatch(name string, values ...any) func(*jsonschema.Schema) {
	return func(s *jsonschema.Schema) { prop(s, name).Enum = values }
}

func emailPatch(s *jsonschema.Schema) {
	p := prop(s, "email")
	p.MinLength, p.MaxLength = new(3), new(254)
}

// sharingErr maps sharing.Service errors for tool on site to tool errors.
func (env *Env) sharingErr(ctx context.Context, tool, siteID string, need rbac.Role, err error) error {
	var ie *sites.InputError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ie):
		return toolErrorf("%s", ie.Msg)
	case errors.Is(err, sharing.ErrNotFound):
		return siteNotFound(siteID)
	case errors.Is(err, sharing.ErrForbidden):
		role := "none"
		if p, perr := principal(ctx); perr == nil {
			if r, rerr := env.Sites.Role(ctx, siteID, p.UserID); rerr == nil && r != "" {
				role = string(r)
			}
		}
		if role == string(rbac.Manager) {
			need = rbac.Owner // managers are refused only owner actions
		}
		return toolErrorf("your role on site %s is %s: %s needs %s", siteID, role, tool, need)
	case errors.Is(err, sharing.ErrNoSuchMember):
		return toolErrorf("no member or pending invite with that id on site %s: see list_members", siteID)
	case errors.Is(err, sharing.ErrAlreadyMember):
		return toolErrorf("that address already has access to site %s: use set_member_role to change its role", siteID)
	case errors.Is(err, sharing.ErrOwner):
		return toolErrorf("the owner's access only changes by transfer_ownership")
	case errors.Is(err, sharing.ErrPublicLinkEnabled):
		return toolErrorf("the public link of site %s is already on and can't be shown again: use op rotate for a new link", siteID)
	case errors.Is(err, sharing.ErrPublicLinkDisabled):
		return toolErrorf("the public link of site %s is off: use op enable", siteID)
	}
	return err
}

func parseID(id string) (uuid.UUID, bool) {
	u, err := uuid.Parse(id)
	return u, err == nil
}

func addSharingTools(s *mcpsdk.Server, env *Env) {
	addTool(s, env, &mcpsdk.Tool{
		Name: "list_members",
		Description: "List who has access to a site: members with their role (owner, manager, viewer) and whether their email address is verified, and pending invites that nobody has accepted yet. " +
			"Use the user_id or invite_id with set_member_role or revoke_access. Invite links are never listed: share_site again to get a fresh one.",
		Annotations: readOnly("List members"),
	}, sitePatch, func(ctx context.Context, in siteIn) (listMembersOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return listMembersOut{}, err
		}
		ms, is, err := env.Sharing.Members(ctx, in.Site, p.UserID)
		if err != nil {
			return listMembersOut{}, env.sharingErr(ctx, "list_members", in.Site, rbac.Manager, err)
		}
		out := listMembersOut{Site: in.Site, Members: []memberOut{}, PendingInvites: []inviteOut{}}
		for _, m := range ms {
			mo := memberOut{UserID: m.UserID.String(), EmailVerified: m.EmailVerified, Role: string(m.Role), You: m.UserID == p.UserID, Since: ts(m.Since)}
			if m.Email != "" {
				mo.Email = &m.Email
			}
			out.Members = append(out.Members, mo)
		}
		for _, v := range is {
			io := inviteOut{InviteID: v.ID.String(), Email: v.Email, Role: string(v.Role), CreatedAt: ts(v.CreatedAt), ExpiresAt: ts(v.ExpiresAt)}
			if v.InvitedBy != "" {
				io.InvitedBy = &v.InvitedBy
			}
			out.PendingInvites = append(out.PendingInvites, io)
		}
		return out, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name: "share_site",
		Description: "Invite someone by email to view (viewer) or help manage (manager) a site. Always returns invite_link: give it to the user to send, " +
			"or set notify to also email it (emailed says whether it did; email_not_sent_reason says why not: mail_disabled, sender_unverified when your own address is not verified, or send_failed). " +
			"The link works once, for whoever opens it and signs in, " +
			"until expires_at. If an account already holds that address verified, it gets access immediately (granted: true). Managers may invite viewers; only the owner may invite managers.",
		Annotations: mutating("Share site", false, false),
	}, patches(sitePatch, emailPatch, enumPatch("role", "viewer", "manager")), func(ctx context.Context, in shareIn) (shareOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return shareOut{}, err
		}
		need := rbac.Manager
		if in.Role == string(rbac.Manager) {
			need = rbac.Owner
		}
		inv, err := env.Sharing.Invite(ctx, in.Site, p.UserID, in.Email, rbac.Role(in.Role), in.Notify)
		if err != nil {
			return shareOut{}, env.sharingErr(ctx, "share_site", in.Site, need, err)
		}
		note := "Give invite_link to the invitee; it works once."
		switch {
		case inv.Granted:
			note = "This address's account already has access now; the link just leads them to the site."
		case inv.Emailed:
			note = "The link was also emailed to the invitee; it works once."
		case inv.NotEmailedReason != "":
			note = notSentNote(inv.NotEmailedReason, "invite_link")
		}
		return shareOut{
			Site: in.Site, InviteID: inv.ID.String(), Email: inv.Email, Role: string(inv.Role), InviteLink: inv.Link,
			Granted: inv.Granted, Emailed: inv.Emailed, EmailNotSentReason: inv.NotEmailedReason, ExpiresAt: ts(inv.ExpiresAt), Note: note,
		}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name:        "set_member_role",
		Description: "Change a member between viewer and manager (owner only). Takes effect on their next request. The owner's own role changes only through transfer_ownership.",
		Annotations: mutating("Set member role", false, true),
	}, patches(sitePatch, uuidPatch("user_id"), enumPatch("role", "viewer", "manager")), func(ctx context.Context, in setRoleIn) (setRoleOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return setRoleOut{}, err
		}
		member, _ := parseID(in.UserID)
		if err := env.Sharing.SetRole(ctx, in.Site, p.UserID, member, rbac.Role(in.Role)); err != nil {
			return setRoleOut{}, env.sharingErr(ctx, "set_member_role", in.Site, rbac.Owner, err)
		}
		return setRoleOut{Site: in.Site, UserID: in.UserID, Role: in.Role}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name: "revoke_access",
		Description: "Remove a member from a site, or revoke a pending invite, by the user_id or invite_id from list_members. A removed member loses access on their next page load. " +
			"Managers may remove viewers and viewer invites; only the owner may remove managers. The owner cannot be removed.",
		Annotations: mutating("Revoke access", true, false),
	}, patches(sitePatch, uuidPatch("id")), func(ctx context.Context, in revokeIn) (revokeOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return revokeOut{}, err
		}
		id, _ := parseID(in.ID)
		if err := env.Sharing.Revoke(ctx, in.Site, p.UserID, id); err != nil {
			return revokeOut{}, env.sharingErr(ctx, "revoke_access", in.Site, rbac.Manager, err)
		}
		return revokeOut{Site: in.Site, Revoked: in.ID}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name: "set_public_link",
		Description: "Turn \"anyone with the link\" access on or off, or replace the link (owner only). Visitors with the link (https://<site>/_auth/share#token=…) need no account. " +
			"enable and rotate return the link; it is shown only in this response and cannot be retrieved later (rotate for a new one). " +
			"rotate and disable cut off everyone using the previous link immediately.",
		Annotations: mutating("Set public link", true, false),
	}, patches(sitePatch, enumPatch("op", sharing.LinkEnable, sharing.LinkDisable, sharing.LinkRotate)), func(ctx context.Context, in publicLinkIn) (publicLinkOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return publicLinkOut{}, err
		}
		link, err := env.Sharing.SetPublicLink(ctx, in.Site, p.UserID, in.Op)
		if err != nil {
			return publicLinkOut{}, env.sharingErr(ctx, "set_public_link", in.Site, rbac.Owner, err)
		}
		if in.Op == sharing.LinkDisable {
			return publicLinkOut{Site: in.Site, Enabled: false, Note: "The public link is off; only members can view the site."}, nil
		}
		return publicLinkOut{Site: in.Site, Enabled: true, Link: &link,
			Note: "This link is shown only now and cannot be retrieved again: give it to the user. Anyone who has it can view the site."}, nil
	})

	addTool(s, env, &mcpsdk.Tool{
		Name: "transfer_ownership",
		Description: "Offer ownership of a site to an email address (owner only) and return acceptance_link. Nothing changes until someone opens the link and accepts while signed in: " +
			"they become the owner and the current owner becomes a manager. Starting a new transfer replaces a pending one. Confirm with the user first. " +
			"With notify, emailed and email_not_sent_reason say whether it was emailed, as for share_site.",
		Annotations: mutating("Transfer ownership", true, false),
	}, patches(sitePatch, emailPatch), func(ctx context.Context, in transferIn) (transferOut, error) {
		p, err := principal(ctx)
		if err != nil {
			return transferOut{}, err
		}
		tr, err := env.Sharing.StartTransfer(ctx, in.Site, p.UserID, in.Email, in.Notify)
		if err != nil {
			return transferOut{}, env.sharingErr(ctx, "transfer_ownership", in.Site, rbac.Owner, err)
		}
		note := "Give acceptance_link to the new owner; it works once."
		switch {
		case tr.Emailed:
			note = "The link was also emailed to them; it works once."
		case tr.NotEmailedReason != "":
			note = notSentNote(tr.NotEmailedReason, "acceptance_link")
		}
		return transferOut{
			Site: in.Site, Email: tr.Email, AcceptanceLink: tr.Link, Emailed: tr.Emailed,
			EmailNotSentReason: tr.NotEmailedReason, ExpiresAt: ts(tr.ExpiresAt), Note: note,
		}, nil
	})
}
