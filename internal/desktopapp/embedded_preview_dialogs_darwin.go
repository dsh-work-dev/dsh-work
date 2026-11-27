//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
)

func (p *macPreviewConsumer) verifyDialogs() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	var err error
	var native map[string]any
	var state embeddedbrowser.ViewState
	promptCtx, stopPrompt := context.WithTimeout(ctx, 12*time.Second)
	beforePromptInput, _ := desktopclient.MacPreviewObserve(window, before.Lease)
	var clickErr error
	if err = waitMacPreview(promptCtx, func() bool {
		clickErr = desktopclient.MacPreviewClick(window, before.Lease, 260, 70)
		if clickErr != nil {
			result["promptClickWaitReason"] = clickErr.Error()
		}
		return clickErr == nil
	}); err != nil {
		stopPrompt()
		return macPreviewFailure("prompt-click", errors.Join(err, clickErr))
	}
	afterPromptInput, _ := desktopclient.MacPreviewObserve(window, before.Lease)
	result["promptNativeInput"] = map[string]any{"before": beforePromptInput, "after": afterPromptInput}
	var promptPending bool
	var promptLastEpoch string
	err = waitMacPreview(promptCtx, func() bool {
		var stateErr error
		state, stateErr = service.State(promptCtx, requested.Document, requested.Slot)
		if stateErr == nil {
			promptLastEpoch = state.Epoch
		}
		promptPending = promptPending || (state.Dialog != nil && state.Dialog.Kind == "prompt")
		return stateErr == nil && state.Dialog != nil && state.Dialog.Kind == "prompt" && state.DialogVisible
	})
	stopPrompt()
	if err != nil {
		page, pageErr := p.readPage()
		lastPromptInput, _ := desktopclient.MacPreviewObserve(window, before.Lease)
		result["promptTransportAfterTimeout"] = lastPromptInput["inputTransport"]
		result["promptLastBrokerEpoch"] = promptLastEpoch
		return macPreviewFailure("prompt-overlay-ready", fmt.Errorf("%w: page-down=%d page-up=%d page-starts=%d page-result=%q point-target=%q native-down=%v→%v native-up=%v→%v key=%v→%v pending-seen=%t state=%s view-visible=%t readback=%v",
			err, page.PromptDown, page.PromptUp, page.PromptStarts, page.Prompt, page.PromptTargetAtPoint,
			beforePromptInput["fixtureLeftDown"], afterPromptInput["fixtureLeftDown"], beforePromptInput["fixtureLeftUp"], afterPromptInput["fixtureLeftUp"],
			beforePromptInput["keyWindow"], afterPromptInput["keyWindow"], promptPending, state.State, state.Visible, pageErr))
	}
	native, err = desktopclient.MacPreviewObserve(window, before.Lease)
	if err != nil || native["visible"] != false || native["nativeHitTestIsCEF"] != false {
		return macPreviewFailure("prompt-native-hit-test", errors.New("MAC_PREVIEW_DIALOG_DID_NOT_SUSPEND_VIEW"))
	}
	p.command("answer-prompt")
	if _, err = p.await("prompt-answered"); err != nil {
		return macPreviewFailure("prompt-answer-from-workbench", err)
	}
	err = p.wait(func() bool {
		value, e := p.readPage()
		native, nativeErr := desktopclient.MacPreviewObserve(window, before.Lease)
		return e == nil && value.PromptStarts == 1 && value.Prompt == "已确认" && value.Loads == 1 && nativeErr == nil && native["visible"] == true
	})
	if err != nil {
		return macPreviewFailure("prompt-response-and-native-restore", err)
	}
	result["promptPassed"] = true
	result["promptStartedOnce"] = true

	for index, action := range []string{"deny-popup", "allow-popup"} {
		beforeClick, _ := desktopclient.MacPreviewObserve(window, before.Lease)
		if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 130); err != nil {
			return macPreviewFailure(action+"-native-click", err)
		}
		afterClick, _ := desktopclient.MacPreviewObserve(window, before.Lease)
		popupCtx, stopPopup := context.WithTimeout(ctx, 12*time.Second)
		var popupPending bool
		err = waitMacPreview(popupCtx, func() bool {
			observed, stateErr := service.State(popupCtx, requested.Document, requested.Slot)
			if stateErr != nil {
				return false
			}
			state = observed
			popupPending = popupPending || state.Popup != nil
			return state.PopupVisible && state.Popup != nil && state.Popup.URL == cfg.URL+"popup" && state.Popup.Origin+"/" == cfg.URL
		})
		stopPopup()
		if err != nil {
			page, pageErr := p.readPage()
			return macPreviewFailure(action+"-overlay-ready", fmt.Errorf("%w: page-clicks=%d trusted=%t activation=%t point-target=%q native-down=%v→%v native-up=%v→%v key=%v→%v pending-seen=%t visible=%t epoch=%q readback=%v",
				err, page.PopupClicks, page.PopupClickTrusted, page.PopupClickActive, page.PopupTargetAtPoint,
				beforeClick["fixtureLeftDown"], afterClick["fixtureLeftDown"], beforeClick["fixtureLeftUp"], afterClick["fixtureLeftUp"],
				beforeClick["keyWindow"], afterClick["keyWindow"], popupPending, state.Visible, state.Epoch, pageErr))
		}
		p.command(action)
		if _, err = p.await("popup-answered"); err != nil {
			return macPreviewFailure(action+"-from-workbench", err)
		}
		err = p.wait(func() bool {
			value, e := p.readPage()
			if e != nil || value.PopupClicks != index+1 || !value.PopupClickTrusted || !value.PopupClickActive {
				return false
			}
			if action == "allow-popup" {
				return value.PopupLoads == 1
			}
			state, stateErr := service.State(ctx, requested.Document, requested.Slot)
			native, nativeErr := desktopclient.MacPreviewObserve(window, before.Lease)
			return value.PopupLoads == 0 && stateErr == nil && state.Popup == nil && nativeErr == nil && native["visible"] == true
		})
		if err != nil {
			return macPreviewFailure(action+"-effect", err)
		}
	}
	result["popupPassed"] = true
	return nil
}
