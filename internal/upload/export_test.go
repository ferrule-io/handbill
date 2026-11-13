package upload

import "testing"

// SetMaxArchiveBytes lowers the body limit for one test.
func SetMaxArchiveBytes(t *testing.T, n int64) {
	old := maxArchiveBytes
	maxArchiveBytes = n
	t.Cleanup(func() { maxArchiveBytes = old })
}
