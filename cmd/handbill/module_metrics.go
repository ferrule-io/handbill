package main

import "github.com/ferrule-io/handbill/internal/metrics"

func init() { modules = append(modules, metrics.Register) }
