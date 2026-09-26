package desktopapp

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestWebviewPermissionsBySurface(t *testing.T) {
	tests := []struct {
		name           string
		wantMicrophone application.Permission
	}{
		{name: "workspace", wantMicrophone: application.PermissionAllow},
		{name: "worker", wantMicrophone: application.PermissionDeny},
		{name: "settings", wantMicrophone: application.PermissionDeny},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			permissions := webviewPermissions(test.name)
			if got := permissions[application.PermissionMicrophone]; got != test.wantMicrophone {
				t.Errorf("microphone permission = %v, want %v", got, test.wantMicrophone)
			}
			for _, capability := range []application.PermissionType{
				application.PermissionCamera,
				application.PermissionGeolocation,
				application.PermissionNotifications,
				application.PermissionClipboardRead,
			} {
				if got := permissions[capability]; got != application.PermissionDeny {
					t.Errorf("permission %v = %v, want deny", capability, got)
				}
			}
		})
	}
}
