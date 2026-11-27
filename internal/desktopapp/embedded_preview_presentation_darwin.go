//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func (p *macPreviewConsumer) verifyOverlaysAndFocus() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	var err error
	var native map[string]any
	var state embeddedbrowser.ViewState
	p.command("open-workspace-picker")
	folderCtx, stopFolder := context.WithTimeout(ctx, 12*time.Second)
	err = waitMacPreview(folderCtx, func() bool { return desktopclient.MacPreviewCancelWorkspacePicker(window) })
	if err == nil {
		err = waitMacPreview(folderCtx, func() bool { return !desktopclient.MacPreviewFilePickerVisible(window) })
	}
	stopFolder()
	if err != nil {
		return macPreviewFailure("workspace-directory-picker", err)
	}
	result["workspaceDirectoryPickerCanceled"] = true
	p.command("open-shell-overlay")
	if _, err = p.await("shell-overlay-opened"); err != nil {
		return macPreviewFailure("shell-overlay-hide-ack", err)
	}
	native, err = desktopclient.MacPreviewObserve(window, before.Lease)
	if err != nil || native["visible"] != false {
		return macPreviewFailure("shell-overlay-native-hide", fmt.Errorf("expected hidden native view: %v", err))
	}
	p.command("open-provider-overlay")
	if _, err = p.await("provider-overlay-opened"); err != nil {
		return macPreviewFailure("provider-overlay-ack", err)
	}
	p.command("close-shell-overlay")
	if _, err = p.await("shell-overlay-still-blocked"); err != nil {
		return macPreviewFailure("shell-overlay-partial-release-ack", err)
	}
	native, err = desktopclient.MacPreviewObserve(window, before.Lease)
	if err != nil || native["visible"] != false {
		return macPreviewFailure("shell-overlay-partial-release", fmt.Errorf("remaining overlay did not keep the native view hidden: %v", err))
	}
	if err = desktopclient.MacPreviewClick(window, before.Lease, 200, 20); err == nil {
		return macPreviewFailure("shell-overlay-input-admitted", errors.New("AppKit delivered input while the provider overlay was active"))
	}
	result["overlappingOverlaysBlockedInput"] = true
	p.command("close-provider-overlay")
	if _, err = p.await("shell-overlay-closed"); err != nil {
		return macPreviewFailure("shell-overlay-resume-ack", err)
	}
	err = p.wait(func() bool {
		var stateErr error
		state, stateErr = service.State(ctx, requested.Document, requested.Slot)
		if stateErr != nil || !state.Visible || state.State != "ready" || state.Lease != before.Lease || state.Document != before.Document || state.Target != before.Target || state.Epoch == before.Epoch {
			return false
		}
		native, stateErr = desktopclient.MacPreviewObserve(window, before.Lease)
		return stateErr == nil && native["visible"] == true
	})
	if err != nil {
		return macPreviewFailure("shell-overlay-fresh-layout", err)
	}
	result["shellOverlayRestored"] = true
	before = state
	var unfocused macPreviewPageState
	err = p.wait(func() bool {
		var pageErr error
		unfocused, pageErr = p.readPage()
		return pageErr == nil && unfocused.Nonce == cfg.Nonce && unfocused.Tick > p.afterInput.Tick && !unfocused.Focused && native["firstResponderIsCEF"] == false
	})
	if err != nil {
		return macPreviewFailure("shell-overlay-focus-preserved", err)
	}
	if err = desktopclient.MacPreviewClick(window, before.Lease, 200, 20); err != nil {
		return macPreviewFailure("shell-overlay-focus-click", err)
	}
	err = p.wait(func() bool {
		value, pageErr := p.readPage()
		return pageErr == nil && value.Nonce == cfg.Nonce && value.Tick > unfocused.Tick && value.Focused
	})
	if err != nil {
		return macPreviewFailure("shell-overlay-user-focus", err)
	}
	result["focusLifecyclePassed"] = true
	p.command("send-stale-layout")
	if _, err = p.await("stale-layout-rejected"); err != nil {
		return macPreviewFailure("shell-stale-layout", err)
	}
	state, err = service.State(ctx, requested.Document, requested.Slot)
	if err != nil || !state.Visible || state.Epoch != before.Epoch || state.Bounds != before.Bounds {
		return macPreviewFailure("shell-stale-layout-state", fmt.Errorf("stale input changed the restored view: %v", err))
	}
	result["staleLayoutRejected"] = true
	// Drive Wails' backing-property listener on the owned fixture window.
	// The physical display stays the same, so the CSS slot must not move.
	window.HandleWindowEvent(uint(events.Mac.WindowDidChangeBackingProperties))
	err = p.wait(func() bool {
		var stateErr error
		state, stateErr = service.State(ctx, requested.Document, requested.Slot)
		if stateErr != nil || !state.Visible || state.State != "ready" || state.Lease != before.Lease || state.Document != before.Document || state.Bounds != before.Bounds || state.Epoch == before.Epoch {
			return false
		}
		native, stateErr = desktopclient.MacPreviewObserve(window, before.Lease)
		return stateErr == nil && native["visible"] == true && native["layoutEpoch"] == state.Epoch
	})
	if err != nil {
		return macPreviewFailure("mac-backing-properties-refresh", err)
	}
	result["displayRefreshPassed"] = true
	p.before = state
	p.backingBytes, _ = native["frameBytes"].(float64)
	return nil
}

