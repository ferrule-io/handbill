package main

import "github.com/ferrule-io/handbill/internal/server"

// modules are the feature packages' route registrations, passed to
// server.New. Each feature appends itself from its own module_<name>.go file
// (one init per file) so parallel workstreams never edit a shared list.
// Route registration order does not matter: ServeMux picks the most specific
// pattern and panics on conflicts. Middleware order does: inits run in file
// name order and the first UseApp/UseSites runs outermost, so the session
// (module_auth) wraps deadlines (module_httpserver), which wrap metrics
// (module_metrics), which wrap rate limits (module_ratelimit); main_test.go
// checks the rate limits see the session's user.
var modules []server.Module
