package desktopbridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedBrowserCompositionProjection(t *testing.T) {
	root := os.Getenv("DSH_WORK_BROWSER_RUNTIME_SOURCE")
	if root == "" {
		t.Skip("installed pinned runtime not supplied")
	}
	data, e := os.ReadFile(filepath.Join(root, "lib", "client.js"))
	if e != nil {
		t.Fatal(e)
	}
	original := string(data)
	projected, e := projectEmbeddedBrowser(original)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(projected, `"aria-label":"Use for this conversation"`) || !strings.Contains(projected, "controlSelection") || projected == original || !strings.Contains(projected, "provider.createPage(sessionId, options)") || !strings.Contains(projected, "else if (desktop === void 0) installFrames(ctx, () => createIframePage);") {
		t.Fatal("factory/default branches not preserved")
	}
	if strings.Contains(projected, `children:controlSelection.busy?"Selecting…":controlSelection.message`) || strings.Contains(projected, "4000") || !strings.Contains(projected, `className:Browser_module_css_default.tool, "aria-label":"Use for this conversation"`) || !strings.Contains(projected, `"aria-busy":controlSelection.busy`) || !strings.Contains(projected, `label:"Use for this conversation",side:"bottom"`) || !strings.Contains(projected, `role:controlSelection.error?"alert":"status"`) || !strings.Contains(projected, `whiteSpace:"normal",overflowWrap:"anywhere"`) {
		t.Fatal("feedback must wrap outside the short action and persist until retry")
	}
	if !strings.Contains(projected, "children: controlPaused ? 'Browser control is paused.'") || !strings.Contains(projected, "disabled:controlSelection.busy||controlPaused") || !strings.Contains(projected, "disabled: controlPaused || target === void 0") || !strings.Contains(projected, `"aria-label": t("address.placeholder"), disabled: controlPaused`) {
		t.Fatal("paused page controls or status missing")
	}
	syntaxFile := filepath.Join(t.TempDir(), "projected-client.js")
	if err := os.WriteFile(syntaxFile, []byte(projected), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("node", "--check", syntaxFile).CombinedOutput(); err != nil {
		t.Fatal("invalid projected JavaScript", err, string(output))
	}
	if _, e := projectEmbeddedBrowser(strings.Replace(original, "this.page = options.createPage({", "this.page = changed({", 1)); e == nil {
		t.Fatal("changed runtime accepted")
	}
}
