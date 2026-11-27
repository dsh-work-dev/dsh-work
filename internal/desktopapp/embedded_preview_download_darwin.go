//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func macPreviewRecordBroker(owner *macowner.Adapter) error {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || !cfg.CrashDownload {
		return err
	}
	target, err := owner.PreviewBrokerTarget()
	if err != nil {
		return err
	}
	proof, err := json.Marshal(target)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-broker.json"), proof, 0600)
}

func macPreviewDownloadConsent() embeddedbrowser.DownloadConsent {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || !cfg.Download || cfg.NativeDownload {
		return nil
	}
	return func(ctx context.Context, request embeddedbrowser.DownloadConsentRequest) (embeddedbrowser.DownloadConsentDecision, error) {
		if err := ctx.Err(); err != nil {
			return embeddedbrowser.DownloadConsentDecision{}, err
		}
		if request.SourceOrigin+"/" != cfg.URL || (request.URL != cfg.URL+"complete.txt" && request.URL != cfg.URL+"cancel.txt") {
			return embeddedbrowser.DownloadConsentDecision{}, errors.New("MAC_PREVIEW_DOWNLOAD_REJECTED")
		}
		if (cfg.InvalidateDownload || cfg.ResizeDownload) && request.URL == cfg.URL+"cancel.txt" {
			if err := os.WriteFile(filepath.Join(cfg.Evidence, "download-consent-started"), nil, 0600); err != nil {
				return embeddedbrowser.DownloadConsentDecision{}, err
			}
			if cfg.InvalidateDownload {
				<-ctx.Done()
				err := os.WriteFile(filepath.Join(cfg.Evidence, "download-consent-canceled"), nil, 0600)
				return embeddedbrowser.DownloadConsentDecision{}, errors.Join(ctx.Err(), err)
			}
			if err := waitMacPreviewDownloadMarker(ctx, cfg.Evidence, "download-resize-complete"); err != nil {
				return embeddedbrowser.DownloadConsentDecision{}, err
			}
		}
		destination, err := downloads.PrepareDestination(filepath.Join(cfg.Workspace, filepath.Base(request.URL)), false)
		return embeddedbrowser.DownloadConsentDecision{Prepared: destination}, err
	}
}

func macPreviewDownloadShutdownChoice() int {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return 0
	}
	switch cfg.DownloadQuit {
	case "wait":
		return 1
	case "cancel":
		return 2
	case "back":
		return 3
	default:
		return 0
	}
}

func macPreviewRecordDownloadShutdown(expected, actual int) error {
	if expected == 0 {
		return nil
	}
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || actual != expected {
		return fmt.Errorf("fixture native exit button: got %d, expected %d (%v)", actual, expected, err)
	}
	return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-quit-decision"), []byte(cfg.Nonce), 0600)
}

func (cfg *macPreviewScenario) recordDownloadQuitReady() error {
	return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-quit-ready"), []byte(cfg.Nonce), 0600)
}

