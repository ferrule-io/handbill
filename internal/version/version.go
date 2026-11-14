// Package version reports the build's version and commit. Release builds
// set both with the linker:
//
//	go build -ldflags "-X github.com/ferrule-io/handbill/internal/version.Version=v0.1.0 \
//	  -X github.com/ferrule-io/handbill/internal/version.Commit=$(git rev-parse HEAD)"
//
// The Makefile and Dockerfile do this (VERSION and COMMIT build args).
package version

// Version is the release version (git describe), "dev" for untagged builds.
var Version = "dev"

// Commit is the full git commit hash, "unknown" when not set.
var Commit = "unknown"

// String returns "handbill <version> (<commit>)".
func String() string { return "handbill " + Version + " (" + Commit + ")" }
