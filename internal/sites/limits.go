package sites

import "github.com/ferrule-io/handbill/internal/config"

// Publishing limits come from config.Limits (MAX_FILE_SIZE, MAX_SITE_SIZE,
// MAX_SITE_FILES, MAX_ARCHIVE_SIZE, MAX_ARCHIVE_ENTRIES), documented in
// docs/design.md#configuration and docs/design.md#publishing.
const (
	// MaxPathLen is the longest file path, in bytes.
	MaxPathLen = 1024

	// memSpool is the largest file buffered in memory while hashing;
	// bigger ones spill to a temporary file.
	memSpool = 1 << 20
)

// Limits returns the service's publishing limits.
func (s *Service) Limits() config.Limits { return s.limits }

// tarOverheadPerEntry is the tar header, padding, and PAX or GNU metadata
// allowed per entry on top of MAX_ARCHIVE_SIZE file bytes (maxTarStream).
const tarOverheadPerEntry = 4 << 10
