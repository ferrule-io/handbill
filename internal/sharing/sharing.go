// Package sharing is who may see and manage a site (docs/design.md#sharing,
// #roles, #public-links): members and their roles, invites, ownership
// transfers, and the public link. Unlike internal/sites, this service checks
// roles itself with rbac.Can, so every surface (the UI's /app-api, MCP
// tools) enforces the same table by calling it.
//
// Every link it issues carries a fresh random token that exists in
// plaintext only in the return value; the database keeps its SHA-256.
package sharing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/identity"
	"github.com/mjquinlan2000/agent-dash/internal/mail"
	"github.com/mjquinlan2000/agent-dash/internal/rbac"
	"github.com/mjquinlan2000/agent-dash/internal/server"
	"github.com/mjquinlan2000/agent-dash/internal/siteid"
	"github.com/mjquinlan2000/agent-dash/internal/sites"
	"github.com/mjquinlan2000/agent-dash/internal/token"
)

var (
	// ErrNotFound: the site does not exist or the caller has no role on
	// it (the same answer either way).
	ErrNotFound = errors.New("sharing: site not found")
	// ErrForbidden: the caller's role does not allow the action.
	ErrForbidden = errors.New("sharing: role does not allow that")
	// ErrNoSuchMember: the member, pending invite, or transfer is gone.
	ErrNoSuchMember = errors.New("sharing: no such member or pending invite")
	// ErrAlreadyMember: the invited address's verified holder already has
	// a role; change it with SetRole instead.
	ErrAlreadyMember = errors.New("sharing: already a member")
	// ErrOwner: the owner's membership cannot be removed or changed except
	// by transferring ownership.
	ErrOwner = errors.New("sharing: the owner's role only changes by transfer")
	// ErrLinkUnusable: an invite or transfer link is unknown, already used
	// by another account, revoked, or expired.
	ErrLinkUnusable = errors.New("sharing: link is no longer valid")
	// ErrAlreadyOwner: the account accepting a transfer already owns the site.
	ErrAlreadyOwner = errors.New("sharing: already the owner")
	// ErrPublicLinkEnabled: enable on a link that is on (rotate instead).
	ErrPublicLinkEnabled = errors.New("sharing: public link is already enabled")
	// ErrPublicLinkDisabled: rotate on a link that is off (enable instead).
	ErrPublicLinkDisabled = errors.New("sharing: public link is not enabled")
)

func inputErrorf(format string, args ...any) error {
	return &sites.InputError{Msg: fmt.Sprintf(format, args...)}
}

// Service is stateless over the pool; construct one per module.
type Service struct {
	db   *pgxpool.Pool
	cfg  *config.Config
	log  *slog.Logger
	mail mail.Sender
}

// NewService returns a Service over d.DB, sending notifications through
// d.Config.SMTP when it is configured.
func NewService(d *server.Deps) *Service {
	return &Service{db: d.DB, cfg: d.Config, log: d.Logger, mail: mail.New(d.Config.SMTP)}
}

// MailEnabled reports whether invites and transfers can be emailed.
func (s *Service) MailEnabled() bool { return s.mail.Enabled() }

// --- Roles ------------------------------------------------------------------

// authorize locks the site row (serializing membership changes per site)
// and checks that by may take action. Callers run it first in a transaction.
func authorize(ctx context.Context, q *db.Queries, siteID string, by uuid.UUID, action rbac.Action) (db.Site, rbac.Role, error) {
	if !siteid.Valid(siteID) {
		return db.Site{}, "", ErrNotFound
	}
	site, err := q.GetSiteForUpdate(ctx, siteID)
	if err != nil {
		return db.Site{}, "", notFound(err, ErrNotFound)
	}
	role, err := memberRole(ctx, q, siteID, by)
	if err != nil {
		return db.Site{}, "", err
	}
	if role == "" {
		return db.Site{}, "", ErrNotFound
	}
	if !rbac.Can(role, action) {
		return db.Site{}, "", ErrForbidden
	}
	return site, role, nil
}

// authorizeRead checks action without locking, for listings.
func (s *Service) authorizeRead(ctx context.Context, siteID string, by uuid.UUID, action rbac.Action) error {
	if !siteid.Valid(siteID) {
		return ErrNotFound
	}
	role, err := memberRole(ctx, db.New(s.db), siteID, by)
	if err != nil {
		return err
	}
	if role == "" {
		return ErrNotFound
	}
	if !rbac.Can(role, action) {
		return ErrForbidden
	}
	return nil
}

