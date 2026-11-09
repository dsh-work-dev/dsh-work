//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

type SystemNodeResolver struct{ executor dshadapter.CommandExecutor }

func NewSystemNodeResolver(executor dshadapter.CommandExecutor) SystemNodeResolver {
	return SystemNodeResolver{executor: executor}
}
func (r SystemNodeResolver) Resolve(ctx context.Context, selection dshmanager.NodeSelection, _ []dshmanager.NodeInstallationInfo) (dshmanager.ResolvedNode, error) {
	if selection.Kind != dshmanager.NodeSelectionSystem {
		return dshmanager.ResolvedNode{}, errors.New("managed Node installations are unavailable on this platform")
	}
	path, err := exec.LookPath("node")
	if err != nil {
		return dshmanager.ResolvedNode{}, err
	}
	result, err := r.executor.Run(ctx, path, []string{"-p", "process.execPath"}, nil, "")
	actual := strings.TrimSpace(result.Stdout)
	if err != nil || !filepath.IsAbs(actual) {
		return dshmanager.ResolvedNode{}, errors.New("system Node executable identity is unavailable")
	}
	info, err := os.Stat(actual)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return dshmanager.ResolvedNode{}, errors.New("system Node executable is unavailable")
	}
	version, err := r.executor.Run(ctx, actual, []string{"--version"}, nil, "")
	v := dshadapter.ParseVersion(version.Stdout + "\n" + version.Stderr)
	if err != nil || v == "" {
		return dshmanager.ResolvedNode{}, errors.New("system Node version is unavailable")
	}
	resolved := dshmanager.ResolvedNode{Selection: selection, NodePath: filepath.Clean(actual), Version: v}
	// Preserve the native tool environment; do not change the application's PATH
	// or select/install a different version. The real executable is pinned above.
	resolved.ChildEnvironment = map[string]string{"PATH": filepath.Dir(actual) + string(os.PathListSeparator) + os.Getenv("PATH")}
	resolved.NPMPath, _ = exec.LookPath("npm")
	resolved.PNPMPath, _ = exec.LookPath("pnpm")
	return resolved, nil
}
