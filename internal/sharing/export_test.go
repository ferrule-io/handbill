package sharing

import "github.com/ferrule-io/handbill/internal/mail"

// SetMailer replaces the SMTP sender in tests.
func (s *Service) SetMailer(m mail.Sender) { s.mail = m }