func (p *macPreviewConsumer) verifyBackingResize() error {
	ctx, window := p.ctx, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	var err error
	var state embeddedbrowser.ViewState
	var backingFrames []map[string]any
	checkBacking := func(base embeddedbrowser.ViewState, growBuffer bool) error {
		backingCtx, stop := context.WithTimeout(ctx, 8*time.Second)
		defer stop()
		var last map[string]any
		err := waitMacPreview(backingCtx, func() bool {
			current, stateErr := service.State(backingCtx, requested.Document, requested.Slot)
			state = current
			currentNative, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
			last = currentNative
			if stateErr != nil || state.State != "ready" || !state.Visible || state.Lease != before.Lease ||
				state.Target != before.Target || state.Document != before.Document || state.Epoch == base.Epoch {
				return false
			}
			if growBuffer {
				if state.Bounds.Height <= base.Bounds.Height {
					return false
				}
			} else {
				// Wails returns the native window size in whole Cocoa points.
				// A restored fractional frame can round by one point per edge.
				got, original := state.Bounds, before.Bounds
				if math.Abs(float64(got.X-original.X)) > 1 || math.Abs(float64(got.Y-original.Y)) > 1 ||
					math.Abs(float64(got.Width-original.Width)) > 1 || math.Abs(float64(got.Height-original.Height)) > 1 {
					return false
				}
			}
			scale, _ := last["scale"].(float64)
			frameBytes, _ := last["frameBytes"].(float64)
			return observeErr == nil && scale >= 1 && last["layerScale"] == scale && last["layoutEpoch"] == state.Epoch &&
				last["pixelWidth"] == math.Ceil(float64(state.Bounds.Width)*scale) &&
				last["pixelHeight"] == math.Ceil(float64(state.Bounds.Height)*scale) &&
				(!growBuffer || frameBytes > p.backingBytes) && last["nativeHitTestIsCEF"] == true
		})
		if err != nil {
			actualWidth, actualHeight := window.Size()
			return fmt.Errorf("window=%dx%d previous=%+v current=%+v native=%v: %w",
				actualWidth, actualHeight, base.Bounds, state.Bounds, last, err)
		}
		backingFrames = append(backingFrames, last)
		return nil
	}
	width, height := window.Size()
	const growBy = 120
	window.SetSize(width, height+growBy)
	err = checkBacking(before, true)
	grown := state
	window.SetSize(width, height)
	if err != nil {
		return macPreviewFailure("mac-backing-pixels-grow", err)
	}
	if err = checkBacking(grown, false); err != nil {
		return macPreviewFailure("mac-backing-pixels-restore", err)
	}
	result["backingFramesAfterWindowResize"] = backingFrames
	before = state
	p.before = before
	return nil
}

