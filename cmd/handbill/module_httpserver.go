package main

import "github.com/ferrule-io/handbill/internal/httpserver"

func init() { modules = append(modules, httpserver.Register) }
