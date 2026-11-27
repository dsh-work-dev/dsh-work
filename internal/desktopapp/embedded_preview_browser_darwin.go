//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/localipc"
)

func (p *macPreviewConsumer) openBrowser() error {
	ctx, cfg, service := p.ctx, p.cfg, p.service
	requested, err := p.await("slot-requested")
	if err != nil {
		return macPreviewFailure("real-DSH-slot", err)
	}
	if !strings.HasPrefix(requested.Session, "session:session-") || !strings.HasPrefix(requested.Slot, strings.TrimPrefix(requested.Session, "session:")+"/") || requested.Document == "" || requested.WorkerOrigin == "" || requested.WorkerOrigin == "null" || requested.ShellOrigin == "" || requested.ShellOrigin == "null" || requested.WorkerOrigin == requested.ShellOrigin {
		return macPreviewFailure("actual-session-origin", errors.New("MAC_PREVIEW_REAL_SESSION_REQUIRED"))
	}
	p.result["actualSession"] = requested.Session
	p.result["slot"] = requested.Slot
	p.result["workerOrigin"] = requested.WorkerOrigin
	p.result["shellOrigin"] = requested.ShellOrigin
	var state embeddedbrowser.ViewState
	p.requested = requested
	if cfg.FailNavigation {
		failureCtx, stopFailure := context.WithTimeout(ctx, 12*time.Second)
		err = waitMacPreview(failureCtx, func() bool {
			var stateErr error
			state, stateErr = service.State(failureCtx, requested.Document, requested.Slot)
			return stateErr == nil && state.State == "navigationFailed" &&
				state.Failure == "CEF_NAVIGATION_FAILED" && state.URL == cfg.URL && state.Frame == ""
		})
		stopFailure()
		if err != nil {
			return macPreviewFailure("network-failure-state", fmt.Errorf("%s error=%s frame=%t: %w", state.State, state.Failure, state.Frame != "", err))
		}
		if err = p.save("mac-preview-navigation-failed", state); err != nil {
			return macPreviewFailure("network-failure-evidence", err)
		}
		if err = os.WriteFile(filepath.Join(cfg.Evidence, "navigation-retry"), []byte(cfg.Nonce), 0600); err == nil {
			err = service.Action(ctx, requested.Document, requested.Slot, "reload")
		}
		if err != nil {
			return macPreviewFailure("network-failure-reload", err)
		}
	}
	var lastSlotObservation string
	err = p.wait(func() bool {
		var stateErr error
		state, stateErr = service.State(ctx, requested.Document, requested.Slot)
		observation := fmt.Sprintf("%s visible=%t frame=%t failure=%s error=%v", state.State, state.Visible, state.Frame != "", state.Failure, stateErr)
		if observation != lastSlotObservation {
			lastSlotObservation = observation
			_ = p.save("mac-preview-slot", map[string]any{"state": state, "error": fmt.Sprint(stateErr)})
		}
		return stateErr == nil && state.State == "ready" && state.Visible && state.URL == cfg.URL && state.Document != "" && state.Frame != "" && state.Lease != ""
	})
	if err != nil {
		return macPreviewFailure("native-slot-ready", fmt.Errorf("%w: %s", err, lastSlotObservation))
	}
	p.before = state
	p.result["before"] = state
	if cfg.FailNavigation {
		p.result["navigationRetryPassed"] = state.Failure == ""
	}
	return nil
}

func (p *macPreviewConsumer) finishNavigationRetry() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	before := p.before

	var err error
	if err = p.wait(func() bool {
		page, err := p.readPage()
		return err == nil && page.Nonce == cfg.Nonce && page.Tick > 0
	}); err != nil {
		return macPreviewFailure("network-retry-page-report", err)
	}
	if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-visible.png")); err != nil {
		return macPreviewFailure("network-retry-render", err)
	}
	return p.finish(ctx, "network-retry-close", "network-retry-evidence")
}

func (p *macPreviewConsumer) waitForNativeView() (map[string]any, error) {
	var native map[string]any
	err := p.wait(func() bool {
		var e error
		native, e = desktopclient.MacPreviewObserve(p.window, p.before.Lease)
		paint, _ := native["paintSequence"].(float64)
		return e == nil && native["visible"] == true && native["nativeHitTestIsCEF"] == true && paint > 0
	})
	if err != nil {
		return nil, macPreviewFailure("actual-native-view", err)
	}
	return native, nil
}

