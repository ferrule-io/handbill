package sites

import "testing"

// SetMaxTarStream lowers the tar.gz decompressed-stream limit for one test.
func SetMaxTarStream(t *testing.T, n int64) {
	old := maxTarStream
	maxTarStream = n
	t.Cleanup(func() { maxTarStream = old })
}
