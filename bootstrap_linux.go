//go:build linux

package main

import linuxplatform "github.com/local/dsh-work/internal/platform/linux"

func supervisorBootstrap() bool { return linuxplatform.RunBootstrap() }
