package main

import "github.com/ferrule-io/handbill/internal/webui"

func init() { modules = append(modules, webui.Register) }
