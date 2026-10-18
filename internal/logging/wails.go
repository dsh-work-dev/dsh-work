package logging

import (
	"errors"
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
)

// WailsErrorHandler logs errors Wails reports. A bound call the user cancelled is
// routine, so it is recorded at DEBUG instead of ERROR.
func WailsErrorHandler() func(error) {
	return func(err error) {
		if err == nil {
			return
		}
		logger := WailsLogger()
		if cancelledCall(err) {
			logger.Debug(err.Error())
			return
		}
		logger.Error(err.Error(), ErrAttrs(err, "")...)
	}
}

func cancelledCall(err error) bool {
	var failure lifecycle.Failure
	if errors.As(err, &failure) && failure.Code == lifecycle.ErrorCancelled {
		return true
	}
	var pointer *lifecycle.Failure
	if errors.As(err, &pointer) && pointer != nil && pointer.Code == lifecycle.ErrorCancelled {
		return true
	}
	return strings.Contains(err.Error(), string(lifecycle.ErrorCancelled)+":")
}
