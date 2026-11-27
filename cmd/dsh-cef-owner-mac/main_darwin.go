//go:build darwin && cgo

package main

import (
	"fmt"
	"os"

	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "--guard" {
		os.Exit(2)
	}
	if err := macowner.RunGuard(os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "MAC_CEF_OWNER_FAILED")
		os.Exit(1)
	}
}