// memberRole is user's role on a site, or "" when they have none.
func memberRole(ctx context.Context, q *db.Queries, siteID string, user uuid.UUID) (rbac.Role, error) {
	r, err := q.GetMemberRole(ctx, db.GetMemberRoleParams{SiteID: siteID, UserID: user})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("sharing: role: %w", err)
	}
	return rbac.Role(r), nil
}

// manageAction is the action needed to grant, change, or remove role.
func manageAction(role rbac.Role) rbac.Action {
	if role == rbac.Viewer {
		return rbac.ManageViewers
	}
	return rbac.ManageManagers
}

func grantableRole(role rbac.Role) error {
	if role != rbac.Viewer && role != rbac.Manager {
		return inputErrorf("role must be viewer or manager")
	}
	return nil
}

// --- Members ----------------------------------------------------------------

// Member is one account's role on a site.
type Member struct {
	UserID uuid.UUID
	Email  string // primary email; "" for an account without one
	Role   rbac.Role
	Since  time.Time
}

// PendingInvite is an invite that has not been accepted, revoked, or expired.
type PendingInvite struct {
	ID        uuid.UUID
	Email     string
	Role      rbac.Role
	InvitedBy string // inviter's primary email; "" when unknown
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Members lists a site's members (owner, managers, viewers) and pending
// invites, for managers and the owner. Invite tokens are never returned.
func (s *Service) Members(ctx context.Context, siteID string, by uuid.UUID) ([]Member, []PendingInvite, error) {
	if err := s.authorizeRead(ctx, siteID, by, rbac.ManageViewers); err != nil {
		return nil, nil, err
	}
	q := db.New(s.db)
	rows, err := q.ListMembers(ctx, siteID)
	if err != nil {
		return nil, nil, fmt.Errorf("sharing: members: %w", err)
	}
	members := make([]Member, len(rows))
	for i, r := range rows {
		members[i] = Member{UserID: r.UserID, Email: deref(r.Email), Role: rbac.Role(r.Role), Since: r.CreatedAt}
	}
	irows, err := q.ListPendingInvites(ctx, siteID)
	if err != nil {
		return nil, nil, fmt.Errorf("sharing: invites: %w", err)
	}
	invites := make([]PendingInvite, len(irows))
	for i, r := range irows {
		invites[i] = PendingInvite{
			ID: r.ID, Email: r.Email, Role: rbac.Role(r.Role), InvitedBy: deref(r.InvitedByEmail),
			CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		}
	}
	return members, invites, nil
}

// SetRole moves a member between viewer and manager (owner only). The
// owner's own role changes only through a transfer.
func (s *Service) SetRole(ctx context.Context, siteID string, by, member uuid.UUID, role rbac.Role) error {
	if err := grantableRole(role); err != nil {
		return err
	}
	return db.InTx(ctx, s.db, func(q *db.Queries) error {
		if _, _, err := authorize(ctx, q, siteID, by, rbac.ManageManagers); err != nil {
			return err
		}
		current, err := memberRole(ctx, q, siteID, member)
		switch {
		case err != nil:
			return err
		case current == "":
			return ErrNoSuchMember
		case current == rbac.Owner:
			return ErrOwner
		}
		_, err = q.SetMemberRole(ctx, db.SetMemberRoleParams{Role: string(role), SiteID: siteID, UserID: member})
		return err
	})
}

// Revoke removes a member or revokes a pending invite, by user id or
// invite id. Managers may remove viewers and viewer invites; only the owner
// may remove managers. The owner cannot be removed. It takes effect on the
// member's next request, on the site host and in the app.
func (s *Service) Revoke(ctx context.Context, siteID string, by, memberOrInvite uuid.UUID) error {
	return db.InTx(ctx, s.db, func(q *db.Queries) error {
		_, byRole, err := authorize(ctx, q, siteID, by, rbac.ManageViewers)
		if err != nil {
			return err
		}
		target, err := memberRole(ctx, q, siteID, memberOrInvite)
		if err != nil {
			return err
		}
		if target != "" {
			if target == rbac.Owner {
				return ErrOwner
			}
			if !rbac.Can(byRole, manageAction(target)) {
				return ErrForbidden
			}
			_, err := q.DeleteMembership(ctx, db.DeleteMembershipParams{SiteID: siteID, UserID: memberOrInvite})
			return err
		}
		inv, err := q.GetPendingInvite(ctx, db.GetPendingInviteParams{ID: memberOrInvite, SiteID: siteID})
		if err != nil {
			return notFound(err, ErrNoSuchMember)
		}
		if !rbac.Can(byRole, manageAction(rbac.Role(inv.Role))) {
			return ErrForbidden
		}
		_, err = q.RevokeInvite(ctx, db.RevokeInviteParams{ID: inv.ID, SiteID: siteID})
		return err
	})
}

// --- Invites ----------------------------------------------------------------

// Invitation is the result of Invite.
type Invitation struct {
	ID        uuid.UUID
	Email     string
	Role      rbac.Role
	ExpiresAt time.Time
	// Link is APP_ORIGIN/invite#token=<token>, returned on every invite.
	Link string
	// Granted: the address's verified holder got the role immediately; the
	// link is then already used, and opening it while signed in to that
	// account just leads to the site.
	Granted bool
	// Emailed: notify was set, SMTP is configured, and the message was
	// handed to the mail server.
	Emailed bool
}

// Invite invites email to a site as viewer or manager and returns the
// link. Managers may invite viewers; only the owner may invite managers.
//
// Rules: inviting one of your own addresses is rejected; an address whose
// verified holder already has a role is rejected with ErrAlreadyMember
// (roles change with SetRole); re-inviting an address with a pending invite
// revokes the old one, so only the newest link works. When an account
// holds the address verified, it gets the role at once (verified-email
// auto-binding); otherwise the role goes to whichever account accepts the
// link.
func (s *Service) Invite(ctx context.Context, siteID string, by uuid.UUID, email string, role rbac.Role, notify bool) (Invitation, error) {
	if err := grantableRole(role); err != nil {
		return Invitation{}, err
	}
	addr, ok := normalizeEmail(email)
	if !ok {
		return Invitation{}, inputErrorf("enter a valid email address, like name@example.com")
	}
	plaintext, hash := token.New()
	var (
		inv            Invitation
		title, byEmail string
	)
	err := db.InTx(ctx, s.db, func(q *db.Queries) error {
		site, byRole, err := authorize(ctx, q, siteID, by, rbac.ManageViewers)
		if err != nil {
			return err
		}
		if !rbac.Can(byRole, manageAction(role)) {
			return ErrForbidden
		}
		own, err := ownsAddress(ctx, q, by, addr)
		if err != nil {
			return err
		}
		if own {
			return inputErrorf("that is your own address")
		}
		holder, err := q.GetVerifiedEmailHolder(ctx, addr)
		hasHolder := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if hasHolder {
			r, err := memberRole(ctx, q, siteID, holder)
			if err != nil {
				return err
			}
			if r != "" {
				return ErrAlreadyMember
			}
		}
		if _, err := q.RevokePendingInvitesForEmail(ctx, db.RevokePendingInvitesForEmailParams{SiteID: siteID, Email: addr}); err != nil {
			return err
		}
		row, err := q.CreateInvite(ctx, db.CreateInviteParams{
			ID: newID(), TokenHash: hash, SiteID: siteID, Email: addr, Role: string(role),
			InvitedBy: uuid.NullUUID{UUID: by, Valid: true}, ExpiresAt: time.Now().Add(s.cfg.InviteTTL),
		})
		if err != nil {
			return err
		}
		if hasHolder {
			if err := q.GrantMembership(ctx, db.GrantMembershipParams{SiteID: siteID, UserID: holder, Role: string(role)}); err != nil {
				return err
			}
			if err := q.MarkInviteConsumed(ctx, db.MarkInviteConsumedParams{ID: row.ID, UserID: uuid.NullUUID{UUID: holder, Valid: true}}); err != nil {
				return err
			}
		}
		inv = Invitation{ID: row.ID, Email: addr, Role: role, ExpiresAt: row.ExpiresAt, Granted: hasHolder}
		title = site.Title
		byEmail, err = primaryEmail(ctx, q, by)
		return err
	})
	if err != nil {
		return Invitation{}, err
	}
	inv.Link = s.cfg.AppOrigin.String() + "/invite#token=" + plaintext
	if notify && s.mail.Enabled() {
		inv.Emailed = s.send(ctx, inviteMessage(addr, byEmail, title, role, inv.Link, inv.ExpiresAt, inv.Granted))
	}
	return inv, nil
}

// Accept redeems an invite or ownership-transfer link for user, whichever
// account that is (the link, not the address, decides), and returns the
// site id. An invite grants its role without ever lowering a higher one; a
// transfer makes user the owner and the previous owner a manager. Accepting
// a link this account already accepted (including one consumed by
// verified-email auto-binding) is a no-op that returns the site.
func (s *Service) Accept(ctx context.Context, tok string, user identity.User) (string, error) {
	if tok == "" || user.ID == uuid.Nil {
		return "", ErrLinkUnusable
	}
	hash := token.Hash(tok)
	var siteID string
	err := db.InTx(ctx, s.db, func(q *db.Queries) error {
		row, err := q.ConsumeInvite(ctx, db.ConsumeInviteParams{UserID: uuid.NullUUID{UUID: user.ID, Valid: true}, TokenHash: hash})
		if err == nil {
			siteID = row.SiteID
			return q.GrantMembership(ctx, db.GrantMembershipParams{SiteID: row.SiteID, UserID: user.ID, Role: row.Role})
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		inv, err := q.GetInviteByToken(ctx, hash)
		if err == nil {
			if inv.ConsumedBy.Valid && inv.ConsumedBy.UUID == user.ID {
				siteID = inv.SiteID
				return nil
			}
			return ErrLinkUnusable
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		siteID, err = acceptTransfer(ctx, q, hash, user.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	return siteID, nil
}

// Kind is what a link grants.
type Kind string

const (
	KindInvite   Kind = "invite"
	KindTransfer Kind = "transfer"
)

// Status is a link's state.
type Status string

const (
	StatusPending  Status = "pending"
	StatusAccepted Status = "accepted"
	StatusExpired  Status = "expired"
	StatusRevoked  Status = "revoked"
)

// Pending is what the invite page shows the holder of a link. Site and
// people details are filled in only while the link is pending, or when
// the viewer's own account accepted it.
type Pending struct {
	Kind          Kind
	Status        Status
	AcceptedByYou bool
	SiteID        string
	SiteTitle     string
	From          string    // inviter or current owner's primary email; "" when unknown
	Email         string    // the address the link was sent to
	Role          rbac.Role // the invite's role; Owner for a transfer
	ExpiresAt     time.Time
}

// Lookup describes an invite or transfer link for its holder; user is the
// signed-in account or uuid.Nil. Unknown tokens are ErrLinkUnusable.
func (s *Service) Lookup(ctx context.Context, tok string, user uuid.UUID) (Pending, error) {
	if tok == "" {
		return Pending{}, ErrLinkUnusable
	}
	hash := token.Hash(tok)
	q := db.New(s.db)
	var p Pending
	if inv, err := q.GetInviteByToken(ctx, hash); err == nil {
		p = Pending{
			Kind: KindInvite, SiteID: inv.SiteID, SiteTitle: inv.SiteTitle, From: deref(inv.InvitedByEmail),
			Email: inv.Email, Role: rbac.Role(inv.Role), ExpiresAt: inv.ExpiresAt,
			Status:        status(inv.ConsumedAt, inv.RevokedAt, inv.ExpiresAt),
			AcceptedByYou: user != uuid.Nil && inv.ConsumedBy.Valid && inv.ConsumedBy.UUID == user,
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Pending{}, err
	} else if t, err := q.GetTransferByToken(ctx, hash); err == nil {
		p = Pending{
			Kind: KindTransfer, SiteID: t.SiteID, SiteTitle: t.SiteTitle, From: deref(t.FromEmail),
			Email: t.Email, Role: rbac.Owner, ExpiresAt: t.ExpiresAt,
			Status:        status(t.ConsumedAt, t.RevokedAt, t.ExpiresAt),
			AcceptedByYou: user != uuid.Nil && t.ConsumedBy.Valid && t.ConsumedBy.UUID == user,
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		return Pending{}, ErrLinkUnusable
	} else {
		return Pending{}, err
	}
	if p.Status != StatusPending && !p.AcceptedByYou {
		p = Pending{Kind: p.Kind, Status: p.Status}
	}
	return p, nil
}

func status(consumed, revoked *time.Time, expires time.Time) Status {
	switch {
	case consumed != nil:
		return StatusAccepted
	case revoked != nil:
		return StatusRevoked
	case !expires.After(time.Now()):
		return StatusExpired
	}
	return StatusPending
}

// --- Ownership transfers ------------------------------------------------------

// Transfer is a pending ownership transfer.
type Transfer struct {
	Email     string
	CreatedAt time.Time
	ExpiresAt time.Time
	// Link is APP_ORIGIN/invite#token=<token>; set only by StartTransfer.
	Link    string
	Emailed bool
}

// StartTransfer offers the site to email (owner only) and returns the
// acceptance link, replacing any earlier pending transfer. It completes
// when an account accepts the link (Accept), with the same binding and
// INVITE_TTL rules as invites.
func (s *Service) StartTransfer(ctx context.Context, siteID string, by uuid.UUID, email string, notify bool) (Transfer, error) {
	addr, ok := normalizeEmail(email)
	if !ok {
		return Transfer{}, inputErrorf("enter a valid email address, like name@example.com")
	}
	plaintext, hash := token.New()
	var (
		tr             Transfer
		title, byEmail string
	)
	err := db.InTx(ctx, s.db, func(q *db.Queries) error {
		site, _, err := authorize(ctx, q, siteID, by, rbac.Transfer)
		if err != nil {
			return err
		}
		own, err := ownsAddress(ctx, q, by, addr)
		if err != nil {
			return err
		}
		if own {
			return inputErrorf("you already own this site; enter the new owner’s address")
		}
		if _, err := q.RevokePendingTransfers(ctx, siteID); err != nil {
			return err
		}
		row, err := q.CreateTransfer(ctx, db.CreateTransferParams{
			ID: newID(), TokenHash: hash, SiteID: siteID, Email: addr,
			FromUserID: uuid.NullUUID{UUID: by, Valid: true}, ExpiresAt: time.Now().Add(s.cfg.InviteTTL),
		})
		if err != nil {
			return err
		}
		tr = Transfer{Email: addr, CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt}
		title = site.Title
		byEmail, err = primaryEmail(ctx, q, by)
		return err
	})
	if err != nil {
		return Transfer{}, err
	}
	tr.Link = s.cfg.AppOrigin.String() + "/invite#token=" + plaintext
	if notify && s.mail.Enabled() {
		tr.Emailed = s.send(ctx, transferMessage(addr, byEmail, title, tr.Link, tr.ExpiresAt))
	}
	return tr, nil
}

// PendingTransfer returns the site's pending transfer, if any (owner only).
func (s *Service) PendingTransfer(ctx context.Context, siteID string, by uuid.UUID) (*Transfer, error) {
	if err := s.authorizeRead(ctx, siteID, by, rbac.Transfer); err != nil {
		return nil, err
	}
	t, err := db.New(s.db).GetPendingTransfer(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sharing: transfer: %w", err)
	}
	return &Transfer{Email: t.Email, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}, nil
}

// CancelTransfer revokes the site's pending transfer (owner only); it is a
// no-op when there is none.
func (s *Service) CancelTransfer(ctx context.Context, siteID string, by uuid.UUID) error {
	return db.InTx(ctx, s.db, func(q *db.Queries) error {
		if _, _, err := authorize(ctx, q, siteID, by, rbac.Transfer); err != nil {
			return err
		}
		_, err := q.RevokePendingTransfers(ctx, siteID)
		return err
	})
}

// acceptTransfer consumes a transfer and swaps the roles in q's
// transaction: the old owner is demoted to manager before the new one is
// promoted (memberships_one_owner), and sites.owner_id follows; the
// deferred sites_owner_membership check passes at commit.
func acceptTransfer(ctx context.Context, q *db.Queries, hash []byte, user uuid.UUID) (string, error) {
	row, err := q.ConsumeTransfer(ctx, db.ConsumeTransferParams{UserID: uuid.NullUUID{UUID: user, Valid: true}, TokenHash: hash})
	if errors.Is(err, pgx.ErrNoRows) {
		t, err := q.GetTransferByToken(ctx, hash)
		if err == nil && t.ConsumedBy.Valid && t.ConsumedBy.UUID == user {
			return t.SiteID, nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		return "", ErrLinkUnusable
	}
	if err != nil {
		return "", err
	}
	site, err := q.GetSiteForUpdate(ctx, row.SiteID)
	if err != nil {
		return "", notFound(err, ErrLinkUnusable)
	}
	if site.OwnerID == user {
		return "", ErrAlreadyOwner
	}
	if !row.FromUserID.Valid || row.FromUserID.UUID != site.OwnerID {
		// Ownership changed hands since this transfer was offered.
		return "", ErrLinkUnusable
	}
	if n, err := q.DemoteOwner(ctx, db.DemoteOwnerParams{SiteID: site.ID, UserID: site.OwnerID}); err != nil || n != 1 {
		return "", errors.Join(err, fmt.Errorf("sharing: demote owner of %s: %d rows", site.ID, n))
	}
	if err := q.PromoteOwner(ctx, db.PromoteOwnerParams{SiteID: site.ID, UserID: user}); err != nil {
		return "", err
	}
	if _, err := q.SetSiteOwner(ctx, db.SetSiteOwnerParams{OwnerID: user, ID: site.ID}); err != nil {
		return "", err
	}
	if _, err := q.RevokePendingTransfers(ctx, site.ID); err != nil {
		return "", err
	}
	return site.ID, nil
}

// --- Public links -----------------------------------------------------------

// Public-link operations for SetPublicLink.
const (
	LinkEnable  = "enable"
	LinkDisable = "disable"
	LinkRotate  = "rotate"
)

// PublicLink is a site's public-link state; the link itself exists only in
// the response that issued it.
type PublicLink struct {
	Enabled   bool
	UpdatedAt *time.Time // nil when never enabled
}

// PublicLinkState reports whether anyone-with-the-link access is on, for
// managers and the owner.
func (s *Service) PublicLinkState(ctx context.Context, siteID string, by uuid.UUID) (PublicLink, error) {
	if err := s.authorizeRead(ctx, siteID, by, rbac.ManageViewers); err != nil {
		return PublicLink{}, err
	}
	pl, err := db.New(s.db).GetPublicLink(ctx, siteID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublicLink{}, nil
	}
	if err != nil {
		return PublicLink{}, fmt.Errorf("sharing: public link: %w", err)
	}
	return PublicLink{Enabled: pl.Enabled, UpdatedAt: &pl.UpdatedAt}, nil
}

// SetPublicLink enables, disables, or rotates the site's public link (owner
// only) and returns the new link for enable and rotate:
// https://<id>.SITES_DOMAIN/_auth/share/<token>. Enabling a link that is
// on fails with ErrPublicLinkEnabled (its token cannot be shown again;
// rotate for a new one), rotating one that is off with
// ErrPublicLinkDisabled; disabling one that is off is a no-op. Enable
// (after a disable) and rotate bump the link generation, so cookies issued
// under an earlier link stop working on their next request.
func (s *Service) SetPublicLink(ctx context.Context, siteID string, by uuid.UUID, op string) (string, error) {
	if op != LinkEnable && op != LinkDisable && op != LinkRotate {
		return "", inputErrorf("op must be enable, disable, or rotate")
	}
	plaintext, hash := token.New()
	err := db.InTx(ctx, s.db, func(q *db.Queries) error {
		if _, _, err := authorize(ctx, q, siteID, by, rbac.ManagePublicLink); err != nil {
			return err
		}
		var err error
		switch op {
		case LinkEnable:
			_, err = q.EnablePublicLink(ctx, db.EnablePublicLinkParams{SiteID: siteID, TokenHash: hash})
			err = notFound(err, ErrPublicLinkEnabled)
		case LinkRotate:
			_, err = q.RotatePublicLink(ctx, db.RotatePublicLinkParams{SiteID: siteID, TokenHash: hash})
			err = notFound(err, ErrPublicLinkDisabled)
		case LinkDisable:
			_, err = q.DisablePublicLink(ctx, siteID)
		}
		return err
	})
	if err != nil || op == LinkDisable {
		return "", err
	}
	return s.cfg.SiteOrigin(siteID) + "/_auth/share/" + plaintext, nil
}

// --- Helpers ----------------------------------------------------------------

// ownsAddress reports whether user claims addr (verified or not).
func ownsAddress(ctx context.Context, q *db.Queries, user uuid.UUID, addr string) (bool, error) {
	_, err := q.GetUserEmail(ctx, db.GetUserEmailParams{UserID: user, Email: addr})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func primaryEmail(ctx context.Context, q *db.Queries, user uuid.UUID) (string, error) {
	es, err := q.ListUserEmails(ctx, user)
	if err != nil {
		return "", err
	}
	if len(es) > 0 && es[0].IsPrimary {
		return es[0].Email, nil
	}
	return "", nil
}

// normalizeEmail lowercases and validates a bare address (matches the
// sign-up form's rule in internal/auth).
func normalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 254 {
		return "", false
	}
	a, err := netmail.ParseAddress(s)
	if err != nil || a.Address != s || a.Name != "" {
		return "", false
	}
	return s, true
}

// notFound maps a missing row to sentinel and passes other errors through.
func notFound(err, sentinel error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return sentinel
	}
	return err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }
