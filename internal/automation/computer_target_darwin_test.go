//go:build darwin && cgo

package automation

import "testing"

func TestMacComputerAppPolicy(t *testing.T) {
	cases := []struct {
		id, executable string
		kind           computerAppKind
		level          string
	}{
		{"com.apple.Safari", "/Applications/Safari.app/Contents/MacOS/Safari", computerAppBrowser, "view"},
		{"", "/Applications/Firefox.app/Contents/MacOS/firefox", computerAppBrowser, "view"},
		{"com.apple.Terminal", "/Applications/Utilities/Terminal.app/Contents/MacOS/Terminal", computerAppTerminal, "click"},
		{"com.jetbrains.intellij", "/Applications/IntelliJ IDEA.app/Contents/MacOS/idea", computerAppOrdinary, "click"},
		{"com.apple.Passwords", "/System/Applications/Passwords.app/Contents/MacOS/Passwords", computerAppOrdinary, "blocked"},
		{"com.google.Chrome", "/Applications/DSH Work.app/Contents/MacOS/DSHWorkBrowser", computerAppOrdinary, "blocked"},
		{"com.example.editor", "/Applications/Editor.app/Contents/MacOS/Editor", computerAppOrdinary, "full"},
	}
	for _, tc := range cases {
		kind, level := macComputerAppPolicy(tc.id, tc.executable)
		if kind != tc.kind || level != tc.level {
			t.Errorf("%s: got (%d, %q), want (%d, %q)", tc.executable, kind, level, tc.kind, tc.level)
		}
	}
}
