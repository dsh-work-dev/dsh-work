package desktopapp

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// The native menu belongs to the process that owns the workbench windows.
// Its actions use the same background authority as Settings and the CLI.
func installDesktopMenu(desktop *application.App, client *daemon.Client, initial daemon.Snapshot, open func(string)) func(daemon.Snapshot) {
	var mu sync.Mutex
	state := initial
	labels := nativeui.LabelsFor(state.Preferences.Locale)
	menu := desktop.NewMenu()
	actions := menu.AddSubmenu(labels.Actions)
	showPet := actions.AddCheckbox(labels.ShowPet, false).SetEnabled(false)
	actions.AddSeparator()
	restart := actions.Add(labels.RestartDSH)
	quit := actions.Add(labels.Quit)
	settingsItem := menu.Add(labels.Settings).OnClick(func(*application.Context) { go open("settings") })
	help := menu.AddSubmenu(labels.Help)
	updates := help.Add(labels.CheckUpdates).OnClick(func(*application.Context) {
		mu.Lock()
		copy := nativeui.LabelsFor(state.Preferences.Locale)
		mu.Unlock()
		desktop.Dialog.Info().SetTitle(copy.UpdateTitle).SetMessage(copy.UpdateMessage).Show()
	})
	about := help.Add(labels.About).OnClick(func(*application.Context) { go open("about") })
	var busy bool
	var petReady, petVisible bool
	var petPreference settings.PetPreference
	var petLoaded bool
	var applied bool
	var lastRestart, lastQuit, lastPetEnabled, lastPetVisible bool
	apply := func() {
		idle := !busy && state.Status.State != lifecycle.StateStarting && state.Status.State != lifecycle.StateStopping
		canQuit := !busy && state.Status.State != lifecycle.StateStopping
		canPet := !busy && petReady
		if !applied || idle != lastRestart {
			restart.SetEnabled(idle)
		}
		if !applied || canQuit != lastQuit {
			quit.SetEnabled(canQuit)
		}
		if !applied || petVisible != lastPetVisible {
			showPet.SetChecked(petVisible)
		}
		if !applied || canPet != lastPetEnabled {
			showPet.SetEnabled(canPet)
		}
		applied, lastRestart, lastQuit, lastPetEnabled, lastPetVisible = true, idle, canQuit, canPet, petVisible
	}
	for method, item := range map[string]*application.MenuItem{"Restart": restart, "Quit": quit} {
		item.OnClick(func(*application.Context) {
			mu.Lock()
			if busy {
				mu.Unlock()
				return
			}
			busy = true
			apply()
			mu.Unlock()
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				var status lifecycle.Status
				err := client.Call(ctx, "HostService", method, "workspace", nil, &status)
				mu.Lock()
				busy = false
				if err == nil {
					state.Status = status
				}
				apply()
				mu.Unlock()
				if err != nil {
					log.Printf("desktop menu %s: %v", method, err)
				}
			}()
		})
	}
	showPet.OnClick(func(event *application.Context) {
		requested := event.IsChecked()
		mu.Lock()
		if busy {
			mu.Unlock()
			return
		}
		busy = true
		apply()
		mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var panel app.PetPanel
			err := client.Call(ctx, "PetSettingsService", "SetPetVisibility", "settings", []any{requested}, &panel)
			mu.Lock()
			busy = false
			if err == nil {
				petVisible = panel.Runtime.EffectiveVisibility == pet.VisibilityVisible
				petReady = panel.Runtime.SelectionStatus == pet.SelectionReady
			}
			copy := nativeui.LabelsFor(state.Preferences.Locale)
			apply()
			mu.Unlock()
			if err != nil {
				log.Printf("desktop menu pet: %v", err)
				desktop.Dialog.Info().SetTitle(copy.PetMenuErrorTitle).SetMessage(copy.PetMenuErrorMessage).Show()
			}
		}()
	})
	desktop.Menu.Set(menu)
	return func(next daemon.Snapshot) {
		mu.Lock()
		if next.Preferences.Locale != state.Preferences.Locale {
			copy := nativeui.LabelsFor(next.Preferences.Locale)
			actions.SetLabel(copy.Actions)
			settingsItem.SetLabel(copy.Settings)
			showPet.SetLabel(copy.ShowPet)
			restart.SetLabel(copy.RestartDSH)
			quit.SetLabel(copy.Quit)
			help.SetLabel(copy.Help)
			updates.SetLabel(copy.CheckUpdates)
			about.SetLabel(copy.About)
		}
		state = next
		refreshPet := !petLoaded || petPreference != next.Preferences.Pet
		petPreference = next.Preferences.Pet
		apply()
		mu.Unlock()
		if refreshPet {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var panel app.PetPanel
			err := client.Call(ctx, "PetSettingsService", "GetPetPanel", "settings", nil, &panel)
			mu.Lock()
			petLoaded = err == nil
			petReady = err == nil && panel.Runtime.SelectionStatus == pet.SelectionReady
			petVisible = err == nil && panel.Runtime.EffectiveVisibility == pet.VisibilityVisible
			apply()
			mu.Unlock()
		}
	}
}
