package sites

// Publishing limits, in one place so hardening (W9) can make them
// configurable. Documented in docs/design.md#publishing.
const (
	// MaxPathLen is the longest file path, in bytes.
	MaxPathLen = 1024
	// MaxFileSize caps one file, whether uploaded directly or extracted
	// from an archive.
	MaxFileSize int64 = 64 << 20
	// MaxArchiveEntries caps the entries (files and directories) in one
	// archive.
	MaxArchiveEntries = 10_000
	// MaxArchiveSize caps one archive's total uncompressed file bytes; it
	// also caps the compressed bytes read.
	MaxArchiveSize int64 = 512 << 20

	// memSpool is the largest file buffered in memory while hashing;
	// bigger ones spill to a temporary file.
	memSpool = 1 << 20
)

// maxTarStream caps a tar.gz archive's decompressed stream: file data plus
// tar headers, padding, and PAX and GNU metadata records, which do not
// count as entries. It allows 4 KiB of such overhead per entry. A variable
// so tests can lower it.
var maxTarStream = MaxArchiveSize + MaxArchiveEntries*4<<10
