//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
)

func (p *macPreviewConsumer) verifyUpload() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	var err error
	if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 190); err != nil {
		return macPreviewFailure("upload-picker-click", err)
	}
	if err = p.wait(func() bool { return desktopclient.MacPreviewFilePickerVisible(window) }); err != nil {
		return macPreviewFailure("upload-picker-owned-sheet", err)
	}
	if err = desktopclient.MacPreviewCheckFilePickerFilter(window); err != nil {
		return macPreviewFailure("upload-picker-file-type-filter", err)
	}
	result["uploadPickerFileFilterPassed"] = true
	if cfg.RetirePicker {
		pickerClose, cancelPickerClose := context.WithTimeout(ctx, 12*time.Second)
		defer cancelPickerClose()
		if err = service.Detach(pickerClose, requested.Document, requested.Slot); err != nil {
			return macPreviewFailure("retire-browser-with-picker", err)
		}
		if err = waitMacPreview(pickerClose, func() bool {
			_, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
			return !desktopclient.MacPreviewFilePickerVisible(window) &&
				observeErr != nil && observeErr.Error() == "MAC_FIXTURE_NATIVE_VIEW_MISSING"
		}); err != nil {
			return macPreviewFailure("retire-picker-and-input-lease", err)
		}
		if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 190); err == nil {
			return macPreviewFailure("retire-picker-and-input-lease", errors.New("detached browser accepted input"))
		}
		result["browserRetiredWithPicker"] = true
		return nil
	}
	p.command("hide")
	if _, err = p.await("hidden-requested"); err != nil {
		return macPreviewFailure("upload-picker-hide-sidebar", err)
	}
	var coveredFrames float64
	if err = p.wait(func() bool {
		if desktopclient.MacPreviewFilePickerVisible(window) {
			return false
		}
		frame, frameErr := desktopclient.MacPreviewObserve(window, before.Lease)
		var validCount bool
		coveredFrames, validCount = frame["displayedFrames"].(float64)
		return frameErr == nil && validCount && frame["visible"] == false
	}); err != nil {
		return macPreviewFailure("upload-picker-drain-on-hide", err)
	}
	p.command("show")
	if _, err = p.await("sidebar-reopened"); err != nil {
		return macPreviewFailure("upload-picker-reopen-sidebar", err)
	}
	var reopenedFrames float64
	err = p.wait(func() bool {
		value, e := p.readPage()
		state, stateErr := service.State(ctx, requested.Document, requested.Slot)
		frame, frameErr := desktopclient.MacPreviewObserve(window, before.Lease)
		reopenedFrames, _ = frame["displayedFrames"].(float64)
		return e == nil && value.ChooserCancels == 1 && value.PromptTargetAtPoint == "prompt" &&
			stateErr == nil && state.Visible && state.Lease == before.Lease && state.Document == before.Document &&
			frameErr == nil && frame["visible"] == true && reopenedFrames > coveredFrames
	})
	if err != nil {
		return macPreviewFailure("upload-picker-cancel-readback", err)
	}
	result["uploadPickerCanceled"] = true
	result["postPickerFrame"] = map[string]float64{"coveredDisplayedFrames": coveredFrames, "reopenedDisplayedFrames": reopenedFrames}
	if cfg.ManualFiles == "both" {
		if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 190); err != nil {
			return macPreviewFailure("manual-upload-click", err)
		}
		if err = p.wait(func() bool { return desktopclient.MacPreviewFilePickerVisible(window) }); err != nil {
			return macPreviewFailure("manual-upload-sheet", err)
		}
		_ = p.save("mac-preview-manual-upload", map[string]string{"choose": filepath.Join(cfg.Workspace, "dsh-parity-upload.txt")})
		err = p.wait(func() bool {
			value, e := p.readPage()
			return e == nil && value.FixtureFileRead
		})
		if err != nil {
			return macPreviewFailure("manual-upload-file-readback", err)
		}
		result["uploadFixtureFileRead"] = true
	}
	return nil
}

