package main

import "github.com/ferrule-io/handbill/internal/upload"

func init() { modules = append(modules, upload.Register) }
