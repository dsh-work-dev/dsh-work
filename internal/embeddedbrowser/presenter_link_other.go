//go:build !darwin || !cgo || !e67presenter

package embeddedbrowser

import "errors"

func presenterParameters(PresenterPeer, uintptr) (map[string]any, error) { return nil, nil }
func ConfigureMacPresenter(PresenterPeer, uintptr, string, string) error {
	return errors.New("CEF_MAC_PRESENTER_UNAVAILABLE")
}
