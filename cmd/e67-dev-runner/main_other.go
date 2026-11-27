//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "The E-67 ordinary-terminal development runner requires Windows.")
	os.Exit(1)
}
