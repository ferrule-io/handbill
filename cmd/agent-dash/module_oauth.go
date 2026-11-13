package main

import "github.com/mjquinlan2000/agent-dash/internal/oauth"

func init() { modules = append(modules, oauth.Register) }
