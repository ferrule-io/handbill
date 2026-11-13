package main

import "github.com/mjquinlan2000/agent-dash/internal/webui"

func init() { modules = append(modules, webui.Register) }
