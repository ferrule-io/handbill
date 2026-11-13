package main

import "github.com/mjquinlan2000/agent-dash/internal/server"

// modules are the feature packages' route registrations, passed to
// server.New. Each feature appends itself from its own module_<name>.go file
// (one init per file) so parallel workstreams never edit a shared list.
// Registration order does not matter: ServeMux picks the most specific
// pattern and panics on conflicts.
var modules []server.Module