func (p *macPreviewConsumer) verifySidebar() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	var native map[string]any
	var state embeddedbrowser.ViewState
	previews, err := service.SetOcclusion(ctx, requested.Document, true)
	if err != nil || len(previews) != 1 {
		return macPreviewFailure("cover-visible-view", fmt.Errorf("expected one native preview, got %d: %v", len(previews), err))
	}
	preview := previews[0]
	if preview.Slot != requested.Slot || preview.Lease != before.Lease || preview.Document != before.Document || preview.Frame != before.Frame || preview.URL != before.URL {
		return macPreviewFailure("cover-preview-identity", errors.New("MAC_PREVIEW_COVER_IDENTITY_CHANGED"))
	}
	encoded, decodeErr := base64.StdEncoding.DecodeString(preview.Data)
	image, imageErr := png.DecodeConfig(bytes.NewReader(encoded))
	if decodeErr != nil || imageErr != nil || image.Width < 100 || image.Height < 100 {
		return macPreviewFailure("cover-preview-png", errors.New("MAC_PREVIEW_COVER_PNG_INVALID"))
	}
	if err = os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-cover.png"), encoded, 0600); err != nil {
		return macPreviewFailure("cover-preview-evidence", err)
	}
	native, err = desktopclient.MacPreviewObserve(window, before.Lease)
	if err != nil || native["visible"] != false {
		return macPreviewFailure("cover-native-hide", errors.New("MAC_PREVIEW_COVER_VIEW_STILL_VISIBLE"))
	}
	p.command("hide")
	if _, err = p.await("hidden-requested"); err != nil {
		return macPreviewFailure("collapse-real-sidebar", err)
	}
	err = p.wait(func() bool {
		var e error
		state, e = service.State(ctx, requested.Document, requested.Slot)
		return e == nil && !state.Visible && state.Document == before.Document && state.Target == before.Target
	})
	if err != nil {
		return macPreviewFailure("native-hide-ACK", err)
	}
	result["hidden"] = state
	native, err = desktopclient.MacPreviewObserve(window, before.Lease)
	if err != nil || native["visible"] != false {
		return macPreviewFailure("actual-hidden-native-view", errors.New("MAC_PREVIEW_NATIVE_HIDE_NOT_OBSERVED"))
	}
	if err = p.save("mac-preview-native-hidden", native); err != nil {
		return macPreviewFailure("native-hidden-evidence", err)
	}
	if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-hidden.png")); err != nil {
		return macPreviewFailure("AppKit-hidden-render", err)
	}
	if _, err = service.SetOcclusion(ctx, requested.Document, false); err != nil {
		return macPreviewFailure("cover-resume", err)
	}
	p.command("show")
	if _, err = p.await("sidebar-reopened"); err != nil {
		return macPreviewFailure("reopen-real-sidebar", err)
	}
	err = p.wait(func() bool {
		var e error
		state, e = service.State(ctx, requested.Document, requested.Slot)
		return e == nil && state.State == "ready" && state.Visible && state.BrokerGeneration == before.BrokerGeneration && state.Target == before.Target && state.Frame == before.Frame && state.Document == before.Document && state.Lease == before.Lease
	})
	if err != nil {
		return macPreviewFailure("same-document-reopen", err)
	}
	result["after"] = state
	err = p.wait(func() bool {
		value, e := p.readPage()
		if e == nil && value.Nonce == cfg.Nonce && value.Input == p.afterInput.Input && value.Clicks == p.afterInput.Clicks && value.Loads == 1 && value.ScrollY > 0 && value.Tick > p.afterInput.Tick {
			result["afterReopen"] = value
			return true
		}
		return false
	})
	if err != nil {
		return macPreviewFailure("same-live-page-readback", err)
	}
	native, err = desktopclient.MacPreviewObserve(window, before.Lease)
	if err != nil || native["visible"] != true || native["nativeHitTestIsCEF"] != true {
		return macPreviewFailure("actual-reopened-native-view", errors.New("MAC_PREVIEW_NATIVE_REOPEN_NOT_OBSERVED"))
	}
	if err = p.save("mac-preview-native-reopened", native); err != nil {
		return macPreviewFailure("native-reopen-evidence", err)
	}
	if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-reopened.png")); err != nil {
		return macPreviewFailure("AppKit-reopened-render", err)
	}
	p.command("switch-to-guide")
	if _, err = p.await("guide-tab-selected"); err != nil {
		return macPreviewFailure("select-guide-tab", err)
	}
	if err = p.wait(func() bool {
		native, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
		return observeErr == nil && native["visible"] == false
	}); err != nil {
		return macPreviewFailure("inactive-browser-tab-hidden", err)
	}
	p.command("switch-to-browser")
	if _, err = p.await("browser-tab-selected"); err != nil {
		return macPreviewFailure("select-browser-tab", err)
	}
	if err = p.wait(func() bool {
		state, stateErr := service.State(ctx, requested.Document, requested.Slot)
		native, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
		return stateErr == nil && state.Visible && state.State == "ready" &&
			state.Lease == before.Lease && state.Target == before.Target && state.Document == before.Document &&
			observeErr == nil && native["visible"] == true && native["nativeHitTestIsCEF"] == true
	}); err != nil {
		return macPreviewFailure("active-browser-tab-restored", err)
	}
	result["browserTabSwitchPassed"] = true
	return nil
}
