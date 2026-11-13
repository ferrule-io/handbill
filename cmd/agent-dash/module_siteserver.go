package main

import "github.com/mjquinlan2000/agent-dash/internal/siteserver"

func init() { modules = append(modules, siteserver.Register) }
