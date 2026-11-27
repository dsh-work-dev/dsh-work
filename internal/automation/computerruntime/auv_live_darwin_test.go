//go:build darwin && arm64 && e67nativefixture

package computerruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveMacAUVPrivateTransport(t *testing.T) {
	dependencies := os.Getenv("DSH_WORK_AUV_TEST_DEPENDENCIES")
	if !filepath.IsAbs(dependencies) {
		t.Skip("set DSH_WORK_AUV_TEST_DEPENDENCIES to the isolated pinned SDK and Mac CLI installation")
	}
	driver := filepath.Join(dependencies, "node_modules", "@auv-js", "cli-darwin-arm64", "bin", "auv")
	source, err := filepath.Abs("auv.mjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 25*time.Second)
	defer stop()
	// Open and close the real adapter without enumerating or reading desktop
	// content. A successful startup includes the credentialed health check.
	const probe = `
		import { pathToFileURL } from 'node:url';
		const [source, dependencies, driver] = process.argv.slice(1);
		const { createAuvBackend } = await import(pathToFileURL(source));
		const backend = await createAuvBackend({dependencies, driver});
		await backend.close();
	`
	cmd := exec.CommandContext(ctx, "node", "--input-type=module", "--eval", probe, source, dependencies, driver)
	cmd.Dir = t.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("AUV adapter private startup or shutdown failed: %v %s", err, out)
	}
}
