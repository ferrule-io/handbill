package main

import "github.com/ferrule-io/handbill/internal/appapi"

func init() { modules = append(modules, appapi.Register) }