func (p *macPreviewConsumer) verifyNativeInput(native map[string]any) error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	before := p.before
	result := p.result
	var err error

	// The initial show can run before the native window is eligible to
	// become key. Focus this owned window once it has a visible native view.
	if native["keyWindow"] != true {
		window.Focus()
	}
	focusCtx, stopFocus := context.WithTimeout(ctx, 5*time.Second)
	focusErr := waitMacPreview(focusCtx, func() bool {
		if observation, e := desktopclient.MacPreviewObserve(window, before.Lease); e == nil {
			native = observation
			frame, _ := native["frame"].(map[string]any)
			width, _ := frame["width"].(float64)
			height, _ := frame["height"].(float64)
			// The first painted view can still be clipped by the opening
			// sidebar. Wait until the fixture's input targets fit.
			return native["keyWindow"] == true && native["visible"] == true && native["nativeHitTestIsCEF"] == true &&
				width >= 280 && height >= 200
		}
		return false
	})
	stopFocus()
	native["pid"] = os.Getpid()
	started, e := localipc.ProcessStarted(uint32(os.Getpid()))
	if e != nil {
		return macPreviewFailure("actual-native-process-instance", e)
	}
	native["started"] = started
	if err = p.save("mac-preview-native-before", native); err != nil {
		return macPreviewFailure("native-before-evidence", err)
	}
	if focusErr != nil {
		return macPreviewFailure("actual-native-input-ready", fmt.Errorf("key=%v visible=%v hit=%v frame=%v: %w", native["keyWindow"], native["visible"], native["nativeHitTestIsCEF"], native["frame"], focusErr))
	}
	if err = desktopclient.MacPreviewInteract(window, before.Lease); err != nil {
		return macPreviewFailure("actual-NSEvent-input", err)
	}
	var changed macPreviewPageState
	err = p.wait(func() bool {
		var e error
		changed, e = p.readPage()
		return e == nil && changed.Nonce == cfg.Nonce && changed.Focused && changed.Input == "mac-ui-dirty中" && changed.Clicks == 1 && changed.Loads == 1 && changed.ScrollY > 0 &&
			slices.Contains(changed.Composition, "compositionupdate:zhong") && slices.Contains(changed.Composition, "compositionend:中")
	})
	if err != nil {
		return macPreviewFailure("input-page-readback", fmt.Errorf("%w: input=%q clicks=%d loads=%d scroll=%v composition=%v", err, changed.Input, changed.Clicks, changed.Loads, changed.ScrollY, changed.Composition))
	}
	result["afterAppKitInput"] = changed
	p.afterInput = changed
	originalPaint := native["paintSequence"].(float64)
	err = p.wait(func() bool {
		value, e := desktopclient.MacPreviewObserve(window, before.Lease)
		if e == nil && value["visible"] == true && value["paintSequence"].(float64) > originalPaint {
			native = value
			return true
		}
		return false
	})
	if err != nil {
		return macPreviewFailure("physical-input-new-paint", err)
	}
	if err = p.save("mac-preview-native-after-input", native); err != nil {
		return macPreviewFailure("native-input-evidence", err)
	}
	if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-visible.png")); err != nil {
		return macPreviewFailure("AppKit-visible-render", err)
	}
	return nil
}

type macPreviewPageState struct {
	Nonce, Input, Prompt       string
	PopupLoads, ChooserCancels int
	PopupClicks                int
	PopupTargetAtPoint         string
	PopupClickTrusted          bool
	PopupClickActive           bool
	PromptStarts               int
	PromptDown, PromptUp       int
	PromptTargetAtPoint        string
	Clicks, Loads, Tick        int
	Composition                []string
	ScrollY                    float64
	FixtureFileRead            bool
	Focused                    bool
	IndicatorEvents            []string
}

func (p *macPreviewConsumer) readPage() (macPreviewPageState, error) {
	var value macPreviewPageState
	request, e := http.NewRequestWithContext(p.ctx, http.MethodGet, p.cfg.URL+"report", nil)
	if e != nil {
		return value, e
	}
	response, e := p.httpClient.Do(request)
	if e != nil {
		return value, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return value, errors.New("MAC_PREVIEW_PAGE_OBSERVATION_FAILED")
	}
	e = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&value)
	return value, e
}

func (p *macPreviewConsumer) verifyBrowser() error {
	for _, check := range []func() error{p.verifyOverlaysAndFocus, p.verifyBackingResize, p.verifySidebar, p.verifyUpload} {
		if err := check(); err != nil {
			return err
		}
	}
	if p.cfg.RetirePicker {
		return nil
	}
	if err := p.verifyDialogs(); err != nil {
		return err
	}
	return p.verifyArtifactPicker()
}
