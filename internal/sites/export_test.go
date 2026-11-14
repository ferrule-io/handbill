package sites

// SetMaxTarStream lowers s's tar.gz decompressed-stream limit.
func SetMaxTarStream(s *Service, n int64) { s.maxTarStream = n }
