package main

import "github.com/ferrule-io/handbill/internal/siteserver"

func init() { modules = append(modules, siteserver.Register) }
