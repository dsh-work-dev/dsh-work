//go:build !windows

package browserhost

import "context"

type unsupported struct{}

func newRegistrar(Config) Registrar { return unsupported{} }
func (unsupported) Ensure(context.Context) []Registration {
	return []Registration{{Browser: "chrome", Failure: "unsupported"}, {Browser: "edge", Failure: "unsupported"}}
}
func (unsupported) Remove(context.Context) error  { return ErrUnsupported }
func (unsupported) Refresh(context.Context) error { return ErrUnsupported }
func parentBrowserInfo() (ParentInfo, error)      { return ParentInfo{}, ErrUnsupported }
