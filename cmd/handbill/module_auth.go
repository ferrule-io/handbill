package main

import "github.com/ferrule-io/handbill/internal/auth"

func init() { modules = append(modules, auth.Register) }
