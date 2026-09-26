package desktopapp

import "testing"

func TestIsWorkspaceOpenURLAcceptsOwnedAndLegacySchemes(t *testing.T) {
	for _, raw := range []string{"dsh-work://open", "dsh://open"} {
		if !isWorkspaceOpenURL(raw) {
			t.Errorf("isWorkspaceOpenURL(%q) = false", raw)
		}
	}
}

func TestIsWorkspaceOpenURLRejectsOtherTargetsAndMalformedURLs(t *testing.T) {
	for _, raw := range []string{
		"https://open",
		"dsh-work://settings",
		"dsh-work://user@open",
		"dsh-work://open:80",
		"dsh-work://open/path",
		"dsh-work://open?token=secret",
		"dsh-work://open#fragment",
	} {
		if isWorkspaceOpenURL(raw) {
			t.Errorf("isWorkspaceOpenURL(%q) = true", raw)
		}
	}
}
