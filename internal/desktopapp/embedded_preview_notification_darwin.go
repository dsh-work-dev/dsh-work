//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

type macPreviewWorkspace struct {
	app              *application.App
	browser          *embeddedBrowserService
	labels           nativeui.Labels
	cfg              *macPreviewScenario
	notificationMu   sync.Mutex
	notificationSent bool
}

type macPreviewWindowCounts struct {
	Total              int  `json:"total"`
	Visible            int  `json:"visible"`
	NotificationPosted bool `json:"notificationPosted,omitempty"`
}

func (d *macPreviewWorkspace) Observe(ctx context.Context, action string) (macPreviewWindowCounts, error) {
	if _, _, err := d.browser.peer(ctx); err != nil {
		return macPreviewWindowCounts{}, err
	}
	windows := d.app.Window.GetAll()
	counts := macPreviewWindowCounts{Total: len(windows)}
	for _, window := range windows {
		if window.IsVisible() {
			counts.Visible++
		}
	}
	switch action {
	case "dock":
		return counts, desktopclient.MacPreviewApplicationReopen()
	case "notification-default":
		return counts, desktopclient.MacPreviewNotificationDefault(dshApprovalCategoryID, d.labels.ApprovalAllowOnce, d.labels.ApprovalReject, true)
	case "notification-manual":
		if !d.cfg.ManualNotification || counts.Visible != 0 {
			return counts, errors.New("manual notification fixture is unavailable")
		}
		posted, err := d.postNotification()
		counts.NotificationPosted = posted
		return counts, err
	case "tray-button":
		return counts, desktopclient.MacPreviewTrayAction(ctx, "", false)
	case "tray-menu-open":
		return counts, desktopclient.MacPreviewTrayAction(ctx, d.labels.OpenWorkspace, true)
	case "tray-menu":
		return counts, desktopclient.MacPreviewTrayAction(ctx, d.labels.OpenWorkspace, false)
	case "":
		return counts, nil
	default:
		return counts, errors.New("unknown native workbench entry")
	}
}

func (d *macPreviewWorkspace) postNotification() (bool, error) {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()
	if d.notificationSent {
		return true, nil
	}
	center := wailsnotifications.New()
	authorized, err := center.CheckNotificationAuthorization()
	if err != nil || !authorized {
		return false, err
	}
	if desktopclient.MacPreviewNotificationDefault(dshApprovalCategoryID, d.labels.ApprovalAllowOnce, d.labels.ApprovalReject, false) != nil {
		return false, nil
	}
	if err := center.SendNotificationWithActions(wailsnotifications.NotificationOptions{
		ID:         d.cfg.notificationID(),
		Title:      "DSH Work 通知测试",
		Body:       "点击通知正文，恢复测试工作台。",
		CategoryID: dshApprovalCategoryID,
	}); err != nil {
		return false, err
	}
	d.notificationSent = true
	return true, nil
}

func (c *macPreviewScenario) notificationID() string {
	return "dsh-preview-notification-" + c.Nonce
}

func recordMacPreviewNotificationResponse(result wailsnotifications.NotificationResult) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || !cfg.ManualNotification || result.Error != nil || result.Response.ID != cfg.notificationID() {
		return
	}
	data, err := json.Marshal(map[string]string{"action": result.Response.ActionIdentifier})
	if err == nil {
		_ = os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-notification-response.json"), data, 0600)
	}
}

func clearMacPreviewNotification() {
	cfg, err := macPreviewConfig()
	if err == nil && cfg != nil && cfg.ManualNotification {
		_ = wailsnotifications.New().RemoveNotification(cfg.notificationID())
	}
}

func (p *macPreviewConsumer) verifyWorkspaceReopen() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	visibleWindows := p.visibleWindows
	counts, err := verifyMacPreviewWorkspaceRestore(ctx, service, window, requested, before, visibleWindows)
	if err != nil {
		return macPreviewFailure("native-dock-reopen", err)
	}
	if err := desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-dock-reopened.png")); err != nil {
		return macPreviewFailure("render-after-dock-reopen", err)
	}
	result["dockReopenPassed"] = true
	result["trayActionsPassed"] = true
	result["trayMenuTrackingPassed"] = true
	result["notificationDefaultResponsePassed"] = true
	result["dockDaemonWindows"] = counts
	if cfg.ManualNotification {
		err = verifyMacPreviewManualNotification(ctx, cfg, service, window, requested, before, visibleWindows)
		if err != nil {
			return macPreviewFailure("native-notification-center", err)
		}
		result["manualNotificationPassed"] = true
	}

	return nil
}

