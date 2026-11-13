package main

import "github.com/mjquinlan2000/agent-dash/internal/auth"

func init() { modules = append(modules, auth.Register) }
