package main

import "github.com/ferrule-io/handbill/internal/oauth"

func init() { modules = append(modules, oauth.Register) }
