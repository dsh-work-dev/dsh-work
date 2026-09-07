//go:build windows

package windows

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
)

type NodeReleaseCatalog struct {
	client acquisition.NodeCatalogClient
}

func NewNodeReleaseCatalog(storeRoot string) NodeReleaseCatalog {
	return NodeReleaseCatalog{client: acquisition.NodeCatalogClient{
		Client:          &http.Client{Timeout: 30 * time.Second},
		OfficialBaseURL: defaultOfficialNodeBase,
		MirrorBaseURL:   defaultMirrorNodeBase,
		Platform:        "windows", Architecture: runtime.GOARCH,
		StagingRoot: filepath.Join(storeRoot, ".staging"),
	}}
}

func (c NodeReleaseCatalog) RefreshLatest(ctx context.Context) (dshmanager.NodeReleaseInfo, error) {
	release, err := c.client.RefreshLatest(ctx)
	if err != nil {
		return dshmanager.NodeReleaseInfo{}, err
	}
	return dshmanager.NodeReleaseInfo{
		Version: release.Version, Platform: release.Platform, Architecture: normalizedNodeArchitecture(release.Architecture),
		Filename: release.Filename, SHA256: release.SHA256,
		Source: nodeInstallSource(release.Route), FallbackUsed: release.FallbackUsed, ObservedAt: release.ObservedAt,
	}, nil
}