func verifyMacPreviewManualNotification(ctx context.Context, cfg *macPreviewScenario, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, original embeddedbrowser.ViewState, visibleWindows func() int) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	mark := func(stage string) error {
		return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-notification-"+stage), []byte(stage+"\n"), 0600)
	}
	if err := mark("permission"); err != nil {
		return err
	}
	if err := service.Client.Call(ctx, "SettingsService", "SetNotificationPreference", window.Name(), []any{string(notifications.PreferenceEnabled), true}, nil); err != nil {
		return err
	}
	window.Close()
	if err := waitMacPreviewWindowVisibility(ctx, window, false); err != nil {
		return err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var counts macPreviewWindowCounts
		if err := service.Client.Call(ctx, "MacPreviewWorkspace", "Observe", window.Name(), []any{"notification-manual"}, &counts); err != nil {
			return err
		}
		if counts.NotificationPosted {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for notification permission and approval category: %w", ctx.Err())
		}
	}
	if err := mark("posted"); err != nil {
		return err
	}
	for {
		var response struct{ Action string }
		data, err := os.ReadFile(filepath.Join(cfg.Evidence, "mac-preview-notification-response.json"))
		if err == nil && json.Unmarshal(data, &response) == nil {
			if response.Action != wailsnotifications.DefaultActionIdentifier {
				return fmt.Errorf("notification response was %q; click the notification body", response.Action)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for a Notification Center response: %w", ctx.Err())
		}
	}
	if err := waitMacPreviewWindowVisibility(ctx, window, true); err != nil {
		return err
	}
	for {
		state, stateErr := service.State(ctx, requested.Document, requested.Slot)
		frame, frameErr := desktopclient.MacPreviewObserve(window, original.Lease)
		if visibleWindows() == 1 && stateErr == nil && frameErr == nil && state.Visible && frame["visible"] == true &&
			state.Lease == original.Lease && state.Document == original.Document && state.BrokerGeneration == original.BrokerGeneration {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for the original workbench and browser: %w", ctx.Err())
		}
	}
}

func verifyMacPreviewWorkspaceRestore(ctx context.Context, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, original embeddedbrowser.ViewState, visibleWindows func() int) (macPreviewWindowCounts, error) {
	observeDaemon := func(ctx context.Context, action string) (macPreviewWindowCounts, error) {
		var counts macPreviewWindowCounts
		err := service.Client.Call(ctx, "MacPreviewWorkspace", "Observe", window.Name(), []any{action}, &counts)
		return counts, err
	}
	var daemonBefore macPreviewWindowCounts
	for _, action := range []string{"workbench-dock", "dock", "tray-button", "tray-menu", "notification-default"} {
		check, stop := context.WithTimeout(ctx, 12*time.Second)
		err := func() error {
			window.Close()
			if err := waitMacPreviewWindowVisibility(check, window, false); err != nil {
				return err
			}
			if visibleWindows() != 0 {
				return errors.New("hidden workbench still counted by the window ledger")
			}
			if action == "workbench-dock" {
				if err := desktopclient.MacPreviewApplicationReopen(); err != nil {
					return err
				}
			} else {
				if action == "tray-menu" {
					counts, err := observeDaemon(check, "tray-menu-open")
					if err != nil {
						return fmt.Errorf("native context menu opening and dismissal: %w", err)
					}
					if window.IsVisible() || visibleWindows() != 0 || counts.Total == 0 || counts.Visible != 0 {
						return fmt.Errorf("right-clicking the tray menu opened an app window: daemon=%+v", counts)
					}
				}
				counts, err := observeDaemon(check, action)
				if err != nil {
					return err
				}
				if counts.Total == 0 || counts.Visible != 0 {
					return fmt.Errorf("expected hidden daemon windows, found %+v", counts)
				}
				daemonBefore = counts
			}
			if err := waitMacPreviewWindowVisibility(check, window, true); err != nil {
				return err
			}
			if visibleWindows() != 1 {
				return errors.New("window ledger did not record the restored workbench")
			}
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				state, stateErr := service.State(check, requested.Document, requested.Slot)
				frame, frameErr := desktopclient.MacPreviewObserve(window, original.Lease)
				if stateErr == nil && frameErr == nil && state.Visible && frame["visible"] == true &&
					state.Lease == original.Lease && state.Document == original.Document && state.BrokerGeneration == original.BrokerGeneration {
					return nil
				}
				select {
				case <-ticker.C:
				case <-check.Done():
					return fmt.Errorf("same browser document did not return: %w", check.Err())
				}
			}
		}()
		stop()
		if err != nil {
			return macPreviewWindowCounts{}, fmt.Errorf("%s reopen: %w", action, err)
		}
	}
	after, err := observeDaemon(ctx, "")
	if err != nil {
		return macPreviewWindowCounts{}, err
	}
	if after != daemonBefore {
		return macPreviewWindowCounts{}, fmt.Errorf("Dock reopen changed daemon window visibility: before=%+v after=%+v", daemonBefore, after)
	}
	return after, nil
}

func waitMacPreviewWindowVisibility(ctx context.Context, window application.Window, visible bool) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for window.IsVisible() != visible {
		select {
		case <-ctx.Done():
			return fmt.Errorf("workbench did not reach visibility %t: %w", visible, ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}
