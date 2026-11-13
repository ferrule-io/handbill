package main

import "github.com/mjquinlan2000/agent-dash/internal/mcp"

func init() { modules = append(modules, mcp.Register) }