func (p *macPreviewConsumer) runDownloads() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	var err error
	downloadTimeout := 25 * time.Second
	if cfg.ManualDownload {
		downloadTimeout = 4 * time.Minute
	}
	downloadCtx, stopDownload := context.WithTimeout(ctx, downloadTimeout)
	err = verifyMacPreviewDownloads(downloadCtx, cfg, service, window, requested, before.Lease)
	stopDownload()
	if err != nil {
		return macPreviewFailure("download-network-and-cancel", err)
	}
	result["downloadsPassed"] = true
	if cfg.DownloadQuit == "wait" || cfg.DownloadQuit == "cancel" {
		result["passed"] = true
		if err := p.save("mac-preview-consumer", result); err != nil {
			return macPreviewFailure("download-quit-evidence", err)
		}
		if err := cfg.recordDownloadQuitReady(); err != nil {
			return macPreviewFailure("download-quit-evidence", err)
		}
		return nil // The requested quit or stop closes this UI.
	}
	if cfg.CrashDownload {
		if err = p.wait(func() bool {
			state, e := service.State(ctx, requested.Document, requested.Slot)
			return e == nil && state.State == "NeedsReload"
		}); err != nil {
			return macPreviewFailure("crashed-slot-reload-admission", err)
		}
		recovered, recoveryErr := service.Recover(ctx, requested.Document, requested.Slot, "")
		if recoveryErr != nil || recovered.Instance != before.Instance ||
			recovered.BrokerGeneration == "" || recovered.BrokerGeneration == before.BrokerGeneration ||
			recovered.Lease == "" || recovered.Lease == before.Lease {
			return macPreviewFailure("crashed-slot-recover", fmt.Errorf("fresh broker and lease for the same browser instance required: %v", recoveryErr))
		}
		bounds := embeddedbrowser.SlotBounds{
			X: float64(before.Bounds.X), Y: float64(before.Bounds.Y),
			Width: float64(before.Bounds.Width), Height: float64(before.Bounds.Height),
		}
		if _, err = service.Update(ctx, requested.Document, requested.Slot, "", bounds, true); err != nil {
			return macPreviewFailure("crashed-slot-present", err)
		}
		if err = p.wait(func() bool {
			state, e := service.State(ctx, requested.Document, requested.Slot)
			if e != nil || state.Lease != recovered.Lease || state.State != "ready" || !state.Visible || state.URL != cfg.URL {
				return false
			}
			native, nativeErr := desktopclient.MacPreviewObserve(window, recovered.Lease)
			paint, _ := native["paintSequence"].(float64)
			return nativeErr == nil && native["visible"] == true && paint > 0
		}); err != nil {
			return macPreviewFailure("crashed-slot-repaint", err)
		}
		result["crashedSlotReopened"] = true
	}
	err = p.closeInstance(ctx)
	if err != nil && (!cfg.CrashDownload || !strings.HasPrefix(err.Error(), "CEF_CLEANUP_COMPLETED_ABNORMALLY:")) {
		return macPreviewFailure("download-instance-close", err)
	}
	result["closeInstanceReturned"] = err == nil
	result["passed"] = true
	_ = p.save("mac-preview-consumer", result)
	p.app.Quit()
	return nil
}

