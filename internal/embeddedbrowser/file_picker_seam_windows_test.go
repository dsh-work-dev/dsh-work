//go:build windows

package embeddedbrowser

import (
	"context"
	"encoding/json"
)

// Test-only seams exercise the authenticated native boundary, deliberately
// bypassing Go URL preflight. They are absent from product builds.
func NativePickerNavigateForTest(ctx context.Context, target *OwnedTarget, address string) error {
	_, err := target.broker.controlCall(ctx, "target.navigate", target.owner, map[string]any{"url": address})
	return err
}
func NativePickerStateForTest(ctx context.Context, target *OwnedTarget) (json.RawMessage, error) {
	raw, err := target.broker.controlCall(ctx, "target.navigation", target.owner, nil)
	if err != nil {
		return nil, err
	}
	var state struct{ FilePicker json.RawMessage }
	err = json.Unmarshal(raw, &state)
	return state.FilePicker, err
}
