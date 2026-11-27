//go:build !linux && (!darwin || !cgo)

package main

func supervisorBootstrap() bool { return false }