func verifyMacPreviewDownloads(ctx context.Context, cfg *macPreviewScenario, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, lease string) error {
	waitFor := func(filename string, condition func(embeddedbrowser.DownloadView) bool) (embeddedbrowser.DownloadView, error) {
		var last []embeddedbrowser.DownloadView
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			state, err := service.State(ctx, requested.Document, requested.Slot)
			if err != nil {
				return embeddedbrowser.DownloadView{}, err
			}
			last = state.Downloads
			for _, download := range last {
				if download.Filename == filename && condition(download) {
					return download, nil
				}
			}
			select {
			case <-ctx.Done():
				return embeddedbrowser.DownloadView{}, fmt.Errorf("%s last state %+v: %w", filename, last, ctx.Err())
			case <-ticker.C:
			}
		}
	}
	if cfg.DownloadQuit == "" && !cfg.DownloadWindowClose {
		const complete = "complete.txt"
		target := filepath.Join(cfg.Workspace, complete)
		if cfg.ManualOverwrite {
			if err := desktopclient.MacPreviewPinDownloadDestination(window, target); err != nil {
				return err
			}
		}
		if err := desktopclient.MacPreviewClick(window, lease, 260, 250); err != nil {
			return fmt.Errorf("click complete download: %w", err)
		}
		if cfg.NativeDownload && !cfg.ManualDownload {
			return cancelMacPreviewSave(ctx, window)
		}
		if cfg.ManualDownload {
			if err := waitMacPreviewSave(ctx, window, true); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-save-target.txt"), []byte(cfg.Workspace+"\n"), 0600); err != nil {
				return err
			}
			if cfg.ManualOverwrite {
				if err := waitMacPreviewDownloadConfirmation(ctx, window); err != nil {
					return err
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "previous preview download\n" {
					return fmt.Errorf("existing fixture file changed before confirmation: %v", err)
				}
				if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-overwrite-confirmation"), nil, 0600); err != nil {
					return err
				}
			}
		}
		if cfg.ContentRisk != "" {
			if _, err := waitFor(complete, func(d embeddedbrowser.DownloadView) bool { return d.State == "finalizing" }); err != nil {
				return err
			}
			if err := respondMacPreviewContentRisk(ctx, window, target, cfg.ContentRisk == "accept"); err != nil {
				return err
			}
			if cfg.ContentRisk == "decline" {
				if _, err := waitFor(complete, func(d embeddedbrowser.DownloadView) bool { return d.State == "failed" }); err != nil {
					return err
				}
				if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("destination published after declining content risk: %v", err)
				}
				stages, err := filepath.Glob(filepath.Join(cfg.Workspace, ".dsh-download-*"))
				if err != nil || len(stages) != 0 {
					return fmt.Errorf("private download stage survived declining content risk: %v (%v)", stages, err)
				}
				return nil
			}
		}
		finished, err := waitFor(complete, func(d embeddedbrowser.DownloadView) bool { return d.State == "complete" })
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(cfg.Workspace, complete))
		expected := cfg.Nonce
		if cfg.ContentRisk == "accept" {
			expected = "\x89PNG\r\n\x1a\n" + expected
		}
		if err != nil || string(data) != expected || finished.Received != int64(len(data)) {
			return errors.Join(err, errors.New("MAC_PREVIEW_DOWNLOAD_BYTES_MISMATCH"))
		}
		if cfg.ManualDownload || cfg.ContentRisk != "" {
			return nil
		}
	}
	if err := desktopclient.MacPreviewClick(window, lease, 260, 310); err != nil {
		return fmt.Errorf("click interrupted download: %w", err)
	}
	const canceled = "cancel.txt"
	if cfg.InvalidateDownload || cfg.ResizeDownload {
		if err := waitMacPreviewDownloadMarker(ctx, cfg.Evidence, "download-consent-started"); err != nil {
			return err
		}
	}
	if cfg.InvalidateDownload {
		if err := service.Hide(ctx); err != nil {
			return err
		}
		if err := waitMacPreviewDownloadMarker(ctx, cfg.Evidence, "download-consent-canceled"); err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(cfg.Workspace, canceled)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("invalidated download created a destination: %v", err)
		}
		return nil
	}
	if cfg.ResizeDownload {
		before, err := service.State(ctx, requested.Document, requested.Slot)
		if err != nil {
			return err
		}
		bounds := embeddedbrowser.SlotBounds{
			X: float64(before.Bounds.X), Y: float64(before.Bounds.Y),
			Width: float64(before.Bounds.Width), Height: float64(before.Bounds.Height - 24),
		}
		after, err := service.Update(ctx, requested.Document, requested.Slot, "", bounds, true)
		if err != nil || !after.Visible || before.Lease != after.Lease || before.Document != after.Document ||
			before.Epoch == after.Epoch || before.Bounds == after.Bounds {
			return fmt.Errorf("same-view native resize was not acknowledged: %v", err)
		}
		native, err := desktopclient.MacPreviewObserve(window, lease)
		if err != nil || native["layoutPreviousEpoch"] != before.Epoch || native["layoutEpoch"] != after.Epoch {
			return fmt.Errorf("input epoch changed before the AppKit layout: previous=%v applied=%v want %s → %s: %v",
				native["layoutPreviousEpoch"], native["layoutEpoch"], before.Epoch, after.Epoch, err)
		}
		if err := os.WriteFile(filepath.Join(cfg.Evidence, "download-resize-complete"), nil, 0600); err != nil {
			return err
		}
	}
	active, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "transferring" && d.Received > 0 })
	if err != nil {
		return err
	}
	if cfg.CrashDownload {
		return verifyMacDownloadCrashCleanup(ctx, cfg, canceled)
	}
	if cfg.DownloadWindowClose {
		before, err := service.State(ctx, requested.Document, requested.Slot)
		if err != nil || before.Document == "" {
			return fmt.Errorf("active workbench download has no browser document: %v", err)
		}
		if err := cfg.recordDownloadQuitReady(); err != nil {
			return err
		}
		if err := waitMacPreviewWindowVisibility(ctx, window, false); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-window-hidden"), []byte(cfg.Nonce), 0600); err != nil {
			return err
		}
		if _, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "complete" }); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-window-complete"), []byte(cfg.Nonce), 0600); err != nil {
			return err
		}
		if err := waitMacPreviewWindowVisibility(ctx, window, true); err != nil {
			return err
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			native, err := desktopclient.MacPreviewObserve(window, lease)
			if err == nil && native["visible"] == true {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("browser did not become visible after reopening the workbench: %w", ctx.Err())
			case <-ticker.C:
			}
		}
		after, err := service.State(ctx, requested.Document, requested.Slot)
		if err != nil || after.Instance != before.Instance || after.BrokerGeneration != before.BrokerGeneration ||
			after.Lease != before.Lease || after.Document != before.Document {
			return fmt.Errorf("workbench close changed the active browser document: %v", err)
		}
		return nil
	}
	if cfg.DownloadQuit != "" {
		if cfg.DownloadQuit != "back" {
			return nil // The parent drives quit while this exact transfer is active.
		}
		if err := cfg.recordDownloadQuitReady(); err != nil {
			return err
		}
		_, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "complete" })
		return err
	}
	if err := service.CancelDownload(ctx, requested.Document, requested.Slot, active.ID); err != nil {
		return err
	}
	if _, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "canceled" }); err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(cfg.Workspace, canceled)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("MAC_PREVIEW_CANCELED_DOWNLOAD_DESTINATION_EXISTS")
	}
	return nil
}

