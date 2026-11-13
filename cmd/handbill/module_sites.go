package main

import "github.com/ferrule-io/handbill/internal/sites"

func init() { modules = append(modules, sites.Register) }