func (p *macPreviewConsumer) verifyArtifactPicker() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested := p.service, p.requested
	result := p.result
	var err error
	pickerCtx, stopPicker := context.WithCancel(ctx)
	picked := make(chan error, 1)
	go func() {
		_, e := service.OpenArtifact(pickerCtx, requested.Document, requested.Slot+"/artifact-fixture")
		picked <- e
	}()
	pickerDeadline := time.NewTimer(12 * time.Second)
	pickerPolling := time.NewTicker(100 * time.Millisecond)
	var pickerErr error
	for !desktopclient.MacPreviewFilePickerVisible(window) && pickerErr == nil {
		select {
		case pickerErr = <-picked:
			if pickerErr == nil {
				pickerErr = errors.New("MAC_PREVIEW_PICKER_COMPLETED_BEFORE_USER_ACTION")
			}
		case <-pickerDeadline.C:
			pickerErr = errors.New("MAC_PREVIEW_PICKER_SHEET_NOT_VISIBLE")
		case <-pickerPolling.C:
		}
	}
	pickerDeadline.Stop()
	pickerPolling.Stop()
	stopPicker()
	if pickerErr != nil {
		return macPreviewFailure("artifact-picker-owned-sheet", pickerErr)
	}
	if e := <-picked; e == nil {
		return macPreviewFailure("artifact-picker-cancel", errors.New("MAC_PREVIEW_PICKER_ACCEPTED_AFTER_CANCEL"))
	}
	if err = p.wait(func() bool { return !desktopclient.MacPreviewFilePickerVisible(window) }); err != nil {
		return macPreviewFailure("artifact-picker-drained", err)
	}
	result["artifactPickerCanceled"] = true
	if cfg.ManualFiles != "" {
		_ = p.save("mac-preview-manual-artifact", map[string]string{"choose": filepath.Join(cfg.Workspace, "dsh-parity-artifact.html")})
		artifactSlot := requested.Slot + "/artifact-fixture"
		artifact, e := service.OpenArtifact(ctx, requested.Document, artifactSlot)
		if e != nil || artifact.ArtifactName != "dsh-parity-artifact.html" || artifact.Lease == "" {
			return macPreviewFailure("manual-artifact-file-open", fmt.Errorf("artifact lease=%t selected=%t: %v", artifact.Lease != "", artifact.ArtifactName == "dsh-parity-artifact.html", e))
		}
		result["artifactFixtureSelected"] = true
		if _, err = service.Update(ctx, requested.Document, requested.Slot, "", embeddedbrowser.SlotBounds{}, false); err != nil {
			return macPreviewFailure("manual-artifact-hide-web-view", err)
		}
		bounds := embeddedbrowser.SlotBounds{X: 96, Y: 96, Width: 400, Height: 280}
		lastView, err := service.Update(ctx, requested.Document, artifactSlot, "", bounds, true)
		if err != nil {
			return macPreviewFailure("manual-artifact-present", err)
		}
		loadCtx, finishLoad := context.WithTimeout(ctx, 15*time.Second)
		tick := time.NewTicker(100 * time.Millisecond)
		var lastPaint float64
		for {
			value, e := service.State(loadCtx, requested.Document, artifactSlot)
			native, nativeErr := desktopclient.MacPreviewObserve(window, artifact.Lease)
			paint, _ := native["paintSequence"].(float64)
			if e == nil {
				lastView = value
			}
			if nativeErr == nil {
				lastPaint = paint
			}
			if e == nil && value.Lease == artifact.Lease && value.State == "ready" && value.Target != "" &&
				value.Title == "DSH Work fixture artifact" && value.Visible &&
				nativeErr == nil && native["visible"] == true && paint > 0 {
				break
			}
			select {
			case <-loadCtx.Done():
				failure := fmt.Errorf("last artifact state=%s title=%q target=%t visible=%t paint=%g failure=%q: %w",
					lastView.State, lastView.Title, lastView.Target != "", lastView.Visible, lastPaint, lastView.Failure, errors.Join(loadCtx.Err(), e, nativeErr))
				tick.Stop()
				finishLoad()
				return macPreviewFailure("manual-artifact-document-ready", failure)
			case <-tick.C:
			}
		}
		tick.Stop()
		finishLoad()
		result["artifactFixtureOpened"] = true
	}
	return nil
}
