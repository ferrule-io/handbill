package sharing

import "github.com/mjquinlan2000/agent-dash/internal/mail"

// SetMailer replaces the SMTP sender in tests.
func (s *Service) SetMailer(m mail.Sender) { s.mail = m }
