//go:build windows || (darwin && cgo)

package platform

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
)

type nodeReleaseCatalog struct {
	client acquisition.NodeCatalogClient
}

func nodeReleaseCatalogFor(storeRoot, platform string) nodeReleaseCatalog {
	return nodeReleaseCatalog{client: acquisition.NodeCatalogClient{
		Client:          &http.Client{Timeout: 30 * time.Second},
		OfficialBaseURL: "https://nodejs.org/dist/",
		MirrorBaseURL:   "https://npmmirror.com/mirrors/node/",
		Platform:        platform, Architecture: nodeCatalogArchitecture(),
		StagingRoot: filepath.Join(storeRoot, ".staging"),
	}}
}

func (c nodeReleaseCatalog) RefreshLatest(ctx context.Context) (dshmanager.NodeReleaseInfo, error) {
	release, err := c.client.RefreshLatest(ctx)
	if err != nil {
		return dshmanager.NodeReleaseInfo{}, err
	}
	return dshmanager.NodeReleaseInfo{
		Version: release.Version, Platform: release.Platform, Architecture: release.Architecture,
		Filename: release.Filename, SHA256: release.SHA256,
		Source: catalogSource(release.Route), FallbackUsed: release.FallbackUsed, ObservedAt: release.ObservedAt,
	}, nil
}

type dshReleaseCatalog struct{ client acquisition.DSHCatalogClient }

func dshReleaseCatalogFor(storeRoot string) dshReleaseCatalog {
	return dshReleaseCatalog{client: acquisition.DSHCatalogClient{
		Client: &http.Client{Timeout: 30 * time.Second}, OfficialBaseURL: "https://registry.npmjs.org/",
		MirrorBaseURL: "https://registry.npmmirror.com/", StagingRoot: filepath.Join(storeRoot, ".staging"),
	}}
}

func (c dshReleaseCatalog) Refresh(ctx context.Context) ([]dshmanager.DSHReleaseInfo, error) {
	releases, err := c.client.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]dshmanager.DSHReleaseInfo, len(releases))
	for index, release := range releases {
		result[index] = dshmanager.DSHReleaseInfo{
			Version: release.Version, Tags: append([]string(nil), release.Tags...), PublishedAt: release.PublishedAt,
			Source: catalogSource(release.Route), FallbackUsed: release.FallbackUsed, ObservedAt: release.ObservedAt,
		}
	}
	return result, nil
}

func nodeCatalogArchitecture() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

func catalogSource(route acquisition.Route) dshmanager.RuntimeArtifactSource {
	if route == acquisition.RouteMirror {
		return dshmanager.RuntimeArtifactSourceMirror
	}
	return dshmanager.RuntimeArtifactSourceOfficial
}
