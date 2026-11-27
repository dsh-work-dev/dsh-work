package desktopbridge

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPinnedArtifactCompositionProjection(t *testing.T) {
	path := filepath.Join("..", "..", "tools", "dsh", "node_modules", "@deepseek-ai", "dsh-client-ui-sidebar-documentpreview", "lib", "client.js")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("installed pinned runtime not supplied")
	}
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	projected, err := projectEmbeddedArtifact(source)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if source != projected {
			t.Fatal("unsupported platform preview changed")
		}
		return
	}
	if source == projected || !strings.Contains(projected, "createArtifactPage(sessionId,tab.id,tab.signal)") || strings.Contains(projected, "openArtifact(resourceAddress)") {
		t.Fatal("real document occurrence or authority seam wrong")
	}
	if _, err = projectEmbeddedArtifact(strings.Replace(source, "function HtmlBody({", "function ChangedBody({", 1)); err == nil {
		t.Fatal("pinned seam drift admitted")
	}
	body := projected[strings.Index(projected, "function HtmlBody("):strings.Index(projected, "function LegacyHtmlBody(")]
	if strings.Count(body, "_deepseek_ai_dsh_client_ui_primitives.Button") != 2 || !strings.Contains(body, "var(--dsh-work-control-gap-size)") || strings.Contains(body, `jsx)("button"`) || strings.Contains(body, "gap:8") {
		t.Fatal("artifact controls bypass public primitive or semantic spacing")
	}
	file := filepath.Join(t.TempDir(), "artifact.js")
	if err = os.WriteFile(file, []byte(projected), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("node", "--check", file).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
}
