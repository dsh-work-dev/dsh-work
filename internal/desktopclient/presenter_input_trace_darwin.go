//go:build darwin && cgo && !e67previewfixture && !server

package desktopclient

type macViewInputTrace struct{}

func (*macViewInputTrace) record(string, []byte, string) {}
func (*macViewInputTrace) recordLayout(string, string)   {}
