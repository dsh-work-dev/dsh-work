//go:build !windows

package dshadapter

import "os"

func openVersionFile(root *os.Root, name string, flags int) (*os.File, error) {
	return root.OpenFile(name, flags, 0600)
}
