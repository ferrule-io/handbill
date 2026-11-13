package auth

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ferrule-io/handbill/internal/appapi"
	"github.com/ferrule-io/handbill/internal/db"
	mailer "github.com/ferrule-io/handbill/internal/mail"
	"github.com/ferrule-io/handbill/internal/token"
)

// verificationTTL is how long an email verification link works.
const verificationTTL = 24 * time.Hour

// sendVerificationEmail mails a single-use link that verifies emailID. The
// token travels in the fragment of /verify-email, so it never reaches
// server logs and link scanners that fetch the page cannot consume it.
func (m *module) sendVerificationEmail(ctx context.Context, emailID uuid.UUID, address string) error {
	plain, hash := token.New()
	if _, err := m.q.CreateEmailVerification(ctx, db.CreateEmailVerificationParams{
		ID: newID(), TokenHash: hash, EmailID: emailID, ExpiresAt: time.Now().Add(verificationTTL),
	}); err != nil {
		return err
	}
	link := m.d.Config.AppOrigin.String() + "/verify-email#token=" + plain
	return m.mail.Send(ctx, mailer.Message{
		To:      address,
		Subject: "Confirm your email for handbill",
		Text: fmt.Sprintf("Confirm that %s is your address on handbill:\n\n%s\n\n"+
			"The link works once, for 24 hours. If you didn't create an account, ignore this email.\n", address, link),
		HTML: fmt.Sprintf(`<p>Confirm that <strong>%s</strong> is your address on handbill.</p>`+
			`<p><a href="%s">Confirm email</a></p>`+
			`<p style="color:#666">The link works once, for 24 hours. If you didn't create an account, ignore this email.</p>`,
			html.EscapeString(address), html.EscapeString(link)),
	})
}

func (m *module) sendVerification(w http.ResponseWriter, r *http.Request) error {
	s, err := fullSession(r)
	if err != nil {
		return err
	}
	if !m.mail.Enabled() {
		return appapi.NewError(http.StatusConflict, "mail_disabled", "This server can't send email.")
	}
	emails, err := m.q.ListUserEmails(r.Context(), s.UserID)
	if err != nil {
		return err
	}
	if len(emails) == 0 || !emails[0].IsPrimary {
		return appapi.NewError(http.StatusConflict, "no_email", "Your account has no email address.")
	}
	if emails[0].Verified {
		return appapi.NewError(http.StatusConflict, "already_verified", "Your email is already verified.")
	}
	if err := m.sendVerificationEmail(r.Context(), emails[0].ID, emails[0].Email); err != nil {
		m.d.Logger.Warn("verification email failed", "err", err)
		return appapi.NewError(http.StatusBadGateway, "mail_failed", "The email could not be sent. Try again later.")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

var errEmailTaken = appapi.NewError(http.StatusConflict, "email_taken", "Another account has already verified this email address.")

// verifyEmail consumes a verification token. Possession of the link is the
// proof, so no session is required.
func (m *module) verifyEmail(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := appapi.ReadJSON(r, &in); err != nil {
		return err
	}
	var email db.Email
	err := db.InTx(r.Context(), m.d.DB, func(q *db.Queries) error {
		id, err := q.ConsumeEmailVerification(r.Context(), token.Hash(in.Token))
		if isNoRows(err) {
			return appapi.NewError(http.StatusBadRequest, "invalid_link", "This link has expired or was already used.")
		}
		if err != nil {
			return err
		}
		if email, err = q.GetEmail(r.Context(), id); err != nil {
			return err
		}
		ok, err := markVerified(r.Context(), q, email)
		if err != nil {
			return err
		}
		if !ok {
			return errEmailTaken
		}
		return nil
	})
	if err != nil {
		return err
	}
	appapi.WriteJSON(w, http.StatusOK, map[string]string{"email": email.Email})
	return nil
}

// markVerified verifies e for its account, displacing other accounts'
// unverified claims to the address. It reports false, changing nothing,
// when another account already holds the address verified.
func markVerified(ctx context.Context, q *db.Queries, e db.Email) (bool, error) {
	if e.Verified {
		return true, nil
	}
	holder, err := q.GetVerifiedEmailHolder(ctx, e.Email)
	switch {
	case err == nil && holder != e.UserID:
		return false, nil
	case err != nil && !isNoRows(err):
		return false, err
	}
	if _, err := q.DisplaceUnverifiedEmailClaims(ctx, db.DisplaceUnverifiedEmailClaimsParams{Email: e.Email, UserID: e.UserID}); err != nil {
		return false, err
	}
	if _, err := q.MarkEmailVerified(ctx, e.ID); err != nil {
		return false, err
	}
	return true, nil
}

// claimVerifiedEmail records that userID controls address (e.g. Google said
// so): it adds the address if missing (as primary when the account has
// none) and verifies it, unless another account holds it verified.
func claimVerifiedEmail(ctx context.Context, q *db.Queries, userID uuid.UUID, address string) error {
	e, err := q.GetUserEmail(ctx, db.GetUserEmailParams{UserID: userID, Email: address})
	if isNoRows(err) {
		if holder, err := q.GetVerifiedEmailHolder(ctx, address); err == nil && holder != userID {
			return nil
		} else if err != nil && !isNoRows(err) {
			return err
		}
		hasPrimary, err := q.HasPrimaryEmail(ctx, userID)
		if err != nil {
			return err
		}
		e, err = q.CreateEmail(ctx, db.CreateEmailParams{ID: newID(), UserID: userID, Email: address, IsPrimary: !hasPrimary})
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	_, err = markVerified(ctx, q, e)
	return err
}
