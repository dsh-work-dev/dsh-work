//go:build windows

package main

import (
	"fmt"
	"github.com/local/dsh-work/internal/devrunner"
	"os"
)

func main() {
	if err := devrunner.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
