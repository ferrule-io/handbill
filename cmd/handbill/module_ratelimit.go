package main

import "github.com/ferrule-io/handbill/internal/ratelimit"

func init() { modules = append(modules, ratelimit.Register) }