func respondMacPreviewContentRisk(ctx context.Context, window application.Window, target string, accept bool) error {
	if err := waitMacPreviewDownloadConfirmation(ctx, window); err != nil {
		return err
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("destination already exists while confirmation is open: %v", err)
	}
	if !desktopclient.MacPreviewRespondDownloadConfirmation(window, accept) {
		return errors.New("MAC_PREVIEW_DOWNLOAD_CONFIRMATION_DISMISSED")
	}
	return nil
}

func waitMacPreviewDownloadConfirmation(ctx context.Context, window application.Window) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for !desktopclient.MacPreviewDownloadConfirmationVisible(window) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("native download confirmation did not appear: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}

func waitMacPreviewDownloadMarker(ctx context.Context, evidence, marker string) error {
	for {
		if _, err := os.Stat(filepath.Join(evidence, marker)); !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("missing %s: %w", marker, ctx.Err())
		case <-time.After(40 * time.Millisecond):
		}
	}
}

func verifyMacDownloadCrashCleanup(ctx context.Context, cfg *macPreviewScenario, filename string) error {
	stages, err := filepath.Glob(filepath.Join(cfg.Workspace, ".dsh-download-*", "partial"))
	if err != nil || len(stages) != 1 {
		return errors.Join(err, errors.New("MAC_PREVIEW_DOWNLOAD_STAGE_REQUIRED"))
	}
	info, err := os.Stat(stages[0])
	if err != nil || info.Size() == 0 {
		return errors.Join(err, errors.New("MAC_PREVIEW_DOWNLOAD_STAGE_EMPTY"))
	}
	if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-crash-ready"), []byte(cfg.Nonce), 0600); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	stage := filepath.Dir(stages[0])
	for {
		if _, err := os.Lstat(stage); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Lstat(filepath.Join(cfg.Workspace, filename)); !errors.Is(err, os.ErrNotExist) {
				return errors.New("MAC_PREVIEW_CRASHED_DOWNLOAD_DESTINATION_EXISTS")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("native stage survived broker termination: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func cancelMacPreviewSave(ctx context.Context, window application.Window) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for !desktopclient.MacPreviewCancelDownloadSave(window) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("native download Save sheet did not accept cancellation: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return waitMacPreviewSave(ctx, window, false)
}

func waitMacPreviewSave(ctx context.Context, window application.Window, visible bool) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for desktopclient.MacPreviewDownloadSaveVisible(window) != visible {
		select {
		case <-ctx.Done():
			return fmt.Errorf("native download Save sheet did not update visibility: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}
