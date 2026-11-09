//go:build !windows

package embeddedbrowser

import "errors"

func presenterAlive(PresenterPeer) bool { return false }
func admitPresenter(PresenterPeer, uintptr) (presenterOwner, error) {
	return nil, errors.New("CEF_PRESENTER_UNAVAILABLE")
}
func presenterBounds(PresenterPeer, uintptr, SlotBounds, float64) (PixelBounds, error) {
	return PixelBounds{}, errors.New("CEF_PRESENTER_UNAVAILABLE")
}
