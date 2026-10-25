//go:build windows

package nativeui

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// EnsureToastActivator establishes the notification identity before Wails starts.
func EnsureToastActivator(appName, displayName, clsid string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+appName, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open toast activation registration: %w", err)
	}
	defer key.Close()
	for _, value := range []struct{ name, text string }{
		{"CustomActivator", clsid},
		{"DisplayName", displayName},
	} {
		current, _, err := key.GetStringValue(value.name)
		if err == nil && current == value.text {
			continue
		}
		if err != nil && !errors.Is(err, registry.ErrNotExist) && !errors.Is(err, registry.ErrUnexpectedType) {
			return fmt.Errorf("read toast %s: %w", value.name, err)
		}
		if err := key.SetStringValue(value.name, value.text); err != nil {
			return fmt.Errorf("write toast %s: %w", value.name, err)
		}
	}
	return nil
}
