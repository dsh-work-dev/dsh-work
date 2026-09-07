//go:build windows

package windows

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshmanager"
)

type DSHReleaseCatalog struct{ client acquisition.DSHCatalogClient }

func NewDSHReleaseCatalog(storeRoot string) DSHReleaseCatalog {
	return DSHReleaseCatalog{client: acquisition.DSHCatalogClient{
		Client: &http.Client{Timeout: 30 * time.Second}, OfficialBaseURL: defaultOfficialRegistry,
		MirrorBaseURL: defaultMirrorRegistry, StagingRoot: filepath.Join(storeRoot, ".staging"),
	}}
}

func (c DSHReleaseCatalog) Refresh(ctx context.Context) ([]dshmanager.DSHReleaseInfo, error) {
	releases, err := c.client.Refresh(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]dshmanager.DSHReleaseInfo, len(releases))
	for index, release := range releases {
		result[index] = dshmanager.DSHReleaseInfo{
			Version: release.Version, Tags: append([]string(nil), release.Tags...), PublishedAt: release.PublishedAt,
			Source: nodeInstallSource(release.Route), FallbackUsed: release.FallbackUsed, ObservedAt: release.ObservedAt,
		}
	}
	return result, nil
}
